package app

// Admission belongs to the trusted host enqueue transaction, never to a worker
// invocation or its event_after field. The marker lives outside the invocation
// JSON so strict workers and historical readers retain their original wire data.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	pluginEventAdmissionRunPrefix      = "LM-PLUGIN-EVENT-ADMISSION-V1\x00"
	pluginEventAdmissionBrushRunPrefix = "LM-BRUSH-EVENT-ADMISSION-V1\x00"
	pluginEventAdmissionRunPurpose     = "run/event-admission/v1"
	pluginEventAdmissionBrushPurpose   = "BrushFlowLanduo@5.2.15/snapshot/run/event-admission/v1"
)

type pluginEventAdmission struct {
	Schema                 int              `json:"schema"`
	Run                    string           `json:"run"`
	Instance               string           `json:"instance"`
	Plugin                 string           `json:"plugin"`
	Version                string           `json:"version"`
	Generation             int64            `json:"generation"`
	Revision               int64            `json:"revision"`
	Action                 string           `json:"action"`
	InputSHA               string           `json:"input_sha"`
	ConfigSHA              string           `json:"config_sha"`
	HighWater              int64            `json:"high_water"`
	PerHandlerReloadCursor map[string]int64 `json:"per_handler_reload_cursor"`
	ActiveCursorFloor      map[string]int64 `json:"active_cursor_floor"`
}

// Only trusted MediaServerRefresh host queries may carry the reviewed config
// handoff across the config write that deactivates old subscriptions. This is
// host-only and refresh-only: decoding a worker input cannot mint this value.
type mediaRefreshAdmissionCursor struct{ after int64 }

// Retain the original MediaServerRefresh same-generation/revision reload
// handoff, including its inactive refresh row. No other handler inherits it.
func (p *PluginStore) mediaRefreshReloadCursorTx(ctx context.Context, tx *sql.Tx, run PluginRun) (*mediaRefreshAdmissionCursor, error) {
	var cursor int64
	err := tx.QueryRowContext(ctx, `SELECT cursor FROM plugin_event_subscriptions WHERE instance_id=? AND handler_key='refresh' AND generation=? AND config_revision=?`, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mediaRefreshAdmissionCursor{after: cursor}, nil
}

func eventAdmissionRunMarked(sealed []byte) bool {
	return bytes.HasPrefix(sealed, []byte(pluginEventAdmissionRunPrefix)) || bytes.HasPrefix(sealed, []byte(pluginEventAdmissionBrushRunPrefix))
}

func (p *PluginStore) sealEventAdmissionRun(instance, key, plugin, version string, raw []byte) ([]byte, error) {
	prefix, purpose := pluginEventAdmissionRunPrefix, pluginEventAdmissionRunPurpose
	if len(raw) > pluginValueLimit {
		if !brushFlowSnapshotOwner(plugin, version) {
			return nil, bad("INVALID_PLUGIN_VALUE", "插件数据超过大小上限")
		}
		prefix, purpose = pluginEventAdmissionBrushRunPrefix, pluginEventAdmissionBrushPurpose
	}
	sealed, err := p.seal(purpose, instance, key, raw)
	if err != nil {
		return nil, err
	}
	return append([]byte(prefix), sealed...), nil
}

// Preserve the admission/legacy storage family during pending normalization.
// Re-sealing cannot invent an admission for an old run or downgrade a new run.
// Large BrushFlow snapshots retain their dedicated purpose and unbounded reader.
func (p *PluginStore) resealRunSnapshot(instance, key, plugin, version string, previous, raw []byte) ([]byte, error) {
	if bytes.HasPrefix(previous, []byte(pluginEventAdmissionBrushRunPrefix)) {
		if !brushFlowSnapshotOwner(plugin, version) {
			return nil, bad("PLUGIN_RUN_PROOF_INVALID", "插件执行快照所属实现已变化")
		}
		sealed, err := p.seal(pluginEventAdmissionBrushPurpose, instance, key, raw)
		if err != nil {
			return nil, err
		}
		return append([]byte(pluginEventAdmissionBrushRunPrefix), sealed...), nil
	}
	if eventAdmissionRunMarked(previous) {
		return p.sealEventAdmissionRun(instance, key, plugin, version, raw)
	}
	return p.sealPluginSnapshot("run", instance, key, plugin, version, raw)
}

func (p *PluginStore) recordEventAdmissionTx(ctx context.Context, tx *sql.Tx, plugin string, run PluginRun, config json.RawMessage, mediaCursor *mediaRefreshAdmissionCursor) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	if run.Action != "init" && run.Action != "reload" {
		return nil
	}
	if _, err := p.instanceMetadata(ctx, tx, run.InstanceID); err != nil {
		return err
	}
	proof := pluginEventAdmission{Schema: 1, Run: run.ID, Instance: run.InstanceID, Plugin: plugin, Version: run.Version, Generation: run.Generation, Revision: run.ConfigRevision, Action: run.Action, InputSHA: deferredInputSHA(run.Input), ConfigSHA: deferredInputSHA(config), PerHandlerReloadCursor: map[string]int64{}, ActiveCursorFloor: map[string]int64{}}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM domain_events`).Scan(&proof.HighWater); err != nil {
		return err
	}
	// Both init and reload protect matching active rows from rewind below a
	// known enqueue-time cursor. Only reload uses that cursor as a fallback:
	// an init with a missing/inactive row still starts at its enqueue high water.
	rows, err := tx.QueryContext(ctx, `SELECT handler_key,cursor FROM plugin_event_subscriptions WHERE instance_id=? AND generation=? AND config_revision=? AND active=1`, run.InstanceID, run.Generation, run.ConfigRevision)
	if err != nil {
		return err
	}
	for rows.Next() {
		var handler string
		var cursor int64
		if err = rows.Scan(&handler, &cursor); err != nil {
			rows.Close()
			return err
		}
		proof.ActiveCursorFloor[handler] = cursor
		if run.Action == "reload" {
			proof.PerHandlerReloadCursor[handler] = cursor
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if mediaCursor != nil {
		if plugin != "MediaServerRefresh" || (run.Action != "init" && run.Action != "reload") {
			return bad("PLUGIN_RUN_PROOF_INVALID", "插件媒体刷新交接范围无效")
		}
		proof.PerHandlerReloadCursor["refresh"] = mediaCursor.after
	}
	for _, cursors := range []map[string]int64{proof.PerHandlerReloadCursor, proof.ActiveCursorFloor} {
		for handler, cursor := range cursors {
			if !pluginStorageID.MatchString(handler) || cursor < 0 || cursor > proof.HighWater {
				return bad("PLUGIN_RUN_PROOF_INVALID", "插件事件交接游标无效")
			}
		}
	}
	raw, err := pluginJSON(proof, true)
	if err != nil {
		return err
	}
	sealed, err := p.seal("event_admission", run.InstanceID, run.ID, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_event_admissions(run_id,sealed) VALUES(?,?)`, run.ID, sealed)
	return err
}

