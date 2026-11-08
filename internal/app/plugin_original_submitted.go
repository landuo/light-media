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
	mu                     sync.Mutex
	leases                 map[*originalSubmittedLease]struct{}
	initializationFinished bool
	unpublished            bool
	sealed                 bool
	privateClosing         bool
	privateStopDone        chan struct{}
	privateMethodsRan      bool
	privateStopErr         error
	privateLifetimes       sync.WaitGroup
	publicClosing          bool
	publicRetiring         bool
	publicRetireReady      bool
	publicRetireStarted    bool
	publicClient           *plugins.Client
}

type originalSubmittedLease struct {
	object           *originalPluginInstance
	privateScheduler bool
	publicService    bool
	sourceKind       originalSourceInvocationKind
	client           *plugins.Client // set once under submitted.mu before actual SDK admission
	cancel           context.CancelFunc
	active           atomic.Bool
}
type originalSubmittedObjectKey struct{}

func (p *originalPluginInstance) originalSubmittedScopeAllowed(ctx context.Context) bool {
	lease, ok := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	if !ok || lease == nil || lease.object != p || !lease.active.Load() {
		return false
	}
	if lease.publicService {
		return p.originalPublicServiceScopeAllowed(ctx)
	}
	p.submitted.mu.Lock()
	defer p.submitted.mu.Unlock()
	_, exists := p.submitted.leases[lease]
	return exists && !p.submitted.sealed && (!p.stopping.Load() || lease.privateScheduler || lease.publicService)
}

// Reservation and global shutdown sealing share the existing admission mutex.
// Close cannot scan the object registry and then miss a late successful submit.
// The reservation is rolled back if SDK admission fails; it is never published
// as an accepted task, nor allowed to retain a failed object on that path.
func (p *originalPluginInstance) reserveOriginalSubmission() (*originalSubmittedLease, context.Context, error) {
	return p.reserveOriginalOwnedSubmission(nil, "")
}

func (p *originalPluginInstance) reserveOriginalOwnedSubmission(parent *originalSubmittedLease, ownership string) (*originalSubmittedLease, context.Context, error) {
	return p.reserveOriginalSubmissionKind(parent, ownership, nil)
}

func (p *originalPluginInstance) reserveOriginalSubmissionKind(parent *originalSubmittedLease, ownership string, publicClient *plugins.Client) (*originalSubmittedLease, context.Context, error) {
	return p.reserveOriginalSourceSubmission(parent, ownership, publicClient, originalSourceScheduler)
}
func (p *originalPluginInstance) reserveOriginalSourceSubmission(parent *originalSubmittedLease, ownership string, publicClient *plugins.Client, sourceKind originalSourceInvocationKind) (*originalSubmittedLease, context.Context, error) {
	global := &p.app.pluginOriginalBackground
	global.mu.Lock()
	defer global.mu.Unlock()
	p.submitted.mu.Lock()
	defer p.submitted.mu.Unlock()
	_, parentExists := p.submitted.leases[parent]
	parentLive := parent != nil && parent.object == p && parent.active.Load() && parentExists && !p.submitted.sealed
	inheritedPrivate := parentLive && parent.privateScheduler
	inheritedPublic := parentLive && parent.publicService && parent.client != nil && parent.client == p.submitted.publicClient
	inherited := inheritedPrivate || inheritedPublic
	isPrivate := ownership == "private_scheduler" || inheritedPrivate
	isPublic := publicClient != nil || inheritedPublic
	if inheritedPublic {
		sourceKind = parent.sourceKind
	}
	if !isPublic {
		sourceKind = 0
	}
	if publicClient != nil && (sourceKind == originalSourceAPI && global.sourceEntriesClosing || sourceKind == originalSourceEvent && global.sourceEventsClosing) {
		return nil, nil, bad("PLUGIN_CALL_CLOSED", "插件公共入口已停止")
	}
	if publicClient != nil && (p.submitted.publicClosing || !originalObjectStillLoaded(p) || p.submitted.publicClient != nil && p.submitted.publicClient != publicClient) {
		return nil, nil, bad("PLUGIN_CALL_CLOSED", "插件公共服务已停止")
	}
	// A timed-out/disconnected HTTP task can leave a genuine non-daemon
	// source API thread running beyond module shutdown. Only its already-live
	// mechanical chain may add descendants; all new/default producers stay shut.
	lateAPI := inheritedPublic && sourceKind == originalSourceAPI && !isPrivate
	if global.closed && !lateAPI || p.submitted.sealed || (p.stopping.Load() || p.submitted.privateClosing) && !inherited || global.privateSchedulerClosing && isPrivate && !inheritedPrivate {
		return nil, nil, bad("PLUGIN_CALL_CLOSED", "插件后台提交已停止")
	}
	ctx, cancel := context.WithCancel(context.Background())
	lease := &originalSubmittedLease{object: p, cancel: cancel, privateScheduler: isPrivate, publicService: isPublic, sourceKind: sourceKind}
	if publicClient != nil {
		lease.client = publicClient
		p.submitted.publicClient = publicClient
	} else if inheritedPublic {
		lease.client = parent.client
	}
	if sourceKind == originalSourceScheduler {
		p.app.pluginOriginalDirectJobs.serviceLifetimes.Add(1)
	}
	if sourceKind == originalSourceEvent {
		global.eventLifetimes.Add(1)
	}
	if lease.privateScheduler {
		p.submitted.privateLifetimes.Add(1)
	}
	lease.active.Store(true)
	if p.submitted.leases == nil {
		p.submitted.leases = make(map[*originalSubmittedLease]struct{})
	}
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
	retire := empty && p.submitted.initializationFinished && p.submitted.unpublished && !p.submitted.sealed && !p.submitted.privateClosing
	publicRetire := p.publicRetirementReadyLocked()
	if retire {
		p.submitted.sealed = true
	}
	if empty {
		p.app.pluginSubmittedObjects.Delete(p)
	}
	p.submitted.mu.Unlock()
	if lease.privateScheduler {
		p.submitted.privateLifetimes.Done()
	}
	if lease.sourceKind == originalSourceScheduler {
		defer p.app.pluginOriginalDirectJobs.serviceLifetimes.Done()
	}
	if lease.sourceKind == originalSourceEvent {
		defer p.app.pluginOriginalBackground.eventLifetimes.Done()
	}
	if publicRetire {
		p.retireOriginalPublicServiceObject()
	}
	if retire {
		p.retireOriginalSubmittedObject()
	}
}

