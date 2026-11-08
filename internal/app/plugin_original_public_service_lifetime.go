package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"slices"
	"time"
)

// A public service is a bound method accepted by the existing source scheduler.
// Only the host admission path can create a root; no worker ownership string
// can request this lifetime or borrow progress access from that root.
func (p *originalPluginInstance) validateOriginalPublicServiceClient() error {
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if !p.manifest.ManagedPublicServices {
		return bad("PLUGIN_SERVICE_STOP_UNSUPPORTED", "公共服务未声明来源调用保留")
	}
	if slices.Contains(p.manifest.Capabilities, plugins.CleanupSubmitCapability) {
		return bad("PLUGIN_SERVICE_CLEANUP_UNSUPPORTED", "公共服务保留不能与清理续段组合")
	}
	if client == nil || !client.SupportsPublicServiceStop() {
		return bad("PLUGIN_SERVICE_STOP_UNSUPPORTED", "公共服务需要协商保留调用的停止能力")
	}
	return nil
}

func (p *originalPluginInstance) reserveOriginalPublicService() (*originalSubmittedLease, context.Context, error) {
	if err := p.validateOriginalPublicServiceClient(); err != nil {
		return nil, nil, err
	}
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	return p.reserveOriginalSubmissionKind(nil, "", client)
}

// This exact object's actual Client and a still-active, physically accepted
// root/descendant are required. Installation removal or a newer same-ID object
// does not revoke the bound source call, nor does it grant the replacement this
// old authority. Other capability/resource checks still run normally.
func (p *originalPluginInstance) originalPublicServiceScopeAllowed(ctx context.Context) bool {
	lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	if !p.manifest.ManagedPublicServices || lease == nil || lease.object != p || !lease.publicService || !lease.active.Load() {
		return false
	}
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	state := &p.submitted
	state.mu.Lock()
	defer state.mu.Unlock()
	_, exists := state.leases[lease]
	return exists && lease.active.Load() && !state.sealed && lease.client == state.publicClient && lease.client == client && client.SupportsPublicServiceStop()
}

func (p *originalPluginInstance) beginOriginalPublicServiceStop() bool {
	state := &p.submitted
	state.mu.Lock()
	defer state.mu.Unlock()
	state.publicClosing = true
	retained := false
	for lease := range state.leases {
		retained = retained || lease.publicService && lease.active.Load()
	}
	if retained {
		state.publicRetiring = true
	}
	return retained
}

// Ordinary Submit retains the historical immediate revocation. Only genuinely
// accepted source-owned public/private calls survive the corresponding hooks.
func (p *originalPluginInstance) closeOriginalUnretainedSubmissions() {
	p.submitted.mu.Lock()
	defer p.submitted.mu.Unlock()
	for lease := range p.submitted.leases {
		if !lease.privateScheduler && !lease.publicService {
			lease.active.Store(false)
			lease.cancel()
		}
	}
}

func (p *originalPluginInstance) stopOriginalPublicServiceMethods(ctx context.Context) error {
	p.submitted.mu.Lock()
	client := p.submitted.publicClient
	p.submitted.mu.Unlock()
	if client == nil {
		return bad("PLUGIN_NOT_LOADED", "公共服务会话已结束")
	}
	for _, method := range []string{"close", "stop_service"} {
		if !slices.Contains(p.manifest.Methods, method) {
			continue
		}
		phaseCtx := context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client, businessHook: true})
		callback, revoke := p.originalBaseSessionHost(phaseCtx, "stop", nil)
		_, err := client.CallPublicServiceStop(phaseCtx, method, json.RawMessage(`{}`), callback)
		revoke()
		if err != nil {
			return err
		}
	}
	return nil
}

// Call only after source hooks and logical manager removal. Completion before
// this stage cannot reap a process while close/stop_service are still running.
func (p *originalPluginInstance) finishOriginalPublicServiceStop() {
	p.submitted.mu.Lock()
	p.submitted.publicRetireReady = true
	retire := p.publicRetirementReadyLocked()
	p.submitted.mu.Unlock()
	if retire {
		p.retireOriginalPublicServiceObject()
	}
}
func (p *originalPluginInstance) publicRetirementReadyLocked() bool {
	state := &p.submitted
	if !state.publicRetiring || !state.publicRetireReady || state.publicRetireStarted {
		return false
	}
	for lease := range state.leases {
		if lease.publicService && lease.active.Load() {
			return false
		}
	}
	state.publicRetireStarted = true
	state.sealed = true
	return true
}

// The object has left the manager and its final real service phase returned.
// Reap the captured Client, never a replacement field, and do not invoke source
// close/stop_service (or the worker stop business handler) a second time.
func (p *originalPluginInstance) retireOriginalPublicServiceObject() {
	p.closeOriginalSubmissions()
	p.submitted.mu.Lock()
	client := p.submitted.publicClient
	p.submitted.mu.Unlock()
	if client == nil {
		return
	}
	p.sessionMu.Lock()
	if p.session == client {
		p.session = nil
	}
	p.sessionMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.CloseTransport(ctx); err != nil {
		p.app.log("ERROR", "plugins", "service_retire_failed", "已停止插件对象清理失败", "", "")
	}
}
