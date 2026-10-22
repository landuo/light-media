package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// DomainEvent v1 is the P0 persisted outbox contract. It contains references and
// committed facts only, never arbitrary payloads, paths or private metainfo.
// No plugin is started or granted authority by producing or reading an event.
type DomainEvent struct {
	SystemErrorID     string `json:"system_error_id,omitempty"`
	PluginTriggerID   string `json:"plugin_trigger_id,omitempty"`
	Sequence          int64  `json:"sequence,omitempty"`
	SchemaVersion     int    `json:"schema_version"`
	EventID           string `json:"event_id"`
	Type              string `json:"event_type"`
	OccurredAt        string `json:"occurred_at"`
	WorkflowID        string `json:"workflow_id"`
	JobID             string `json:"job_id,omitempty"`
	MediaID           string `json:"media_id"`
	IntentID          string `json:"intent_id,omitempty"`
	NativeDownloadID  string `json:"native_download_id,omitempty"`
	DownloadDeletedID string `json:"download_deleted_id,omitempty"`
	FileEventID       string `json:"file_event_id,omitempty"`
	NoticeID          string `json:"notice_id,omitempty"`
	AssetID           string `json:"asset_id,omitempty"`
	SiteID            string `json:"site_id,omitempty"`
	SourceSiteID      int64  `json:"source_site_id,omitempty"`
	SubscriptionID    string `json:"subscription_id,omitempty"`
	Purpose           string `json:"purpose"`
	SubjectID         string `json:"subject_id"`
	SubjectKind       string `json:"subject_kind"`
	CausationID       string `json:"causation_id,omitempty"`
	ParentJobID       string `json:"parent_job_id,omitempty"`
	Origin            string `json:"origin"`
}
type DomainEventPage struct {
	Events     []DomainEvent `json:"events"`
	NextCursor int64         `json:"next_cursor"`
	Complete   bool          `json:"complete"`
}

const domainEventSchema = `CREATE TABLE IF NOT EXISTS domain_events(seq INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT UNIQUE NOT NULL,payload BLOB NOT NULL)`

var domainEventIDPattern = regexp.MustCompile(`^evt_[a-f0-9]{64}$`)
var domainReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)
var domainMediaPattern = regexp.MustCompile(`^(movie|tv):[1-9][0-9]{0,9}$`)

func domainEventID(eventType, stableID string) string {
	sum := sha256.Sum256([]byte("v1\x00" + eventType + "\x00" + stableID))
	return "evt_" + hex.EncodeToString(sum[:])
}
func validateDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) {
		return in, bad("INVALID_DOMAIN_EVENT", "域事件契约无效，业务结果未提交")
	}
	if in.Type == "system.error" {
		return validateSystemErrorDomainEvent(in)
	}
	if in.SystemErrorID != "" {
		return invalid()
	}
	if in.Type == "subscription.completed" {
		return validateSubscriptionCompletedDomainEvent(in)
	}
	if in.Type == "download.deleted" {
		if in.SchemaVersion == 0 {
			in.SchemaVersion = 1
		}
		return validateDownloadDeletedDomainEvent(in)
	}
	if in.DownloadDeletedID != "" {
		return invalid()
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	if in.Type == "plugin.triggered" {
		return validatePluginTriggeredDomainEvent(in)
	}
	if in.PluginTriggerID != "" {
		return invalid()
	}
	if in.Type == "site.deleted" {
		return validateSiteDeletedDomainEvent(in)
	}
	if in.SourceSiteID != 0 {
		return invalid()
	}
	if in.Type == "notice.message" {
		return validateNoticeMessageDomainEvent(in)
	}
	if in.NoticeID != "" {
		return invalid()
	}
	if in.Type == "downloadfile.deleted" {
		return validateDownloadFileDomainEvent(in)
	}
	if in.FileEventID != "" {
		return invalid()
	}
	if in.SchemaVersion != 1 || in.Sequence != 0 || !domainEventIDPattern.MatchString(in.EventID) || (in.JobID == "" && in.Type != "download.completed" && in.Type != "site.refreshed" && in.Type != "subscription.added") || (in.JobID != "" && !domainReferencePattern.MatchString(in.JobID)) {
		return invalid()
	}
	for _, value := range []string{in.IntentID, in.NativeDownloadID, in.AssetID, in.WorkflowID, in.CausationID, in.ParentJobID, in.SubscriptionID} {
		if value != "" && !domainReferencePattern.MatchString(value) {
			return invalid()
		}
	}
	if in.Purpose == "" {
		in.Purpose = "core_download"
		if in.Type == "site.refreshed" {
			in.Purpose = "site_statistics"
		}
		if in.Type == "subscription.added" {
			in.Purpose = "subscription"
		}
	}
	if in.Type == "subscription.added" {
		if in.Purpose != "subscription" || in.SiteID != "" || in.IntentID != "" || in.AssetID != "" || in.JobID != "" || in.WorkflowID != "" {
			return invalid()
		}
	} else if in.SubscriptionID != "" || (in.Type == "site.refreshed" && in.Purpose != "site_statistics") || (in.Type != "site.refreshed" && (in.Purpose != "core_download" || in.SiteID != "")) {
		return invalid()
	}
	if in.Origin == "" {
		in.Origin = "core"
	}
	if in.Origin != "core" {
		return invalid()
	}
	if in.NativeDownloadID != "" && (in.Type != "download.accepted" || in.IntentID != "" || in.AssetID != "" || in.SiteID != "" || in.SubscriptionID != "" || in.Purpose != "core_download") {
		return invalid()
	}
	subject, kind := "", ""
	switch in.Type {
	case "download.accepted", "download.completed":
		if in.NativeDownloadID != "" {
			subject, kind = in.NativeDownloadID, "native_download"
		} else if in.IntentID == "" {
			return invalid()
		} else {
			subject, kind = in.IntentID, "intent"
		}
	case "import.completed", "scrape.completed", "library.visible":
		if in.AssetID == "" {
			return invalid()
		}
		subject, kind = in.AssetID, "asset"
	case "job.failed":
		subject, kind = in.JobID, "job"
	case "site.refreshed":
		// Source fact: a userdata DTO was received, including one with err_msg.
		// It does not assert healthy authentication or a successful native job.
		if (in.SiteID != "*" && !domainReferencePattern.MatchString(in.SiteID)) || in.MediaID != "" || in.AssetID != "" || in.IntentID != "" {
			return invalid()
		}
		subject, kind = in.SiteID, "site"
	case "subscription.added", "subscription.completed":
		if in.SubscriptionID == "" || !domainMediaPattern.MatchString(in.MediaID) {
			return invalid()
		}
		subject, kind = in.SubscriptionID, "subscription"
	default:
		return invalid()
	}
	if in.Type != "job.failed" && in.Type != "site.refreshed" && in.Type != "subscription.added" && in.NativeDownloadID == "" && (in.WorkflowID == "" || !domainMediaPattern.MatchString(in.MediaID)) {
		return invalid()
	}
	if in.MediaID != "" && !domainMediaPattern.MatchString(in.MediaID) {
		return invalid()
	}
	if in.SubjectID == "" {
		in.SubjectID = subject
	}
	if in.SubjectKind == "" {
		in.SubjectKind = kind
	}
	if in.SubjectID != subject || in.SubjectKind != kind {
		return invalid()
	}
	if in.OccurredAt == "" {
		in.OccurredAt = now()
	}
	t, err := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if err != nil {
		return invalid()
	}
	in.OccurredAt = t.UTC().Format(time.RFC3339Nano)
	return in, nil
}

