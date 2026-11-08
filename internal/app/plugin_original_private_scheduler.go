package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"slices"
)

func (p *originalPluginInstance) originalPrivateSchedulerScopeAllowed(ctx context.Context) bool {
	lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	return lease != nil && lease.object == p && lease.privateScheduler && p.originalSubmittedScopeAllowed(ctx)
}

// Only this object's explicitly opted-in, actually accepted scheduler leases
// are retained. Ordinary Submit retains its old immediate revocation behavior.
func (p *originalPluginInstance) drainOriginalPrivateScheduler(ctx context.Context) bool {
	if !p.manifest.ManagedPrivateScheduler || slices.Contains(p.manifest.Capabilities, plugins.CleanupSubmitCapability) {
		return false
	}
	state := &p.submitted
	state.mu.Lock()
	if state.privateStopDone != nil {
		done := state.privateStopDone
		state.mu.Unlock()
		<-done
		return true
	}
	state.privateClosing = true
	owned := false
	for lease := range state.leases {
		owned = owned || lease.privateScheduler && lease.active.Load()
	}
	if !owned {
		p.stopping.Store(true)
		state.mu.Unlock()
		return false
	}
	done := make(chan struct{})
	state.privateStopDone = done
	state.privateMethodsRan = true
	p.stopping.Store(true)
	for lease := range state.leases {
		if !lease.privateScheduler && !lease.publicService {
			lease.active.Store(false)
			lease.cancel()
		}
	}
	state.mu.Unlock()
	// The source-owned callback must retain ordinary Submit/notice authority while
	// close then stop_service cancel future dates and join already-active work.
	// Never hold state, operation, or callback locks over those business methods.
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	var stopErr error
	if client == nil {
		stopErr = bad("PLUGIN_NOT_LOADED", "私有调度器会话已结束")
	} else {
		for _, method := range []string{"close", "stop_service"} {
			if !slices.Contains(p.manifest.Methods, method) {
				continue
			}
			phaseCtx := context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client, businessHook: true})
			callback, revoke := p.originalBaseSessionHost(phaseCtx, "stop", nil)
			_, stopErr = client.CallPrivateSchedulerStop(phaseCtx, method, json.RawMessage(`{}`), callback)
			revoke()
			if stopErr != nil {
				break
			}
		}
	}
	if stopErr == nil {
		state.privateLifetimes.Wait()
	} else {
		// Keep the pre-existing private error cancellation: only real public
		// source leases are exempt while their old Client is retained.
		state.mu.Lock()
		for lease := range state.leases {
			if !lease.publicService {
				lease.active.Store(false)
				lease.cancel()
			}
		}
		state.mu.Unlock()
	}
	state.mu.Lock()
	state.privateStopErr = stopErr
	close(done)
	state.mu.Unlock()
	return true
}

// Global Close drains opted-in private owners while the original background
// manager and notice resources are still open. The following ordinary cleanup
// fence remains responsible for every session/process and old non-owned task.
// Seal new private roots before taking the backup snapshot. Real accepted
// descendants still belong to the old root, and its actual stop follows backup.
func (a *App) beginOriginalPrivateSchedulerDrain() bool {
	a.pluginOriginalBackground.mu.Lock()
	a.pluginOriginalBackground.privateSchedulerClosing = true
	a.pluginOriginalBackground.mu.Unlock()
	owned := false
	a.pluginSubmittedObjects.Range(func(key, _ any) bool {
		p := key.(*originalPluginInstance)
		if !p.manifest.ManagedPrivateScheduler || !originalObjectStillLoaded(p) {
			return true
		}
		p.submitted.mu.Lock()
		for lease := range p.submitted.leases {
			owned = owned || lease.privateScheduler && lease.active.Load()
		}
		p.submitted.mu.Unlock()
		return !owned
	})
	return owned
}

// Installation metadata can advance before stop begins on force update. The
// already-accepted private callback still belongs to this loaded old object and
// its actual Client until the existing manager replaces/revokes them. No ordinary,
// returned, foreign-object or replacement-client scope gains snapshot authority.
func (p *originalPluginInstance) originalPrivateSchedulerSnapshotAllowed(ctx context.Context, current PluginInstallation) bool {
	if !p.manifest.ManagedPrivateScheduler || current.InstanceID != p.installation.InstanceID || current.PluginID != p.installation.PluginID || !current.Installed || current.Generation <= p.installation.Generation || !originalObjectStillLoaded(p) {
		return false
	}
	lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
	if lease == nil || lease.object != p || !lease.privateScheduler {
		return false
	}
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if client == nil || !client.SupportsPrivateSchedulerStop() {
		return false
	}
	state := &p.submitted
	state.mu.Lock()
	defer state.mu.Unlock()
	_, present := state.leases[lease]
	return present && lease.active.Load() && lease.client == client && !state.sealed
}
