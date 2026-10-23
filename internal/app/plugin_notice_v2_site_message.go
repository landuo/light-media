package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

const siteMessageNoticeOwner = "system.site.message"

func siteMessageNoticeEvidenceError() error {
	return bad("SITE_MESSAGE_UNCONFIRMED", "站点消息的采集来源或通知回执无法核对")
}

func siteMessageEffect(identity siteMessageIdentity) string {
	return identity.Collection + "/" + fmt.Sprint(identity.Ordinal)
}

// Reading an immutable route checks its frozen core receipt, not a synthetic
// plugin run. Terminal receipts remain readable; execution has a separate gate.
func (p *PluginStore) validateSiteMessageNoticeBinding(ctx context.Context, q subscriptionCompletionReader, r pluginNoticeRoute) error {
	b := r.BusinessV2
	if b == nil || b.Binding.Kind != "site_message" || b.Binding.SiteMessage == nil || b.Binding.Added != nil || b.Binding.Completion != nil || r.Instance != siteMessageNoticeOwner || r.Generation != 1 || b.Binding.Inbox != "" || b.Binding.Ordinal != 0 || b.Binding.CoreInputSHA != "" {
		return siteMessageNoticeEvidenceError()
	}
	identity := *b.Binding.SiteMessage
	if identity.Collection == "" || identity.Ordinal < 0 || r.EffectID != siteMessageEffect(identity) || r.Token != p.noticeID("site-message-business", identity, tokenHash(string(b.Input))) {
		return siteMessageNoticeEvidenceError()
	}
	var payload sourceBusinessNotice
	if cleanupJournalStrict(b.Input, &payload) != nil || !payload.AbsentContext {
		return siteMessageNoticeEvidenceError()
	}
	business, e := p.siteMessageBusiness(ctx, q, identity)
	if e != nil || !bytes.Equal(business, b.Input) {
		return siteMessageNoticeEvidenceError()
	}
	return nil
}