// recordDomainEventTx must share the transaction that commits the stated fact.
// Duplicate IDs retain the original time/sequence and cannot rewrite references.
func recordDomainEventTx(ctx context.Context, tx *sql.Tx, in DomainEvent) error {
	if tx == nil {
		return bad("DOMAIN_EVENT_TX_REQUIRED", "域事件必须与业务状态在同一事务提交")
	}
	event, err := validateDomainEvent(in)
	if err != nil {
		return err
	}
	if event.Type == "system.error" {
		if err = validateSystemErrorBackingTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if event.Type == "subscription.completed" {
		if err = validateSubscriptionCompletedBackingTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if event.Type == "notice.message" {
		if err = validateNoticeMessageBackingTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if event.Type == "site.deleted" {
		if err = validateSiteDeletedBackingTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if event.Type == "download.deleted" {
		if err = validateDownloadDeletedBackingTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if event.Type == "downloadfile.deleted" {
		if err = validateDownloadFileEventBackingTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, domainEventSchema); err != nil {
		return err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, event.EventID).Scan(&raw)
	if err == nil {
		var prior DomainEvent
		if json.Unmarshal(raw, &prior) != nil {
			return bad("DOMAIN_EVENT_CORRUPT", "已有域事件无法读取")
		}
		// A replay may have a new observation time, but no new authority or subject.
		prior.OccurredAt = event.OccurredAt
		if prior != event {
			return bad("DOMAIN_EVENT_ID_CONFLICT", "域事件标识已绑定其他事实，拒绝覆盖")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	raw, err = json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO domain_events(event_id,payload) VALUES(?,?)`, event.EventID, raw)
	return err
}

// domainEvents is deliberately internal. Consumers resume after the last durably
// handled sequence; replay is at least once, so they deduplicate by event_id.
// A cursor/subject is not a grant to mutate downloaders, files or credentials.
func (a *App) domainEvents(ctx context.Context, after int64, limit int) (DomainEventPage, error) {
	out := DomainEventPage{Events: []DomainEvent{}, NextCursor: after}
	if after < 0 || limit < 1 || limit > 200 {
		return out, bad("INVALID_EVENT_CURSOR", "域事件游标或每页数量无效")
	}
	if _, err := a.Store.DB.ExecContext(ctx, domainEventSchema); err != nil {
		return out, err
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT seq,event_id,payload FROM domain_events WHERE seq>? ORDER BY seq LIMIT ?`, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int64
		var storedID string
		var raw []byte
		var event DomainEvent
		if err = rows.Scan(&sequence, &storedID, &raw); err != nil {
			return out, err
		}
		if json.Unmarshal(raw, &event) != nil {
			return out, bad("DOMAIN_EVENT_CORRUPT", "域事件无法读取")
		}
		if event.SchemaVersion != 1 || event.OccurredAt == "" || event.EventID != storedID {
			return out, bad("DOMAIN_EVENT_CORRUPT", "域事件版本或固定标识不一致")
		}
		if event, err = validateDomainEvent(event); err != nil {
			return out, err
		}
		event.Sequence = sequence
		out.Events = append(out.Events, event)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Complete = len(out.Events) <= limit
	if !out.Complete {
		out.Events = out.Events[:limit]
	}
	if len(out.Events) > 0 {
		out.NextCursor = out.Events[len(out.Events)-1].Sequence
	}
	return out, nil
}
func assetDomainEvent(eventType string, job Job, asset Asset) DomainEvent {
	return DomainEvent{EventID: domainEventID(eventType, asset.ID+":"+job.ID), Type: eventType, WorkflowID: job.WorkflowID, JobID: job.ID, ParentJobID: job.ParentJobID, MediaID: mediaKey(asset.Media), IntentID: asset.IntentID, AssetID: asset.ID, Purpose: "core_download"}
}
