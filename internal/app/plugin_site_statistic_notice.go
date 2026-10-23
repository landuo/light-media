package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/notice"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/statistic"
)

const siteStatisticNoticeSchema = `CREATE TABLE IF NOT EXISTS plugin_site_statistic_notices(token TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_site_statistic_pending(instance_id TEXT PRIMARY KEY,sealed BLOB NOT NULL);`

// This fact is created only by the host from a validated event invocation. SDK
// JSON supplies only a batch identifier, never a notice, route or source proof.
type siteStatisticNotice struct {
	Version int
	// Present (even when empty) only in v2; omission preserves v1 facts.
	AppDomain                                             *string `json:",omitempty"`
	Instance, RunID, RunVersion, InputSHA, Token, BatchID string
	Generation, Revision                                  int64
	Config                                                json.RawMessage
	Event                                                 DomainEvent
	DataRevision                                          int64
	LastNotify                                            siteStatisticLastNotify
	Payload                                               statistic.Notification
	Timezone                                              string
	TimezoneOffset                                        int
	Envelope                                              json.RawMessage
	CreatedAt, CurrentTime, HistoryID, HistoryDigest      string
}
type siteStatisticNoticeReceipt struct {
	Notified   bool   `json:"notified"`
	RouteID    string `json:"route_id,omitempty"`
	InitialSHA string `json:"initial_sha,omitempty"`
}

