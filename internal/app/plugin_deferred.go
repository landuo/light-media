package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/removelink"
	"light-media/internal/plugins/scrapefileclean"
	"light-media/internal/plugins/uninstall"
)

// A terminal SDK call is an authorization handshake, not the operation itself.
// The trusted host starts it only after the worker has exited successfully and
// its global process slot has been released. No owner wait holds that slot.
const pluginDeferredSchema = `CREATE TABLE IF NOT EXISTS plugin_deferred_operations(
 run_id TEXT PRIMARY KEY,sealed BLOB NOT NULL,FOREIGN KEY(run_id) REFERENCES plugin_runs(id));`

type pluginDeferredRecord struct {
	Schema                                         int `json:"schema"`
	Plugin, Instance, Version, Run, Action, Method string
	Generation, Revision                           int64
	InputSHA                                       string
	Raw, Ack, Result                               json.RawMessage
	Phase                                          string
	ErrorCode                                      string `json:",omitempty"`
}
type pluginDeferredCall struct {
	a                *App
	installation     PluginInstallation
	run              PluginRun
	record           pluginDeferredRecord
	mu               sync.Mutex
	called, poisoned bool
}
type pluginDeferredExecutionKey struct{}

func deferredInputSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func pluginDeferredSpec(in PluginInstallation, run PluginRun) (string, json.RawMessage, error) {
	if isTorrentTransfer(in.PluginID) && in.Version == "1.10.3" {
		return transferDeferredSpec(run)
	}
	if in.PluginID == "IYUUAutoSeed" && in.Version == "2.19" {
		return iyuuDeferredSpec(run)
	}
	if in.PluginID == "PluginUnInstall" && in.Version == "2.2" && (run.Action == "init" || run.Action == "reload") {
		var invocation plugins.Invocation
		if json.Unmarshal(run.Input, &invocation) != nil {
			return "", nil, bad("INVALID_PLUGIN_RUN", "插件执行快照无效")
		}
		c, err := uninstall.ParseConfiguration(invocation.Config)
		if err != nil {
			return "", nil, bad("INVALID_PLUGIN_CONFIG", "卸载插件配置无效")
		}
		selected, err := c.Selected()
		if err != nil {
			return "", nil, bad("INVALID_PLUGIN_CONFIG", "卸载插件配置无效")
		}
		if selected {
			return uninstall.ApplyCapability, json.RawMessage(`{}`), nil
		}
	}
	if in.PluginID == "ScrapeFileClean" && in.Version == "1.0.7" {
		switch run.Action {
		case "init", "reload":
			return scrapefileclean.ConfigureCapability, json.RawMessage(`{}`), nil
		case "event":
			var invocation plugins.Invocation
			var event struct {
				ID string `json:"event_id"`
			}
			if json.Unmarshal(run.Input, &invocation) != nil || json.Unmarshal(invocation.Input, &event) != nil || event.ID == "" {
				return "", nil, bad("PLUGIN_RUN_SCOPE_DENIED", "清理事件没有绑定持久执行")
			}
			raw, _ := json.Marshal(event)
			return scrapefileclean.ApplyCapability, raw, nil
		}
	}
	if in.PluginID == "RemoveLink" && in.Version == "2.17" {
		switch run.Action {
		case "init", "reload":
			return removelink.ConfigureCapability, json.RawMessage(`{}`), nil
		case "event":
			var invocation plugins.Invocation
			var event struct {
				ID string `json:"event_id"`
			}
			if json.Unmarshal(run.Input, &invocation) != nil || json.Unmarshal(invocation.Input, &event) != nil || event.ID == "" {
				return "", nil, bad("PLUGIN_RUN_SCOPE_DENIED", "清理事件没有绑定持久执行")
			}
			raw, _ := json.Marshal(event)
			return removelink.ApplyCapability, raw, nil
		}
	}
	if in.PluginID == "CleanInvalidSeed" && in.Version == "2.1" {
		switch run.Action {
		case "init", "reload":
			return cleaninvalidseed.ConfigureCapability, json.RawMessage(`{}`), nil
		case "run":
			return cleaninvalidseed.ExecuteCapability, json.RawMessage(`{"action":"clean"}`), nil
		case "event", "service", "once", "command":
			var invocation plugins.Invocation
			var event struct {
				ID string `json:"event_id"`
			}
			if json.Unmarshal(run.Input, &invocation) != nil {
				return "", nil, bad("INVALID_PLUGIN_RUN", "插件执行快照无效")
			}
			d := json.NewDecoder(bytes.NewReader(invocation.Input))
			d.DisallowUnknownFields()
			if d.Decode(&event) != nil || d.Decode(new(any)) != io.EOF || event.ID == "" || len(event.ID) > 128 {
				return "", nil, bad("PLUGIN_RUN_SCOPE_DENIED", "清理事件没有绑定持久执行")
			}
			raw, _ := json.Marshal(event)
			return cleaninvalidseed.ExecuteCapability, raw, nil
		}
	}
	if in.PluginID == "CloudflareSpeedTest" && in.Version == "1.5.1" {
		return cloudflareDeferredSpec(run)
	}
	if in.PluginID == "AutoBackup" && in.Version == "2.1.4" {
		return autoBackupDeferredSpec(run)
	}
	if in.PluginID == "BrushFlowLanduo" && in.Version == "5.2.15" {
		return brushFlowDeferredSpec(run)
	}
	if in.PluginID == "SeedRescue" && in.Version == "1.6.3" {
		return seedRescueDeferredSpec(run)
	}
	return "", nil, nil
}
func deferredSameRun(r pluginDeferredRecord, in PluginInstallation, run PluginRun) bool {
	return run.InstanceID == r.Instance && run.Version == r.Version && r.Schema == 1 && r.Plugin == in.PluginID && r.Instance == in.InstanceID && r.Version == in.Version && r.Run == run.ID && r.Action == run.Action && r.Generation == run.Generation && r.Revision == run.ConfigRevision && r.InputSHA == deferredInputSHA(run.Input)
}
func (p *PluginStore) readDeferred(ctx context.Context, q pluginRemoveLinkJournalReader, run PluginRun) (pluginDeferredRecord, []byte, error) {
	var r pluginDeferredRecord
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, run.ID).Scan(&sealed); err != nil {
		return r, nil, err
	}
	raw, err := p.unseal("run_result", run.InstanceID, "deferred:v1:"+run.ID, sealed)
	if err != nil {
		return r, nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.Run != run.ID || r.Instance != run.InstanceID || r.Schema != 1 || !json.Valid(r.Ack) || !json.Valid(r.Raw) {
		return r, nil, bad("PLUGIN_DEFERRED_INVALID", "插件操作收据损坏")
	}
	switch r.Phase {
	case "prepared", "started", "confirmed", "uncertain", "terminal_failed", "snapshot_cut", "abandoned_unknown":
	default:
		return r, nil, bad("PLUGIN_DEFERRED_INVALID", "插件操作阶段无效")
	}
	if !validPluginDeferredOutcome(r) {
		return r, nil, bad("PLUGIN_DEFERRED_INVALID", "插件操作终态收据无效")
	}
	return r, sealed, nil
}