// Called only by the manager's initial start, after actual publication or
// failed initialization. Direct reinitialization of a running object does not
// use this hook. Successful submit is independent of the initializer's error.
func (p *originalPluginInstance) finishOriginalInitialization(published bool) {
	p.submitted.mu.Lock()
	p.submitted.initializationFinished = true
	p.submitted.unpublished = !published
	retire := !published && len(p.submitted.leases) == 0 && !p.submitted.sealed && !p.submitted.privateClosing
	if retire {
		p.submitted.sealed = true
	}
	p.submitted.mu.Unlock()
	if retire {
		p.retireOriginalSubmittedObject()
	}
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
		if instance != "" && p.installation.InstanceID != instance || originalObjectStillLoaded(p) {
			return true
		}
		// A manager-removed published source object still belongs to its accepted
		// public callbacks. It is not an unpublished initialization failure.
		p.submitted.mu.Lock()
		retainedPublic := p.submitted.publicRetiring
		p.submitted.mu.Unlock()
		if retainedPublic {
			return true
		}
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
	if p == nil || p.app != a || scope.phase == "stop" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台提交未绑定当前业务对象")
	}
	var input struct {
		Method    string          `json:"method"`
		Payload   json.RawMessage `json:"payload"`
		Ownership string          `json:"ownership,omitempty"`
	}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if input.Ownership != "" && input.Ownership != "private_scheduler" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台所有权无效")
	}
	if p.installation.PluginID == "SeedRescue" && (scope.seedRescue == nil || scope.seedRescue.action != "scan_now" ||
		input.Method != "scan_background" || input.Ownership != "private_scheduler") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台扫描必须来自稀缺保种的已认证扫描接口")
	}
	if p.installation.PluginID == "CloudflareSpeedTest" && (scope.phase != "init" || originalLiveInitialization(ctx, p) == nil ||
		input.Method != "private_once" || input.Ownership != "private_scheduler") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "单次优选必须由实际插件初始化登记")
	}
	if input.Ownership == "private_scheduler" && (!p.manifest.ManagedPrivateScheduler || slices.Contains(p.manifest.Capabilities, plugins.CleanupSubmitCapability)) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "私有调度需独立声明，不与清理续段组合")
	}
	if !slices.Contains(p.manifest.Methods, input.Method) || len(input.Payload) == 0 || !json.Valid(input.Payload) || strings.HasPrefix(input.Method, "transport.") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台方法未声明或参数无效")
	}
	switch input.Method {
	case "init", "handshake", "stop", "close", "stop_service":
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "后台提交不得触发管理器生命周期")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	parent, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	var client *plugins.Client
	if p.originalPublicServiceScopeAllowed(ctx) {
		client = parent.client
	} else {
		p.sessionMu.Lock()
		client = p.session
		p.sessionMu.Unlock()
	}
	if client == nil || !client.SupportsConcurrent() || !client.SupportsTransportClose() {
		return nil, bad("PLUGIN_CONCURRENT_REQUIRED", "后台提交需要协商并发worker")
	}
	if input.Ownership == "private_scheduler" && !client.SupportsPrivateSchedulerStop() {
		return nil, bad("PLUGIN_PRIVATE_SCHEDULER_UNSUPPORTED", "worker尚未协商私有调度停止能力")
	}
	lease, childCtx, err := p.reserveOriginalOwnedSubmission(parent, input.Ownership)
	if err != nil {
		return nil, err
	}
	p.submitted.mu.Lock()
	if lease.client == nil {
		lease.client = client
	}
	p.submitted.mu.Unlock()
	// Preserve only the actual host operation owner, not the parent's deadline
	// or cancellation. A synchronous source pass waits for these fresh phases;
	// an asynchronous pass outliving init sees the closed lease and must acquire
	// the ordinary manager mutex itself.
	if owner := a.originalOperationOwner(ctx); owner != nil {
		childCtx = context.WithValue(childCtx, originalOperationOwnerKey{}, owner)
	}
	if call := originalLiveInitialization(ctx, p); call != nil {
		childCtx = context.WithValue(childCtx, originalInitializingObjectKey{}, call)
	}
	callback, revoke := p.originalBaseSessionHost(childCtx, input.Method, nil)
	pending, err := client.StartCallWithAdmission(ctx, childCtx, input.Method, input.Payload, callback, plugins.CallOptions{CallerDeadlineOnly: true})
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
		if err != nil {
			a.log("ERROR", "plugins", "submitted_method_failed", "插件后台方法执行失败", "", "")
		}
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
	if client == nil {
		p.sessionMu.Unlock()
		return nil
	}
	if !client.SupportsTransportClose() {
		p.sessionMu.Unlock()
		return p.stopSession(ctx)
	}
	p.session = nil
	p.sessionMu.Unlock()
	return client.CloseTransport(ctx)
}
