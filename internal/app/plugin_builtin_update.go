package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

type pluginUpdateRequest struct {
	FromVersion   string `json:"from_version"`
	TargetVersion string `json:"target_version"`
	Generation    int64  `json:"generation"`
	Revision      int64  `json:"revision"`
}
type pluginUpdateResult struct {
	ReviewPending      bool       `json:"review_pending,omitempty"`
	Changed            bool       `json:"changed"`
	MigrationCommitted bool       `json:"migration_committed"`
	Version            string     `json:"version"`
	Generation         int64      `json:"generation"`
	Initialization     *PluginRun `json:"initialization,omitempty"`
}

func pluginUpdateTarget(in PluginInstallation, request pluginUpdateRequest) (plugins.Manifest, builtin.UpdateMigration, error) {
	target, _, ok := builtin.Lookup(in.PluginID)
	if !ok || target.PluginID != in.PluginID || target.Validate() != nil || request.TargetVersion != target.Version {
		return target, builtin.UpdateMigration{}, bad("PLUGIN_UPDATE_TARGET_UNAVAILABLE", "更新目标不是当前已编译的插件实现")
	}
	if in.Version == target.Version {
		return target, builtin.UpdateMigration{}, nil
	}
	migration, ok := builtin.UpdateMigrationFor(in.PluginID, in.Version)
	if !ok || migration.FromVersion != in.Version || migration.ToVersion != target.Version || migration.ValidateConfig == nil {
		return target, migration, bad("PLUGIN_UPDATE_MIGRATION_UNAVAILABLE", "尚未实现此版本的安全迁移，未改动插件数据")
	}
	if request.FromVersion != in.Version || request.Generation != in.Generation || request.Revision < 0 {
		return target, migration, bad("PLUGIN_SNAPSHOT_STALE", "插件版本或代际已变化，请刷新后更新")
	}
	return target, migration, nil
}

func (p *PluginStore) updateBuiltin(ctx context.Context, instance string, request pluginUpdateRequest) (out pluginUpdateResult, returnErr error) {
	if err := p.checkPluginFormWrite(ctx, instance, pluginFormWriteLifecycle); err != nil {
		return out, err
	}
	in, err := p.pluginLifecycleInstallation(ctx, p.store.DB, instance)
	if err != nil {
		return out, err
	}
	if !in.Installed {
		return out, &APIError{"NOT_FOUND", "插件已卸载", http.StatusNotFound}
	}
	if _, err = pluginLifecycleState(in); err != nil {
		return out, err
	}
	target, migration, err := pluginUpdateTarget(in, request)
	if err != nil {
		return out, err
	}
	if in.Version == target.Version {
		// A repeated/current-version request is read-only, even while work is active.
		return pluginUpdateResult{Version: in.Version, Generation: in.Generation}, nil
	}
	// Reuse the same host-only quiescence primitive as Reset. No reset deletion,
	// owner recreation, install upsert, or business hook is called by this path.
	ctx, admission, err := p.beginResetAdmission(ctx, in, false)
	if err != nil {
		return out, err
	}
	defer func() {
		returnErr = admission.finish(returnErr)
		if returnErr != nil {
			out = pluginUpdateResult{}
		}
	}()
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer admission.rollback(tx)
	if err = p.requireResetAdmissionTx(ctx, tx, instance); err != nil {
		return out, err
	}
	current, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		return out, err
	}
	if current.Version != in.Version || current.Generation != in.Generation || current.PluginID != in.PluginID || current.DesiredEnabled != in.DesiredEnabled {
		return out, bad("PLUGIN_SNAPSHOT_STALE", "插件状态已变化")
	}
	var revision int64
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instance).Scan(&revision, &sealed)
	raw := json.RawMessage(`{}`)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		raw, err = p.unseal("config", instance, "main", sealed)
		if err != nil {
			return out, err
		}
	}
	if revision != request.Revision {
		return out, bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请刷新后更新")
	}
	if err = migration.ValidateConfig(append(json.RawMessage(nil), raw...)); err != nil {
		return out, bad("PLUGIN_UPDATE_CONFIG_INVALID", "原配置不能安全自动迁移，或包含待确认的一次性操作，未更新")
	}
	reviewRequired := migration.RequiresExplicitSave != nil && migration.RequiresExplicitSave(append(json.RawMessage(nil), raw...))
	// Retain only the authenticated host instance marker. Executable metadata is
	// always the compiled target, never an HTTP body or an old stored manifest.
	var oldFields, targetFields map[string]json.RawMessage
	if json.Unmarshal(current.Manifest, &oldFields) != nil {
		return out, pluginLifecycleInvalid()
	}
	targetRaw, err := json.Marshal(target)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(targetRaw, &targetFields) != nil {
		return out, pluginLifecycleInvalid()
	}
	if marker, ok := oldFields["host_instance"]; ok {
		targetFields["host_instance"] = marker
	}
	targetRaw, err = json.Marshal(targetFields)
	if err != nil {
		return out, err
	}
	next := current
	next.Version = target.Version
	next.ManifestSchemaVersion = target.SchemaVersion
	next.Generation++
	next.Manifest = targetRaw
	next.Manifest, err = pluginLifecycleManifest(next, pluginLifecycleCold)
	if err != nil {
		return out, err
	}
	if reviewRequired {
		next.Manifest, err = pluginUpdateReviewManifest(next, pluginLifecycleCold, revision, deferredInputSHA(raw))
		if err != nil {
			return out, err
		}
	}
	manifestSealed, err := p.seal("manifest", instance, "main", next.Manifest)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET version=?,manifest_schema=?,manifest=?,generation=?,updated_at=? WHERE instance_id=? AND version=? AND generation=?`, next.Version, next.ManifestSchemaVersion, manifestSealed, next.Generation, now(), instance, current.Version, current.Generation)
	if err != nil {
		return out, err
	}
	// Config/KV/lineage/folder bytes are intentionally untouched for this declared
	// preserve-data conversion. Old queued work is retired in the same transaction.
	for _, statement := range []string{
		`UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`,
		`UPDATE plugin_schedules SET active=0 WHERE instance_id=?`,
		`UPDATE plugin_event_batches SET state='canceled' WHERE instance_id=? AND run_id IS NULL`,
		`UPDATE plugin_runs SET state='canceled',error_code='PLUGIN_IMPLEMENTATION_UPDATED' WHERE instance_id=? AND state='pending'`,
	} {
		if _, err = tx.ExecContext(ctx, statement, instance); err != nil {
			return out, err
		}
	}
	out = pluginUpdateResult{ReviewPending: reviewRequired, Changed: true, MigrationCommitted: true, Version: next.Version, Generation: next.Generation}
	if next.DesiredEnabled && !reviewRequired {
		run, e := p.enqueueRunTx(ctx, tx, instance, "init", nil)
		if e != nil {
			return out, e
		}
		out.Initialization = &run
	}
	if err = admission.commit(tx); err != nil {
		return pluginUpdateResult{}, err
	}
	return out, nil
}

func (a *App) pluginUpdateHTTP(w http.ResponseWriter, r *http.Request, instance string) (any, error) {
	var request pluginUpdateRequest
	if err := decodePluginBody(w, r, &request); err != nil {
		return nil, err
	}
	result, err := a.pluginStore.updateBuiltin(r.Context(), instance, request)
	if err == nil && result.Initialization != nil {
		a.wakePlugins()
	}
	return result, err
}