// Called before the common host lock. Acceptance is serialized with lifecycle
// edits, but routing can call Lark/network and must never hold pluginMu.
func (a *App) pluginSiteStatisticNotifyHost(ctx context.Context, installation PluginInstallation, run *PluginRun, raw json.RawMessage) (json.RawMessage, error) {
	release, err := a.pluginStore.noticeLock(ctx, "site-statistic-instance/"+installation.InstanceID)
	if err != nil {
		return nil, err
	}
	defer release()
	a.pluginMu.Lock()
	result, err := func() (json.RawMessage, error) {
		if err := a.validatePluginHostRun(ctx, installation, run, "site.statistics.notify"); err != nil {
			return nil, err
		}
		return a.pluginSiteStatisticHost(ctx, installation, run, "site.statistics.notify", raw)
	}()
	a.pluginMu.Unlock()
	if err != nil {
		return nil, err
	}
	var receipt siteStatisticNoticeReceipt
	if json.Unmarshal(result, &receipt) != nil {
		return nil, errors.New("invalid statistic receipt")
	}
	if receipt.RouteID != "" {
		if err = a.runNoticeRoute(ctx, receipt.RouteID); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func siteStatisticEnvelope(n statistic.Notification) (*notice.Envelope, error) {
	return notice.NewEnvelope(notice.Object{"mtype": "站点", "title": n.Title, "text": n.Text})
}

// v1 was published without the base-plugin default link. Its accepted bytes
// remain authoritative; neither replay nor recovery upgrades old facts.
func siteStatisticNoticeEnvelope(box siteStatisticNotice) (*notice.Envelope, error) {
	switch box.Version {
	case 1:
		if box.AppDomain != nil {
			return nil, errors.New("legacy statistic notice has frozen domain")
		}
		return siteStatisticEnvelope(box.Payload)
	case 2:
		if box.AppDomain == nil {
			return nil, errors.New("statistic notice missing frozen domain")
		}
		link, err := sourcePluginDefaultLink(*box.AppDomain, "SiteStatistic", "")
		if err != nil {
			return nil, err
		}
		return notice.NewEnvelope(notice.Object{"mtype": "站点", "title": box.Payload.Title, "text": box.Payload.Text, "link": link})
	default:
		return nil, errors.New("unsupported statistic notice version")
	}
}

// Re-read the genuine persisted run and event rather than accepting an
// in-memory run/action supplied by a caller as authority. Called under pluginMu.
func (a *App) siteStatisticNoticeSource(ctx context.Context, run PluginRun, mode string) (siteStatisticNotice, error) {
	var box siteStatisticNotice
	stored, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		return box, err
	}
	if stored.Action != "event" || stored.InstanceID != run.InstanceID || stored.Version != run.Version || stored.Generation != run.Generation || stored.ConfigRevision != run.ConfigRevision || !bytes.Equal(stored.Input, run.Input) || run.Action != stored.Action {
		return box, bad("PLUGIN_CAPABILITY_DENIED", "统计通知必须来自绑定的事件执行")
	}
	installation, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		return box, err
	}
	if installation.PluginID != "SiteStatistic" {
		return box, bad("PLUGIN_CAPABILITY_DENIED", "统计通知来源插件无效")
	}
	if err = a.validatePluginHostRun(ctx, installation, &stored, "site.statistics.notify"); err != nil {
		return box, err
	}
	config, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return box, err
	}
	var value builtin.SiteStatisticConfiguration
	if !pluginStrictJSON(config.Value, &value) || value.Validate() != nil || !value.Enabled || value.NotifyType == "" || value.NotifyType != mode {
		return box, bad("PLUGIN_CAPABILITY_DENIED", "统计通知配置无效")
	}
	var invocation plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(stored.Input, &invocation) != nil || invocation.Action != "event" || !siteStatisticConfigEqual(invocation.Config, config.Value) || !pluginStrictJSON(invocation.Input, &input) || input.BatchID == "" {
		return box, bad("PLUGIN_CAPABILITY_DENIED", "统计通知事件输入无效")
	}
	var handler string
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT handler_key FROM plugin_event_batches WHERE id=? AND run_id=?`, input.BatchID, run.ID).Scan(&handler); err != nil {
		return box, err
	}
	if handler != "notify" {
		return box, bad("PLUGIN_BATCH_SCOPE_DENIED", "统计通知事件处理器无效")
	}
	page, err := a.pluginStore.pluginBatchEvents(ctx, stored, input.BatchID, 0, 200)
	if err != nil {
		return box, err
	}
	if !page.Complete || len(page.Events) != 1 || page.Events[0].Type != "site.refreshed" {
		return box, bad("PLUGIN_BATCH_SCOPE_DENIED", "统计通知事件批次无效")
	}
	box = siteStatisticNotice{Version: 1, Instance: run.InstanceID, RunID: run.ID, RunVersion: run.Version, Generation: run.Generation, Revision: run.ConfigRevision, InputSHA: deferredInputSHA(run.Input), Config: config.Value, BatchID: input.BatchID, Event: page.Events[0]}
	return box, nil
}

func (a *App) acceptSiteStatisticNoticeTx(ctx context.Context, tx *sql.Tx, box siteStatisticNotice, payload statistic.Notification, at time.Time) (siteStatisticNoticeReceipt, error) {
	p := a.pluginStore
	result := siteStatisticNoticeReceipt{Notified: true}
	// Freeze configuration at acceptance, never during historical recovery.
	domain := a.Settings().AppDomain
	box.Version, box.AppDomain = 2, &domain
	box.Payload = payload
	env, err := siteStatisticNoticeEnvelope(box)
	if err != nil {
		return result, err
	}
	box.Payload = payload
	var config builtin.SiteStatisticConfiguration
	if !pluginStrictJSON(box.Config, &config) {
		return result, errors.New("invalid statistic source config")
	}
	box.LastNotify = siteStatisticLastNotify{Date: at.In(shanghai()).Format("2006-01-02"), Type: config.NotifyType, Fingerprint: payload.Fingerprint, Time: at.In(shanghai()).Format("15:04:05")}
	box.Envelope, err = json.Marshal(env.Dump())
	if err != nil {
		return result, err
	}
	box.Token = p.noticeID("site-statistic-source", box.Instance, box.Generation, box.RunID, box.Revision, box.InputSHA)
	box.CreatedAt = at.UTC().Format(time.RFC3339Nano)
	// Routing uses the host process clock, not the statistics day boundary.
	// Persist its offset so a later TZ change cannot reinterpret this fact.
	box.Timezone = time.Local.String()
	_, box.TimezoneOffset = at.Local().Zone()
	box.CurrentTime = at.Local().Format("2006-01-02 15:04:05")
	if env.Get("save_history") == true {
		box.HistoryID = "plugin_notice_" + box.RunID
		link, _ := env.Get("link").(string)
		n := Notification{ID: box.HistoryID, Title: payload.Title, Message: a.clean(payload.Text), MType: "站点", Link: link, CreatedAt: box.CreatedAt}
		box.HistoryDigest = p.noticeID("site-statistic-history-value", n)
		if err = putCleanupNoticeTx(ctx, tx, n); err != nil {
			return result, err
		}
	}
	raw, err := json.Marshal(box)
	if err != nil {
		return result, err
	}
	sealed, err := p.seal("run_result", box.Instance, "site-statistic-notice:"+box.Token, raw)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_site_statistic_notices VALUES(?,?,?,?)`, box.Token, box.Instance, box.Generation, sealed); err != nil {
		return result, err
	}
	route := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", box.Instance, box.Generation, box.Token), Kind: "site_statistic", Instance: box.Instance, Generation: box.Generation, Token: box.Token, EffectID: box.RunID, AcceptedSHA: tokenHash(string(raw)), Envelope: box.Envelope, CreatedAt: box.CreatedAt, CurrentTime: box.CurrentTime, HistoryID: box.HistoryID, HistoryDigest: []byte(box.HistoryDigest)}
	sealed, err = p.noticeSeal("route", route.ID, route)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, route.ID, route.Instance, route.Generation, route.Token, sealed); err != nil {
		return result, err
	}
	marker, err := json.Marshal(siteStatisticPending{Generation: box.Generation, RouteID: route.ID})
	if err != nil {
		return result, err
	}
	sealed, err = p.seal("site_statistic_pending", box.Instance, "current", marker)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_site_statistic_pending VALUES(?,?) ON CONFLICT(instance_id) DO UPDATE SET sealed=excluded.sealed`, box.Instance, sealed); err != nil {
		return result, err
	}
	result.RouteID, result.InitialSHA = route.ID, noticeInitialSHA(route)
	return result, nil
}

// Historical validation deliberately does not consult live config/generation:
// an already accepted source remains its original instance's immutable fact.
func (p *PluginStore) validateSiteStatisticNotice(ctx context.Context, q pluginRemoveLinkJournalReader, route pluginNoticeRoute) error {
	invalid := errors.New("site statistic notice source proof mismatch")
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_site_statistic_notices WHERE token=? AND instance_id=? AND generation=?`, route.Token, route.Instance, route.Generation).Scan(&sealed); err != nil {
		return err
	}
	raw, err := p.unseal("run_result", route.Instance, "site-statistic-notice:"+route.Token, sealed)
	if err != nil {
		return err
	}
	var box siteStatisticNotice
	if cleanupJournalStrict(raw, &box) != nil || (box.Version != 1 && box.Version != 2) || box.DataRevision < 0 || box.Instance != route.Instance || box.Generation != route.Generation || box.Token != route.Token || box.Token != p.noticeID("site-statistic-source", box.Instance, box.Generation, box.RunID, box.Revision, box.InputSHA) || box.Event.Type != "site.refreshed" || box.Event.SiteID != "*" {
		return invalid
	}
	var config builtin.SiteStatisticConfiguration
	if !pluginStrictJSON(box.Config, &config) || config.Validate() != nil || !config.Enabled || config.NotifyType == "" {
		return invalid
	}
	env, err := siteStatisticNoticeEnvelope(box)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(env.Dump())
	if err != nil || !bytes.Equal(envelope, box.Envelope) {
		return invalid
	}
	at, err := time.Parse(time.RFC3339Nano, box.CreatedAt)
	if box.LastNotify.Type != config.NotifyType || box.LastNotify.Fingerprint != box.Payload.Fingerprint || box.LastNotify.Date != at.In(shanghai()).Format("2006-01-02") || box.LastNotify.Time != at.In(shanghai()).Format("15:04:05") {
		return invalid
	}
	if err != nil || box.Timezone == "" || box.TimezoneOffset < -86400 || box.TimezoneOffset > 86400 || box.CurrentTime != at.In(time.FixedZone(box.Timezone, box.TimezoneOffset)).Format("2006-01-02 15:04:05") {
		return invalid
	}
	if route.EffectID != box.RunID || route.AcceptedSHA != tokenHash(string(raw)) || !bytes.Equal(route.Envelope, box.Envelope) || route.CreatedAt != box.CreatedAt || route.CurrentTime != box.CurrentTime || route.HistoryID != box.HistoryID || !bytes.Equal(route.HistoryDigest, []byte(box.HistoryDigest)) {
		return invalid
	}
	run, err := p.scanPluginRun(q.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, box.RunID))
	if err != nil {
		return err
	}
	if run.InstanceID != box.Instance || run.Generation != box.Generation || run.ConfigRevision != box.Revision || run.Version != box.RunVersion || run.Action != "event" || deferredInputSHA(run.Input) != box.InputSHA {
		return invalid
	}
	var invocation plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil || invocation.Action != "event" || !siteStatisticConfigEqual(invocation.Config, box.Config) || !pluginStrictJSON(invocation.Input, &input) || input.BatchID != box.BatchID {
		return invalid
	}
	if err = q.QueryRowContext(ctx, `SELECT result FROM plugin_site_statistic_operations WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=?`, box.RunID, box.Instance, box.Generation, box.Revision).Scan(&sealed); err != nil {
		return err
	}
	receipt, err := p.unseal("site_statistic_result", box.Instance, box.RunID, sealed)
	if err != nil {
		return err
	}
	var proof siteStatisticNoticeReceipt
	if cleanupJournalStrict(receipt, &proof) != nil || !proof.Notified || proof.RouteID != route.ID || proof.InitialSHA != noticeInitialSHA(route) {
		return invalid
	}
	if box.HistoryID != "" {
		var body []byte
		if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, box.HistoryID).Scan(&body); err != nil {
			return err
		}
		var n Notification
		if cleanupJournalStrict(body, &n) != nil {
			return invalid
		}
		n.Read = false
		if p.noticeID("site-statistic-history-value", n) != box.HistoryDigest {
			return invalid
		}
	}
	return nil
}

