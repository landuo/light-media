package app

import (
	"context"
	"database/sql"
	bf "light-media/internal/plugins/brushflow"
	"strconv"
	"sync"
)

// Native Host time is outside worker time, so a lifecycle writer must drain
// active calls without holding pluginMu or a SQL transaction. Waiting writers
// prevent new reader leases; cancellation never leaves a reservation behind.
type brushFlowGate struct {
	mu               sync.Mutex
	changed          chan struct{}
	readers, writers int
	writer           bool
}

func (g *brushFlowGate) signal() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}
func (g *brushFlowGate) acquire(ctx context.Context, exclusive bool) error {
	g.mu.Lock()
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	if exclusive {
		g.writers++
	}
	for {
		if err := ctx.Err(); err != nil {
			if exclusive {
				g.writers--
				g.signal()
			}
			g.mu.Unlock()
			return err
		}
		if !g.writer && (exclusive && g.readers == 0 || !exclusive && g.writers == 0) {
			if exclusive {
				g.writers--
				g.writer = true
			} else {
				g.readers++
			}
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		g.mu.Lock()
	}
}
func (g *brushFlowGate) release(exclusive bool) {
	g.mu.Lock()
	if exclusive {
		g.writer = false
	} else {
		g.readers--
	}
	g.signal()
	g.mu.Unlock()
}

type brushFlowLiveLease struct {
	d *pluginDeferredCall
	c *brushFlowCall
}

func (l *brushFlowLiveLease) matches(s brushFlowState, r brushFlowAction) bool {
	if l == nil || l.d == nil || l.d.run.ID != r.RunID || l.d.run.InstanceID != s.Instance || l.d.run.Generation != s.Generation || l.d.run.ConfigRevision != r.Revision || l.d.run.Action != r.Action || deferredInputSHA(l.d.run.Input) != r.InputSHA || l.d.record.Method != r.Method {
		return false
	}
	return l.c == nil || !l.c.uncertain.Load()
}
func (o *pluginBrushFlowOwner) hasLive(s brushFlowState, r brushFlowAction) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.live[r.RunID].matches(s, r)
}
func (p *PluginStore) brushFlowRuntimeOwner(instance string, generation int64) *pluginBrushFlowOwner {
	p.brushFlowLiveMu.Lock()
	runtime := p.brushFlowRuntime
	p.brushFlowLiveMu.Unlock()
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	o := runtime.owners[instance+":"+strconv.FormatInt(generation, 10)]
	if o == nil || o.a.pluginStore != p {
		return nil
	}
	return o
}
func (p *PluginStore) brushFlowWithLiveState(s brushFlowState) brushFlowState {
	o := p.brushFlowRuntimeOwner(s.Instance, s.Generation)
	if o == nil {
		return s
	}
	copyRuns := make(map[string]brushFlowAction, len(s.Runs))
	for id, r := range s.Runs {
		if r.Phase == "started" && o.hasLive(s, r) {
			continue
		}
		copyRuns[id] = r
	}
	s.Runs = copyRuns
	return s
}

func (o *pluginBrushFlowOwner) machineSnapshot() *bf.Core {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.machine
}
func (o *pluginBrushFlowOwner) setMachine(core *bf.Core) {
	o.mu.Lock()
	o.machine = core
	o.mu.Unlock()
}
func (o *pluginBrushFlowOwner) runtimeMachine(config bf.Row) (*bf.Core, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.machine == nil {
		cfg, err := bf.LoadApplied(config)
		if err != nil {
			return nil, err
		}
		o.machine = bf.NewReadProjection(o, cfg)
	}
	return o.machine, nil
}

func (a *App) brushFlowBackgroundRun(ctx context.Context, run PluginRun) bool {
	if run.Action != "task" && run.Action != "service" {
		return false
	}
	in, err := a.pluginStore.Installation(ctx, run.InstanceID)
	return err == nil && in.PluginID == bf.PluginID && in.Version == bf.Version && run.Version == bf.Version && in.Generation == run.Generation && in.Installed && in.DesiredEnabled
}

// Only an already accepted run with its exact live owner lease can follow a
// compatible, authenticated source API config publication. This never rebases a
// queued/prepared invocation, changes its immutable input, or revives a cold run.
func (p *PluginStore) brushFlowAcceptedRevisionTx(ctx context.Context, tx *sql.Tx, record pluginDeferredRecord, run PluginRun) error {
	if record.Plugin != bf.PluginID || record.Version != bf.Version || record.Phase != "started" {
		return brushFlowEvidenceError()
	}
	s, _, err := p.brushFlowRead(ctx, tx, record.Instance, record.Generation)
	if err != nil {
		return err
	}
	r, ok := s.Runs[run.ID]
	o := p.brushFlowRuntimeOwner(record.Instance, record.Generation)
	if !ok || o == nil || !o.hasLive(s, r) || r.Phase != "started" || r.RunID != record.Run || r.Action != record.Action || r.Revision != record.Revision || r.InputSHA != record.InputSHA || r.Method != record.Method {
		return brushFlowEvidenceError()
	}
	var current int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id=? AND i.version=? AND i.generation=? AND i.installed=1 AND i.desired_enabled=1 AND v.revision=?`, record.Instance, bf.PluginID, bf.Version, record.Generation, r.EffectiveRevision).Scan(&current)
	if err != nil {
		return err
	}
	if current != 1 || s.AppliedRevision != r.EffectiveRevision {
		return brushFlowEvidenceError()
	}
	return brushFlowAdmissionState(ctx, p, s)
}

// Prepared is a durable proof that the compiled SDK's private handshake never
// crossed the atomic started+owner-fact transaction. Missing evidence is not a
// zero-effect proof. This also works for a retired generation without changing
// its authenticated root or reviving its owner.
func (p *PluginStore) brushFlowPreparedNoEffect(ctx context.Context, record pluginDeferredRecord, run PluginRun) error {
	if record.Plugin != bf.PluginID || record.Version != bf.Version || record.Phase != "prepared" || record.Run != run.ID || record.Instance != run.InstanceID || record.Generation != run.Generation || record.Revision != run.ConfigRevision || record.Action != run.Action || record.InputSHA != deferredInputSHA(run.Input) {
		return brushFlowEvidenceError()
	}
	method, payload, err := brushFlowDeferredSpec(run)
	if err != nil || method != record.Method || !deferredPayloadEqual(record.Raw, payload) {
		return brushFlowEvidenceError()
	}
	s, _, err := p.brushFlowRead(ctx, p.store.DB, run.InstanceID, run.Generation)
	if err != nil {
		return err
	}
	if _, exists := s.Runs[run.ID]; exists {
		return brushFlowEvidenceError()
	}
	return nil
}
