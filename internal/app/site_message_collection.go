package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// These are core collection identities, never plugin notification identities.
// The API instance identifies the invoking API grant, not a notification owner.
type siteCollectionOrigin struct {
	Kind, ID, Instance, Version, Domain string
	Generation, Revision                int64
}
type siteCollectionLeaseKey struct{}
type siteCollectionLease struct {
	ID         string
	Generation int64
}

type siteMessageIdentity struct {
	Collection string
	Ordinal    int
}
type siteMessageStep struct {
	Kind     string
	Row      string
	Business json.RawMessage `json:",omitempty"`
	State    string
	Result   json.RawMessage `json:",omitempty"`
	Failure  string
}
type siteMessageCollection struct {
	Version                         int
	ID, Stage, Failure              string
	Origin                          siteCollectionOrigin
	Snapshot                        siteSnapshot
	RowsJSON                        string
	Name, URL, PolicyVersion        string
	ReadMessages                    bool
	SiteEnabled                     bool
	EventKey, InputSHA, SnapshotSHA string
	Steps                           []siteMessageStep
}

// Only errors produced by the local host authority guard carry this marker.
// A provider/transport/SQL error with the same text or code is not evidence of
// a confirmed pre-fact rejection and remains an unresolved admission.
type siteMessageGuardDenied struct{ Cause error }

func (e *siteMessageGuardDenied) Error() string { return e.Cause.Error() }
func (e *siteMessageGuardDenied) Unwrap() error { return e.Cause }
func siteMessageDefiniteGuardError(err error) error {
	var api *APIError
	if errors.As(err, &api) && (api.Code == "PLUGIN_API_AUTH_REVOKED" || api.Code == "SITE_CHANGED") {
		return &siteMessageGuardDenied{Cause: err}
	}
	return err
}
func siteMessageAdmissionFailureCode(err error) string {
	var denied *siteMessageGuardDenied
	var api *APIError
	if errors.As(err, &denied) && errors.As(denied.Cause, &api) && (api.Code == "PLUGIN_API_AUTH_REVOKED" || api.Code == "SITE_CHANGED") {
		return api.Code
	}
	return ""
}

type siteMessagePostError struct{ Cause error }

func (e *siteMessagePostError) Error() string { return "site_message_post_failed" }
func (e *siteMessagePostError) Unwrap() error { return e.Cause }
func siteCollectionError() error {
	return bad("SITE_MESSAGE_COLLECTION_UNCONFIRMED", "站点采集收据无法确认，未重复采集或读取消息")
}

var siteRatioExempt = regexp.MustCompile(`(?i)(贵宾|V[Iİı]P?)`)

// Admission freezes only raw ordinal inputs. It does not unpack later rows,
// stringify their contents, or construct Notification envelopes before earlier
// posts return. Construction failures belong to their original ordinal.
func siteMessageBusinesses(name, url string, snapshot siteSnapshot) []siteMessageStep {
	out := []siteMessageStep{}
	d := snapshot.SourceData
	if d == nil {
		return out
	}
	if d.MessageUnread != 0 {
		rows := d.SourceMessageRows()
		if len(rows) == 0 {
			out = append(out, siteMessageStep{Kind: "summary", State: "pending"})
		} else {
			for _, row := range rows {
				raw, _ := json.Marshal(row)
				out = append(out, siteMessageStep{Kind: "detail", State: "pending", Row: string(raw)})
			}
		}
	}
	level := ""
	if d.UserLevel != nil {
		level = *d.UserLevel
	}
	if d.Ratio != 0 && d.Ratio < 1 && !siteRatioExempt.MatchString(level) {
		out = append(out, siteMessageStep{Kind: "ratio", State: "pending"})
	}
	return out
}

