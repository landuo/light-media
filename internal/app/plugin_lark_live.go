package app

import (
	"context"
	"sync"

	lark "light-media/internal/plugins/larkmessager"
)

// Only native App owners populate these process-local leases. They distinguish
// a currently running source thread from a cold, unacknowledged execution. They
// never replace the persisted input, generation or journal checks. Lifecycle
// and configuration transitions deliberately do not use these queue exemptions.
type pluginLarkLive struct {
	mu     sync.Mutex
	cores  map[string]pluginLarkLiveCore
	leases map[string]pluginLarkLiveLease
}
type pluginLarkLiveCore struct {
	ctx  context.Context
	core *pluginLarkCoreExecution
}
type pluginLarkLiveLease struct {
	ctx   context.Context
	lease *pluginLarkLease
}

func (p *PluginStore) larkKeepCore(ctx context.Context, core *pluginLarkCoreExecution) (func(), error) {
	p.larkLive.mu.Lock()
	defer p.larkLive.mu.Unlock()
	if core == nil || core.a == nil || core.a.pluginStore != p || ctx.Err() != nil {
		return nil, lark.ErrDenied
	}
	if p.larkLive.cores == nil {
		p.larkLive.cores = map[string]pluginLarkLiveCore{}
	}
	if _, exists := p.larkLive.cores[core.inbox.ID]; exists {
		return nil, lark.ErrDenied
	}
	p.larkLive.cores[core.inbox.ID] = pluginLarkLiveCore{ctx, core}
	return func() {
		p.larkLive.mu.Lock()
		defer p.larkLive.mu.Unlock()
		if v, ok := p.larkLive.cores[core.inbox.ID]; ok && v.core == core {
			delete(p.larkLive.cores, core.inbox.ID)
		}
	}, nil
}
func (p *PluginStore) larkCoreIsLive(f pluginLarkInbox) bool {
	p.larkLive.mu.Lock()
	defer p.larkLive.mu.Unlock()
	v, ok := p.larkLive.cores[f.ID]
	return ok && v.ctx.Err() == nil && v.core.a.pluginStore == p && v.core.inbox.Instance == f.Instance && v.core.inbox.Generation == f.Generation && v.core.inbox.Revision == f.Revision && v.core.inbox.Job == f.Job && v.core.inbox.ConfigSHA == f.ConfigSHA && v.core.inbox.InputSHA == f.InputSHA
}
func (p *PluginStore) larkKeepLease(ctx context.Context, lease *pluginLarkLease) func() {
	p.larkLive.mu.Lock()
	defer p.larkLive.mu.Unlock()
	if p.larkLive.leases == nil {
		p.larkLive.leases = map[string]pluginLarkLiveLease{}
	}
	id := lease.call.run.ID
	p.larkLive.leases[id] = pluginLarkLiveLease{ctx, lease}
	return func() {
		p.larkLive.mu.Lock()
		defer p.larkLive.mu.Unlock()
		if v, ok := p.larkLive.leases[id]; ok && v.lease == lease {
			delete(p.larkLive.leases, id)
		}
	}
}
func (p *PluginStore) larkAdmissionIsLive(a pluginLarkAdmission) bool {
	p.larkLive.mu.Lock()
	defer p.larkLive.mu.Unlock()
	v, ok := p.larkLive.leases[a.Run]
	if !ok || v.ctx.Err() != nil || v.lease.call.a.pluginStore != p {
		return false
	}
	run := v.lease.call.run
	return v.lease.owner.instance == a.Instance && v.lease.owner.generation == a.Generation && larkAdmissionSame(a, run, v.lease.call.record.Method)
}

// Receipt finalization may outlive request cancellation while the native core
// still holds this exact lease. It may persist an outcome, never dispatch new I/O.
func (p *PluginStore) larkCoreIsHeld(core *pluginLarkCoreExecution) bool {
	p.larkLive.mu.Lock()
	defer p.larkLive.mu.Unlock()
	if core == nil {
		return false
	}
	v, ok := p.larkLive.cores[core.inbox.ID]
	return ok && v.core == core && core.a.pluginStore == p
}
