package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Only actual admitted calls hold an object after a failed initializer. This
// is object ownership, not a persistent job, receipt, execution queue or a
// second running-plugin registry. A replacement with the same ID is unrelated.
type originalSubmittedState struct {
	mu sync.Mutex
	leases map[*originalSubmittedLease]struct{}
	initializationFinished bool
	unpublished bool
	sealed bool
}

type originalSubmittedLease struct {
	object *originalPluginInstance
	cancel context.CancelFunc
	active atomic.Bool
}
type originalSubmittedObjectKey struct{}

func (p *originalPluginInstance) originalSubmittedScopeAllowed(ctx context.Context) bool {
	lease, ok := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	if !ok || lease == nil || lease.object != p || !lease.active.Load() || p.stopping.Load() { return false }
	p.submitted.mu.Lock()
	defer p.submitted.mu.Unlock()
	_, exists := p.submitted.leases[lease]
	return exists && !p.submitted.sealed
}

// Reservation and global shutdown sealing share the existing admission mutex.
// Close cannot scan the object registry and then miss a late successful submit.
// The reservation is rolled back if SDK admission fails; it is never published
// as an accepted task, nor allowed to retain a failed object on that path.
func (p *originalPluginInstance) reserveOriginalSubmission() (*originalSubmittedLease, context.Context, error) {
	global := &p.app.pluginOriginalBackground
	global.mu.Lock()
	defer global.mu.Unlock()
	p.submitted.mu.Lock()
	defer p.submitted.mu.Unlock()
	if global.closed || p.submitted.sealed || p.stopping.Load() {
		return nil, nil, bad("PLUGIN_CALL_CLOSED", "插件后台提交已停止")
	}
	ctx, cancel := context.WithCancel(context.Background())
	lease := &originalSubmittedLease{object:p, cancel:cancel}
	lease.active.Store(true)
	if p.submitted.leases == nil { p.submitted.leases = make(map[*originalSubmittedLease]struct{}) }
	p.submitted.leases[lease] = struct{}{}
	p.app.pluginSubmittedObjects.Store(p, struct{}{})
	p.app.wg.Add(1)
	return lease, context.WithValue(ctx, originalSubmittedObjectKey{}, lease), nil
}

func (p *originalPluginInstance) releaseOriginalSubmission(lease *originalSubmittedLease) {
	lease.active.Store(false)
	lease.cancel()
	p.submitted.mu.Lock()
	delete(p.submitted.leases, lease)
	empty := len(p.submitted.leases) == 0
	retire := empty && p.submitted.initializationFinished && p.submitted.unpublished && !p.submitted.sealed
	if retire { p.submitted.sealed = true }
	if empty { p.app.pluginSubmittedObjects.Delete(p) }
	p.submitted.mu.Unlock()
	if retire { p.retireOriginalSubmittedObject() }
}

// Called only by the manager's initial start, after actual publication or
// failed initialization. Direct reinitialization of a running object does not
// use this hook. Successful submit is independent of the initializer's error.
func (p *originalPluginInstance) finishOriginalInitialization(published bool) {
	p.submitted.mu.Lock()
	p.submitted.initializationFinished = true
	p.submitted.unpublished = !published
	retire := !published && len(p.submitted.leases) == 0 && !p.submitted.sealed
	if retire { p.submitted.sealed = true }
	p.submitted.mu.Unlock()
	if retire { p.retireOriginalSubmittedObject() }
}

func (p *originalPluginInstance) retireOriginalSubmittedObject() {
	p.stopping.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The last submitted call has already returned and revoked its phase.
	// Never wait on App.wg here: this goroutine owns one of its references.
	if err := p.closeOriginalSubmittedTransport(ctx); err != nil {
		p.app.log("ERROR", "plugins", "submitted_stop_failed", "未发布插件对象清理失败", "", "")
	}
}

// Explicit object shutdown revokes submissions, unlike ordinary parent-call
// cancellation. Final Client.Stop is responsible for bounded join and reap.
func (p *originalPluginInstance) closeOriginalSubmissions() {
	p.submitted.mu.Lock()
	p.submitted.sealed = true
	for lease := range p.submitted.leases {
		lease.active.Store(false)
		lease.cancel()
	}
	p.submitted.mu.Unlock()
}

// Call after closeOriginalSessions during App.Close, and on an explicit stop
// whose ID has no running object. Only tracked unpublished exact objects are
// stopped here; loaded objects retain manager close -> stop_service -> stop.
func (a *App) stopOriginalUnpublishedSubmissions(ctx context.Context, instance string) {
	a.pluginSubmittedObjects.Range(func(key, _ any) bool {
		p := key.(*originalPluginInstance)
		if instance != "" && p.installation.InstanceID != instance || originalObjectStillLoaded(p) { return true }
		p.stopping.Store(true)
		p.closeOriginalSubmissions()
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := p.closeOriginalSubmittedTransport(stopCtx); err != nil {
			a.log("ERROR", "plugins", "submitted_stop_failed", "未发布插件对象清理失败", "", "")
		}
		cancel()
		return true
	})
}

func (a *App) originalSubmitResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	p := scope.object
	if p == nil || p.app != a || scope.phase == "stop" { return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台提交未绑定当前业务对象") }
	var input struct {
		Method string `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := originalResourceDecode(raw, &input); err != nil { return nil, err }
	if !slices.Contains(p.manifest.Methods, input.Method) || len(input.Payload) == 0 || !json.Valid(input.Payload) || strings.HasPrefix(input.Method, "transport.") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台方法未声明或参数无效")
	}
	switch input.Method {
	case "init", "handshake", "stop", "close", "stop_service":
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台提交不得触发管理器生命周期")
	}
	if err := scope.validate(ctx); err != nil { return nil, err }
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if client == nil || !client.SupportsConcurrent() || !client.SupportsTransportClose() { return nil, bad("PLUGIN_CONCURRENT_REQUIRED", "后台提交需要协商并发worker") }
	lease, childCtx, err := p.reserveOriginalSubmission()
	if err != nil { return nil, err }
	callback, revoke := p.originalBaseSessionHost(childCtx, input.Method, nil)
	pending, err := client.StartCallWithAdmission(ctx, childCtx, input.Method, input.Payload, callback, plugins.CallOptions{CallerDeadlineOnly:true})
	if err != nil {
		revoke()
		p.releaseOriginalSubmission(lease)
		a.wg.Done()
		return nil, err
	}
	go func() {
		defer a.wg.Done()
		_, err := pending.Wait()
		revoke()
		if err != nil { a.log("ERROR", "plugins", "submitted_method_failed", "插件后台方法执行失败", "", "") }
		p.releaseOriginalSubmission(lease)
	}()
	return json.RawMessage(`{"accepted":true}`), nil
}

// Unpublished retirement is transport-only. In particular it must not run the
// worker's stop handler or reuse its host grants as an invented source hook.
func (p *originalPluginInstance) closeOriginalSubmittedTransport(ctx context.Context) error {
	p.closeOriginalSubmissions()
	p.sessionMu.Lock()
	client := p.session
	if client == nil { p.sessionMu.Unlock(); return nil }
	if !client.SupportsTransportClose() { p.sessionMu.Unlock(); return p.stopSession(ctx) }
	p.session = nil
	p.sessionMu.Unlock()
	return client.CloseTransport(ctx)
}
