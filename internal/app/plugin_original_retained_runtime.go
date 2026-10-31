package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
)

type originalRetainedCallKey struct{}

// Carry an exact occupancy parent into callSession. This is not callback or
// HTTP authority: reserveOriginalCall derives a fresh call lease and the
// existing identity/generation/capability checks still apply to its phase.
func (p *originalPluginInstance) originalRetainedParentContext(ctx context.Context, parent *originalRetainedLease) (context.Context, error) {
	if parent == nil || parent.lifetime == nil || parent.lifetime.object != p {
		return nil, bad("PLUGIN_CALL_CLOSED", "插件保留调用未绑定当前对象")
	}
	life := parent.lifetime
	life.mu.Lock()
	valid := !life.sealed && life.containsLocked(parent) && parent.phaseActive
	life.mu.Unlock()
	if !valid {
		return nil, bad("PLUGIN_CALL_CLOSED", "插件保留引用已失效")
	}
	return context.WithValue(ctx, originalRetainedCallKey{}, parent), nil
}

// Registry owners call this only AFTER unlocking. Last-owner retirement may
// synchronously close and reap the exact worker; never release under a map lock.
func (p *originalPluginInstance) releaseOriginalRetainedOwner(owner *originalRetainedLease) {
	if owner == nil || owner.lifetime == nil || owner.lifetime.object != p {
		return
	}
	retire, err := owner.releaseOwner()
	if err != nil {
		p.app.log("ERROR", "plugins", "retained_owner_invalid", "插件保留引用类型无效", "", "")
		return
	}
	if retire {
		p.retireOriginalRetainedObject(owner.lifetime)
	}
}

// Source-local object references span complete multi-RPC operations. Select
// and pin under the short manager registry lock, never across worker RPC.
func (a *App) pinOriginalRunningObject(ctx context.Context, instance string) (*originalPluginInstance, *originalRetainedLease, error) {
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	value, present := a.pluginRunningInstances.Load(instance)
	if !present {
		return nil, nil, nil
	}
	p := value.(*originalPluginInstance)
	owner, err := p.originalRetainedState().retain(originalRetainedOperation, p.originalRetainedParent(ctx))
	if err != nil {
		return nil, nil, err
	}
	return p, owner, nil
}

func (p *originalPluginInstance) originalRetainedState() *originalRetainedLifetime {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	if p.retained == nil {
		p.retained = newOriginalRetainedLifetime(p)
	}
	return p.retained
}

func (p *originalPluginInstance) originalRetainedParent(ctx context.Context) *originalRetainedLease {
	if lease, ok := ctx.Value(originalRetainedCallKey{}).(*originalRetainedLease); ok && lease != nil && lease.lifetime != nil && lease.lifetime.object == p {
		return lease
	}
	if submission, ok := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease); ok && submission != nil && submission.object == p {
		return submission.retained
	}
	return nil
}

func (p *originalPluginInstance) originalRetainedScopeAllowed(ctx context.Context) bool {
	lease, _ := ctx.Value(originalRetainedCallKey{}).(*originalRetainedLease)
	return lease.callPhaseAllowed(p)
}

// Global admission seals before App.Close enumerates retained objects and
// waits. A cancellation-returned invocation cannot disappear from that cleanup
// set or its WaitGroup before the actual SDK drain fact is observed.
func (p *originalPluginInstance) reserveOriginalCall(ctx context.Context) (*originalRetainedLease, context.Context, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	global := &p.app.pluginOriginalBackground
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.closed || p.stopping.Load() {
		return nil, nil, bad("PLUGIN_CALL_CLOSED", "插件调用已停止")
	}
	life := p.originalRetainedState()
	lease, err := life.retain(originalRetainedCall, p.originalRetainedParent(ctx))
	if err != nil {
		return nil, nil, err
	}
	p.app.pluginRetainedObjects.Store(p, struct{}{})
	p.app.wg.Add(1)
	return lease, context.WithValue(ctx, originalRetainedCallKey{}, lease), nil
}

// Must follow Wait returning and revoke(). nil is only the known no-admission
// or completed legacy synchronous-exchange case, never a guessed PendingCall
// drain receipt. A real PendingCall always supplies its actual Drained channel.
func (p *originalPluginInstance) finishOriginalRetainedCall(lease *originalRetainedLease, drained <-chan struct{}) {
	_, _ = lease.callReturned()
	finish := func() {
		defer p.app.wg.Done()
		retire, _ := lease.callDrained()
		if retire {
			p.retireOriginalRetainedObject(lease.lifetime)
		}
	}
	if drained == nil {
		finish()
		return
	}
	select {
	case <-drained:
		finish()
	default:
		go func() { <-drained; finish() }()
	}
}