func (a *App) initSiteMessageNotice(ctx context.Context, identity siteMessageIdentity, business []byte) (string, error) {
	var payload sourceBusinessNotice
	if cleanupJournalStrict(business, &payload) != nil || !payload.AbsentContext {
		return "", siteMessageNoticeEvidenceError()
	}
	input, e := larkBusinessNoticeContext(payload)
	if e != nil {
		return "", e
	}
	encoded, e := noticeV2PackContext(input)
	if e != nil {
		return "", e
	}
	env, e := notice.NewEnvelope(payload.Envelope)
	if e != nil {
		return "", e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	p := a.pluginStore
	original, e := p.siteMessageBusiness(ctx, tx, identity)
	if e != nil || !bytes.Equal(original, business) || !bytes.Equal(original, mustCleanSeedJSON(payload)) {
		return "", siteMessageNoticeEvidenceError()
	}
	token := p.noticeID("site-message-business", identity, tokenHash(string(business)))
	id := p.noticeID("route", siteMessageNoticeOwner, int64(1), token)
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_notice_routes WHERE id=?`, id).Scan(&sealed)
	if e == nil {
		existing, _, e := p.readNoticeRoute(ctx, tx, id)
		if e != nil || existing.BusinessV2 == nil || existing.BusinessV2.Binding.SiteMessage == nil || *existing.BusinessV2.Binding.SiteMessage != identity || !bytes.Equal(existing.BusinessV2.Input, business) {
			return "", siteMessageNoticeEvidenceError()
		}
		return id, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	if e = p.requireSiteMessageExecutionTx(ctx, tx, identity); e != nil {
		return "", e
	}
	stamp := p.noticeNow()
	binding := pluginNoticeV2Binding{Kind: "site_message", Instance: siteMessageNoticeOwner, Generation: 1, SiteMessage: &identity}
	b := &pluginNoticeV2Business{Version: 1, Projection: sourceBusinessProjection(payload), Binding: binding, Input: original, Context: encoded, Kwargs: payload.Kwargs, EnvelopeSHA: tokenHash(string(mustCleanSeedJSON(env.Dump()))), BusinessSHA: noticeV2BusinessSHA(encoded, payload.Kwargs)}
	r := pluginNoticeRoute{Version: 2, ID: id, Kind: "business", Instance: siteMessageNoticeOwner, Generation: 1, Token: token, EffectID: siteMessageEffect(identity), AcceptedSHA: tokenHash(string(mustCleanSeedJSON(b))), Envelope: mustCleanSeedJSON(env.Dump()), CreatedAt: stamp.UTC().Format(time.RFC3339Nano), CurrentTime: stamp.Local().Format("2006-01-02 15:04:05"), BusinessV2: b}
	if e = a.requireSiteMessageAuthorityTx(ctx, tx, r); e != nil {
		return "", e
	}
	sealed, e = p.noticeSeal("route", id, r)
	if e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, id, r.Instance, r.Generation, r.Token, sealed); e != nil {
		return "", e
	}
	if e = p.commitNoticeTx(tx, "business_accept"); e != nil {
		_ = tx.Rollback()
		read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		got, _, cause := p.readNoticeRoute(read, a.Store.DB, id)
		if cause != nil || !bytes.Equal(mustCleanSeedJSON(got), mustCleanSeedJSON(r)) {
			return "", seedhost.ErrUnknown
		}
	}
	return id, nil
}

// Normal Router return is success, including disabled channels or queue skips;
// it does not assert delivery. Source exceptions and unknown effects stay errors.
func (a *App) postSiteMessageNotice(ctx context.Context, identity siteMessageIdentity, business []byte) ([]byte, error) {
	id, e := a.initSiteMessageNotice(ctx, identity, business)
	if e != nil {
		return nil, e
	}
	if e = a.runNoticeRoute(ctx, id); e != nil {
		return nil, e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	route, _, e := a.pluginStore.readNoticeRoute(ctx, tx, id)
	if e != nil || !route.Complete || route.SourceError != "" || route.BusinessV2 == nil || route.Count < 2 || route.LastSHA == "" {
		return nil, siteMessageNoticeEvidenceError()
	}
	result := pluginNoticeV2Result{Version: 1, RouteID: id, EnvelopeSHA: route.BusinessV2.EnvelopeSHA, BusinessSHA: route.BusinessV2.BusinessSHA, Count: route.Count, LastSHA: route.LastSHA, SourceReturned: true}
	raw := mustCleanSeedJSON(result)
	if e = a.requireSiteMessageNoticeTx(ctx, tx, identity, business, raw); e != nil {
		return nil, e
	}
	return raw, tx.Commit()
}

func (a *App) requireSiteMessageNoticeTx(ctx context.Context, tx *sql.Tx, identity siteMessageIdentity, business, result []byte) error {
	var proof pluginNoticeV2Result
	if cleanupJournalStrict(result, &proof) != nil || proof.Version != 1 || !proof.SourceReturned || proof.SourceError != "" {
		return siteMessageNoticeEvidenceError()
	}
	original, e := a.pluginStore.siteMessageBusiness(ctx, tx, identity)
	if e != nil || !bytes.Equal(original, business) {
		return siteMessageNoticeEvidenceError()
	}
	r, _, e := a.pluginStore.readNoticeRoute(ctx, tx, proof.RouteID)
	if e != nil || r.BusinessV2 == nil || r.BusinessV2.Binding.Kind != "site_message" || r.BusinessV2.Binding.SiteMessage == nil || *r.BusinessV2.Binding.SiteMessage != identity || !bytes.Equal(r.BusinessV2.Input, business) || !r.Complete || r.SourceError != "" || r.Count != proof.Count || r.LastSHA != proof.LastSHA || r.BusinessV2.EnvelopeSHA != proof.EnvelopeSHA || r.BusinessV2.BusinessSHA != proof.BusinessSHA {
		return siteMessageNoticeEvidenceError()
	}
	return nil
}