// Python tuple unpack accepts any iterable JSON row. Object keys retain input
// order; scalar/null rows fail only when reached. Strings iterate Unicode code
// points, not UTF-8 bytes. The optional fourth value is the only source metadata.
func siteMessageUnpack(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, siteCollectionError()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	first, e := dec.Token()
	if e != nil {
		return nil, e
	}
	var values []json.RawMessage
	switch v := first.(type) {
	case json.Delim:
		if v == '[' {
			for dec.More() {
				var value json.RawMessage
				if e = dec.Decode(&value); e != nil {
					return nil, e
				}
				values = append(values, value)
			}
		} else if v == '{' {
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return nil, err
				}
				encoded, _ := json.Marshal(key)
				if !seen[key.(string)] {
					values = append(values, encoded)
					seen[key.(string)] = true
				}
				var ignored json.RawMessage
				if err = dec.Decode(&ignored); err != nil {
					return nil, err
				}
			}
		} else {
			return nil, notice.SourceError{Class: "TypeError"}
		}
		if _, e = dec.Token(); e != nil {
			return nil, e
		}
	case string:
		for _, r := range v {
			encoded, _ := json.Marshal(string(r))
			values = append(values, encoded)
		}
	default:
		return nil, notice.SourceError{Class: "TypeError"}
	}
	if _, e = dec.Token(); e != io.EOF {
		return nil, siteCollectionError()
	}
	if len(values) < 3 {
		return nil, notice.SourceError{Class: "ValueError"}
	}
	return values, nil
}
func siteMessageBusinessAt(name, url string, snapshot siteSnapshot, step siteMessageStep) (json.RawMessage, error) {
	d := snapshot.SourceData
	if d == nil {
		return nil, siteCollectionError()
	}
	fields := notice.Object{"mtype": "站点"}
	switch step.Kind {
	case "summary":
		fields["title"] = fmt.Sprintf("站点 %s 收到 %d 条新消息，请登陆查看", name, d.MessageUnread)
		fields["link"] = url
	case "detail":
		row, e := siteMessageUnpack(json.RawMessage(step.Row))
		if e != nil {
			return nil, e
		}
		var source any
		if len(row) > 3 {
			dec := json.NewDecoder(bytes.NewReader(row[3]))
			dec.UseNumber()
			if e = dec.Decode(&source); e != nil {
				return nil, e
			}
		}
		fields["source"] = source
		fields["title"] = "【站点 " + name + " 消息】"
		labels := [3]string{}
		for i := range labels {
			labels[i], e = siteMessagePythonString(row[i])
			if e != nil {
				return nil, e
			}
		}
		fields["text"] = "时间：" + labels[1] + "\n标题：" + labels[0] + "\n内容：\n" + labels[2]
		fields["link"] = url
	case "ratio":
		fields["title"] = "【站点分享率低预警】"
		fields["text"] = "站点 " + name + " 分享率 " + exec.AsValue(d.Ratio).String() + "，请注意！"
	default:
		return nil, siteCollectionError()
	}
	return mustCleanSeedJSON(sourceBusinessNotice{AbsentContext: true, Envelope: fields}), nil
}

