package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// PluginRun's public projection is metadata only. Config snapshots, input and
// worker results may contain secrets and are never marshaled by list APIs.
type PluginRun struct {
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
	stored, err := p.saveValueTx(ctx, tx, instanceID, "config", "main", revision, raw)
	if err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=1,updated_at=? WHERE instance_id=?`, now(), instanceID); err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	run, err := p.enqueueRunTx(ctx, tx, instanceID, "init", nil)
	if err != nil {
		return PluginStoredValue{}, PluginRun{}, err
	}
	return stored, run, tx.Commit()
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
	if err := tx.QueryRowContext(ctx, `SELECT version,generation,installed,desired_enabled FROM plugin_installations WHERE instance_id=?`, instanceID).Scan(&run.Version, &run.Generation, &installed, &allowed); err != nil {
		return PluginRun{}, err
	}
	if !installed || !allowed {
		return PluginRun{}, bad("PLUGIN_INACTIVE", "插件已卸载或仍处于迁移暂存状态")
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
	rows, err := p.store.DB.QueryContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE instance_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, instanceID, limit)
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
		out = append(out, run)
	}
	return out, rows.Err()
}

// ClaimRun atomically claims one current invocation. Old config/generation
// snapshots are canceled rather than being replayed after user edits or reset.
func (p *PluginStore) ClaimRun(ctx context.Context) (PluginRun, error) {
	for {
		tx, err := p.store.DB.BeginTx(ctx, nil)
		if err != nil {
			return PluginRun{}, err
		}
		run, err := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE state='pending' ORDER BY created_at,id LIMIT 1`))
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
	raw, err := pluginJSON(result, false)
	if err != nil {
		return err
	}
	sealed, err := p.seal("run_result", run.InstanceID, runID, raw)
	if err != nil {
		return err
	}
	changed, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state=?,result=?,error_code=?,updated_at=? WHERE id=? AND state='running'`, state, sealed, errorCode, now(), runID)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		return bad("PLUGIN_RUN_TERMINAL", "插件执行已结束或不在执行状态")
	}
	return nil
}

func (p *PluginStore) recoverPluginRuns(ctx context.Context) error {
	// The category write replaces one file with the same validated content and
	// uses an idempotent notification id. Other plugins require own reconciliation.
	_, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state=CASE WHEN instance_id IN (SELECT instance_id FROM plugin_installations WHERE plugin_id='CategoryEditor') THEN 'pending' ELSE 'reconcile_pending' END,error_code='PLUGIN_INTERRUPTED',updated_at=? WHERE state='running' OR (state='reconcile_pending' AND instance_id IN (SELECT instance_id FROM plugin_installations WHERE plugin_id='CategoryEditor'))`, now())
	return err
}

func (p *PluginStore) patchRunConfig(ctx context.Context, run *PluginRun, patch map[string]json.RawMessage) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	if err := tx.Commit(); err != nil {
		return err
	}
	run.ConfigRevision = value.Revision
	return nil
}

func missingPluginValue(err error) bool { return errors.Is(err, sql.ErrNoRows) }