// A nil proof means an authenticated historical unmarked run, not a missing
// table row. Authenticate stored input BEFORE choosing that legacy branch:
// stripping, adding or swapping the external marker must fail AEAD verification.
func (p *PluginStore) readEventAdmissionTx(ctx context.Context, q PluginProofReader, run PluginRun) (*pluginEventAdmission, error) {
	var actual PluginRun
	var input []byte
	err := q.QueryRowContext(ctx, `SELECT id,instance_id,version,generation,config_revision,action,state,input FROM plugin_runs WHERE id=?`, run.ID).Scan(&actual.ID, &actual.InstanceID, &actual.Version, &actual.Generation, &actual.ConfigRevision, &actual.Action, &actual.State, &input)
	if errors.Is(err, sql.ErrNoRows) || err == nil && actual.State != "running" {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "插件执行已停止，未注册旧事件订阅")
	}
	if err != nil {
		return nil, err
	}
	actual.Input, err = p.unseal("run", actual.InstanceID, actual.ID, input)
	if err != nil {
		return nil, err
	}
	if actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || !bytes.Equal(actual.Input, run.Input) {
		return nil, bad("PLUGIN_RUN_PROOF_INVALID", "插件事件订阅的执行身份不匹配")
	}
	var plugin, version string
	var generation, revision int64
	var installed, enabled bool
	var configSealed []byte
	err = q.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, run.InstanceID).Scan(&plugin, &version, &generation, &installed, &enabled, &revision, &configSealed)
	if err != nil {
		return nil, err
	}
	if !installed || !enabled || version != run.Version || generation != run.Generation || revision != run.ConfigRevision {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "插件配置已变化，未注册旧事件订阅")
	}
	if _, err = p.instanceMetadata(ctx, q, run.InstanceID); err != nil {
		return nil, err
	}
	if !eventAdmissionRunMarked(input) {
		return nil, nil
	}
	var sealed []byte
	err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_event_admissions WHERE run_id=?`, run.ID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, bad("PLUGIN_RUN_PROOF_INVALID", "插件事件准入证明缺失")
	}
	if err != nil {
		return nil, err
	}
	raw, err := p.unseal("event_admission", run.InstanceID, run.ID, sealed)
	if err != nil {
		return nil, err
	}
	var proof pluginEventAdmission
	if json.Unmarshal(raw, &proof) != nil || proof.Schema != 1 || proof.Run != run.ID || proof.Instance != run.InstanceID || proof.Plugin != plugin || proof.Version != run.Version || proof.Generation != run.Generation || proof.Action != run.Action || (proof.Action != "init" && proof.Action != "reload") || proof.Revision < 1 || proof.HighWater < 0 || !pluginStorageDigest.MatchString(proof.InputSHA) || !pluginStorageDigest.MatchString(proof.ConfigSHA) || proof.PerHandlerReloadCursor == nil {
		return nil, bad("PLUGIN_RUN_PROOF_INVALID", "插件事件准入证明不匹配")
	}
	// Previously sealed schema-1 admissions may omit ActiveCursorFloor. They
	// remain readable, but cannot prove a lower floor that was never captured.
	for _, cursors := range []map[string]int64{proof.PerHandlerReloadCursor, proof.ActiveCursorFloor} {
		for handler, cursor := range cursors {
			if !pluginStorageID.MatchString(handler) || cursor < 0 || cursor > proof.HighWater {
				return nil, bad("PLUGIN_RUN_PROOF_INVALID", "插件事件准入游标无效")
			}
		}
	}
	var successor bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=?)`, run.ID, run.ConfigRevision).Scan(&successor); err != nil {
		return nil, err
	}
	if successor {
		return nil, bad("PLUGIN_RUN_PROOF_INVALID", "插件事件准入修订头已回退")
	}
	configSHA, err := p.verifyRunConfigChainTx(ctx, q, plugin, actual, proof.Revision, proof.InputSHA, proof.ConfigSHA)
	if err != nil {
		return nil, err
	}
	config, err := p.unseal("config", run.InstanceID, "main", configSealed)
	if err != nil {
		return nil, err
	}
	if configSHA != deferredInputSHA(config) {
		return nil, bad("PLUGIN_RUN_PROOF_INVALID", "插件事件准入配置内容不匹配")
	}
	return &proof, nil
}
