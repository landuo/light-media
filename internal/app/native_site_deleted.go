package app

import (
	"context"
	"database/sql"
	"time"
)

// Deletion uses the retired native identity, not a name/origin lookup that could
// now select a newly added site. No URL or credentials enter the public event.
func nativeSiteDeletedEvent(identity nativeSiteIdentity) DomainEvent {
	return DomainEvent{SchemaVersion: 1, Type: "site.deleted", EventID: domainEventID("site.deleted", identity.SiteID), SiteID: identity.SiteID, SourceSiteID: identity.SourceID, OccurredAt: identity.DeletedAt, Purpose: "site_management", SubjectID: identity.SiteID, SubjectKind: "site", Origin: "core"}
}

func invalidSiteDeletionEvent() error {
	return bad("SITE_DELETION_FACT_INVALID", "站点删除事件缺少可信原始事实")
}

type nativeSiteDeletionFact struct {
	Version  int
	EventID  string
	Purpose  string
	Identity nativeSiteIdentity
}

func (s *Store) recordNativeSiteDeletionTx(ctx context.Context, tx *sql.Tx, identity nativeSiteIdentity) error {
	event := nativeSiteDeletedEvent(identity)
	fact := nativeSiteDeletionFact{Version: 1, EventID: event.EventID, Purpose: "native-site-deletion/v1", Identity: identity}
	sealed, err := s.Seal(fact)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_site_deletions VALUES(?,?,?,?)`, event.EventID, identity.SiteID, identity.SourceID, sealed); err != nil {
		return err
	}
	return recordDomainEventTx(ctx, tx, event)
}

func validateSiteDeletedDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) { return in, bad("INVALID_DOMAIN_EVENT", "站点删除事件契约无效") }
	if in.SchemaVersion != 1 || in.Sequence != 0 || in.Type != "site.deleted" || !domainReferencePattern.MatchString(in.SiteID) || in.SourceSiteID < 1 || in.EventID != domainEventID("site.deleted", in.SiteID) {
		return invalid()
	}
	if in.JobID != "" || in.WorkflowID != "" || in.MediaID != "" || in.IntentID != "" || in.NativeDownloadID != "" || in.FileEventID != "" || in.NoticeID != "" || in.AssetID != "" || in.SubscriptionID != "" || in.CausationID != "" || in.ParentJobID != "" {
		return invalid()
	}
	if in.Purpose == "" {
		in.Purpose = "site_management"
	}
	if in.SubjectID == "" {
		in.SubjectID = in.SiteID
	}
	if in.SubjectKind == "" {
		in.SubjectKind = "site"
	}
	if in.Origin == "" {
		in.Origin = "core"
	}
	if in.Purpose != "site_management" || in.SubjectID != in.SiteID || in.SubjectKind != "site" || in.Origin != "core" {
		return invalid()
	}
	// The timestamp must come from the tombstone transaction; no caller clock
	// default is accepted for this already committed source fact.
	at, err := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if err != nil {
		return invalid()
	}
	in.OccurredAt = at.UTC().Format(time.RFC3339Nano)
	return in, nil
}

func validateSiteDeletedBackingTx(ctx context.Context, tx *sql.Tx, event DomainEvent) error {
	var sourceID int64
	var state, deletedAt, proof string
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT n.source_id,n.state,n.deleted_at,n.proof,d.sealed FROM native_site_deletions d JOIN native_site_identities n ON n.site_id=d.site_id AND n.source_id=d.source_id WHERE d.event_id=? AND d.site_id=?`, event.EventID, event.SiteID).Scan(&sourceID, &state, &deletedAt, &proof, &sealed)
	if err != nil || sourceID != event.SourceSiteID || state != "deleted" || deletedAt != event.OccurredAt || proof == "" || len(sealed) == 0 {
		return invalidSiteDeletionEvent()
	}
	return nil
}

// Historical event access authenticates the permanent tombstone MAC. It never
// allocates/rebinds an identity or provides a live site's transport authority.
func (s *Store) siteDeletedSourceData(ctx context.Context, q siteIdentityReader, event DomainEvent) (map[string]any, error) {
	copy := event
	copy.Sequence = 0
	normalized, err := validateSiteDeletedDomainEvent(copy)
	if err != nil {
		return nil, err
	}
	identity, err := s.readNativeSiteIdentity(ctx, q, "site_id", event.SiteID)
	if err != nil || identity.State != "deleted" || nativeSiteDeletedEvent(identity) != normalized {
		return nil, invalidSiteDeletionEvent()
	}
	var siteID string
	var sourceID int64
	var sealed []byte
	if err = q.QueryRowContext(ctx, `SELECT site_id,source_id,sealed FROM native_site_deletions WHERE event_id=?`, event.EventID).Scan(&siteID, &sourceID, &sealed); err != nil {
		return nil, invalidSiteDeletionEvent()
	}
	var fact nativeSiteDeletionFact
	if siteID != identity.SiteID || sourceID != identity.SourceID || s.Unseal(sealed, &fact) != nil || fact.Version != 1 || fact.Purpose != "native-site-deletion/v1" || fact.EventID != event.EventID || fact.Identity != identity {
		return nil, invalidSiteDeletionEvent()
	}
	// The original SiteDeleted consumer receives a numeric site_id. Native
	// numeric IDs and old MoviePilot IDs still need separate migration receipts.
	return map[string]any{"site_id": identity.publicSourceID()}, nil
}