// RowsJSON is opaque inside the canonical sealed record. Canonical JSON maps
// sort keys; Python dictionary iteration and nested repr must retain DTO order.
func freezeSiteCollectionRows(r *siteMessageCollection) error {
	if r.Snapshot.SourceData == nil {
		r.RowsJSON = ""
		return nil
	}
	raw, e := json.Marshal(r.Snapshot.SourceData.SourceMessageRows())
	if e != nil {
		return e
	}
	r.RowsJSON = string(raw)
	return nil
}
func restoreSiteCollectionRows(r *siteMessageCollection) error {
	if r.Snapshot.SourceData == nil {
		if r.RowsJSON != "" {
			return siteCollectionError()
		}
		return nil
	}
	if r.RowsJSON == "" {
		return siteCollectionError()
	}
	raw, e := json.Marshal(r.Snapshot.SourceData)
	if e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return e
	}
	fields["message_unread_contents"] = json.RawMessage(r.RowsJSON)
	raw, e = json.Marshal(fields)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, r.Snapshot.SourceData)
}
func siteCollectionSnapshotSHA(r siteMessageCollection) string {
	return tokenHash(string(mustCleanSeedJSON(struct {
		Snapshot siteSnapshot
		RowsJSON string
	}{r.Snapshot, r.RowsJSON})))
}
func siteMessageHasJSON(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func siteCollectionInputSHA(r siteMessageCollection) string {
	return tokenHash(string(mustCleanSeedJSON(struct {
		Origin                                                siteCollectionOrigin
		SiteID, ConfigKey, Name, URL, PolicyVersion, EventKey string
		ReadMessages, SiteEnabled                             bool
	}{r.Origin, r.Snapshot.SiteID, r.Snapshot.ConfigKey, r.Name, r.URL, r.PolicyVersion, r.EventKey, r.ReadMessages, r.SiteEnabled})))
}
func (p *PluginStore) readSiteMessageCollection(ctx context.Context, q subscriptionCompletionReader, key string) (siteMessageCollection, error) {
	var r siteMessageCollection
	var sealed []byte
	if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='site_message_collection' AND id=?`, key).Scan(&sealed); e != nil {
		return r, e
	}
	raw, e := p.unseal("site_message_collection", "system", key, sealed)
	if e != nil || cleanupJournalStrict(raw, &r) != nil || r.Version != 1 || r.ID != key || r.Origin.ID == "" || r.Origin.Generation < 1 || (r.Origin.Kind != "job" && r.Origin.Kind != "api") || r.ID != p.noticeID("site-collection", r.Origin.Kind, r.Origin.ID, r.Snapshot.SiteID) || r.InputSHA != siteCollectionInputSHA(r) {
		return r, siteCollectionError()
	}
	for i := range r.Steps {
		if !siteMessageHasJSON(r.Steps[i].Business) {
			r.Steps[i].Business = nil
		}
		if !siteMessageHasJSON(r.Steps[i].Result) {
			r.Steps[i].Result = nil
		}
	}
	switch r.Stage {
	case "admitted", "aborted":
		if r.Stage == "aborted" && r.Failure != "SITE_CHANGED" && r.Failure != "PLUGIN_API_AUTH_REVOKED" {
			return r, siteCollectionError()
		}
		if r.SnapshotSHA != "" || r.RowsJSON != "" || len(r.Steps) != 0 || r.Snapshot.SourceData != nil || (r.Stage == "admitted" && r.Failure != "") {
			return r, siteCollectionError()
		}
	case "collected", "complete", "failed":
		if restoreSiteCollectionRows(&r) != nil || r.SnapshotSHA != siteCollectionSnapshotSHA(r) {
			return r, siteCollectionError()
		}
		expected := siteMessageBusinesses(r.Name, r.URL, r.Snapshot)
		if len(expected) != len(r.Steps) {
			return r, siteCollectionError()
		}
		for i, s := range r.Steps {
			if s.Kind != expected[i].Kind || s.Row != expected[i].Row {
				return r, siteCollectionError()
			}
			if siteMessageHasJSON(s.Business) {
				business, buildErr := siteMessageBusinessAt(r.Name, r.URL, r.Snapshot, expected[i])
				if buildErr != nil || !bytes.Equal(s.Business, business) {
					return r, siteCollectionError()
				}
			}
			if (s.State == "routing" || s.State == "complete" || s.State == "skipped") && !siteMessageHasJSON(s.Business) {
				return r, siteCollectionError()
			}
			switch s.State {
			case "pending", "routing", "complete", "skipped", "failed":
			default:
				return r, siteCollectionError()
			}
		}
	default:
		return r, siteCollectionError()
	}
	if r.Stage != "admitted" && r.Snapshot.SourceData != nil {
		var raw []byte
		if e := q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("site.refreshed", r.EventKey)).Scan(&raw); e != nil {
			return r, siteCollectionError()
		}
		var event DomainEvent
		if json.Unmarshal(raw, &event) != nil || event.EventID != domainEventID("site.refreshed", r.EventKey) || event.Type != "site.refreshed" || event.SiteID != r.Snapshot.SiteID {
			return r, siteCollectionError()
		}
	}
	return r, nil
}
func (p *PluginStore) saveSiteMessageCollection(ctx context.Context, tx *sql.Tx, r siteMessageCollection) error {
	sealed, e := p.seal("site_message_collection", "system", r.ID, mustCleanSeedJSON(r))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('site_message_collection',?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, r.ID, sealed, now())
	return e
}
func (p *PluginStore) siteMessageBusiness(ctx context.Context, q subscriptionCompletionReader, i siteMessageIdentity) ([]byte, error) {
	r, e := p.readSiteMessageCollection(ctx, q, i.Collection)
	if e != nil {
		return nil, e
	}
	if r.Stage == "admitted" || r.Stage == "aborted" || i.Ordinal < 0 || i.Ordinal >= len(r.Steps) || !siteMessageHasJSON(r.Steps[i.Ordinal].Business) {
		return nil, siteCollectionError()
	}
	return r.Steps[i.Ordinal].Business, nil
}
func (p *PluginStore) siteCollectionOwner(ctx context.Context, q subscriptionCompletionReader, r siteMessageCollection) error {
	var count int
	if r.Origin.Kind == "job" {
		generation := r.Origin.Generation
		if lease, ok := ctx.Value(siteCollectionLeaseKey{}).(siteCollectionLease); ok && lease.ID == r.Origin.ID {
			generation = lease.Generation
		}
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=? AND kind='site_refresh' AND generation=? AND state='running'`, r.Origin.ID, generation).Scan(&count); e != nil {
			return e
		}
	} else {
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "旧插件站点消息入口尚未适配标准模块")
	}
	if count != 1 {
		return siteCollectionError()
	}
	return nil
}
func (p *PluginStore) requireSiteMessageExecutionTx(ctx context.Context, q subscriptionCompletionReader, i siteMessageIdentity) error {
	r, e := p.readSiteMessageCollection(ctx, q, i.Collection)
	if e != nil {
		return e
	}
	if r.Stage != "collected" || i.Ordinal < 0 || i.Ordinal >= len(r.Steps) || r.Steps[i.Ordinal].State != "routing" {
		return siteCollectionError()
	}
	for _, s := range r.Steps[:i.Ordinal] {
		if s.State != "complete" && s.State != "skipped" {
			return siteCollectionError()
		}
	}
	return p.siteCollectionOwner(ctx, q, r)
}