func (p *originalPluginInstance) completeOriginalRetainedTermination(life *originalRetainedLifetime) {
	// CloseTransport/StopWithHost have observed their actual child reaper before
	// returning on the first termination claim, including their kill fallback.
	p.app.pluginRetainedObjects.Delete(p)
	_ = life.finishRetirement()
}

func (p *originalPluginInstance) waitOriginalRetainedTermination(ctx context.Context, life *originalRetainedLifetime) error {
	select {
	case <-life.retired():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *originalPluginInstance) retireOriginalRetainedObject(life *originalRetainedLifetime) {
	p.stopping.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reaped, err := p.closeOriginalRetainedTransport(ctx)
	if reaped {
		p.completeOriginalRetainedTermination(life)
	}
	if err != nil {
		p.app.log("ERROR", "plugins", "retained_stop_failed", "插件保留对象清理失败", "", "")
	}
}

func (p *originalPluginInstance) forceOriginalRetainedObject(ctx context.Context) error {
	life := p.originalRetainedState()
	if !life.claimForcedRetirement() {
		return p.waitOriginalRetainedTermination(ctx, life)
	}
	p.stopping.Store(true)
	reaped, err := p.closeOriginalRetainedTransport(ctx)
	if reaped {
		p.completeOriginalRetainedTermination(life)
	}
	return err
}

// Logical Manager.remove must never invent an extra business stop for a
// legacy child. Its source hooks already ran. Keep the prior unpublished
// fallback untouched for objects which were not logically removed.
func (p *originalPluginInstance) closeOriginalRetainedTransport(ctx context.Context) (bool, error) {
	if !p.managerRemoved.Load() {
		return true, p.closeOriginalSubmittedTransport(ctx)
	}
	p.closeOriginalSubmissions()
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if client == nil {
		return true, nil
	}
	var err error
	if client.SupportsTransportClose() {
		err = client.CloseTransport(ctx)
	} else {
		// This is the one exact terminal owner, not an HTTP/business call.
		// No inherited deadline may abandon admission before the real reaper.
		err = client.TerminateTransport(context.Background())
		var failure *plugins.Error
		if errors.As(err, &failure) && (failure.Code == "transport_termination_pending" || failure.Code == "invalid_invocation") {
			// No actual Wait receipt. Retain both the client and cleanup entry;
			// do not convert this defensive failure into completed retirement.
			return false, err
		}
	}
	p.sessionMu.Lock()
	if p.session == client {
		p.session = nil
	}
	p.sessionMu.Unlock()
	return true, err
}

func (a *App) closeOriginalRetainedObjects() {
	// App.Close already sealed global admission. This set is only a cleanup
	// enumeration, never a second manager running registry or lookup source.
	a.pluginRetainedObjects.Range(func(key, _ any) bool {
		p := key.(*originalPluginInstance)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := p.forceOriginalRetainedObject(ctx); err != nil {
			a.log("ERROR", "plugins", "retained_stop_failed", "插件保留对象清理失败", "", "")
		}
		cancel()
		return true
	})
}

// Existing actual business Stop behavior, without taking a second retirement
// claim. Normal explicit stop still owns close/stop_service followed by this.
func (p *originalPluginInstance) stopExactOriginalClient(ctx context.Context, client *plugins.Client) error {
	if client == nil {
		return nil
	}
	ctx = context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
	callback, revoke := p.originalBaseSessionHost(ctx, "stop", nil)
	defer revoke()
	return client.StopWithHost(ctx, callback)
}

// Not a new lifecycle hook: legacy synchronous calls retain their existing
// serial exchange semantics; only negotiated concurrent calls use StartCall.
func (p *originalPluginInstance) callRetainedSession(ctx context.Context, client *plugins.Client, method string, payload json.RawMessage, callback plugins.HostHandler, options plugins.CallOptions, lease *originalRetainedLease, revoke func()) (plugins.RunResult, error) {
	if !client.SupportsConcurrent() {
		out, err := client.CallWithOptions(ctx, method, payload, callback, options)
		revoke()
		p.finishOriginalRetainedCall(lease, nil)
		return out, err
	}
	pending, err := client.StartCall(ctx, method, payload, callback, options)
	if err != nil {
		revoke()
		p.finishOriginalRetainedCall(lease, nil)
		return plugins.RunResult{}, err
	}
	out, err := pending.Wait()
	revoke()
	p.finishOriginalRetainedCall(lease, pending.Drained())
	return out, err
}
