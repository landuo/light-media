package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

const subscriptionAddNoticeOwner = "system.subscription.add"

func (p *PluginStore) validateSubscriptionAddNoticeBinding(ctx context.Context, q subscriptionCompletionReader, r pluginNoticeRoute) error {
	b := r.BusinessV2
	if b == nil || b.Binding.SiteMessage != nil || b.Binding.Kind != "subscription_add" || b.Binding.Added == nil || b.Binding.Completion != nil || r.Instance != subscriptionAddNoticeOwner || r.Generation != 1 || b.Binding.Inbox != "" || b.Binding.Ordinal != 0 || b.Binding.CoreInputSHA != "" {
		return subscriptionAddEvidenceError()
	}
	identity := *b.Binding.Added
	source, e := p.subscriptionAddReceipt(ctx, q, identity, false)
	if e != nil || source.Stage == "admitted" || !bytes.Equal(source.Input.Business, b.Input) || r.EffectID != identity.ID || r.Token != p.noticeID("subscription-add-business", identity, tokenHash(string(b.Input))) {
		return subscriptionAddEvidenceError()
	}
	return nil
}
func (a *App) initSubscriptionAddNotice(ctx context.Context, identity subscriptionAddIdentity, business []byte) (string, error) {
	var payload sourceBusinessNotice
	if cleanupJournalStrict(business, &payload) != nil {
		return "", subscriptionAddEvidenceError()
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
	source, e := a.pluginStore.subscriptionAddReceipt(ctx, tx, identity, true)
	if e != nil || !bytes.Equal(source.Input.Business, mustCleanSeedJSON(payload)) {
		return "", subscriptionAddEvidenceError()
	}
	p := a.pluginStore
	token := p.noticeID("subscription-add-business", identity, tokenHash(string(source.Input.Business)))
	id := p.noticeID("route", subscriptionAddNoticeOwner, int64(1), token)
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_notice_routes WHERE id=?`, id).Scan(&sealed)
	if e == nil {
		existing, _, e := p.readNoticeRoute(ctx, tx, id)
		if e != nil || existing.BusinessV2 == nil || existing.BusinessV2.Binding.Added == nil || *existing.BusinessV2.Binding.Added != identity || !bytes.Equal(existing.BusinessV2.Input, source.Input.Business) {
			return "", subscriptionAddEvidenceError()
		}
		return id, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	if source.Stage != "saved" {
		return "", subscriptionAddEvidenceError()
	}
	stamp := p.noticeNow()
	binding := pluginNoticeV2Binding{Kind: "subscription_add", Instance: subscriptionAddNoticeOwner, Generation: 1, Added: &identity}
	b := &pluginNoticeV2Business{Version: 1, Projection: "tmdb-source-attributes/v1", Binding: binding, Input: source.Input.Business, Context: encoded, Kwargs: payload.Kwargs, EnvelopeSHA: tokenHash(string(mustCleanSeedJSON(env.Dump()))), BusinessSHA: noticeV2BusinessSHA(encoded, payload.Kwargs)}
	r := pluginNoticeRoute{Version: 2, ID: id, Kind: "business", Instance: subscriptionAddNoticeOwner, Generation: 1, Token: token, EffectID: identity.ID, AcceptedSHA: tokenHash(string(mustCleanSeedJSON(b))), Envelope: mustCleanSeedJSON(env.Dump()), CreatedAt: stamp.UTC().Format(time.RFC3339Nano), CurrentTime: stamp.Local().Format("2006-01-02 15:04:05"), BusinessV2: b}
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
func (a *App) postSubscriptionAddNotice(ctx context.Context, identity subscriptionAddIdentity, business []byte) ([]byte, error) {
	id, e := a.initSubscriptionAddNotice(ctx, identity, business)
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
		return nil, subscriptionAddEvidenceError()
	}
	result := pluginNoticeV2Result{Version: 1, RouteID: id, EnvelopeSHA: route.BusinessV2.EnvelopeSHA, BusinessSHA: route.BusinessV2.BusinessSHA, Count: route.Count, LastSHA: route.LastSHA, SourceReturned: true}
	raw := mustCleanSeedJSON(result)
	if e = a.requireSubscriptionAddNoticeTx(ctx, tx, identity, business, raw); e != nil {
		return nil, e
	}
	return raw, tx.Commit()
}
func (a *App) requireSubscriptionAddNoticeTx(ctx context.Context, tx *sql.Tx, identity subscriptionAddIdentity, business, result []byte) error {
	var proof pluginNoticeV2Result
	if cleanupJournalStrict(result, &proof) != nil || proof.Version != 1 || !proof.SourceReturned || proof.SourceError != "" {
		return subscriptionAddEvidenceError()
	}
	source, e := a.pluginStore.subscriptionAddReceipt(ctx, tx, identity, true)
	if e != nil || source.Stage == "admitted" || !bytes.Equal(source.Input.Business, business) {
		return subscriptionAddEvidenceError()
	}
	r, _, e := a.pluginStore.readNoticeRoute(ctx, tx, proof.RouteID)
	if e != nil || r.BusinessV2 == nil || r.BusinessV2.Binding.Kind != "subscription_add" || r.BusinessV2.Binding.Added == nil || *r.BusinessV2.Binding.Added != identity || !bytes.Equal(r.BusinessV2.Input, business) || !r.Complete || r.SourceError != "" || r.Count != proof.Count || r.LastSHA != proof.LastSHA || r.BusinessV2.EnvelopeSHA != proof.EnvelopeSHA || r.BusinessV2.BusinessSHA != proof.BusinessSHA {
		return subscriptionAddEvidenceError()
	}
	return nil
}
