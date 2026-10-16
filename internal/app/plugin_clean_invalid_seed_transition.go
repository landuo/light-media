package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

type pluginCleanInvalidSeedPrepared struct {
	o        *pluginCleanInvalidSeedOwner
	id, kind string
	from     seedhost.Binding
	once     sync.Once
}

func cleanSeedNoUnknown(s seedhost.State) error {
	for _, r := range s.Runs {
		if r.Phase == "started" || r.Phase == "unknown" {
			return seedhost.ErrUnknown
		}
	}
	for _, e := range s.Effects {
		if e.Phase == "started" || e.Phase == "unknown" || e.Phase == "fs_confirmed" {
			return seedhost.ErrUnknown
		}
	}
	return nil
}
func (a *App) prepareCleanInvalidSeedTransition(ctx context.Context, i PluginInstallation, kind string) (*pluginCleanInvalidSeedPrepared, error) {
	all, err := a.prepareCleanInvalidSeedTransitions(ctx, []PluginInstallation{i}, kind)
	if err != nil {
		return nil, err
	}
	if len(all) != 1 {
		return nil, seedhost.ErrUnsafe
	}
	return all[0], nil
}
func (a *App) prepareCleanInvalidSeedTransitions(ctx context.Context, installations []PluginInstallation, kind string) ([]*pluginCleanInvalidSeedPrepared, error) {
	if kind != "init" && kind != "reload" && kind != "reset" && kind != "uninstall" {
		return nil, seedhost.ErrUnsafe
	}
	byID := map[string]PluginInstallation{}
	ids := []string{}
	for _, i := range installations {
		if i.PluginID == "CleanInvalidSeed" && i.Installed {
			if _, ok := byID[i.InstanceID]; ok {
				return nil, seedhost.ErrUnsafe
			}
			byID[i.InstanceID] = i
			ids = append(ids, i.InstanceID)
		}
	}
	sort.Strings(ids)
	out := []*pluginCleanInvalidSeedPrepared{}
	fail := func(cause error) ([]*pluginCleanInvalidSeedPrepared, error) {
		for n := len(out) - 1; n >= 0; n-- {
			fresh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cause = errors.Join(cause, out[n].resolve(fresh, false))
			cancel()
			out[n].release()
		}
		return nil, cause
	}
	for _, id := range ids {
		o, err := a.cleanSeedOwner(ctx, byID[id])
		if err != nil {
			return fail(err)
		}
		if err = o.lock(ctx); err != nil {
			return fail(err)
		}
		p := &pluginCleanInvalidSeedPrepared{o: o, id: idForCleanSeedTransition(), kind: kind}
		err = func() error {
			a.pluginLifecycleMu.Lock()
			defer a.pluginLifecycleMu.Unlock()
			a.pluginMu.Lock()
			defer a.pluginMu.Unlock()
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			var generation, revision int64
			var installed bool
			if err = tx.QueryRowContext(ctx, `SELECT i.generation,i.installed,COALESCE(v.revision,0) FROM plugin_installations i LEFT JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id='CleanInvalidSeed' AND i.version='2.1'`, o.instance).Scan(&generation, &installed, &revision); err != nil {
				return err
			}
			if !installed || generation != o.generation {
				return seedhost.ErrUnsafe
			}
			p.from = seedhost.Binding{InstanceID: o.instance, Generation: generation, Revision: revision}
			if err = o.j.updateTx(ctx, tx, func(s *seedhost.State, m *pluginCleanInvalidSeedMeta) error {
				if err := cleanSeedNoUnknown(*s); err != nil {
					return err
				}
				if m.Transition != nil && m.Transition.Phase != "applied" {
					return seedhost.ErrUnknown
				}
				m.Transition = &pluginCleanInvalidSeedTransition{Version: 1, ID: p.id, Kind: kind, Phase: "prepared", From: p.from}
				return nil
			}); err != nil {
				return err
			}
			// Source StopService has no file queue. Pausing actual core schedules
			// is enough to join admission; retain source plans for an exact abort.
			if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND group_key='clean_seed'`, now(), o.instance); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err != nil {
			o.unlock()
			return fail(err)
		}
		out = append(out, p)
	}
	return out, nil
}
func idForCleanSeedTransition() string { return id("cst_") }
func (p *pluginCleanInvalidSeedPrepared) release() {
	if p != nil {
		p.once.Do(p.o.unlock)
	}
}
func (p *pluginCleanInvalidSeedPrepared) validateConfig(ctx context.Context, raw json.RawMessage, kind string) error {
	if p == nil || p.kind != kind || (kind != "init" && kind != "reload") {
		return seedhost.ErrUnsafe
	}
	if _, err := cleanSeedConfigMap(raw); err != nil {
		return err
	}
	s, err := p.o.j.Read(ctx)
	if err != nil {
		return err
	}
	warm := json.RawMessage(nil)
	if p.o.h != nil {
		warm, err = p.o.h.RuntimeConfig(ctx)
		if err != nil {
			return err
		}
	}
	effective, err := cleanSeedEffectiveConfig(warm, s.Config, raw, kind)
	if err != nil {
		return err
	}
	p.o.a.downloadMu.Lock()
	defer p.o.a.downloadMu.Unlock()
	p.o.a.importMu.Lock()
	defer p.o.a.importMu.Unlock()
	_, err = cleanSeedRoots(p.o.a.Settings(), effective)
	return err
}

// CleanInvalidSeed StopService only removes schedules; there is no native
// deletion queue to execute here. Preparation already paused actual admission.
func (p *pluginCleanInvalidSeedPrepared) drain(ctx context.Context) error {
	if p == nil {
		return seedhost.ErrUnsafe
	}
	s, err := p.o.j.Read(ctx)
	if err != nil {
		return err
	}
	return cleanSeedNoUnknown(s)
}
func (p *pluginCleanInvalidSeedPrepared) publishTx(ctx context.Context, tx *sql.Tx, to seedhost.Binding, runID string) error {
	if p == nil || to.InstanceID != p.o.instance {
		return seedhost.ErrUnsafe
	}
	return p.o.j.updateTx(ctx, tx, func(s *seedhost.State, m *pluginCleanInvalidSeedMeta) error {
		t := m.Transition
		if t == nil || t.ID != p.id || t.Phase != "prepared" || t.From != p.from || t.Kind != p.kind {
			return seedhost.ErrUnsafe
		}
		if err := cleanSeedNoUnknown(*s); err != nil {
			return err
		}
		if p.kind == "reset" || p.kind == "uninstall" {
			if to.Generation != p.from.Generation+1 || to.Revision != 0 || runID != "" {
				return seedhost.ErrUnsafe
			}
			var generation int64
			var installed, enabled bool
			if err := tx.QueryRowContext(ctx, `SELECT generation,installed,desired_enabled FROM plugin_installations WHERE instance_id=?`, p.o.instance).Scan(&generation, &installed, &enabled); err != nil {
				return err
			}
			if generation != to.Generation || enabled || (p.kind == "reset" && !installed) || (p.kind == "uninstall" && installed) {
				return seedhost.ErrUnsafe
			}
		} else {
			if to.Generation != p.from.Generation || to.Revision < 1 || runID == "" {
				return seedhost.ErrUnsafe
			}
			var current int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id='CleanInvalidSeed' AND i.version='2.1' AND i.installed=1 AND i.desired_enabled=1 AND i.generation=? AND v.revision=?`, p.o.instance, to.Generation, to.Revision).Scan(&current); err != nil {
				return err
			}
			if current != 1 {
				return seedhost.ErrUnsafe
			}
			run, err := p.o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, runID))
			if err != nil {
				return err
			}
			if run.InstanceID != p.o.instance || run.Version != "2.1" || run.Generation != to.Generation || run.ConfigRevision != to.Revision || run.Action != p.kind || run.State != "pending" {
				return seedhost.ErrUnsafe
			}
			var sealed []byte
			var revision int64
			if err = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, p.o.instance).Scan(&revision, &sealed); err != nil {
				return err
			}
			if revision != to.Revision {
				return seedhost.ErrUnsafe
			}
			config, err := p.o.a.pluginStore.unseal("config", p.o.instance, "main", sealed)
			if err != nil {
				return err
			}
			t.ConfigSHA = tokenHash(string(config))
		}
		t.To, t.RunID, t.Phase = to, runID, "published"
		for id, tr := range s.Triggers {
			if tr.Phase == "queued" {
				// Explicit reload keeps the same core revision. Retire only this
				// private, still-pending admission in the publication transaction;
				// a running invocation may already have committed source effects.
				if tr.Queued == nil || tr.QueuedRunID != tr.Queued.RunID || tr.Binding != tr.Queued.Binding {
					return seedhost.ErrUnsafe
				}
				actual, err := p.o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, tr.QueuedRunID))
				if err != nil {
					return err
				}
				actualInvocation, err := cleanSeedInvocation(actual)
				if err != nil || actualInvocation != *tr.Queued {
					return seedhost.ErrUnsafe
				}
				if actual.State == "pending" {
					if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code='PLUGIN_SNAPSHOT_STALE',updated_at=? WHERE id=? AND state='pending'`, now(), actual.ID); err != nil {
						return err
					}
					if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='canceled' WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=? AND state='queued'`, actual.ID, actual.InstanceID, actual.Generation, actual.ConfigRevision); err != nil {
						return err
					}
				}
				tr.Phase = "canceled"
				s.Triggers[id] = tr
			}
			if (p.kind == "reset" || p.kind == "uninstall") && tr.Phase == "ready" {
				tr.Phase = "canceled"
				s.Triggers[id] = tr
			}
		}
		return nil
	})
}
func (p *pluginCleanInvalidSeedPrepared) resolve(ctx context.Context, committed bool) error {
	if p == nil {
		return seedhost.ErrUnsafe
	}
	tx, err := p.o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, m, err := p.o.j.read(ctx, tx)
	if err != nil {
		return err
	}
	t := m.Transition
	if t == nil || t.ID != p.id || t.From != p.from || t.Kind != p.kind {
		return seedhost.ErrUnknown
	}
	if t.Phase == "published" || t.Phase == "applied" {
		if !committed {
			return bad("PLUGIN_TRANSITION_UNCERTAIN", "配置提交结果需要核对，已保留插件执行状态")
		}
		if p.kind != "reset" && p.kind != "uninstall" {
			return nil
		}
		var generation int64
		var installed, enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT generation,installed,desired_enabled FROM plugin_installations WHERE instance_id=?`, p.o.instance).Scan(&generation, &installed, &enabled); err != nil {
			return err
		}
		if generation != t.To.Generation || enabled || (p.kind == "reset" && !installed) || (p.kind == "uninstall" && installed) {
			return seedhost.ErrUnknown
		}
		if err = p.o.j.updateTx(ctx, tx, func(_ *seedhost.State, meta *pluginCleanInvalidSeedMeta) error {
			if meta.Transition == nil || meta.Transition.ID != p.id {
				return seedhost.ErrUnsafe
			}
			meta.Transition.Phase = "applied"
			meta.Closed = true
			return nil
		}); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		r := &p.o.a.pluginCleanInvalidSeed
		r.mu.Lock()
		if r.owners[p.o.instance] == p.o {
			delete(r.owners, p.o.instance)
		}
		r.mu.Unlock()
		return nil
	}
	if committed || t.Phase != "prepared" {
		return seedhost.ErrUnknown
	}
	var generation, revision int64
	var installed bool
	if err = tx.QueryRowContext(ctx, `SELECT i.generation,i.installed,COALESCE(v.revision,0) FROM plugin_installations i LEFT JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, p.o.instance).Scan(&generation, &installed, &revision); err != nil {
		return err
	}
	if !installed || generation != p.from.Generation || revision != p.from.Revision {
		return seedhost.ErrUnknown
	}
	if err = p.o.j.updateTx(ctx, tx, func(_ *seedhost.State, meta *pluginCleanInvalidSeedMeta) error {
		if meta.Transition == nil || meta.Transition.ID != p.id || meta.Transition.Phase != "prepared" {
			return seedhost.ErrUnknown
		}
		meta.Transition = nil
		return nil
	}); err != nil {
		return err
	}
	if err = p.o.projectSchedulesTx(ctx, tx, s); err != nil {
		return err
	}
	return tx.Commit()
}

