package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// Normalization changes a configuration value, never an admitted invocation.
// The original revision/input remain the operation identity. A watcher points
// at the separately committed result revision; redundant queued initializations
// are explicitly canceled, with their original identities sealed in this proof.
type siteControlCanceledAdmission struct {
	ID, Version, Action, InputSHA string
	Generation, Revision          int64
}
type siteControlNormalizationReceipt struct {
	Version                                                   int
	Purpose, RunID, Instance, PluginVersion, Action, InputSHA string
	Generation, AdmissionRevision, ResultRevision             int64
	BeforeSHA, AfterSHA                                       string
	Canceled                                                  []siteControlCanceledAdmission
	Result                                                    json.RawMessage
}

func siteControlConfigEqual(left, right json.RawMessage) bool {
	var a, b builtin.SiteControls
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil || a == nil || b == nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func (a *App) siteControlAdmissionTx(ctx context.Context, tx *sql.Tx, run PluginRun, resultRevision int64) error {
	if err := a.pluginStore.requireSiteReferenceProtocolTx(ctx, tx, run.InstanceID); err != nil {
		return err
	}
	actual, err := a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if err != nil {
		return err
	}
	if actual.State != "running" || actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || !bytes.Equal(actual.Input, run.Input) {
		return bad("PLUGIN_SNAPSHOT_STALE", "聚合站点开关原执行身份已变化")
	}
	originalInit := a.pluginStore.originalInitializationRunBound(run) && originalInitializationMatches(ctx, run.InstanceID, "MergeSiteSwitch", run.Version, run.Generation, run.Action)
	var count int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i LEFT JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id='MergeSiteSwitch' AND ((i.installed=1 AND i.desired_enabled=1) OR ?) AND i.version=? AND i.generation=? AND (v.revision=? OR (v.instance_id IS NULL AND ? AND ?=0))`, run.InstanceID, originalInit, run.Version, run.Generation, resultRevision, originalInit, resultRevision).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return bad("PLUGIN_SNAPSHOT_STALE", "聚合站点开关配置已变化")
	}
	return nil
}

func (a *App) registerSiteControlsRevisionTx(ctx context.Context, tx *sql.Tx, run PluginRun, config builtin.SiteControls, resultRevision int64) error {
	if err := a.siteControlAdmissionTx(ctx, tx, run, resultRevision); err != nil {
		return err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	var current []byte
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main' AND revision=?`, run.InstanceID, resultRevision).Scan(&current)
	absent := errors.Is(err, sql.ErrNoRows) && resultRevision == 0 && run.ConfigRevision == 0 && len(config) == 0 && a.pluginStore.originalInitializationRunBound(run) && originalInitializationMatches(ctx, run.InstanceID, "MergeSiteSwitch", run.Version, run.Generation, run.Action)
	if err != nil && !absent {
		return err
	}
	clear := raw
	if !absent {
		clear, err = a.pluginStore.unseal("config", run.InstanceID, "main", current)
	} else {
		err = nil
	}
	if err != nil || (!absent && !siteControlConfigEqual(clear, raw)) {
		return bad("PLUGIN_SNAPSHOT_STALE", "聚合站点开关监听配置与已提交结果不符")
	}
	sealed, err := a.pluginStore.seal("site_controls_watch", run.InstanceID, "main", raw)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO plugin_site_control_watchers VALUES(?,?,?,?,?) ON CONFLICT(instance_id) DO UPDATE SET generation=excluded.generation,config_revision=excluded.config_revision,config=excluded.config,updated_at=excluded.updated_at`, run.InstanceID, run.Generation, resultRevision, sealed, now())
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return bad("PLUGIN_SNAPSHOT_STALE", "聚合站点开关监听配置未提交")
	}
	return nil
}

func (a *App) commitSiteControlNormalizationTx(ctx context.Context, tx *sql.Tx, run PluginRun, normalized builtin.SiteControls, result json.RawMessage) error {
	if err := a.siteControlAdmissionTx(ctx, tx, run, run.ConfigRevision); err != nil {
		return err
	}
	var invocation plugins.Invocation
	var activation struct {
		ConfigSaved bool `json:"config_saved"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil || invocation.Action != run.Action || run.Action != "init" || json.Unmarshal(invocation.Input, &activation) != nil || !activation.ConfigSaved {
		return bad("INVALID_PLUGIN_RUN", "聚合站点开关保存来源无效")
	}
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main' AND revision=?`, run.InstanceID, run.ConfigRevision).Scan(&sealed); err != nil {
		return err
	}
	before, err := a.pluginStore.unseal("config", run.InstanceID, "main", sealed)
	if err != nil || !siteControlConfigEqual(before, invocation.Config) {
		return bad("PLUGIN_SNAPSHOT_STALE", "聚合站点开关保存快照已变化")
	}
	raw, err := pluginJSON(normalized, true)
	if err != nil {
		return err
	}
	stored, err := a.pluginStore.saveValueTx(ctx, tx, run.InstanceID, "config", "main", run.ConfigRevision, raw)
	if err != nil {
		return err
	}
	proof := siteControlNormalizationReceipt{Version: 2, Purpose: "site-controls-normalization/v2", RunID: run.ID, Instance: run.InstanceID, PluginVersion: run.Version, Action: run.Action, InputSHA: tokenHash(string(run.Input)), Generation: run.Generation, AdmissionRevision: run.ConfigRevision, ResultRevision: stored.Revision, BeforeSHA: tokenHash(string(before)), AfterSHA: tokenHash(string(raw)), Canceled: []siteControlCanceledAdmission{}, Result: result}
	// These exact-revision init/reload actions cannot add new source intent: this
	// save has already applied the same config and initialized its normalized
	// watcher. Preserve every queued input/revision and cancel it explicitly.
	rows, err := tx.QueryContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE instance_id=? AND generation=? AND config_revision=? AND state='pending' ORDER BY created_at,id`, run.InstanceID, run.Generation, run.ConfigRevision)
	if err != nil {
		return err
	}
	pending := []PluginRun{}
	for rows.Next() {
		item, err := a.pluginStore.scanPluginRun(rows)
		if err != nil {
			rows.Close()
			return err
		}
		var pendingInvocation plugins.Invocation
		if item.Version != run.Version || (item.Action != "init" && item.Action != "reload") || json.Unmarshal(item.Input, &pendingInvocation) != nil || pendingInvocation.Action != item.Action || !siteControlConfigEqual(pendingInvocation.Config, before) {
			rows.Close()
			return bad("PLUGIN_SITE_PENDING_UNSUPPORTED", "聚合站点开关有不能由配置规范化续接的排队操作")
		}
		pending = append(pending, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range pending {
		changed, err := tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code='PLUGIN_SITE_NORMALIZATION_SUPERSEDED',updated_at=? WHERE id=? AND instance_id=? AND version=? AND generation=? AND config_revision=? AND state='pending'`, now(), item.ID, run.InstanceID, item.Version, run.Generation, run.ConfigRevision)
		if err != nil {
			return err
		}
		if count, err := changed.RowsAffected(); err != nil || count != 1 {
			return bad("PLUGIN_SNAPSHOT_STALE", "排队站点开关执行已变化")
		}
		proof.Canceled = append(proof.Canceled, siteControlCanceledAdmission{ID: item.ID, Version: item.Version, Action: item.Action, InputSHA: tokenHash(string(item.Input)), Generation: item.Generation, Revision: item.ConfigRevision})
	}
	if err = a.registerSiteControlsRevisionTx(ctx, tx, run, normalized, stored.Revision); err != nil {
		return err
	}
	envelope, err := pluginJSON(proof, true)
	if err != nil {
		return err
	}
	sealed, err = a.pluginStore.seal("site_controls_result", run.InstanceID, run.ID, envelope)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_site_control_operations VALUES(?,?,?,?,?)`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, sealed)
	return err
}

func (p *PluginStore) siteControlNormalizationResult(ctx context.Context, run PluginRun, raw json.RawMessage) (json.RawMessage, error) {
	var header struct{ Version int }
	if json.Unmarshal(raw, &header) != nil {
		return nil, bad("PLUGIN_SITE_RECEIPT_INVALID", "站点开关提交收据无效")
	}
	if header.Version == 0 {
		// Existing pre-versioned operations were sealed public results under the
		// original stored revision. Preserve that historical format explicitly.
		var legacy struct{ Configured, Applied bool }
		if cleanupJournalStrict(raw, &legacy) != nil || !legacy.Configured || !legacy.Applied {
			return nil, bad("PLUGIN_SITE_RECEIPT_INVALID", "旧站点开关提交收据无效")
		}
		return raw, nil
	}
	actual, err := p.Run(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	var proof siteControlNormalizationReceipt
	if cleanupJournalStrict(raw, &proof) != nil || proof.Version != 2 || proof.Purpose != "site-controls-normalization/v2" || proof.RunID != run.ID || proof.Instance != run.InstanceID || proof.Generation != run.Generation || proof.AdmissionRevision != run.ConfigRevision || proof.PluginVersion != actual.Version || proof.Action != actual.Action || proof.Action != "init" || proof.InputSHA != tokenHash(string(actual.Input)) || actual.ConfigRevision != run.ConfigRevision || actual.Generation != run.Generation || actual.InstanceID != run.InstanceID || proof.ResultRevision != proof.AdmissionRevision+1 || !pluginStorageDigest.MatchString(proof.BeforeSHA) || !pluginStorageDigest.MatchString(proof.AfterSHA) || proof.Canceled == nil {
		return nil, bad("PLUGIN_SITE_RECEIPT_INVALID", "站点开关规范化收据与原执行不符")
	}
	var invocation plugins.Invocation
	var activation struct {
		ConfigSaved bool `json:"config_saved"`
	}
	var admitted builtin.SiteControls
	if json.Unmarshal(actual.Input, &invocation) != nil || invocation.Action != actual.Action || json.Unmarshal(invocation.Input, &activation) != nil || !activation.ConfigSaved || json.Unmarshal(invocation.Config, &admitted) != nil || admitted == nil || admitted.Validate() != nil || proof.BeforeSHA != tokenHash(string(invocation.Config)) {
		return nil, bad("PLUGIN_SITE_RECEIPT_INVALID", "站点开关原保存快照无法校验")
	}
	seen := map[string]bool{}
	for _, old := range proof.Canceled {
		if !pluginStorageID.MatchString(old.ID) || old.ID == run.ID || seen[old.ID] || old.Version != proof.PluginVersion || old.Generation != proof.Generation || old.Revision != proof.AdmissionRevision || (old.Action != "init" && old.Action != "reload") || !pluginStorageDigest.MatchString(old.InputSHA) {
			return nil, bad("PLUGIN_SITE_RECEIPT_INVALID", "站点开关取消执行收据无效")
		}
		seen[old.ID] = true
	}
	var result struct{ Configured, Applied bool }
	if cleanupJournalStrict(proof.Result, &result) != nil || !result.Configured || !result.Applied {
		return nil, bad("PLUGIN_SITE_RECEIPT_INVALID", "站点开关提交结果无效")
	}
	return proof.Result, nil
}
