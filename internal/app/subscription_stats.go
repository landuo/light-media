package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/iyuuautoseed/compat/urlprep"
	"light-media/internal/subscriptionstats"
)

// A thread start in the source is represented by a committed child job. Source
// child false/None/exception and native dependency are not a parent rollback.
// The encrypted source snapshot is separate from the actual HTTP receipt.
type subscriptionStatsRuntime struct {
	asyncClient   *subscriptionstats.AsyncClient
	lifeMu        sync.Mutex
	lifeContext   context.Context
	lifeCancel    context.CancelFunc
	lifeWG        sync.WaitGroup
	closed        bool
	closeHTTP     func()
	closeHTTPOnce sync.Once
	mu            sync.Mutex
	base          *http.Transport
	afterCommit   func(string) error
}
type subscriptionStatsLark struct {
	Instance, Inbox, InputSHA, EventID, SubscriptionID string
	Generation                                         int64
	Ordinal                                            int
	Notice                                             sourceBusinessNotice
}
type subscriptionStatsInput struct {
	Mode         string                   `json:",omitempty"`
	Added        *subscriptionAddIdentity `json:",omitempty"`
	Registration []byte                   `json:",omitempty"`
	Version      int
	Operation    string
	Completion   *subscriptionCompletionIdentity `json:",omitempty"`
	Lark         *subscriptionStatsLark          `json:",omitempty"`
	// Bytes preserve a child JSON-encoding failure without forcing that failure
	// into the already successful parent admission.
	Subscribe, MediaInfo []byte `json:",omitempty"`
	Settings             subscriptionstats.SettingsSnapshot
	Environment          []string // explicit [] preserves a captured empty proxy environment
	SettingsMissing      string   `json:",omitempty"`
}
type subscriptionStatsReceipt struct {
	Version                    int
	ID, JobID, InputSHA, Stage string
	Input                      subscriptionStatsInput
	Result                     subscriptionstats.Result
	NativeError                string `json:",omitempty"`
}
type sourceStatisticsSettings struct {
	Version     int
	Snapshot    subscriptionstats.SettingsSnapshot
	Environment *[]string // nil uses current native requests environment; pointer to [] is known empty
}

