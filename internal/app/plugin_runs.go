package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// PluginRun's public projection is metadata only. Config snapshots, input and
// worker results may contain secrets and are never marshaled by list APIs.
type PluginRun struct {
	Retryable      bool            `json:"retryable,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
	ID             string          `json:"id"`
	InstanceID     string          `json:"instance_id"`
	Version        string          `json:"version"`
	Generation     int64           `json:"generation"`
	ConfigRevision int64           `json:"config_revision"`
	Action         string          `json:"action"`
	State          string          `json:"state"`
	Input          json.RawMessage `json:"-"`
	Result         json.RawMessage `json:"-"`
	ErrorCode      string          `json:"error_code,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

const pluginRunColumns = `id,instance_id,version,generation,config_revision,action,state,input,result,error_code,created_at,updated_at`
const pluginRunListColumns = `id,instance_id,version,generation,config_revision,action,state,input,NULL,error_code,created_at,updated_at`

func (p *PluginStore) InstallAndQueue(ctx context.Context, in PluginInstallation, defaults any) (PluginRun, error) {
	in, err := validatePluginInstallation(in)
	if err != nil {
		return PluginRun{}, err
	}
	raw, err := pluginJSON(defaults, true)
	if err != nil {
		return PluginRun{}, err
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginRun{}, err
	}
	defer tx.Rollback()
	var installed bool
	err = tx.QueryRowContext(ctx, `SELECT installed FROM plugin_installations WHERE instance_id=?`, in.InstanceID).Scan(&installed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PluginRun{}, err
	}
	if installed {
		return PluginRun{}, bad("PLUGIN_ALREADY_INSTALLED", "插件已安装")
	}
	in.DesiredEnabled = true
	if err := p.installTx(ctx, tx, in); err != nil {
		return PluginRun{}, err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID).Scan(&exists); err != nil {
		return PluginRun{}, err
	}
	if exists == 0 {
		if _, err := p.saveValueTx(ctx, tx, in.InstanceID, "config", "main", 0, raw); err != nil {
			return PluginRun{}, err
		}
	}
	run, err := p.enqueueRunTx(ctx, tx, in.InstanceID, "init", nil)
	if err != nil {
		return PluginRun{}, err
	}
	return run, tx.Commit()
}

func (p *PluginStore) Installations(ctx context.Context) ([]PluginInstallation, error) {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation,created_at,updated_at FROM plugin_installations WHERE installed=1 ORDER BY created_at,instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PluginInstallation{}
	for rows.Next() {
		var in PluginInstallation
		var sealed []byte
		if err := rows.Scan(&in.InstanceID, &in.PluginID, &in.Version, &in.ManifestSchemaVersion, &sealed, &in.DesiredEnabled, &in.Installed, &in.Generation, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		in.Manifest, err = p.unseal("manifest", in.InstanceID, "main", sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// SaveConfigAndQueue commits the user's config and its init invocation together.
// This explicit user save releases migration staging, not config.enabled.
func (p *PluginStore) SaveConfigAndQueue(ctx context.Context, instanceID string, revision int64, config any) (PluginStoredValue, PluginRun, error) {
	raw, err := pluginJSON(config, true)
	if err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	defer tx.Rollback()
	activation, err := p.mediaRefreshHandoffTx(ctx, tx, instanceID, revision, raw)
	if err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	stored, err := p.saveValueTx(ctx, tx, instanceID, "config", "main", revision, raw)
	if err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=1,updated_at=? WHERE instance_id=?`, now(), instanceID); err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, instanceID); err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0 WHERE instance_id=?`, instanceID); err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_event_batches SET state='canceled' WHERE instance_id=? AND run_id IS NULL`, instanceID); err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	var pluginID string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, instanceID).Scan(&pluginID); err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	if pluginID == "MergeSiteSwitch" {
		activation = json.RawMessage(`{"config_saved":true}`)
	}
	run, err := p.enqueueRunTx(ctx, tx, instanceID, "init", activation)
	if err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	return stored, run, tx.Commit()
}

// Carry the last drained cursor across an enabled-to-enabled config handoff.
// Facts committed after drain froze its high water must reach the replacement
// config. A previously disabled config still starts from the new save boundary.
func (p *PluginStore) mediaRefreshHandoffTx(ctx context.Context, tx *sql.Tx, instanceID string, revision int64, next json.RawMessage) (json.RawMessage, error) {
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT v.sealed FROM plugin_values v JOIN plugin_installations i ON i.instance_id=v.instance_id WHERE i.instance_id=? AND i.plugin_id='MediaServerRefresh' AND v.kind='config' AND v.item_key='main' AND v.revision=?`, instanceID, revision).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	old, err := p.unseal("config", instanceID, "main", sealed)
	if err != nil {
		return nil, err
	}
	type state struct {
		Enabled bool     `json:"enabled"`
		Servers []string `json:"mediaservers"`
	}
	var before, after state
	if json.Unmarshal(old, &before) != nil || json.Unmarshal(next, &after) != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "媒体库刷新配置无法读取")
	}
	if !before.Enabled || !after.Enabled || len(before.Servers) == 0 || len(after.Servers) == 0 {
		return nil, nil
	}
	var cursor int64
	err = tx.QueryRowContext(ctx, `SELECT s.cursor FROM plugin_event_subscriptions s JOIN plugin_installations i ON i.instance_id=s.instance_id WHERE s.instance_id=? AND s.handler_key='refresh' AND s.generation=i.generation AND s.config_revision=?`, instanceID, revision).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int64{"event_after": cursor})
}

