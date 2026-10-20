package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

const subscriptionCompletionNoticeOwner = "system.subscription"

func (p *PluginStore) validateSubscriptionCompletionNoticeBinding(ctx context.Context, q subscriptionCompletionReader, r pluginNoticeRoute) error {
	b := r.BusinessV2
	if b == nil || b.Binding.Added != nil || b.Binding.Kind != "subscription_completion" || b.Binding.Completion == nil || r.Instance != subscriptionCompletionNoticeOwner || r.Generation != 1 || b.Binding.Inbox != "" || b.Binding.Ordinal != 0 || b.Binding.CoreInputSHA != "" {
		return seedhost.ErrUnsafe
	}
	identity := *b.Binding.Completion
	if r.EffectID != identity.ID || r.Token != p.noticeID("subscription-completion-business", identity, tokenHash(string(b.Input))) {
		return seedhost.ErrUnsafe
	}
	input, e := p.subscriptionCompletionNoticeReadInputTx(ctx, q, identity)
	if e != nil || !bytes.Equal(mustCleanSeedJSON(input), mustCleanSeedJSON(b.Input)) {
		return completionEvidenceError()
	}
	return nil
}

// initSubscriptionCompletionNotice only accepts the original business payload
// from the sealed completion receipt after check/history/delete. A valid ID is
// insufficient authority to replace its text, metadata or accepted download.
func (a *App) initSubscriptionCompletionNotice(ctx context.Context, identity subscriptionCompletionIdentity, payload sourceBusinessNotice) (string, error) {
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
	original, e := a.pluginStore.subscriptionCompletionNoticeInputTx(ctx, tx, identity)
	if e != nil {
		return "", e
	}
	rawInput := mustCleanSeedJSON(payload)
	if !bytes.Equal(mustCleanSeedJSON(original), rawInput) {
		return "", completionEvidenceError()
	}
	p := a.pluginStore
	token := p.noticeID("subscription-completion-business", identity, tokenHash(string(rawInput)))
	id := p.noticeID("route", subscriptionCompletionNoticeOwner, int64(1), token)
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_notice_routes WHERE id=?`, id).Scan(&sealed)
	if e == nil {
		existing, _, e := p.readNoticeRoute(ctx, tx, id)
		if e != nil || existing.BusinessV2.Binding.Completion == nil || *existing.BusinessV2.Binding.Completion != identity || !bytes.Equal(existing.BusinessV2.Input, rawInput) {
			return "", completionEvidenceError()
		}
		return id, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	stage, e := p.subscriptionCompletionNoticeStageTx(ctx, tx, identity)
	if e != nil || stage != "notify_pending" {
		return "", completionEvidenceError()
	}
	stamp := p.noticeNow()
	b := &pluginNoticeV2Business{Version: 1, Projection: "tmdb-source-attributes/v1", Binding: pluginNoticeV2Binding{Kind: "subscription_completion", Instance: subscriptionCompletionNoticeOwner, Generation: 1, Completion: &identity}, Input: rawInput, Context: encoded, Kwargs: payload.Kwargs, EnvelopeSHA: tokenHash(string(mustCleanSeedJSON(env.Dump()))), BusinessSHA: noticeV2BusinessSHA(encoded, payload.Kwargs)}
	r := pluginNoticeRoute{Version: 2, ID: id, Kind: "business", Instance: subscriptionCompletionNoticeOwner, Generation: 1, Token: token, EffectID: identity.ID, AcceptedSHA: tokenHash(string(mustCleanSeedJSON(b))), Envelope: mustCleanSeedJSON(env.Dump()), CreatedAt: stamp.UTC().Format(time.RFC3339Nano), CurrentTime: stamp.Local().Format("2006-01-02 15:04:05"), BusinessV2: b}
	sealed, e = p.noticeSeal("route", r.ID, r)
	if e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, r.ID, r.Instance, r.Generation, r.Token, sealed); e != nil {
		return "", e
	}
	if e = p.commitNoticeTx(tx, "business_accept"); e != nil {
		_ = tx.Rollback()
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		got, _, readErr := p.readNoticeRoute(readCtx, a.Store.DB, id)
		if readErr != nil || !bytes.Equal(mustCleanSeedJSON(got), mustCleanSeedJSON(r)) {
			return "", seedhost.ErrUnknown
		}
	}
	return id, nil
}

func (a *App) postSubscriptionCompletionNotice(ctx context.Context, identity subscriptionCompletionIdentity, payload sourceBusinessNotice) (pluginNoticeV2Result, error) {
	var result pluginNoticeV2Result
	id, e := a.initSubscriptionCompletionNotice(ctx, identity, payload)
	if e != nil {
		return result, e
	}
	if e = a.runNoticeRoute(ctx, id); e != nil {
		return result, e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	r, _, e := a.pluginStore.readNoticeRoute(ctx, tx, id)
	if e != nil || r.BusinessV2 == nil || !r.Complete || r.SourceError != "" || r.Count < 2 || r.LastSHA == "" {
		return result, completionEvidenceError()
	}
	result = pluginNoticeV2Result{Version: 1, RouteID: r.ID, EnvelopeSHA: r.BusinessV2.EnvelopeSHA, BusinessSHA: r.BusinessV2.BusinessSHA, Count: r.Count, LastSHA: r.LastSHA, SourceReturned: true}
	if e = a.requireSubscriptionCompletionNoticeTx(ctx, tx, identity, payload, result); e != nil {
		return pluginNoticeV2Result{}, e
	}
	return result, tx.Commit()
}

// The completion executor calls this in the same transaction immediately before
// SubscribeComplete/stats and before advancing its immutable completion stage.
func (a *App) requireSubscriptionCompletionNoticeTx(ctx context.Context, tx *sql.Tx, identity subscriptionCompletionIdentity, payload sourceBusinessNotice, result pluginNoticeV2Result) error {
	if result.Version != 1 || !result.SourceReturned || result.SourceError != "" {
		return completionEvidenceError()
	}
	if e := a.pluginStore.subscriptionCompletionNoticeIdentityTx(ctx, tx, identity); e != nil {
		return e
	}
	r, _, e := a.pluginStore.readNoticeRoute(ctx, tx, result.RouteID)
	if e != nil || r.BusinessV2 == nil || r.BusinessV2.Binding.Kind != "subscription_completion" || r.BusinessV2.Binding.Completion == nil || *r.BusinessV2.Binding.Completion != identity || !bytes.Equal(r.BusinessV2.Input, mustCleanSeedJSON(payload)) || !r.Complete || r.SourceError != "" || r.Count != result.Count || r.LastSHA != result.LastSHA || r.BusinessV2.EnvelopeSHA != result.EnvelopeSHA || r.BusinessV2.BusinessSHA != result.BusinessSHA {
		return completionEvidenceError()
	}
	return nil
}

// The quality executor uses a narrow raw boundary so it need not know Lark's
// caller types. Raw JSON carries no send authority: the post and final guard
// independently require the same sealed completion input and complete route.
func (a *App) postSubscriptionCompletionRawNotice(ctx context.Context, identity subscriptionCompletionIdentity, business json.RawMessage) (json.RawMessage, error) {
	var payload sourceBusinessNotice
	if cleanupJournalStrict(business, &payload) != nil {
		return nil, completionEvidenceError()
	}
	result, e := a.postSubscriptionCompletionNotice(ctx, identity, payload)
	if e != nil {
		return nil, e
	}
	return mustCleanSeedJSON(result), nil
}
func (a *App) requireSubscriptionCompletionRawNoticeTx(ctx context.Context, tx *sql.Tx, identity subscriptionCompletionIdentity, business, result json.RawMessage) error {
	var payload sourceBusinessNotice
	var proof pluginNoticeV2Result
	if cleanupJournalStrict(business, &payload) != nil || cleanupJournalStrict(result, &proof) != nil {
		return completionEvidenceError()
	}
	return a.requireSubscriptionCompletionNoticeTx(ctx, tx, identity, payload, proof)
}

func (p *PluginStore) requireNoticeV2ExecutionTx(ctx context.Context, q subscriptionCompletionReader, r pluginNoticeRoute) error {
	if r.Version == 2 && r.BusinessV2 != nil && r.BusinessV2.Binding.Kind == "subscription_add" {
		if r.BusinessV2.Binding.Added == nil {
			return subscriptionAddEvidenceError()
		}
		source, e := p.subscriptionAddReceipt(ctx, q, *r.BusinessV2.Binding.Added, true)
		if e != nil || source.Stage != "saved" {
			return subscriptionAddEvidenceError()
		}
		return nil
	}
	if r.Version != 2 || r.BusinessV2 == nil || r.BusinessV2.Binding.Kind != "subscription_completion" {
		return nil
	}
	if r.BusinessV2.Binding.Completion == nil {
		return completionEvidenceError()
	}
	stage, e := p.subscriptionCompletionNoticeStageTx(ctx, q, *r.BusinessV2.Binding.Completion)
	if e != nil || stage != "notify_pending" {
		return completionEvidenceError()
	}
	return nil
}
