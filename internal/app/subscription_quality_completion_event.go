package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type subscriptionCompletionEventKey struct{}
type subscriptionCompletionEventGrant struct {
	a                *App
	Identity         subscriptionCompletionIdentity
	Business, Result json.RawMessage
}

func validateSubscriptionCompletedDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) {
		return in, bad("INVALID_DOMAIN_EVENT", "订阅完成事件契约无效，业务结果未提交")
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	if in.Type != "subscription.completed" || in.SchemaVersion != 1 || in.Sequence != 0 || !domainEventIDPattern.MatchString(in.EventID) || !domainReferencePattern.MatchString(in.JobID) || !domainReferencePattern.MatchString(in.WorkflowID) || !domainReferencePattern.MatchString(in.SubscriptionID) || !domainMediaPattern.MatchString(in.MediaID) {
		return invalid()
	}
	if in.IntentID != "" || in.NativeDownloadID != "" || in.AssetID != "" || in.SiteID != "" || in.FileEventID != "" || in.NoticeID != "" || in.DownloadDeletedID != "" || in.PluginTriggerID != "" || in.SourceSiteID != 0 || in.CausationID != "" || in.ParentJobID != "" {
		return invalid()
	}
	if in.Purpose == "" {
		in.Purpose = "subscription"
	}
	if in.Origin == "" {
		in.Origin = "core"
	}
	if in.Purpose != "subscription" || in.Origin != "core" {
		return invalid()
	}
	if in.SubjectID == "" {
		in.SubjectID = in.SubscriptionID
	}
	if in.SubjectKind == "" {
		in.SubjectKind = "subscription"
	}
	if in.SubjectID != in.SubscriptionID || in.SubjectKind != "subscription" {
		return invalid()
	}
	if in.OccurredAt == "" {
		in.OccurredAt = now()
	}
	at, e := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if e != nil {
		return invalid()
	}
	in.OccurredAt = at.UTC().Format(time.RFC3339Nano)
	return in, nil
}

// The public event remains an opaque reference. Its write requires this exact
// native job's complete Notice proof in the same transaction, including actual
// check/history/deletion and accepted download closure. A row's existence or a
// forged Complete flag cannot grant publication.
func validateSubscriptionCompletedBackingTx(ctx context.Context, tx *sql.Tx, event DomainEvent) error {
	grant, ok := ctx.Value(subscriptionCompletionEventKey{}).(*subscriptionCompletionEventGrant)
	if !ok || grant == nil || grant.a == nil || grant.Identity.JobID != event.JobID || grant.Identity.SubscriptionID != event.SubscriptionID || event.EventID != domainEventID("subscription.completed", grant.Identity.ID) {
		return completionEvidenceError()
	}
	a := grant.a
	if e := a.requireSubscriptionCompletionRawNoticeTx(ctx, tx, grant.Identity, grant.Business, grant.Result); e != nil {
		return e
	}
	var receipt subscriptionCompletionReceipt
	if e := a.readQualitySealed(ctx, tx, "subscription_completion", grant.Identity.ID, &receipt); e != nil || receipt.Identity != grant.Identity || sourceSubscriptionMediaKey(receipt.Input.Source.Subscription) != event.MediaID {
		return completionEvidenceError()
	}
	var source struct {
		SubscribeID          string `json:"subscribe_id"`
		Subscribe, MediaInfo json.RawMessage
	}
	raw, e := a.pluginStore.readSubscriptionCompletionSealed(ctx, tx, "source_subscribe_complete", grant.Identity.ID)
	if e != nil {
		return e
	}
	// Source JSON uses the same original public keys as __finish_subscribe.
	var sealed map[string]json.RawMessage
	if json.Unmarshal(raw, &sealed) != nil || json.Unmarshal(sealed["subscribe_id"], &source.SubscribeID) != nil || source.SubscribeID != event.SubscriptionID || !larkSameRaw(mustCleanSeedJSON(sealed["subscribe_info"]), mustCleanSeedJSON(receipt.Input.Source.Subscribe)) || !larkSameRaw(mustCleanSeedJSON(sealed["mediainfo"]), mustCleanSeedJSON(receipt.Input.Source.MediaInfo)) {
		return completionEvidenceError()
	}
	var kind, state, workflow string
	if e = tx.QueryRowContext(ctx, `SELECT kind,state,workflow_id FROM jobs WHERE id=?`, event.JobID).Scan(&kind, &state, &workflow); e != nil || kind != "subscription_completion" || state != "running" || workflow != event.WorkflowID {
		return completionEvidenceError()
	}
	return nil
}
