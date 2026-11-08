package app

import (
	"context"
)

// The host selects an actual source call's owner. These are lifetime stages,
// never worker-supplied ownership strings, jobs, executors or query handles.
type originalSourceInvocationKind uint8

const (
	originalSourceScheduler originalSourceInvocationKind = iota + 1
	originalSourceEvent
	originalSourceAPI
)

// Call only after real class/route/authority validation. New ordinary requests
// cannot borrow a retired object, while this accepted call and its real Submit
// descendants keep the exact captured Client until they physically return.
func (p *originalPluginInstance) beginOriginalBoundEntry(ctx context.Context, kind originalSourceInvocationKind) (context.Context, func(), error) {
	if kind != originalSourceEvent && kind != originalSourceAPI {
		return nil, nil, bad("PLUGIN_CAPABILITY_DENIED", "入口来源无效")
	}
	if err := p.validateOriginalPublicServiceClient(); err != nil {
		return nil, nil, err
	}
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	lease, _, err := p.reserveOriginalSourceSubmission(nil, "", client, kind)
	if err != nil {
		return nil, nil, err
	}
	entryCtx := context.WithValue(context.WithoutCancel(ctx), originalSubmittedObjectKey{}, lease)
	// This is a fresh ordinary entry, never its sender's scheduler invocation.
	entryCtx = context.WithValue(entryCtx, originalServiceCallKey{}, (*originalServiceCall)(nil))
	return entryCtx, func() { p.releaseOriginalSubmission(lease); p.app.wg.Done() }, nil
}

// Source stops the plugin manager before joining EventManager's ThreadHelper
// pool. Seal external API root admission first. Registered events remain
// eligible during manager hooks while their target class is still enabled.
// Their root admission seals only after PM stops and before the event join.
// Never wait on App.wg here.
// Any accepted registered entry requires manager hooks before module/message
// shutdown. Only the separate event wait group is joined at that stage.
func (a *App) beginOriginalBoundEntriesShutdown() bool {
	global := &a.pluginOriginalBackground
	global.mu.Lock()
	defer global.mu.Unlock()
	global.sourceEntriesClosing = true
	// Source close/stop_service may themselves use messages even when no
	// request is currently active. Opted-in loaded objects need the same
	// manager-before-message order; legacy Go objects keep their old mode.
	owned := false
	a.pluginRunningInstances.Range(func(_, value any) bool {
		object := value.(*originalPluginInstance)
		owned = object.manifest.ManagedPublicServices || object.manifest.ManagedPrivateScheduler
		return !owned
	})
	a.pluginSubmittedObjects.Range(func(key, _ any) bool {
		p := key.(*originalPluginInstance)
		p.submitted.mu.Lock()
		for lease := range p.submitted.leases {
			owned = owned || (lease.sourceKind == originalSourceEvent || lease.sourceKind == originalSourceAPI) && lease.active.Load()
		}
		p.submitted.mu.Unlock()
		return !owned
	})
	return owned
}
func (a *App) drainOriginalBoundEvents() {
	global := &a.pluginOriginalBackground
	global.mu.Lock()
	global.sourceEventsClosing = true
	global.mu.Unlock()
	a.log("INFO", "plugins", "events_stopping", "正在停止事件处理...", "", "")
	a.pluginOriginalBackground.eventLifetimes.Wait()
	a.log("INFO", "plugins", "events_stopped", "事件处理停止完成", "", "")
}
