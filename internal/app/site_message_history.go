package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// An execution receipt proves the original SaveHistory transaction, not the
// continued existence of its user-manageable business row. In particular this
// record is NEVER used by exists_by_source and cannot reserve a source forever.
type siteMessageHistoryExecution struct {
	Version               int
	RouteID               string
	Identity              siteMessageIdentity
	Ordinal               int
	AcceptedSHA, InputSHA string
	Saved                 pluginNoticeV2History
}

func siteMessageHistoryExecutionID(route string, ordinal int) string {
	return route + "/" + fmt.Sprint(ordinal)
}

// This is called only inside the genuine Router.SaveHistory step transaction,
// after insertion of the notification and before the authenticated route step.
func (p *PluginStore) saveSiteMessageHistoryExecutionTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute, ordinal int, fields notice.Object, saved pluginNoticeV2History) error {
	if r.Version != 2 || r.BusinessV2 == nil || r.BusinessV2.Binding.Kind != "site_message" || r.BusinessV2.Binding.SiteMessage == nil || ordinal != r.Count || saved.History.ID != "notice_"+p.noticeID("business-history", r.ID, ordinal) || saved.Digest != p.noticeID("business-history-value", saved.History) {
		return seedhost.ErrUnsafe
	}
	proof := siteMessageHistoryExecution{Version: 1, RouteID: r.ID, Identity: *r.BusinessV2.Binding.SiteMessage, Ordinal: ordinal, AcceptedSHA: r.AcceptedSHA, InputSHA: tokenHash(string(mustCleanSeedJSON(fields))), Saved: saved}
	key := siteMessageHistoryExecutionID(r.ID, ordinal)
	sealed, e := p.noticeSeal("site-message-history-execution", key, proof)
	if e != nil {
		return e
	}
	// INSERT, never upsert: an existing execution cannot be replaced or adopted.
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('site_message_history_execution',?,?,?)`, key, sealed, now())
	return e
}

func (p *PluginStore) validateSiteMessageHistoryExecution(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute, step pluginNoticeStep, saved pluginNoticeV2History) error {
	if r.Version != 2 || r.BusinessV2 == nil || r.BusinessV2.Binding.Kind != "site_message" || r.BusinessV2.Binding.SiteMessage == nil || step.Kind != "history" || step.RouteID != r.ID {
		return seedhost.ErrUnsafe
	}
	key := siteMessageHistoryExecutionID(r.ID, step.Ordinal)
	var sealed []byte
	if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='site_message_history_execution' AND id=?`, key).Scan(&sealed); e != nil {
		return seedhost.ErrUnsafe
	}
	var proof siteMessageHistoryExecution
	if e := p.noticeUnseal("site-message-history-execution", key, sealed, &proof); e != nil {
		return seedhost.ErrUnsafe
	}
	if proof.Version != 1 || proof.RouteID != r.ID || proof.Identity != *r.BusinessV2.Binding.SiteMessage || proof.Ordinal != step.Ordinal || proof.AcceptedSHA != r.AcceptedSHA || proof.InputSHA != step.InputSHA || !larkSameRaw(mustCleanSeedJSON(proof.Saved), mustCleanSeedJSON(saved)) || proof.Saved.Digest != p.noticeID("business-history-value", proof.Saved.History) {
		return seedhost.ErrUnsafe
	}
	var raw []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, saved.History.ID).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	} // A removed row must stay removed.
	if e != nil {
		return seedhost.ErrUnsafe
	}
	var current Notification
	if cleanupJournalStrict(raw, &current) != nil {
		return seedhost.ErrUnsafe
	}
	current.Read = false
	if saved.Digest != p.noticeID("business-history-value", current) {
		return seedhost.ErrUnsafe
	}
	return nil
}
