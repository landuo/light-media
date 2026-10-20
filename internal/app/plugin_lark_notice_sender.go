package app

import (
	"context"
	"database/sql"
	"errors"

	"light-media/internal/notice"
	"light-media/internal/plugins/builtin"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	lark "light-media/internal/plugins/larkmessager"
)

type pluginLarkQueueKey struct{}

func (a *App) ensureLarkInitialized(ctx context.Context, in PluginInstallation) error {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE instance_id=? AND generation=? AND action IN ('init','reload') AND config_revision=(SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main') AND state IN ('pending','running') ORDER BY created_at,id`, in.InstanceID, in.Generation, in.InstanceID)
	if e != nil {
		return e
	}
	var runs []PluginRun
	for rows.Next() {
		run, err := a.pluginStore.scanPluginRun(rows)
		if err != nil {
			e = err
			break
		}
		runs = append(runs, run)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	for _, run := range runs {
		if _, e = a.executeLarkBoundRun(ctx, run, lark.ConfigureCapability); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) larkMessageModules(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
	installed, e := a.pluginStore.Installations(ctx)
	if e != nil {
		return 0, e
	}
	modules := 0
	for _, in := range installed {
		if in.PluginID != "LarkMessager" || !in.DesiredEnabled {
			continue
		}
		manifest, _, exists := builtin.Lookup(in.PluginID)
		if !exists || manifest.Version != in.Version {
			return modules, notice.ErrDependency
		}
		config, e := a.pluginStore.Config(ctx, in.InstanceID)
		if e != nil {
			return modules, e
		}
		v, e := lark.DecodeSourceObject(config.Value)
		if e != nil {
			return modules, e
		}
		state, e := lark.ConfigFromTrusted(v)
		if e != nil {
			return modules, e
		}
		if !state.Enabled {
			continue
		} // exact get_module enabled/client predicate.
		modules++
		if e = a.ensureLarkInitialized(ctx, in); e != nil {
			return modules, larkNoticeSenderError(e)
		}
		run, e := a.enqueueLarkNotice(ctx, in.InstanceID, q.ID)
		if e != nil {
			return modules, larkNoticeSenderError(e)
		}
		if _, e = a.executeLarkSourceRun(ctx, run); e != nil {
			return modules, larkNoticeSenderError(e)
		}
	}
	return modules, nil
}
func larkNoticeSenderError(e error) error {
	if pluginErrorCodeInternal(e) == "LARK_UNCONFIRMED" || pluginErrorCodeInternal(e) == "LARK_EVIDENCE_MISSING" || errors.Is(e, lark.ErrUnknown) {
		return seedhost.ErrUnknown
	}
	if errors.Is(e, sql.ErrNoRows) {
		return larkEvidenceError()
	}
	return e
}