// Record only a proven local pre-fact guard rejection. Unknown transport or
// commit failures remain admitted and unretryable.
func (p *PluginStore) abortSiteMessageAdmission(ctx context.Context, id, code string) error {
	if code != "SITE_CHANGED" && code != "PLUGIN_API_AUTH_REVOKED" {
		return siteCollectionError()
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := p.readSiteMessageCollection(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.Stage == "aborted" {
		if r.Failure != code {
			return siteCollectionError()
		}
		return nil
	}
	if r.Stage != "admitted" {
		return siteCollectionError()
	}
	r.Stage, r.Failure = "aborted", code
	if err = p.saveSiteMessageCollection(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

// Admission precedes the first network read. An interrupted admitted receipt
// intentionally cannot collect again: marking a remote message read is unknown.
func (a *App) collectManagedSite(ctx context.Context, key, eventKey string, hooks *siteCollectionHooks) (siteSnapshot, error) {
	if hooks == nil {
		return a.collectManagedSiteObserved(ctx, key, eventKey, nil)
	}
	p := a.pluginStore
	if hooks.Origin.Kind == "job" {
		ctx = context.WithValue(ctx, siteCollectionLeaseKey{}, siteCollectionLease{hooks.Origin.ID, hooks.Origin.Generation})
	}
	collectionID := p.noticeID("site-collection", hooks.Origin.Kind, hooks.Origin.ID, key)
	release, ok := p.tryNoticeLock("site-collection/" + collectionID)
	if !ok {
		return siteSnapshot{}, &siteMessagePostError{seedhost.ErrUnknown}
	}
	defer release()
	r, e := p.readSiteMessageCollection(ctx, a.Store.DB, collectionID)
	if errors.Is(e, sql.ErrNoRows) {
		site, err := a.siteForRequest(ctx, key)
		if err != nil {
			return siteSnapshot{}, err
		}
		policy, err := a.sourceSiteMessagePolicy(ctx, a.Settings())
		if err != nil {
			return siteSnapshot{}, err
		}
		r = siteMessageCollection{Version: 1, ID: collectionID, Stage: "admitted", Origin: hooks.Origin, Snapshot: siteSnapshot{SiteID: key, ConfigKey: a.managedSiteKey(site)}, Name: site.Name, URL: site.BaseURL, PolicyVersion: "site-chain/1528176/v1:" + policy.sourceProof, ReadMessages: policy.enabled, SiteEnabled: site.Enabled, EventKey: eventKey}
		r.InputSHA = siteCollectionInputSHA(r)
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return siteSnapshot{}, err
		}
		defer tx.Rollback()
		if hooks.Guard != nil {
			if err = hooks.Guard(tx); err != nil {
				return siteSnapshot{}, err
			}
		}
		if err = p.siteCollectionOwner(ctx, tx, r); err != nil {
			return siteSnapshot{}, err
		}
		if err = p.saveSiteMessageCollection(ctx, tx, r); err != nil {
			return siteSnapshot{}, err
		}
		if r.Origin.Kind == "api" {
			if err = txPut(ctx, tx, "site_message_collection_run", r.Origin.ID, map[string]string{"collection": r.ID}); err != nil {
				return siteSnapshot{}, err
			}
		}
		if err = tx.Commit(); err != nil {
			return siteSnapshot{}, &siteMessagePostError{seedhost.ErrUnknown}
		}
		observedHooks := *hooks
		observedHooks.ExpectedMessagePolicy = &policy
		if hooks.RequestGuard != nil {
			observedHooks.RequestGuard = func(guardCtx context.Context) error {
				return siteMessageDefiniteGuardError(hooks.RequestGuard(guardCtx))
			}
		}
		if hooks.Guard != nil {
			observedHooks.Guard = func(tx *sql.Tx) error { return siteMessageDefiniteGuardError(hooks.Guard(tx)) }
		}
		observedHooks.Commit = func(tx *sql.Tx, s siteSnapshot) error {
			// Reject changes between durable admission and the observed DTO commit.
			if s.ConfigKey != r.Snapshot.ConfigKey || s.Name != r.Name {
				return siteCollectionError()
			}
			r.Snapshot = s
			if e := freezeSiteCollectionRows(&r); e != nil {
				return e
			}
			r.SnapshotSHA = siteCollectionSnapshotSHA(r)
			r.Steps = siteMessageBusinesses(r.Name, r.URL, s)
			r.Stage = "collected"
			return p.saveSiteMessageCollection(ctx, tx, r)
		}
		_, err = a.collectManagedSiteObserved(ctx, key, eventKey, &observedHooks)
		if err != nil {
			// A marked host guard rejection is a synchronous local outcome,
			// not an interrupted network/mark-read or ambiguous DB commit.
			// Preserve the original failure without authorizing another read.
			if code := siteMessageAdmissionFailureCode(err); code != "" {
				if abortErr := p.abortSiteMessageAdmission(ctx, r.ID, code); abortErr != nil {
					return siteSnapshot{}, &siteMessagePostError{abortErr}
				}
			}
			return siteSnapshot{}, &siteMessagePostError{err}
		}
	} else if e != nil {
		return siteSnapshot{}, &siteMessagePostError{e}
	}
	sameOrigin := r.Origin == hooks.Origin
	if r.Origin.Kind == "job" && hooks.Origin.Kind == "job" && r.Origin.ID == hooks.Origin.ID {
		sameOrigin = true
	}
	if !sameOrigin {
		return siteSnapshot{}, &siteMessagePostError{siteCollectionError()}
	}
	if e = a.guardSiteCollection(ctx, r); e != nil {
		return r.Snapshot, &siteMessagePostError{e}
	}
	r, e = a.finishSiteMessageCollection(ctx, r.ID)
	if e != nil {
		return r.Snapshot, &siteMessagePostError{e}
	}
	// Source API normal return is separate from the committed account/event.
	// No routing occurs under settingsMu/pluginMu or a database transaction.
	if hooks.Commit != nil {
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return r.Snapshot, err
		}
		defer tx.Rollback()
		if hooks.Guard != nil {
			if err = hooks.Guard(tx); err != nil {
				return r.Snapshot, err
			}
		}
		if err = a.requireSiteCollectionCompleteTx(ctx, tx, r); err != nil {
			return r.Snapshot, err
		}
		if err = hooks.Commit(tx, r.Snapshot); err != nil {
			return r.Snapshot, err
		}
		if err = tx.Commit(); err != nil {
			return r.Snapshot, err
		}
	}
	return r.Snapshot, nil
}

func (a *App) requireSiteCollectionCompleteTx(ctx context.Context, tx *sql.Tx, r siteMessageCollection) error {
	stored, e := a.pluginStore.readSiteMessageCollection(ctx, tx, r.ID)
	if e != nil {
		return e
	}
	if stored.Stage != "complete" || stored.SnapshotSHA != r.SnapshotSHA {
		return siteCollectionError()
	}
	for i, s := range stored.Steps {
		switch s.State {
		case "skipped":
		case "complete":
			if e = a.requireSiteMessageNoticeTx(ctx, tx, siteMessageIdentity{stored.ID, i}, s.Business, s.Result); e != nil {
				return e
			}
		default:
			return siteCollectionError()
		}
	}
	return nil
}
func (a *App) siteMessageSourceExistsTx(ctx context.Context, tx *sql.Tx, source any) (bool, error) {
	// The original model's source column has TEXT affinity. This query examines
	// real, deletable notification history, never a permanent source reservation.
	arg, err := siteMessageSourceSQLValue(source)
	if err != nil {
		return false, err
	}
	var count int
	e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='notification' AND CAST(json_extract(payload,'$.source') AS TEXT)=CAST(? AS TEXT)`, arg).Scan(&count)
	return count > 0, e
}
func siteMessageKnownFailure(e error) string {
	var validation *notice.ValidationError
	if errors.As(e, &validation) {
		return "ValidationError"
	}
	var source notice.SourceError
	if errors.As(e, &source) {
		return source.Class
	}
	// Router's completed exception is returned through the cleanup source type.
	if errors.Is(e, seedhost.ErrUnknown) || errors.Is(e, seedhost.ErrUnsafe) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return ""
	}
	return ""
}
func (a *App) guardSiteCollection(ctx context.Context, r siteMessageCollection) error {
	current, e := a.siteForRequest(allowDisabledSiteRequests(ctx), r.Snapshot.SiteID)
	if e != nil || a.managedSiteKey(current) != r.Snapshot.ConfigKey || (current.Enabled != r.SiteEnabled || (r.Origin.Kind == "job" && !current.Enabled)) {
		return siteCollectionError()
	}
	if e := a.pluginStore.siteCollectionOwner(ctx, a.Store.DB, r); e != nil {
		return e
	}
	if r.Origin.Kind == "api" {
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "旧插件站点消息入口尚未适配标准模块")
	}
	return nil
}
func (a *App) finishSiteMessageCollection(ctx context.Context, key string) (siteMessageCollection, error) {
	p := a.pluginStore
	r, e := p.readSiteMessageCollection(ctx, a.Store.DB, key)
	if e != nil {
		return r, e
	}
	if r.Stage == "admitted" {
		return r, seedhost.ErrUnknown
	}
	if r.Stage == "aborted" {
		return r, bad(r.Failure, "站点采集授权已变化，采集已终止")
	}
	if r.Stage == "failed" {
		return r, notice.SourceError{Class: r.Failure}
	}
	if r.Stage == "complete" {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return r, err
		}
		defer tx.Rollback()
		if err = a.requireSiteCollectionCompleteTx(ctx, tx, r); err != nil {
			return r, err
		}
		return r, tx.Commit()
	}
	if r.Origin.Kind == "job" {
		if _, ok := ctx.Value(siteCollectionLeaseKey{}).(siteCollectionLease); !ok {
			var generation int64
			if e = a.Store.DB.QueryRowContext(ctx, `SELECT generation FROM jobs WHERE id=? AND kind='site_refresh' AND state='running'`, r.Origin.ID).Scan(&generation); e != nil {
				return r, e
			}
			ctx = context.WithValue(ctx, siteCollectionLeaseKey{}, siteCollectionLease{r.Origin.ID, generation})
		}
	}
	if e = a.guardSiteCollection(ctx, r); e != nil {
		return r, e
	}
	for ordinal := range r.Steps {
		step := r.Steps[ordinal]
		if step.State == "complete" || step.State == "skipped" {
			continue
		}
		if step.State == "failed" {
			return r, notice.SourceError{Class: step.Failure}
		}
		if e = a.guardSiteCollection(ctx, r); e != nil {
			return r, e
		}
		identity := siteMessageIdentity{r.ID, ordinal}
		if step.State == "pending" {
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return r, err
			}
			if err = p.siteCollectionOwner(ctx, tx, r); err != nil {
				tx.Rollback()
				return r, err
			}
			business, buildErr := siteMessageBusinessAt(r.Name, r.URL, r.Snapshot, step)
			if buildErr != nil {
				step.Failure = sourceNoticeError(buildErr)
			} else {
				step.Business = business
			}
			if step.Failure != "" {
				step.State = "failed"
				r.Stage = "failed"
				r.Failure = step.Failure
			} else {
				var payload sourceBusinessNotice
				if cleanupJournalStrict(step.Business, &payload) != nil {
					tx.Rollback()
					return r, siteCollectionError()
				}
				exists := false
				if sourceSubscriptionTruth(payload.Envelope["source"]) {
					exists, err = a.siteMessageSourceExistsTx(ctx, tx, payload.Envelope["source"])
					if err != nil {
						step.State = "failed"
						step.Failure = sourceNoticeError(err)
						r.Stage = "failed"
						r.Failure = step.Failure
					}
				}
				if step.State == "failed" {
				} else if exists {
					step.State = "skipped"
				} else {
					step.State = "routing"
				}
			}
			r.Steps[ordinal] = step
			if err = p.saveSiteMessageCollection(ctx, tx, r); err != nil {
				tx.Rollback()
				return r, err
			}
			if err = tx.Commit(); err != nil {
				return r, seedhost.ErrUnknown
			}
			if step.State == "failed" {
				return r, notice.SourceError{Class: step.Failure}
			}
			if step.State == "skipped" {
				continue
			}
		}
		result, err := a.postSiteMessageNotice(ctx, identity, step.Business)
		if err != nil {
			failure := siteMessageKnownFailure(err)
			// A completed route is definitive even when its transport-facing error
			// is wrapped. Unknown journal/dispatch effects must never become success.
			var payload sourceBusinessNotice
			if cleanupJournalStrict(step.Business, &payload) == nil {
				token := p.noticeID("site-message-business", identity, tokenHash(string(step.Business)))
				routeID := p.noticeID("route", siteMessageNoticeOwner, int64(1), token)
				if route, _, readErr := p.readNoticeRoute(ctx, a.Store.DB, routeID); readErr == nil && route.Complete && route.SourceError != "" {
					failure = route.SourceError
				}
			}
			if failure != "" {
				tx, writeErr := a.Store.DB.BeginTx(context.WithoutCancel(ctx), nil)
				if writeErr != nil {
					return r, writeErr
				}
				r.Stage = "failed"
				r.Failure = failure
				r.Steps[ordinal].State = "failed"
				r.Steps[ordinal].Failure = failure
				writeErr = p.saveSiteMessageCollection(context.WithoutCancel(ctx), tx, r)
				if writeErr == nil {
					writeErr = tx.Commit()
				} else {
					tx.Rollback()
				}
				if writeErr != nil {
					return r, seedhost.ErrUnknown
				}
			}
			return r, err
		}
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return r, err
		}
		if err = a.requireSiteMessageNoticeTx(ctx, tx, identity, step.Business, result); err != nil {
			tx.Rollback()
			return r, err
		}
		r.Steps[ordinal].State = "complete"
		r.Steps[ordinal].Result = result
		if err = p.saveSiteMessageCollection(ctx, tx, r); err != nil {
			tx.Rollback()
			return r, err
		}
		if err = tx.Commit(); err != nil {
			return r, seedhost.ErrUnknown
		}
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	for i, s := range r.Steps {
		if s.State == "complete" {
			if e = a.requireSiteMessageNoticeTx(ctx, tx, siteMessageIdentity{r.ID, i}, s.Business, s.Result); e != nil {
				return r, e
			}
		} else if s.State != "skipped" {
			return r, siteCollectionError()
		}
	}
	r.Stage = "complete"
	if e = p.saveSiteMessageCollection(ctx, tx, r); e != nil {
		return r, e
	}
	return r, tx.Commit()
}

// Recovery advances only a durable collected snapshot and its existing source
// sequence. It cannot invoke a site client, SDK refresh, or remote mark-read.
func (a *App) resumeSiteMessageCollections(ctx context.Context) error {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT id FROM records WHERE kind='site_message_collection' ORDER BY id`)
	if e != nil {
		return e
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			rows.Close()
			return e
		}
		keys = append(keys, key)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	var first error
	for _, key := range keys {
		release, ok := a.pluginStore.tryNoticeLock("site-collection/" + key)
		if !ok {
			continue
		}
		r, err := a.pluginStore.readSiteMessageCollection(ctx, a.Store.DB, key)
		if err == nil && r.Stage == "collected" {
			r, err = a.finishSiteMessageCollection(ctx, key)
		}
		if err == nil && r.Origin.Kind == "api" && r.Stage == "complete" {
			err = a.finishSiteMessageAPI(ctx, r)
		}
		release()
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
func (a *App) finishSiteMessageAPI(ctx context.Context, r siteMessageCollection) error {
	return bad("PLUGIN_ADAPTER_UNAVAILABLE", "旧插件站点消息入口尚未适配标准模块")
}

// Verify normal-return proofs without entering a new route or requiring an
// active lease. Route loading recursively validates only immutable input facts.
func (p *PluginStore) validateSiteCollectionCompletion(ctx context.Context, q pluginRemoveLinkJournalReader, r siteMessageCollection) error {
	stored, e := p.readSiteMessageCollection(ctx, q, r.ID)
	if e != nil {
		return e
	}
	if stored.Stage != "complete" || stored.SnapshotSHA != r.SnapshotSHA {
		return siteCollectionError()
	}
	for i, s := range stored.Steps {
		if s.State == "skipped" {
			continue
		}
		if s.State != "complete" {
			return siteCollectionError()
		}
		var proof pluginNoticeV2Result
		if cleanupJournalStrict(s.Result, &proof) != nil || proof.Version != 1 || !proof.SourceReturned || proof.SourceError != "" {
			return siteCollectionError()
		}
		route, _, err := p.readNoticeRoute(ctx, q, proof.RouteID)
		if err != nil {
			return err
		}
		identity := siteMessageIdentity{stored.ID, i}
		if !route.Complete || route.SourceError != "" || route.BusinessV2 == nil || route.BusinessV2.Binding.SiteMessage == nil || *route.BusinessV2.Binding.SiteMessage != identity || !bytes.Equal(route.BusinessV2.Input, s.Business) || proof.Count != route.Count || proof.LastSHA != route.LastSHA || proof.EnvelopeSHA != route.BusinessV2.EnvelopeSHA || proof.BusinessSHA != route.BusinessV2.BusinessSHA {
			return siteCollectionError()
		}
	}
	return nil
}