func (p *PluginStore) enqueueRunTx(ctx context.Context, tx *sql.Tx, instanceID, action string, input json.RawMessage) (PluginRun, error) {
	if !pluginStorageID.MatchString(instanceID) || !pluginStorageID.MatchString(action) || (len(input) > 0 && !json.Valid(input)) {
		return PluginRun{}, bad("INVALID_PLUGIN_RUN", "插件执行参数无效")
	}
	run := PluginRun{ID: id("pr_"), InstanceID: instanceID, Action: action, State: "pending", CreatedAt: now(), UpdatedAt: now()}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE state='pending'`).Scan(&pending); err != nil {
		return PluginRun{}, err
	}
	if pending >= 1000 {
		return PluginRun{}, bad("PLUGIN_QUEUE_FULL", "插件执行队列已满，请等待现有任务处理")
	}
	var installed, allowed bool
	var pluginID string
	if err := tx.QueryRowContext(ctx, `SELECT version,generation,installed,desired_enabled,plugin_id FROM plugin_installations WHERE instance_id=?`, instanceID).Scan(&run.Version, &run.Generation, &installed, &allowed, &pluginID); err != nil {
		return PluginRun{}, err
	}
	if !installed || !allowed {
		return PluginRun{}, bad("PLUGIN_INACTIVE", "插件已卸载或仍处于迁移暂存状态")
	}
	if pluginID == "MediaServerRefresh" && (action == "init" || action == "reload") && len(input) == 0 {
		// Save the activation boundary with the queued config, not when the
		// asynchronous worker eventually starts. Imports committed in between
		// must be delivered without replaying earlier disabled history.
		var cursor int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM domain_events`).Scan(&cursor); err != nil {
			return PluginRun{}, err
		}
		if action == "reload" {
			var drained int64
			err := tx.QueryRowContext(ctx, `SELECT s.cursor FROM plugin_event_subscriptions s JOIN plugin_values v ON v.instance_id=s.instance_id AND v.kind='config' AND v.item_key='main' WHERE s.instance_id=? AND s.handler_key='refresh' AND s.generation=? AND s.config_revision=v.revision`, instanceID, run.Generation).Scan(&drained)
			if err == nil {
				cursor = drained
			} else if !errors.Is(err, sql.ErrNoRows) {
				return PluginRun{}, err
			}
		}
		input, _ = json.Marshal(map[string]int64{"event_after": cursor})
	}
	var config []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instanceID).Scan(&run.ConfigRevision, &config); err != nil {
		return PluginRun{}, err
	}
	clear, err := p.unseal("config", instanceID, "main", config)
	if err != nil {
		return PluginRun{}, err
	}
	run.Input, err = pluginJSON(struct {
		Action string          `json:"action"`
		Config json.RawMessage `json:"config"`
		Input  json.RawMessage `json:"input,omitempty"`
	}{action, clear, input}, false)
	if err != nil {
		return PluginRun{}, err
	}
	sealed, err := p.seal("run", instanceID, run.ID, run.Input)
	if err != nil {
		return PluginRun{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_runs VALUES(?,?,?,?,?,?,?,?,NULL,'',?,?)`, run.ID, instanceID, run.Version, run.Generation, run.ConfigRevision, action, run.State, sealed, run.CreatedAt, run.UpdatedAt)
	return run, err
}

func (p *PluginStore) EnqueueRun(ctx context.Context, instanceID, action string, input json.RawMessage) (PluginRun, error) {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginRun{}, err
	}
	defer tx.Rollback()
	run, err := p.enqueueRunTx(ctx, tx, instanceID, action, input)
	if err != nil {
		return PluginRun{}, err
	}
	return run, tx.Commit()
}

func (p *PluginStore) scanPluginRun(row interface{ Scan(...any) error }) (PluginRun, error) {
	var run PluginRun
	var input, result []byte
	err := row.Scan(&run.ID, &run.InstanceID, &run.Version, &run.Generation, &run.ConfigRevision, &run.Action, &run.State, &input, &result, &run.ErrorCode, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return run, err
	}
	run.Input, err = p.unseal("run", run.InstanceID, run.ID, input)
	if err == nil && run.Action == "api" {
		var envelope struct {
			Input struct {
				RequestID string `json:"request_id"`
			} `json:"input"`
		}
		if json.Unmarshal(run.Input, &envelope) == nil && strings.HasPrefix(envelope.Input.RequestID, "req_") && domainReferencePattern.MatchString(envelope.Input.RequestID) {
			run.RequestID = envelope.Input.RequestID
		}
	}
	if err == nil && len(result) > 0 {
		run.Result, err = p.unseal("run_result", run.InstanceID, run.ID, result)
	}
	return run, err
}

func (p *PluginStore) Run(ctx context.Context, runID string) (PluginRun, error) {
	return p.scanPluginRun(p.store.DB.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, runID))
}

func (p *PluginStore) Runs(ctx context.Context, instanceID string, limit int) ([]PluginRun, error) {
	if limit < 1 || limit > 100 {
		return nil, bad("INVALID_PLUGIN_RUN", "插件执行记录每页为1到100条")
	}
	// List APIs expose metadata only. Never load/decrypt up to 100 large private
	// results just to omit them during JSON encoding. Input is read for the API
	// trace ID and then released instead of accumulating configuration snapshots.
	rows, err := p.store.DB.QueryContext(ctx, `SELECT `+pluginRunListColumns+` FROM plugin_runs WHERE instance_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PluginRun{}
	for rows.Next() {
		run, err := p.scanPluginRun(rows)
		if err != nil {
			return nil, err
		}
		run.Input = nil
		out = append(out, run)
	}
	return out, rows.Err()
}

// ClaimRun atomically claims one current invocation. Old config/generation
// snapshots are canceled rather than being replayed after user edits or reset.
func (p *PluginStore) ClaimRun(ctx context.Context) (PluginRun, error) {
	return p.claimRunForInstance(ctx, "")
}

func (p *PluginStore) claimRunForInstance(ctx context.Context, instanceID string) (PluginRun, error) {
	for {
		tx, err := p.store.DB.BeginTx(ctx, nil)
		if err != nil {
			return PluginRun{}, err
		}
		run, err := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE state='pending' AND (?='' OR instance_id=?) ORDER BY created_at,id LIMIT 1`, instanceID, instanceID))
		if err != nil {
			_ = tx.Rollback()
			return PluginRun{}, err
		}
		var current int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.installed=1 AND i.desired_enabled=1 AND i.version=? AND i.generation=? AND v.revision=?`, run.InstanceID, run.Version, run.Generation, run.ConfigRevision).Scan(&current)
		if err != nil {
			_ = tx.Rollback()
			return PluginRun{}, err
		}
		state, errorCode := "running", ""
		if current != 1 {
			state, errorCode = "canceled", "PLUGIN_SNAPSHOT_STALE"
		} else if run.Action == "service" {
			errorCode, err = p.claimPluginScheduleTx(ctx, tx, run, time.Now())
			if err != nil {
				_ = tx.Rollback()
				return PluginRun{}, err
			}
			if errorCode != "" {
				state = "canceled"
			}
		}
		if state == "canceled" {
			if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state=CASE WHEN state='missed' THEN state ELSE 'canceled' END WHERE run_id=? AND state IN ('queued','running','missed')`, run.ID); err != nil {
				_ = tx.Rollback()
				return PluginRun{}, err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state=?,error_code=?,updated_at=? WHERE id=? AND state='pending'`, state, errorCode, now(), run.ID)
		if err != nil {
			_ = tx.Rollback()
			return PluginRun{}, err
		}
		if err = tx.Commit(); err != nil {
			return PluginRun{}, err
		}
		if state == "canceled" {
			continue
		}
		run.State = state
		return run, nil
	}
}