// This is a native receipt format, never an SDK-supplied error or permission to
// retry. Only the source-specific private journal can establish known failure.
func validPluginDeferredOutcome(r pluginDeferredRecord) bool {
	if r.Phase == "abandoned_unknown" {
		return validAutoBackupDeferredAbandon(r) || validCloudflareDeferredAbandon(r)
	}
	if r.Phase == "snapshot_cut" {
		return validAutoBackupDeferredCut(r)
	}
	if r.Phase != "terminal_failed" {
		return r.ErrorCode == ""
	}
	if isTorrentTransfer(r.Plugin) && r.Version == "1.10.3" {
		return r.ErrorCode == transferSourceFailed && transferResultErrorCode(r.Result) == r.ErrorCode
	}

	if r.Plugin == "CloudflareSpeedTest" && r.Version == "1.5.1" {
		return (r.ErrorCode == "CLOUDFLARE_SOURCE_FAILED" && json.Valid(r.Result)) || (r.ErrorCode == cloudflareBlockedByUnknown && bytes.Equal(r.Result, cloudflareBlockedResult()))
	}
	if r.Plugin == "AutoBackup" && r.Version == "2.1.4" {
		return (r.ErrorCode == "AUTO_BACKUP_SOURCE_FAILED" && json.Valid(r.Result)) || (r.ErrorCode == autoBackupBlockedByUnknown && bytes.Equal(r.Result, autoBackupBlockedResult()))
	}
	if r.Plugin == "BrushFlowLanduo" && r.Version == "5.2.15" {
		return ((r.ErrorCode == "BRUSH_FLOW_SOURCE_FAILED" || r.ErrorCode == "BRUSH_FLOW_OBSERVED") && json.Valid(r.Result)) || (r.ErrorCode == brushFlowBlockedByUnknown && bytes.Equal(r.Result, brushFlowBlockedResult())) || (r.ErrorCode == "BRUSH_FLOW_NOT_STARTED" && bytes.Equal(r.Result, brushFlowNotStartedResult()))
	}
	if r.Plugin == "SeedRescue" && r.Version == "1.6.3" {
		return ((r.ErrorCode == "SEED_RESCUE_SOURCE_FAILED" || r.ErrorCode == seedRescueObserved) && json.Valid(r.Result)) || (r.ErrorCode == seedRescueBlockedByUnknown && bytes.Equal(r.Result, seedRescueBlockedResult()))
	}
	return r.Plugin == "CleanInvalidSeed" && r.Version == "2.1" &&
		(r.ErrorCode == "CLEAN_SEED_SOURCE_FAILED" || r.ErrorCode == "CLEAN_SEED_HOST_DENIED") && json.Valid(r.Result)
}
func (p *PluginStore) writeDeferred(ctx context.Context, tx *sql.Tx, r pluginDeferredRecord, previous []byte) error {
	if !validPluginDeferredOutcome(r) {
		return bad("PLUGIN_DEFERRED_INVALID", "插件操作终态收据无效")
	}
	raw, err := json.Marshal(r)
	if err == nil {
		raw, err = pluginResultJSON(raw)
	}
	if err != nil {
		return err
	}
	sealed, err := p.seal("run_result", r.Instance, "deferred:v1:"+r.Run, raw)
	if err != nil {
		return err
	}
	if previous == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO plugin_deferred_operations(run_id,sealed) VALUES(?,?)`, r.Run, sealed)
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_deferred_operations SET sealed=? WHERE run_id=? AND sealed=?`, sealed, r.Run, previous)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		return bad("PLUGIN_DEFERRED_CONFLICT", "插件操作阶段已变化")
	}
	return nil
}
func (a *App) preparePluginDeferred(ctx context.Context, in PluginInstallation, run PluginRun) (*pluginDeferredCall, error) {
	method, raw, err := pluginDeferredSpec(in, run)
	if err != nil || method == "" {
		return nil, err
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err = a.validatePluginHostRun(ctx, in, &run, method); err != nil {
		return nil, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	persisted, err := a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if err != nil || persisted.State != "running" || !bytes.Equal(persisted.Input, run.Input) || persisted.Version != run.Version || persisted.Generation != run.Generation || persisted.ConfigRevision != run.ConfigRevision || persisted.InstanceID != run.InstanceID || persisted.Action != run.Action {
		return nil, bad("PLUGIN_RUN_SCOPE_DENIED", "插件执行身份已变化")
	}
	if err = a.pluginStore.pluginNativeQueueAdmissionTx(ctx, tx, run.InstanceID, in.PluginID); err != nil {
		return nil, err
	}
	r, _, err := a.pluginStore.readDeferred(ctx, tx, run)
	if errors.Is(err, sql.ErrNoRows) {
		ack, _ := json.Marshal(map[string]string{"deferred": id("pd_")})
		r = pluginDeferredRecord{Schema: 1, Plugin: in.PluginID, Instance: in.InstanceID, Version: in.Version, Run: run.ID, Action: run.Action, Method: method, Generation: run.Generation, Revision: run.ConfigRevision, InputSHA: deferredInputSHA(run.Input), Raw: raw, Ack: ack, Phase: "prepared"}
		err = a.pluginStore.writeDeferred(ctx, tx, r, nil)
	} else if err == nil && (!deferredSameRun(r, in, run) || r.Method != method || !bytes.Equal(r.Raw, raw) || r.Phase != "prepared") {
		err = bad("PLUGIN_DEFERRED_UNCERTAIN", "插件操作已经开始，不能重复执行")
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &pluginDeferredCall{a: a, installation: in, run: run, record: r}, nil
}
func (d *pluginDeferredCall) handshake(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Compare semantic JSON after strict decoding: SDK whitespace is irrelevant,
	// but extra fields, duplicate keys, and arbitrary targets are not accepted.
	valid := identity.PluginID == d.installation.PluginID && identity.InstanceID == d.installation.InstanceID && identity.Version == d.installation.Version && method == d.record.Method && deferredPayloadEqual(raw, d.record.Raw)
	if d.called || !valid || ctx.Err() != nil {
		d.poisoned = true
		return nil, bad("PLUGIN_DEFERRED_PROTOCOL", "插件必须只提交一次绑定的终态操作")
	}
	d.called = true
	return append(json.RawMessage(nil), d.record.Ack...), nil
}
func deferredPayloadEqual(raw, want []byte) bool {
	// The two supported payload shapes are either {} or one event_id string.
	var expected map[string]json.RawMessage
	if json.Unmarshal(want, &expected) != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	got := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok {
			return false
		}
		if _, exists := got[key]; exists {
			return false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
		got[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || decoder.Decode(new(any)) != io.EOF || len(got) != len(expected) {
		return false
	}
	for key, value := range expected {
		var a, b string
		if json.Unmarshal(value, &a) != nil || json.Unmarshal(got[key], &b) != nil || a != b {
			return false
		}
	}
	return true
}
func (d *pluginDeferredCall) changePhase(ctx context.Context, from, to string, result json.RawMessage) error {
	return d.changeOutcome(ctx, from, to, result, "")
}
func (d *pluginDeferredCall) changeOutcome(ctx context.Context, from, to string, result json.RawMessage, code string) error {
	tx, err := d.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, sealed, err := d.a.pluginStore.readDeferred(ctx, tx, d.run)
	if err != nil {
		return err
	}
	if !deferredSameRun(r, d.installation, d.run) || r.Method != d.record.Method || !bytes.Equal(r.Ack, d.record.Ack) || !bytes.Equal(r.Raw, d.record.Raw) || r.Phase != from {
		return bad("PLUGIN_DEFERRED_CONFLICT", "插件操作执行身份或阶段已变化")
	}
	if from == "prepared" {
		if err = d.a.pluginStore.validateDeferredRunTx(ctx, tx, r, d.run); err != nil {
			return err
		}
		if r.Plugin == "CloudflareSpeedTest" && r.Version == "1.5.1" && to == "started" {
			if err = d.cloudflareStartedTx(ctx, tx); err != nil {
				return err
			}
		}
		if err = d.a.pluginStore.pluginNativeQueueAdmissionTx(ctx, tx, r.Instance, r.Plugin); err != nil {
			return err
		}
	}
	if from == "prepared" && to == "started" && r.Plugin == "BrushFlowLanduo" && r.Version == "5.2.15" {
		if err = d.brushFlowStartedTx(ctx, tx); err != nil {
			return err
		}
	}
	if from == "prepared" && to == "started" && r.Plugin == "SeedRescue" && r.Version == "1.6.3" {
		if err = d.seedRescueStartedTx(ctx, tx); err != nil {
			return err
		}
	}

	r.Phase, r.Result, r.ErrorCode = to, result, code
	if err = d.a.pluginStore.writeDeferred(ctx, tx, r, sealed); err != nil {
		return err
	}
	if from == "prepared" && to == "started" && isTorrentTransfer(r.Plugin) {
		admission := context.WithValue(ctx, transferAdmissionKey{}, transferAdmissionFromRun(d.run, r.Method))
		if err = d.a.pluginStore.transferAcceptRunTx(admission, tx, d.run); err != nil {
			return err
		}
	}
	if from == "prepared" && to == "started" && r.Plugin == "IYUUAutoSeed" {
		admission := context.WithValue(ctx, iyuuAdmissionKey{}, iyuuAdmissionFromRun(d.run, r.Method))
		if err = d.a.pluginStore.iyuuAcceptRunTx(admission, tx, d.run); err != nil {
			return err
		}
	}
	if from == "prepared" && to == "started" && r.Plugin == "ScrapeFileClean" {
		if err = d.a.pluginStore.scrapeCleanAcceptTx(ctx, tx, d.run, r.Method); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (d *pluginDeferredCall) ready(result plugins.RunResult) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.called || d.poisoned || result.HostCalls != 1 || !bytes.Equal(result.Result, d.record.Ack) {
		return bad("PLUGIN_DEFERRED_PROTOCOL", "插件没有完成唯一终态操作握手")
	}
	return nil
}

// requireDeferredExecution is a private host capability. It proves both the
// actual trusted execution stack and the sealed started phase; it grants no
// lifecycle/file locks and cannot be constructed from worker JSON.
func (a *App) requireDeferredExecution(ctx context.Context, run *PluginRun, method string) error {
	d, ok := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	if !ok || d == nil || d.a != a || run == nil || !deferredSameRun(d.record, d.installation, *run) || method != d.record.Method || ctx.Err() != nil {
		return bad("PLUGIN_DEFERRED_REQUIRED", "操作没有绑定可信宿主执行")
	}
	r, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, *run)
	if err != nil {
		return err
	}
	if !deferredSameRun(r, d.installation, *run) || r.Phase != "started" || r.Method != method || !bytes.Equal(r.Ack, d.record.Ack) || !bytes.Equal(r.Raw, d.record.Raw) {
		return bad("PLUGIN_DEFERRED_REQUIRED", "操作没有处于已授权阶段")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.pluginStore.validateDeferredRunTx(ctx, tx, r, *run)
}

func (p *PluginStore) validateDeferredRunTx(ctx context.Context, tx *sql.Tx, r pluginDeferredRecord, run PluginRun) error {
	actual, err := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if err != nil {
		return err
	}
	if actual.State != "running" || actual.ID != r.Run || actual.InstanceID != r.Instance || actual.Version != r.Version || actual.Generation != r.Generation || actual.ConfigRevision != r.Revision || actual.Action != r.Action || deferredInputSHA(actual.Input) != r.InputSHA || !bytes.Equal(actual.Input, run.Input) {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件执行已结束或快照已变化")
	}
	var current int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id=? AND i.version=? AND i.generation=? AND i.installed=1 AND i.desired_enabled=1 AND v.revision=?`, r.Instance, r.Plugin, r.Version, r.Generation, r.Revision).Scan(&current)
	if err != nil {
		return err
	}
	if current != 1 {
		if r.Plugin == "BrushFlowLanduo" && r.Version == "5.2.15" && r.Phase == "started" {
			if err = p.brushFlowAcceptedRevisionTx(ctx, tx, r, run); err == nil {
				return nil
			}
		}
		return bad("PLUGIN_SNAPSHOT_STALE", "插件安装或配置已变化")
	}
	return nil
}

func (d *pluginDeferredCall) execute(ctx context.Context, run *PluginRun) (json.RawMessage, error) {
	if d.installation.PluginID == "CloudflareSpeedTest" {
		var release func()
		var err error
		ctx, release, err = d.lockCloudflareDeferred(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if isTorrentTransfer(d.installation.PluginID) {
		var release func()
		var err error
		ctx, release, err = d.lockTransferDeferred(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if d.installation.PluginID == "IYUUAutoSeed" {
		// No worker/global/SQL lock is held while waiting. This owner gate
		// spans admission, started, actual effects and the final receipt.
		var release func()
		var err error
		ctx, release, err = d.lockIYUUDeferred(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if d.record.Plugin == "BrushFlowLanduo" && d.record.Version == "5.2.15" {
		held, release, e := d.lockBrushFlow(ctx)
		if errors.Is(e, errBrushFlowUnknownAdmission) {
			return d.brushFlowBlocked(ctx)
		}
		if errors.Is(e, errBrushFlowBusy) {
			return d.brushFlowBusy(ctx)
		}
		if e != nil {
			return nil, e
		}
		defer release()
		ctx = held
	}
	if d.record.Plugin == "SeedRescue" && d.record.Version == "1.6.3" {
		held, release, e := d.lockSeedRescue(ctx)
		if errors.Is(e, errSeedRescueBusy) {
			return d.seedRescueBusy(ctx)
		}
		if e != nil {
			return nil, e
		}
		defer release()
		ctx = held
	}
	if err := d.changePhase(ctx, "prepared", "started", nil); err != nil {
		return nil, err
	}
	actualCtx := context.WithValue(ctx, pluginDeferredExecutionKey{}, d)
	result, err := d.a.pluginHostCall(actualCtx, d.installation, run, plugins.Identity{PluginID: d.installation.PluginID, InstanceID: d.installation.InstanceID, Version: d.installation.Version}, d.record.Method, d.record.Raw)
	// A cancellation or a lost storage reply after starting is not proof that
	// nothing happened. Persist only the known outcome using a fresh context.
	finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if brushFlowSnapshotOwner(d.record.Plugin, d.record.Version) && ctx.Err() == nil {
		cancel()
		finish, cancel = brushFlowCompletionContext(ctx)
	}
	defer cancel()
	phase, code := "confirmed", ""
	if err != nil {
		phase = "uncertain"
		result = nil
	}
	if isTorrentTransfer(d.record.Plugin) && d.record.Version == "1.10.3" {
		known, found, proofErr := d.a.pluginStore.transferDeferredOutcome(finish, *run, d.record)
		if proofErr != nil || !found {
			phase, result = "uncertain", nil
			err = bad("PLUGIN_DEFERRED_UNCERTAIN", "转移做种结果尚未持久确认")
		} else {
			phase, result, err = "confirmed", known, nil
			if knownCode := transferResultErrorCode(known); knownCode != "" {
				phase, code = "terminal_failed", knownCode
				err = bad(code, "转移做种源流程失败，已保留确定结果")
			}
		}
	}
	if d.record.Plugin == "CloudflareSpeedTest" && d.record.Version == "1.5.1" {
		known, knownCode, found, proofErr := d.a.pluginStore.cloudflareOutcome(finish, d.run, d.record.Method)
		if proofErr != nil || !found {
			phase, result = "uncertain", nil
			err = bad("PLUGIN_DEFERRED_UNCERTAIN", "Cloudflare操作尚未持久确认")
		} else {
			phase, result, err = "confirmed", known, nil
			if knownCode != "" {
				phase, code = "terminal_failed", knownCode
				err = bad(code, "Cloudflare执行失败，已保留确认结果")
			}
		}
	}
	if d.record.Plugin == "AutoBackup" && d.record.Version == "2.1.4" {
		known, knownCode, found, proofErr := d.a.pluginStore.autoBackupOutcome(finish, d.run, d.record.Method)
		if proofErr != nil || !found {
			phase, result = "uncertain", nil
			err = bad("PLUGIN_DEFERRED_UNCERTAIN", "备份操作尚未持久确认")
		} else {
			phase, result, err = "confirmed", known, nil
			if knownCode != "" {
				phase, code = "terminal_failed", knownCode
				err = bad(code, "自动备份执行失败，已保留确认结果")
			}
		}
	}
	if d.record.Plugin == "CleanInvalidSeed" && d.record.Version == "2.1" {
		known, knownCode, found, proofErr := d.a.pluginStore.cleanInvalidSeedDeferredOutcome(finish, d.run, d.record.Method)
		if proofErr != nil || !found {
			phase, result = "uncertain", nil
			err = bad("PLUGIN_DEFERRED_UNCERTAIN", "插件操作结果尚未持久确认，请核对执行记录")
			if proofErr != nil {
				err = bad("PLUGIN_DEFERRED_RECEIPT_FAILED", "插件操作结果无法核验")
			}
		} else {
			phase, result, err = "confirmed", known, nil
			if knownCode != "" {
				phase, code = "terminal_failed", knownCode
				err = bad(code, "插件操作失败，已保留确证结果")
			}
		}
	}
	if d.record.Plugin == "BrushFlowLanduo" && d.record.Version == "5.2.15" {
		known, knownCode, found, proofErr := d.a.pluginStore.brushFlowOutcome(finish, d.run, d.record.Method)
		if proofErr != nil || !found {
			phase, result = "uncertain", nil
			err = bad("PLUGIN_DEFERRED_UNCERTAIN", "稀缺保种结果尚未持久确认")
		} else {
			phase, result, err = "confirmed", known, nil
			if knownCode != "" {
				phase, code = "terminal_failed", knownCode
				err = bad(code, "稀缺保种操作失败，已保留确证结果")
			}
		}
	}
	if d.record.Plugin == "SeedRescue" && d.record.Version == "1.6.3" {
		known, knownCode, found, proofErr := d.a.pluginStore.seedRescueOutcome(finish, d.run, d.record.Method)
		if proofErr != nil || !found {
			phase, result = "uncertain", nil
			err = bad("PLUGIN_DEFERRED_UNCERTAIN", "稀缺保种结果尚未持久确认")
		} else {
			phase, result, err = "confirmed", known, nil
			if knownCode != "" {
				phase, code = "terminal_failed", knownCode
				err = bad(code, "稀缺保种操作失败，已保留确证结果")
			}
		}
	}
	saveErr := d.changeOutcome(finish, "started", phase, result, code)
	if saveErr != nil {
		return nil, bad("PLUGIN_DEFERRED_UNCERTAIN", "插件操作结果尚未持久确认，请核对执行记录")
	}
	return result, err
}

// Recovery never replays started operations. A confirmed receipt finishes the
// old run even after reset/reinstallation; prepared interrupted workers can be
// retried because the actual owner entry was never entered.
func (p *PluginStore) recoverDeferredRuns(ctx context.Context) error {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT r.id,r.instance_id FROM plugin_runs r JOIN plugin_deferred_operations d ON d.run_id=r.id WHERE r.state IN ('pending','running','reconcile_pending','failed')`)
	if err != nil {
		return err
	}
	type ref struct{ id, instance string }
	var refs []ref
	for rows.Next() {
		var r ref
		if err = rows.Scan(&r.id, &r.instance); err != nil {
			break
		}
		refs = append(refs, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		run, err := p.Run(ctx, ref.id)
		if err != nil {
			return err
		}
		r, previous, err := p.readDeferred(ctx, p.store.DB, run)
		if err != nil {
			return err
		}
		// An uninstall receipt may have advanced its own revision. That recovery
		// already completed above; an unfinished deferred record stays conservative.
		if r.Run != run.ID || r.Instance != run.InstanceID || r.Version != run.Version || r.Generation != run.Generation || r.Action != run.Action || r.InputSHA != deferredInputSHA(run.Input) {
			return bad("PLUGIN_DEFERRED_INVALID", "插件操作恢复身份不匹配")
		}

		if isTorrentTransfer(r.Plugin) && (r.Phase == "started" || r.Phase == "uncertain") {
			result, found, e := p.transferDeferredOutcome(ctx, run, r)
			if e != nil {
				if code := transferRecoveryErrorCode(e); code != "" {
					if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='reconcile_pending',error_code=?,updated_at=? WHERE id=? AND state IN ('running','reconcile_pending','failed')`, code, now(), run.ID); err != nil {
						return err
					}
					continue
				}
				return e
			}
			if found {
				r.Phase, r.Result = "confirmed", result
				if code := transferResultErrorCode(result); code != "" {
					r.Phase, r.ErrorCode = "terminal_failed", code
				}
				tx, e := p.store.DB.BeginTx(ctx, nil)
				if e != nil {
					return e
				}
				if e = p.writeDeferred(ctx, tx, r, previous); e != nil {
					tx.Rollback()
					return e
				}
				if e = tx.Commit(); e != nil {
					return e
				}
				r, previous, e = p.readDeferred(ctx, p.store.DB, run)
				if e != nil {
					return e
				}
			}
		}

		if r.Plugin == "IYUUAutoSeed" && (r.Phase == "started" || r.Phase == "uncertain") {
			result, found, e := p.iyuuDeferredOutcome(ctx, run, r)
			if e != nil {
				if code := iyuuRecoveryErrorCode(e); code != "" {
					if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='reconcile_pending',error_code=?,updated_at=? WHERE id=? AND state IN ('running','reconcile_pending','failed')`, code, now(), run.ID); err != nil {
						return err
					}
					continue // Keep original private evidence; unrelated App features can start.
				}
				return e
			}
			if found {
				r.Phase, r.Result = "confirmed", result
				tx, e := p.store.DB.BeginTx(ctx, nil)
				if e != nil {
					return e
				}
				if e = p.writeDeferred(ctx, tx, r, previous); e != nil {
					tx.Rollback()
					return e
				}
				if e = tx.Commit(); e != nil {
					return e
				}
				r, previous, e = p.readDeferred(ctx, p.store.DB, run)
				if e != nil {
					return e
				}
			}
		}
		if r.Plugin == "CleanInvalidSeed" && r.Revision != run.ConfigRevision {
			return bad("PLUGIN_DEFERRED_INVALID", "清理操作原始配置身份已变化")
		}
		if (r.Plugin == "ScrapeFileClean" || r.Plugin == "RemoveLink" || r.Plugin == "CleanInvalidSeed" || r.Plugin == "AutoBackup" || r.Plugin == "CloudflareSpeedTest" || r.Plugin == "SeedRescue" || r.Plugin == "BrushFlowLanduo") && (r.Phase == "started" || r.Phase == "uncertain") {
			var result json.RawMessage
			var code string
			var found bool
			var proofErr error
			if r.Plugin == "BrushFlowLanduo" {
				result, code, found, proofErr = p.brushFlowOutcome(ctx, run, r.Method)
			} else if r.Plugin == "SeedRescue" {
				result, code, found, proofErr = p.seedRescueOutcome(ctx, run, r.Method)
			} else if r.Plugin == "CloudflareSpeedTest" {
				result, code, found, proofErr = p.cloudflareOutcome(ctx, run, r.Method)
			} else if r.Plugin == "AutoBackup" {
				result, code, found, proofErr = p.autoBackupOutcome(ctx, run, r.Method)
			} else if r.Plugin == "ScrapeFileClean" {
				result, found, proofErr = p.scrapeCleanDeferredOutcome(ctx, run, r.Method)
			} else if r.Plugin == "RemoveLink" {
				result, found, proofErr = p.removeLinkDeferredOutcome(ctx, run, r.Method)
			} else {
				result, code, found, proofErr = p.cleanInvalidSeedDeferredOutcome(ctx, run, r.Method)
			}
			if proofErr != nil {
				if (r.Plugin == "AutoBackup" && autoBackupErrorCode(proofErr) == "AUTO_BACKUP_EVIDENCE_MISSING") || (r.Plugin == "CloudflareSpeedTest" && cloudflareErrorCode(proofErr) == "CLOUDFLARE_EVIDENCE_MISSING") || (r.Plugin == "BrushFlowLanduo" && brushFlowErrorCode(proofErr) == "BRUSH_FLOW_EVIDENCE_MISSING") {
					code := "AUTO_BACKUP_EVIDENCE_MISSING"
					if r.Plugin == "BrushFlowLanduo" {
						code = "BRUSH_FLOW_EVIDENCE_MISSING"
					}
					if r.Plugin == "CloudflareSpeedTest" {
						code = "CLOUDFLARE_EVIDENCE_MISSING"
					}
					if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='reconcile_pending',error_code=?,updated_at=? WHERE id=? AND state IN ('running','reconcile_pending','failed')`, code, now(), run.ID); err != nil {
						return err
					}
					continue // Preserve private evidence; other plugins and diagnostics can still start.
				}
				return proofErr
			}
			if found {
				r.Phase, r.Result = "confirmed", result
				if code != "" {
					r.Phase, r.ErrorCode = "terminal_failed", code
				}
				tx, err := p.store.DB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				if err = p.writeDeferred(ctx, tx, r, previous); err != nil {
					_ = tx.Rollback()
					return err
				}
				if err = tx.Commit(); err != nil {
					return err
				}
			}
		}
		if run.State == "failed" && r.Phase != "confirmed" && r.Phase != "terminal_failed" {
			continue
		}
		state, code := "reconcile_pending", "PLUGIN_DEFERRED_UNCERTAIN"
		var result []byte
		switch r.Phase {
		case "prepared":
			var current int
			if err = p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id=? AND i.version=? AND i.generation=? AND i.installed=1 AND i.desired_enabled=1 AND v.revision=?`, r.Instance, r.Plugin, r.Version, r.Generation, r.Revision).Scan(&current); err != nil {
				return err
			}
			if current == 1 && run.ConfigRevision == r.Revision {
				state, code = "pending", ""
			} else {
				state, code = "canceled", "PLUGIN_SNAPSHOT_STALE"
			}
		case "confirmed":
			result, err = p.seal("run_result", r.Instance, r.Run, r.Result)
			if err != nil {
				return err
			}
			state, code = "succeeded", ""
		case "terminal_failed":
			result, err = p.seal("run_result", r.Instance, r.Run, r.Result)
			if err != nil {
				return err
			}
			state, code = "failed", r.ErrorCode
		}
		tx, err := p.store.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state=?,error_code=?,result=COALESCE(?,result),updated_at=? WHERE id=? AND state IN ('pending','running','reconcile_pending','failed')`, state, code, result, now(), run.ID)
		if err == nil && r.Phase == "terminal_failed" && (run.Action == "init" || run.Action == "reload") && !(r.Plugin == "BrushFlowLanduo" && r.Version == "5.2.15" && r.ErrorCode == "BRUSH_FLOW_NOT_STARTED" && bytes.Equal(r.Result, brushFlowNotStartedResult())) {
			// A crash between receipt and finishRun must not bypass the same
			// startup replay block used by the ordinary confirmed-failure path.
			_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO plugin_run_startup_blocks(run_id) SELECT id FROM plugin_runs WHERE id=? AND state='failed'`, run.ID)
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Only a failed worker whose trusted owner entry was never started is eligible.
// The existing run/input and transition are retained; reload is not repurposed.
func (a *App) retryPluginDeferred(ctx context.Context, instance, runID string) (PluginRun, error) {
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	run, err := a.pluginStore.Run(ctx, runID)
	if err != nil {
		return PluginRun{}, err
	}
	installation, err := a.pluginStore.Installation(ctx, instance)
	if err != nil {
		return PluginRun{}, err
	}
	if run.InstanceID != instance || run.State != "failed" {
		return PluginRun{}, bad("PLUGIN_RETRY_DENIED", "只能重试尚未开始实际操作的失败执行")
	}
	r, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if err != nil {
		return PluginRun{}, bad("PLUGIN_RETRY_DENIED", "没有可证明尚未执行的操作记录")
	}
	if r.Phase != "prepared" || !deferredSameRun(r, installation, run) {
		return PluginRun{}, bad("PLUGIN_RETRY_DENIED", "操作已开始或配置已变化，不能直接重试")
	}
	if installation.PluginID == "ScrapeFileClean" {
		if err = a.validateScrapeFileCleanDeferredRetry(ctx, run); err != nil {
			return PluginRun{}, err
		}
	}
	if installation.PluginID == "RemoveLink" {
		if err = a.validateRemoveLinkDeferredRetry(ctx, run); err != nil {
			return PluginRun{}, err
		}
	}
	if installation.PluginID == "CleanInvalidSeed" {
		if err = a.validateCleanInvalidSeedDeferredRetry(ctx, run); err != nil {
			return PluginRun{}, err
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginRun{}, err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id=? AND i.version=? AND i.generation=? AND i.installed=1 AND i.desired_enabled=1 AND v.revision=?`, instance, r.Plugin, r.Version, r.Generation, r.Revision).Scan(&current); err != nil {
		return PluginRun{}, err
	}
	if current != 1 {
		return PluginRun{}, bad("PLUGIN_RETRY_DENIED", "插件安装或配置已变化")
	}
	var other int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND id<>? AND state IN ('pending','running','reconcile_pending')`, instance, runID).Scan(&other); err != nil {
		return PluginRun{}, err
	}
	if other != 0 {
		return PluginRun{}, bad("PLUGIN_RETRY_DENIED", "该插件还有未完成执行，请先核对")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='pending',result=NULL,error_code='',updated_at=? WHERE id=? AND state='failed'`, now(), runID); err != nil {
		return PluginRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_run_startup_blocks WHERE run_id=?`, runID); err != nil {
		return PluginRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return PluginRun{}, err
	}
	run.State, run.Result, run.ErrorCode = "pending", nil, ""
	return run, nil
}

func (a *App) pluginDeferredRetryHTTP(w http.ResponseWriter, r *http.Request, instance string) (any, error) {
	var body struct {
		RunID string `json:"run_id"`
	}
	if err := decodePluginBody(w, r, &body); err != nil {
		return nil, err
	}
	if !pluginStorageID.MatchString(instance) || !pluginStorageID.MatchString(body.RunID) {
		return nil, bad("INVALID_PLUGIN_RUN", "插件执行编号无效")
	}
	run, err := a.retryPluginDeferred(r.Context(), instance, body.RunID)
	if err == nil {
		a.wakePlugins()
	}
	return map[string]any{"run": run}, err
}

func (a *App) pluginRunHistory(ctx context.Context, instance string) ([]PluginRun, error) {
	runs, err := a.pluginStore.Runs(ctx, instance, 50)
	if err != nil {
		return nil, err
	}
	installation, err := a.pluginStore.Installation(ctx, instance)
	if err != nil {
		return nil, err
	}
	config, configErr := a.pluginStore.Config(ctx, instance)
	for index := range runs {
		run := &runs[index]
		if installation.PluginID == "CloudflareSpeedTest" {
			full, e := a.pluginStore.Run(ctx, run.ID)
			if e != nil {
				return nil, e
			}
			run.Recovery, e = a.pluginStore.cloudflareDiagnostic(ctx, installation, full)
			if e != nil {
				return nil, e
			}
		}
		if installation.PluginID == "AutoBackup" {
			full, e := a.pluginStore.Run(ctx, run.ID)
			if e != nil {
				return nil, e
			}
			run.Recovery, e = a.pluginStore.autoBackupDiagnostic(ctx, installation, full)
			if e != nil {
				return nil, e
			}
		}
		if run.State != "failed" || configErr != nil || !installation.Installed || !installation.DesiredEnabled || installation.Generation != run.Generation || installation.Version != run.Version || config.Revision != run.ConfigRevision {
			continue
		}
		r, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, *run)
		if err != nil || r.Phase != "prepared" || r.Instance != instance || r.Plugin != installation.PluginID || r.Version != run.Version || r.Generation != run.Generation || r.Revision != run.ConfigRevision || r.Action != run.Action {
			continue
		}
		run.Retryable = true
	}
	return runs, nil
}