// Compare decoded source fields, not JSON member order. Both snapshots must
// satisfy the strict plugin configuration contract before they can agree.
func siteStatisticConfigEqual(left, right json.RawMessage) bool {
	var a, b builtin.SiteStatisticConfiguration
	if !pluginStrictJSON(left, &a) || !pluginStrictJSON(right, &b) || a.Validate() != nil || b.Validate() != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// Accepted and completed are separate durable facts. A legacy local-only
// receipt is not upgraded into an outgoing route by recovery.
func (p *PluginStore) siteStatisticReceiptOutcome(ctx context.Context, run PluginRun, result json.RawMessage) (state, code string, routed bool, err error) {
	var proof siteStatisticNoticeReceipt
	if err = json.Unmarshal(result, &proof); err != nil {
		return
	}
	if proof.RouteID == "" {
		return "succeeded", "", false, nil
	}
	routed = true
	route, _, e := p.readNoticeRoute(ctx, p.store.DB, proof.RouteID)
	if e != nil {
		return "reconcile_pending", "PLUGIN_SITE_STATISTIC_NOTICE_INVALID", true, e
	}
	if route.Kind != "site_statistic" || route.Instance != run.InstanceID || route.Generation != run.Generation || route.EffectID != run.ID || !proof.Notified || proof.InitialSHA != noticeInitialSHA(route) {
		return "reconcile_pending", "PLUGIN_SITE_STATISTIC_NOTICE_INVALID", true, errors.New("statistic receipt route mismatch")
	}
	if !route.Complete {
		return "reconcile_pending", "PLUGIN_NOTICE_PENDING", true, nil
	}
	if route.SourceError != "" {
		return "failed", "PLUGIN_HOST_FAILED", true, nil
	}
	return "succeeded", "", true, nil
}

// The exact 1.9.7 source calls post_message before save_data(last_notify).
// Acceptance must therefore reserve an in-flight fingerprint separately;
// confirmed Router source errors do not advance the successful-notice KV.
type siteStatisticLastNotify struct {
	Date        string `json:"date"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Time        string `json:"time"`
}
type siteStatisticPending struct {
	Generation int64
	RouteID    string
}

func (p *PluginStore) siteStatisticPending(ctx context.Context, q pluginRemoveLinkJournalReader, instance string) (siteStatisticPending, error) {
	var value siteStatisticPending
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_site_statistic_pending WHERE instance_id=?`, instance).Scan(&sealed); err != nil {
		return value, err
	}
	raw, err := p.unseal("site_statistic_pending", instance, "current", sealed)
	if err != nil {
		return value, err
	}
	if cleanupJournalStrict(raw, &value) != nil || value.Generation < 1 || value.RouteID == "" {
		return value, errors.New("statistic pending marker invalid")
	}
	return value, nil
}
func (p *PluginStore) siteStatisticSourceBox(ctx context.Context, q pluginRemoveLinkJournalReader, route pluginNoticeRoute) (siteStatisticNotice, error) {
	var box siteStatisticNotice
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_site_statistic_notices WHERE token=? AND instance_id=? AND generation=?`, route.Token, route.Instance, route.Generation).Scan(&sealed); err != nil {
		return box, err
	}
	raw, err := p.unseal("run_result", route.Instance, "site-statistic-notice:"+route.Token, sealed)
	if err != nil {
		return box, err
	}
	if cleanupJournalStrict(raw, &box) != nil {
		return box, errors.New("statistic source invalid")
	}
	return box, nil
}
func (p *PluginStore) siteStatisticPendingDuplicateTx(ctx context.Context, tx *sql.Tx, run PluginRun, payload statistic.Notification, mode string, at time.Time) (bool, error) {
	pending, err := p.siteStatisticPending(ctx, tx, run.InstanceID)
	if errors.Is(err, sql.ErrNoRows) {
		var accepted int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_site_statistic_notices WHERE instance_id=? AND generation=?`, run.InstanceID, run.Generation).Scan(&accepted); e != nil {
			return false, e
		}
		if accepted != 0 {
			return false, errors.New("statistic pending marker missing")
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pending.Generation != run.Generation {
		return false, nil
	}
	route, _, err := p.readNoticeRoute(ctx, tx, pending.RouteID)
	if err != nil {
		return false, err
	}
	if route.Kind != "site_statistic" || route.Instance != run.InstanceID || route.Generation != pending.Generation {
		return false, errors.New("statistic pending route mismatch")
	}
	box, err := p.siteStatisticSourceBox(ctx, tx, route)
	if err != nil {
		return false, err
	}
	same := box.LastNotify.Date == at.In(shanghai()).Format("2006-01-02") && box.LastNotify.Type == mode && box.LastNotify.Fingerprint == payload.Fingerprint
	return same && (!route.Complete || route.SourceError == ""), nil
}

// Invoked inside Router's completion transaction. Both route completion and
// last_notify become durable together; failed/lost replies cannot replay sends.
// A later config/reset or a newer acceptance cannot inherit this old KV write.
func (p *PluginStore) finishSiteStatisticNoticeTx(ctx context.Context, tx *sql.Tx, route pluginNoticeRoute) error {
	if route.SourceError != "" {
		return nil
	}
	pending, err := p.siteStatisticPending(ctx, tx, route.Instance)
	if err != nil {
		return err
	}
	if pending.Generation != route.Generation || pending.RouteID != route.ID {
		return nil
	}
	box, err := p.siteStatisticSourceBox(ctx, tx, route)
	if err != nil {
		return err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.generation=? AND i.installed=1 AND i.desired_enabled=1 AND v.revision=?`, route.Instance, route.Generation, box.Revision).Scan(&active); err != nil {
		return err
	}
	if active != 1 {
		return nil
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='last_notify'`, route.Instance).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if revision != box.DataRevision {
		return nil
	}
	value, err := json.Marshal(box.LastNotify)
	if err != nil {
		return err
	}
	_, err = p.saveValueTx(ctx, tx, route.Instance, "data", "last_notify", revision, value)
	return err
}