// Read-only helper for the existing administrator retry of the same admission.
// The caller may already hold lifecycle/pluginMu; do not acquire either here.
func (a *App) validateCleanInvalidSeedDeferredRetry(ctx context.Context, run PluginRun) error {
	inv, err := cleanSeedInvocation(run)
	if err != nil {
		return err
	}
	d, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if err != nil {
		return err
	}
	if d.Phase != "prepared" {
		return seedhost.ErrUnknown
	}
	i, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		return err
	}
	if cleanSeedInstalled(i) != nil || !i.DesiredEnabled || i.Generation != run.Generation {
		return seedhost.ErrUnsafe
	}
	c, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return err
	}
	if c.Revision != run.ConfigRevision {
		return seedhost.ErrUnsafe
	}
	j := &pluginCleanInvalidSeedJournal{p: a.pluginStore, instance: run.InstanceID, generation: run.Generation}
	var exists int
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_clean_seed_meta'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 && run.Action == "init" {
		return nil
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, m, err := j.read(ctx, tx)
	if err != nil {
		return err
	}
	if err = cleanSeedNoUnknown(s); err != nil {
		return err
	}
	if _, entered := s.Runs[run.ID]; entered {
		return seedhost.ErrUnknown
	}
	if m.Transition != nil && m.Transition.Phase != "applied" {
		if m.Transition.Phase != "published" || m.Transition.RunID != run.ID || m.Transition.To != inv.Binding || m.Transition.Kind != run.Action {
			return seedhost.ErrUnsafe
		}
	} else if s.Binding != (seedhost.Binding{}) && (run.Action == "init" || run.Action == "reload") {
		return seedhost.ErrUnsafe
	}
	return nil
}