func (p *PluginStore) FinishRun(ctx context.Context, runID, state, errorCode string, result json.RawMessage) error {
	return p.finishRun(ctx, runID, state, errorCode, result, false)
}

func (p *PluginStore) finishRun(ctx context.Context, runID, state, errorCode string, result json.RawMessage, blockStartup bool) error {
	if state != "succeeded" && state != "failed" && state != "reconcile_pending" || (errorCode != "" && !pluginStorageID.MatchString(errorCode)) || (state == "succeeded" && errorCode != "") {
		return bad("INVALID_PLUGIN_RUN", "插件执行终态无效")
	}
	run, err := p.Run(ctx, runID)
	if err != nil {
		return err
	}
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	raw, err := pluginResultJSON(result)
	if err != nil {
		return err
	}
	sealed, err := p.seal("run_result", run.InstanceID, runID, raw)
	if err != nil {
		return err
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_runs SET state=?,result=?,error_code=?,updated_at=? WHERE id=? AND state='running'`, state, sealed, errorCode, now(), runID)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		return bad("PLUGIN_RUN_TERMINAL", "插件执行已结束或不在执行状态")
	}
	if blockStartup && state == "failed" && (run.Action == "init" || run.Action == "reload") {
		if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_run_startup_blocks(run_id) VALUES(?)`, runID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state=? WHERE run_id=? AND state='running'`, state, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PluginStore) recoverPluginRuns(ctx context.Context) error {
	// These native operations are idempotent: category replaces a validated
	// file; Hosts replaces only its marked tail; process init has no side effect.
	// Other plugins require their own external-operation reconciliation.
	_, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state=CASE WHEN instance_id IN (SELECT instance_id FROM plugin_installations WHERE plugin_id IN ('CategoryEditor','CustomHosts','SystemProcess')) THEN 'pending' ELSE 'reconcile_pending' END,error_code='PLUGIN_INTERRUPTED',updated_at=? WHERE state='running' OR (state='reconcile_pending' AND instance_id IN (SELECT instance_id FROM plugin_installations WHERE plugin_id IN ('CategoryEditor','CustomHosts','SystemProcess')))`, now())
	if err == nil {
		err = p.recoverMediaRefreshRuns(ctx)
	}
	if err == nil {
		err = p.recoverIdentifiersRuns(ctx)
	}
	if err == nil {
		err = p.recoverSiteControlRuns(ctx)
	}
	if err == nil {
		err = p.recoverSiteStatisticRuns(ctx)
	}
	if err == nil {
		err = p.recoverSubscribeGroupRuns(ctx)
	}
	if err == nil {
		err = p.recoverUninstallRuns(ctx)
	}
	if err == nil {
		err = p.recoverDeferredRuns(ctx)
	}
	if err == nil {
		_, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state=(SELECT CASE WHEN r.state='pending' THEN 'queued' ELSE r.state END FROM plugin_runs r WHERE r.id=run_id) WHERE run_id IS NOT NULL AND state IN ('queued','running','reconcile_pending')`)
	}
	return err
}

func (p *PluginStore) recoverMediaRefreshRuns(ctx context.Context) error {
	if _, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='reconcile_pending',error_code='PLUGIN_INTERRUPTED',updated_at=? WHERE state='running' AND instance_id IN (SELECT instance_id FROM plugin_installations WHERE plugin_id='MediaServerRefresh') AND EXISTS(SELECT 1 FROM plugin_media_refresh_operations o WHERE o.run_id=plugin_runs.id AND o.state='submitting')`, now()); err != nil {
		return err
	}
	_, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='pending',updated_at=? WHERE state IN ('running','reconcile_pending') AND instance_id IN (SELECT instance_id FROM plugin_installations WHERE plugin_id='MediaServerRefresh') AND action IN ('init','reload','event') AND NOT EXISTS(SELECT 1 FROM plugin_media_refresh_operations o WHERE o.run_id=plugin_runs.id AND o.state='submitting')`, now())
	return err
}

func (p *PluginStore) patchRunConfig(ctx context.Context, run *PluginRun, patch map[string]json.RawMessage) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updated := *run
	if err := p.patchRunConfigTx(ctx, tx, &updated, patch); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	*run = updated
	return nil
}

func (p *PluginStore) patchRunConfigTx(ctx context.Context, tx *sql.Tx, run *PluginRun, patch map[string]json.RawMessage) error {
	var revision int64
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, run.InstanceID).Scan(&revision, &sealed); err != nil {
		return err
	}
	if revision != run.ConfigRevision {
		return bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，旧执行已停止")
	}
	raw, err := p.unseal("config", run.InstanceID, "main", sealed)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad("INVALID_PLUGIN_CONFIG", "插件配置无效")
	}
	for key, value := range patch {
		fields[key] = value
	}
	raw, err = pluginJSON(fields, true)
	if err != nil {
		return err
	}
	return p.replaceRunConfigTx(ctx, tx, run, raw)
}

// Internal full replacements share the same snapshot propagation as patches.
// Only trusted host adapters choose a replacement; it is not a worker method.
func (p *PluginStore) replaceRunConfigTx(ctx context.Context, tx *sql.Tx, run *PluginRun, raw json.RawMessage) error {
	checked, err := pluginJSON(raw, true)
	if err != nil {
		return err
	}
	raw = checked
	revision := run.ConfigRevision
	value, err := p.saveValueTx(ctx, tx, run.InstanceID, "config", "main", revision, raw)
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_runs SET config_revision=?,updated_at=? WHERE id=? AND state='running' AND generation=? AND config_revision=?`, value.Revision, now(), run.ID, run.Generation, revision)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		return bad("PLUGIN_RUN_TERMINAL", "插件执行已停止")
	}
	// Internal normalization and clearing one-shot flags must reach already
	// queued invocations too. Only advance snapshots that still match this
	// exact generation/revision; a user edit continues to invalidate old work.
	for {
		var pendingID string
		var pendingSealed []byte
		err := tx.QueryRowContext(ctx, `SELECT id,input FROM plugin_runs WHERE instance_id=? AND generation=? AND config_revision=? AND state='pending' ORDER BY id LIMIT 1`, run.InstanceID, run.Generation, revision).Scan(&pendingID, &pendingSealed)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		pendingRaw, err := p.unseal("run", run.InstanceID, pendingID, pendingSealed)
		if err != nil {
			return err
		}
		var invocation map[string]json.RawMessage
		if json.Unmarshal(pendingRaw, &invocation) != nil || invocation == nil {
			return bad("INVALID_PLUGIN_RUN", "排队插件执行快照无效")
		}
		invocation["config"] = raw
		pendingRaw, err = pluginJSON(invocation, false)
		if err != nil {
			return err
		}
		pendingSealed, err = p.seal("run", run.InstanceID, pendingID, pendingRaw)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_runs SET config_revision=?,input=?,updated_at=? WHERE id=?`, value.Revision, pendingSealed, now(), pendingID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET config_revision=? WHERE instance_id=? AND generation=? AND config_revision=?`, value.Revision, run.InstanceID, run.Generation, revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_api_calls SET config_revision=? WHERE instance_id=? AND generation=? AND config_revision=? AND run_id IN (SELECT id FROM plugin_runs WHERE state IN ('pending','running'))`, value.Revision, run.InstanceID, run.Generation, revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedules SET config_revision=? WHERE instance_id=? AND generation=? AND config_revision=?`, value.Revision, run.InstanceID, run.Generation, revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET config_revision=? WHERE instance_id=? AND generation=? AND config_revision=? AND run_id IN (SELECT id FROM plugin_runs WHERE state IN ('pending','running'))`, value.Revision, run.InstanceID, run.Generation, revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_batches SET config_revision=? WHERE instance_id=? AND generation=? AND config_revision=? AND (run_id IS NULL OR run_id IN (SELECT id FROM plugin_runs WHERE state IN ('pending','running')))`, value.Revision, run.InstanceID, run.Generation, revision); err != nil {
		return err
	}
	run.ConfigRevision = value.Revision
	return nil
}

func missingPluginValue(err error) bool { return errors.Is(err, sql.ErrNoRows) }