func subscriptionStatsEvidenceError() error {
	return bad("SUBSCRIPTION_STATS_UNCONFIRMED", "订阅统计的任务、原始业务或发送记录无法核对")
}
func (a *App) subscriptionStatsSettingsTx(ctx context.Context, tx *sql.Tx) (subscriptionstats.SettingsSnapshot, string, error) {
	var sealed []byte
	e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_statistics_settings' AND id='main'`).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return subscriptionstats.SettingsSnapshot{}, "source_settings_not_obtained", nil
	}
	if e != nil {
		if ctx.Err() != nil {
			return subscriptionstats.SettingsSnapshot{}, "", ctx.Err()
		}
		return subscriptionstats.SettingsSnapshot{}, "source_settings_read_failed", nil
	}
	raw, e := a.pluginStore.unseal("source_statistics_settings", "system", "main", sealed)
	var value sourceStatisticsSettings
	if e != nil || cleanupJournalStrict(raw, &value) != nil || value.Version != 1 {
		return subscriptionstats.SettingsSnapshot{}, "source_settings_unconfirmed", nil
	}
	// Only requests proxy environment values are relevant. Never seal the entire
	// process environment, which may contain unrelated credentials.
	if value.Environment != nil {
		value.Snapshot.Environment = append([]string{}, (*value.Environment)...)
		return value.Snapshot, "", nil
	}
	value.Snapshot.Environment = []string{}
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			value.Snapshot.Environment = append(value.Snapshot.Environment, item)
		}
	}
	return value.Snapshot, "", nil
}
func (a *App) enqueueSubscriptionStatsTx(ctx context.Context, tx *sql.Tx, input subscriptionStatsInput, wf string) (string, error) {
	input.Version = 1
	var e error
	input.Settings, input.SettingsMissing, e = a.subscriptionStatsSettingsTx(ctx, tx)
	if e != nil {
		return "", e
	}
	input.Environment = append([]string{}, input.Settings.Environment...)
	input.Settings.Environment = nil
	raw, e := cleanupJournalJSON(input)
	if e != nil {
		return "", e
	}
	key := "subscription_stats_" + a.pluginStore.noticeID("subscription-statistics", input.Operation, tokenHash(string(raw)))
	var old subscriptionStatsReceipt
	e = a.readQualitySealed(ctx, tx, "subscription_stats", key, &old)
	if e == nil {
		if old.ID != key || old.InputSHA != tokenHash(string(raw)) || !bytes.Equal(mustCleanSeedJSON(old.Input), raw) {
			return "", subscriptionStatsEvidenceError()
		}
		return key, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	job, e := enqueueTx(ctx, tx, "subscription_stats", map[string]string{"id": key}, wf)
	if e != nil {
		return "", e
	}
	receipt := subscriptionStatsReceipt{Version: 1, ID: key, JobID: job.ID, InputSHA: tokenHash(string(raw)), Stage: "accepted", Input: input}
	return key, a.writeQualitySealedTx(ctx, tx, "subscription_stats", key, receipt)
}
func (a *App) enqueueSubscriptionCompletionStatsTx(ctx context.Context, tx *sql.Tx, identity subscriptionCompletionIdentity, subscribe, mediaInfo json.RawMessage) error {
	// The parent is still running in this TX; its final complete marker commits
	// together with this job. No HTTP or child encoding occurs in this scope.
	if _, e := a.pluginStore.subscriptionCompletionNoticeStageTx(ctx, tx, identity); e != nil {
		return e
	}
	var source subscriptionCompletionReceipt
	if e := a.readQualitySealed(ctx, tx, "subscription_completion", identity.ID, &source); e != nil {
		return e
	}
	if source.Identity != identity || !bytes.Equal(source.Input.Source.Subscribe, subscribe) || !bytes.Equal(source.Input.Source.MediaInfo, mediaInfo) {
		return subscriptionStatsEvidenceError()
	}
	var wf string
	if e := tx.QueryRowContext(ctx, `SELECT workflow_id FROM jobs WHERE id=? AND kind='subscription_completion' AND state='running'`, identity.JobID).Scan(&wf); e != nil {
		return e
	}
	_, e := a.enqueueSubscriptionStatsTx(ctx, tx, subscriptionStatsInput{Operation: "done", Completion: &identity, Subscribe: append([]byte(nil), subscribe...), MediaInfo: append([]byte(nil), mediaInfo...)}, wf)
	return e
}

func (a *App) validateSubscriptionStatsSource(ctx context.Context, q pluginRemoveLinkJournalReader, r subscriptionStatsReceipt, running Job) (cause error) {
	phase := "envelope"
	defer func() {
		if cause != nil {
			cause = fmt.Errorf("statistics source %s: %w", phase, cause)
		}
	}()
	if r.Version != 1 || r.ID == "" || r.JobID == "" || r.Input.Version != 1 || r.InputSHA != tokenHash(string(mustCleanSeedJSON(r.Input))) {
		return subscriptionStatsEvidenceError()
	}
	phase = "job"
	var job Job
	var e error
	job, e = scanJob(q.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, r.JobID))
	if e != nil || job.Kind != "subscription_stats" || job.State != "running" || job.Generation != running.Generation || job.ID != running.ID || job.WorkflowID != running.WorkflowID {
		return subscriptionStatsEvidenceError()
	}
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(job.Payload, &payload) != nil || payload.ID != r.ID {
		return subscriptionStatsEvidenceError()
	}
	if r.Input.Mode == "await_async" {
		return a.validateUISubscriptionStatisticsSource(ctx, q, r)
	}
	if r.Input.Mode != "" || r.Input.Added != nil || len(r.Input.Registration) != 0 {
		return subscriptionStatsEvidenceError()
	}
	switch r.Input.Operation {
	case "done":
		if r.Input.Completion == nil || r.Input.Lark != nil {
			return subscriptionStatsEvidenceError()
		}
		phase = "completion_stage"
		identity := *r.Input.Completion
		stage, e := a.pluginStore.readSubscriptionCompletionNoticeStageTx(ctx, q, identity, false)
		if e != nil || stage != "complete" {
			return subscriptionStatsEvidenceError()
		}
		phase = "completion_input"
		var source subscriptionCompletionReceipt
		if e = a.readQualitySealed(ctx, q, "subscription_completion", identity.ID, &source); e != nil || !bytes.Equal(source.Input.Source.Subscribe, r.Input.Subscribe) || !bytes.Equal(source.Input.Source.MediaInfo, r.Input.MediaInfo) {
			return subscriptionStatsEvidenceError()
		}
		phase = "completion_event"
		var complete struct {
			SubscribeID string          `json:"subscribe_id"`
			Subscribe   json.RawMessage `json:"subscribe_info"`
			MediaInfo   json.RawMessage `json:"mediainfo"`
		}
		if e = a.readQualitySealed(ctx, q, "source_subscribe_complete", identity.ID, &complete); e != nil || complete.SubscribeID != identity.SubscriptionID || !bytes.Equal(complete.Subscribe, r.Input.Subscribe) || !bytes.Equal(complete.MediaInfo, r.Input.MediaInfo) {
			return subscriptionStatsEvidenceError()
		}
	case "add":
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "旧插件订阅统计入口尚未适配标准模块")
	default:
		return subscriptionStatsEvidenceError()
	}
	eventID := ""
	if r.Input.Operation == "done" {
		eventID = domainEventID("subscription.completed", r.Input.Completion.ID)
	} else {
		eventID = r.Input.Lark.EventID
	}
	var eventRaw []byte
	if e = q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, eventID).Scan(&eventRaw); e != nil {
		return subscriptionStatsEvidenceError()
	}
	var event DomainEvent
	if json.Unmarshal(eventRaw, &event) != nil || event.EventID != eventID {
		return subscriptionStatsEvidenceError()
	}
	if r.Input.Operation == "done" && (event.Type != "subscription.completed" || event.SubscriptionID != r.Input.Completion.SubscriptionID || event.JobID != r.Input.Completion.JobID) {
		return subscriptionStatsEvidenceError()
	}
	if r.Input.Operation == "add" && (event.Type != "subscription.added" || event.SubscriptionID != r.Input.Lark.SubscriptionID) {
		return subscriptionStatsEvidenceError()
	}
	return nil
}
func (a *App) commitSubscriptionStatsTx(ctx context.Context, tx *sql.Tx, r subscriptionStatsReceipt, operation string) error {
	if e := a.writeQualitySealedTx(ctx, tx, "subscription_stats", r.ID, r); e != nil {
		return e
	}
	if e := tx.Commit(); e != nil {
		return e
	}
	if hook := a.subscriptionStats.afterCommit; hook != nil {
		if e := hook(operation); e != nil {
			// Lost local ACK: authenticate the exact committed receipt. Never replay
			// HTTP merely because its final local response was lost.
			var got subscriptionStatsReceipt
			read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			if a.readQualitySealed(read, a.Store.DB, "subscription_stats", r.ID, &got) != nil || !bytes.Equal(mustCleanSeedJSON(got), mustCleanSeedJSON(r)) {
				return subscriptionStatsEvidenceError()
			}
		}
	}
	return nil
}
func subscriptionStatsChildError(r subscriptionStatsReceipt) error {
	if !r.Result.SourceReturned && r.Result.SourceError != "" {
		return &APIError{Code: "SUBSCRIPTION_STATS_SOURCE_ERROR", Message: "原订阅统计后台调用异常：" + r.Result.SourceError, Status: 500}
	}
	if r.NativeError != "" {
		return bad(r.NativeError, "订阅统计后台任务未能执行，原订阅结果已保留")
	}
	return nil
}
func (a *App) runSubscriptionStats(ctx context.Context, j Job, key string) error {
	ctx, end, e := a.beginSubscriptionStatistics(ctx)
	if e != nil {
		return e
	}
	defer end()
	a.subscriptionStats.mu.Lock()
	defer a.subscriptionStats.mu.Unlock()
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var r subscriptionStatsReceipt
	if e = a.readQualitySealed(ctx, tx, "subscription_stats", key, &r); e != nil {
		return e
	}
	if r.ID != key || r.JobID != j.ID {
		return subscriptionStatsEvidenceError()
	}
	if e = a.validateSubscriptionStatsSource(ctx, tx, r, j); e != nil {
		return e
	}
	switch r.Stage {
	case "complete", "dependency", "unknown":
		return subscriptionStatsChildError(r)
	case "intent":
		r.Stage = "unknown"
		r.Result = subscriptionstats.Result{Outcome: "unconfirmed"}
		r.NativeError = "SUBSCRIPTION_STATS_UNCONFIRMED"
		if e = a.commitSubscriptionStatsTx(ctx, tx, r, "recover_unknown"); e != nil {
			return e
		}
		return subscriptionStatsChildError(r)
	case "accepted":
	default:
		return subscriptionStatsEvidenceError()
	}
	r.Stage = "intent"
	if e = a.commitSubscriptionStatsTx(ctx, tx, r, "intent"); e != nil {
		return e
	}
	result, cause := a.executeSubscriptionStats(ctx, r.Input)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	tx, e = a.Store.DB.BeginTx(finish, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var current subscriptionStatsReceipt
	if e = a.readQualitySealed(finish, tx, "subscription_stats", key, &current); e != nil {
		return e
	}
	if current.Stage != "intent" || !bytes.Equal(mustCleanSeedJSON(current.Input), mustCleanSeedJSON(r.Input)) {
		return subscriptionStatsEvidenceError()
	}
	r.Result = result
	r.Stage = "complete"
	if cause != nil {
		if errors.Is(cause, subscriptionstats.ErrDependency) {
			r.Stage = "dependency"
			r.NativeError = "SUBSCRIPTION_STATS_DEPENDENCY"
		} else {
			r.Stage = "unknown"
			r.NativeError = "SUBSCRIPTION_STATS_UNCONFIRMED"
		}
	}
	level, event, message := "INFO", "source_returned", "订阅统计调用已返回"
	if r.Stage == "dependency" {
		level, event, message = "WARN", "native_dependency", "订阅统计来源配置或能力尚未就绪，原订阅结果已保留"
	} else if r.Stage == "unknown" || r.Result.Outcome == "unconfirmed" {
		level, event, message = "WARN", "unconfirmed", "订阅统计响应未确认，未重复发送"
	} else if r.Result.SourceError != "" {
		level, event, message = "ERROR", "source_exception", "原订阅统计调用异常："+r.Result.SourceError
	} else if r.Result.Outcome == "disabled" {
		event, message = "sharing_disabled", "原订阅统计分享已关闭"
	} else if !r.Result.SourceValue {
		level, event, message = "WARN", "source_false", "订阅统计服务未返回成功状态"
	}
	if _, e = tx.ExecContext(finish, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), j.WorkflowID, j.ID, level, "subscription_stats", event, message); e != nil {
		return e
	}
	if e = a.commitSubscriptionStatsTx(finish, tx, r, "finish"); e != nil {
		return e
	}
	return subscriptionStatsChildError(r)
}
func (a *App) executeSubscriptionStats(ctx context.Context, in subscriptionStatsInput) (subscriptionstats.Result, error) {
	in.Settings.Environment = append([]string{}, in.Environment...)
	// Source checks SHARE before evaluating JSON kwargs and other settings.
	if in.Settings.Share == nil || !*in.Settings.Share {
		return (subscriptionstats.Client{}).SubDone(ctx, in.Settings, nil)
	}
	if in.Mode == "await_async" {
		return a.executeUISubscriptionStatistics(ctx, in)
	}
	var body []byte
	var e error
	if in.Operation == "done" {
		body, e = subscriptionstats.BuildCompletion(in.Subscribe, in.MediaInfo)
	} else if in.Lark != nil {
		body, e = sourceLarkRegistration(in.Lark.Notice)
	} else {
		e = subscriptionstats.ErrDependency
	}
	if e != nil {
		if caught, ok := subscriptionstats.CaughtJSONResult(e); ok {
			return caught, nil
		}
		return subscriptionstats.Result{}, e
	}
	client := subscriptionstats.Client{Base: a.subscriptionStats.base, Guard: func(ctx context.Context, target *url.URL) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		prepared, e := urlprep.Prepare(strings.TrimRight(in.Settings.ServerHost, "/") + "/subscribe/" + in.Operation)
		if e != nil {
			return subscriptionstats.ErrDependency
		}
		want, e := url.Parse(prepared)
		if e != nil || target.Scheme != want.Scheme || target.Host != want.Host {
			return subscriptionStatsEvidenceError()
		}
		return nil
	}}
	if in.Operation == "add" {
		return client.SubReg(ctx, in.Settings, body)
	}
	return client.SubDone(ctx, in.Settings, body)
}
