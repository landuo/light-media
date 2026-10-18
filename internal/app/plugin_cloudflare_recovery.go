package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Public diagnostics contain only a fixed phase label and explanation. Original
// paths, remote endpoints, credentials, payloads and private evidence stay sealed.
type cloudflareRecovery = autoBackupRecovery
type cloudflareResolution struct {
	Version                                                            int
	ID, Instance, RunID, Action, Method, InputSHA, At, Actor, Decision string
	Generation, Revision                                               int64
	OriginalAction, OriginalDeferred                                   json.RawMessage
}

func validCloudflareResolution(r cloudflareAction, instance string, generation int64) bool {
	v := r.Resolution
	if v == nil || v.Version != 1 || v.Decision != "abandon_without_replay" || v.Actor != "authenticated_management" || !pluginStorageID.MatchString(v.ID) || v.Instance != instance || v.RunID != r.RunID || v.Generation != generation || v.Revision != r.Revision || v.Action != r.Action || v.Method != r.Method || v.InputSHA != r.InputSHA || r.ErrorCode != "CLOUDFLARE_ABANDONED_UNKNOWN" {
		return false
	}
	if _, e := time.Parse(time.RFC3339Nano, v.At); e != nil {
		return false
	}
	var original cloudflareAction
	if string(v.OriginalAction) != "null" {
		if cleanupJournalStrict(v.OriginalAction, &original) != nil || original.Phase != "started" || original.RunID != r.RunID || original.InputSHA != r.InputSHA || original.Revision != r.Revision || original.Action != r.Action || original.Method != r.Method || original.Resolution != nil || !bytes.Equal(mustCleanSeedJSON(original.Effects), mustCleanSeedJSON(r.Effects)) {
			return false
		}
	}
	var d pluginDeferredRecord
	if cleanupJournalStrict(v.OriginalDeferred, &d) != nil || d.Plugin != "CloudflareSpeedTest" || d.Version != "1.5.1" || d.Run != r.RunID || d.Instance != instance || d.Generation != generation || d.Revision != r.Revision || d.InputSHA != r.InputSHA || d.Method != r.Method || (d.Phase != "started" && d.Phase != "uncertain") {
		return false
	}
	return true
}
func validCloudflareDeferredAbandon(d pluginDeferredRecord) bool {
	var v cloudflareResolution
	return d.Plugin == "CloudflareSpeedTest" && d.Version == "1.5.1" && d.ErrorCode == "CLOUDFLARE_ABANDONED_UNKNOWN" && cleanupJournalStrict(d.Result, &v) == nil && v.Version == 1 && v.Decision == "abandon_without_replay" && v.Actor == "authenticated_management" && v.RunID == d.Run && v.Instance == d.Instance && v.Generation == d.Generation && v.Revision == d.Revision && v.Action == d.Action && v.Method == d.Method && v.InputSHA == d.InputSHA
}
func cloudflareStage(kind string) string {
	switch kind {
	case "runtime_reset":
		return "测速目录重建"
	case "archive_download":
		return "官方测速程序获取"
	case "archive_extract":
		return "测速程序解包"
	case "cli":
		return "原生测速程序执行"
	case "custom_hosts":
		return "CustomHosts配置保存"
	case "own_config":
		return "优选IP配置保存"
	case "custom_reload":
		return "CustomHosts重载投递"
	case "notice":
		return "通知处理"
	case "configure":
		return "配置与定时服务发布"
	}
	return "优选结果保存"
}
func (p *PluginStore) cloudflareDiagnostic(ctx context.Context, i PluginInstallation, run PluginRun) (*cloudflareRecovery, error) {
	if i.PluginID != "CloudflareSpeedTest" || run.Version != "1.5.1" {
		return nil, nil
	}
	if run.ErrorCode == "CLOUDFLARE_ABANDONED_UNKNOWN" {
		return &cloudflareRecovery{Stage: "已确认放弃旧执行", Message: "未知副作用证据已保留；旧动作不会重放，可发起新的优选。"}, nil
	}
	if run.State != "reconcile_pending" && run.State != "failed" {
		return nil, nil
	}
	d, _, e := p.readDeferred(ctx, p.store.DB, run)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return &cloudflareRecovery{Stage: "执行证据校验", Message: "执行记录无法校验，请检查存储或恢复可信优选。"}, nil
	}
	if d.Phase == "terminal_failed" && d.ErrorCode == "CLOUDFLARE_SOURCE_FAILED" {
		var result struct {
			ErrorType string `json:"error_type"`
		}
		_ = json.Unmarshal(d.Result, &result)
		return &cloudflareRecovery{Stage: "优选业务处理", Message: "优选未完成（" + result.ErrorType + "）；已确认过的操作已保留，可检查配置后发起新执行。"}, nil
	}
	if d.Phase == "terminal_failed" && d.ErrorCode == cloudflareBlockedByUnknown {
		return &cloudflareRecovery{Stage: "优选被阻止", Message: "已有优选结果待核对，此次执行未开始文件或远程操作；请处理原待核对记录后再发起优选。"}, nil
	}
	if d.Phase != "started" && d.Phase != "uncertain" {
		return nil, nil
	}
	s, _, e := p.cloudflareRead(ctx, p.store.DB, run.InstanceID, run.Generation)
	if e != nil {
		if cloudflareErrorCode(e) == "CLOUDFLARE_EVIDENCE_MISSING" {
			return &cloudflareRecovery{Stage: "优选执行证据校验", Message: "Cloudflare优选执行证据缺失或无法校验，请恢复可信优选；未提供放弃或重新初始化入口。"}, nil
		}
		return &cloudflareRecovery{Stage: "执行证据校验", Message: "私有执行证据无法校验，未提供放弃入口，请检查存储或恢复可信优选。"}, nil
	}
	r, ok := s.Runs[run.ID]
	stage := "优选授权检查"
	if ok {
		stage = cloudflareStage("configure")
		if run.Action != "init" && run.Action != "reload" {
			stage = cloudflareStage("")
		}
		for _, effect := range r.Effects {
			if effect.Phase == "started" {
				stage = cloudflareStage(effect.Kind)
				break
			}
		}
		if r.Phase != "started" {
			return nil, nil
		}
	}
	allowed := i.Installed && i.Generation == run.Generation && i.Version == run.Version && d.Instance == run.InstanceID && d.Run == run.ID && d.InputSHA == deferredInputSHA(run.Input) && d.Revision == run.ConfigRevision
	return &cloudflareRecovery{Stage: stage, Message: "此阶段的结果未能确认。请先核对测速目录、优选IP及CustomHosts配置和重载任务；确认放弃只取消旧执行、保留全部未知证据，不回滚或重试测速或配置操作。", AbandonAllowed: allowed}, nil
}
func (a *App) pluginCloudflareAbandonHTTP(w http.ResponseWriter, r *http.Request, instance string) (any, error) {
	if r.Method != "POST" || !a.authenticated(r) || !sameRequestOrigin(r) {
		return nil, &APIError{"AUTH_REQUIRED", "请登录后在同一站点操作", 403}
	}
	var body struct {
		RunID   string `json:"run_id"`
		Confirm bool   `json:"confirm_abandon"`
	}
	if e := decodePluginBody(w, r, &body); e != nil {
		return nil, e
	}
	if !body.Confirm || !pluginStorageID.MatchString(body.RunID) {
		return nil, bad("CLOUDFLARE_CONFIRM_REQUIRED", "请核对实际文件后明确确认放弃旧执行")
	}
	ctx := r.Context()
	i, e := a.pluginStore.Installation(ctx, instance)
	if e != nil {
		return nil, e
	}
	o, e := a.cloudflareOwner(ctx, i)
	if e != nil {
		return nil, e
	}
	if e = o.lock(ctx); e != nil {
		return nil, e
	}
	defer o.unlock()
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	run, e := a.pluginStore.Run(ctx, body.RunID)
	if e != nil || run.InstanceID != instance {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "执行不属于此插件")
	}
	diag, e := a.pluginStore.cloudflareDiagnostic(ctx, i, run)
	if e != nil || diag == nil || !diag.AbandonAllowed {
		return nil, bad("CLOUDFLARE_ABANDON_DENIED", "此执行不允许放弃，或状态已改变")
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	actual, e := a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if e != nil || actual.InstanceID != instance || actual.Generation != i.Generation || actual.ConfigRevision != run.ConfigRevision || !bytes.Equal(actual.Input, run.Input) || (actual.State != "reconcile_pending" && actual.State != "failed") {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "执行身份或阶段已改变")
	}
	s, previous, e := a.pluginStore.cloudflareRead(ctx, tx, instance, run.Generation)
	if e != nil {
		return nil, e
	}
	d, old, e := a.pluginStore.readDeferred(ctx, tx, run)
	if e != nil || (d.Phase != "started" && d.Phase != "uncertain") {
		return nil, bad("CLOUDFLARE_ABANDON_DENIED", "原执行阶段已改变")
	}
	method, _, e := cloudflareDeferredSpec(run)
	if e != nil || d.Method != method || d.InputSHA != deferredInputSHA(run.Input) || d.Revision != run.ConfigRevision {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "原执行授权无法校验")
	}
	original, ok := s.Runs[run.ID]
	oldAction := json.RawMessage(`null`)
	if ok {
		if original.Phase != "started" {
			return nil, bad("CLOUDFLARE_ABANDON_DENIED", "原生执行结果已经确认")
		}
		oldAction = mustCleanSeedJSON(original)
	} else {
		original = cloudflareAction{RunID: run.ID, Action: run.Action, Method: method, InputSHA: deferredInputSHA(run.Input), Revision: run.ConfigRevision}
	}
	resolution := cloudflareResolution{Version: 1, ID: id("abr_"), Instance: instance, RunID: run.ID, Generation: run.Generation, Revision: run.ConfigRevision, Action: run.Action, Method: method, InputSHA: deferredInputSHA(run.Input), At: time.Now().UTC().Format(time.RFC3339Nano), Actor: "authenticated_management", Decision: "abandon_without_replay", OriginalAction: oldAction, OriginalDeferred: mustCleanSeedJSON(d)}
	original.Phase = "abandoned_unknown"
	original.ErrorCode = "CLOUDFLARE_ABANDONED_UNKNOWN"
	original.Resolution = &resolution
	if !validCloudflareResolution(original, instance, run.Generation) {
		return nil, bad("CLOUDFLARE_JOURNAL_INVALID", "放弃旧执行的证据无法校验")
	}
	s.Runs[run.ID] = original
	if e = a.pluginStore.cloudflareSave(ctx, tx, s, previous); e != nil {
		return nil, e
	}
	d.Phase = "abandoned_unknown"
	d.ErrorCode = original.ErrorCode
	d.Result = mustCleanSeedJSON(resolution)
	if e = a.pluginStore.writeDeferred(ctx, tx, d, old); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code=?,updated_at=? WHERE id=? AND state IN ('reconcile_pending','failed')`, original.ErrorCode, now(), run.ID); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='canceled' WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=? AND state IN ('queued','running','reconcile_pending','failed')`, run.ID, instance, run.Generation, run.ConfigRevision); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	a.pluginExecutionLog(i, run, "WARN", "backup_abandoned_unknown", "用户已确认放弃旧优选执行；未知副作用证据保留，旧动作不会重放，允许发起新优选", original.ErrorCode)
	return map[string]bool{"canceled": true, "evidence_retained": true, "old_actions_will_not_replay": true}, nil
}
