package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
)

// Stable shared identity for Notice v2. A completion is a core business job,
// never a Lark run. Every digest refers to the immutable sealed input below.
type subscriptionCompletionIdentity struct {
	ID, JobID, InputSHA, SourceSHA, AcceptedSHA, SubscriptionID string
	Revision                                                    int
}
type subscriptionCompletionSource struct {
	Subscription                                       json.RawMessage
	Origin                                             json.RawMessage
	Subscribe, Meta, MediaInfo, ProviderInfo, Business json.RawMessage
}
type subscriptionCompletionAccepted struct {
	ID  string
	Raw json.RawMessage
}
type subscriptionCompletionInput struct {
	Version  int
	Source   subscriptionCompletionSource
	Accepted []subscriptionCompletionAccepted
	Ordinary *subscriptionCompletionOrdinaryBasis `json:",omitempty"`
}
type subscriptionCompletionCheckResult struct {
	Event          string
	Cancel         bool
	Source, Reason string
	// Retained for decoding older completion records; this field is not
	// populated as a claim of actual chain execution.
	Consumers []string `json:",omitempty"`
}
type subscriptionCompletionReceipt struct {
	Version                      int
	Identity                     subscriptionCompletionIdentity
	Input                        subscriptionCompletionInput
	Stage, HistoryID, HistorySHA string
	Check                        subscriptionCompletionCheckResult
}
type subscriptionCompletionReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func completionEvidenceError() error {
	return bad("SUBSCRIPTION_COMPLETION_UNCONFIRMED", "订阅完成的原始来源、下载事实或任务记录无法核对")
}
func (p *PluginStore) readSubscriptionCompletionSealed(ctx context.Context, q subscriptionCompletionReader, kind, id string) (json.RawMessage, error) {
	var sealed []byte
	if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, id).Scan(&sealed); e != nil {
		return nil, completionEvidenceError()
	}
	raw, e := p.unseal(kind, "system", id, sealed)
	if e != nil {
		return nil, completionEvidenceError()
	}
	return raw, nil
}

func (p *PluginStore) subscriptionCompletionNoticeStageTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity) (string, error) {
	return p.readSubscriptionCompletionNoticeStageTx(ctx, q, want, true)
}

// Immutable notice/publication evidence survives the executor waiting or a
// process recovery. This read grants no new route step, send or final event.
func (p *PluginStore) readSubscriptionCompletionNoticeStageTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity, requireRunning bool) (string, error) {
	if want.ID == "" || want.JobID == "" || want.InputSHA == "" || want.SourceSHA == "" || want.AcceptedSHA == "" || want.SubscriptionID == "" || want.Revision < 1 {
		return "", completionEvidenceError()
	}
	raw, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_completion", want.ID)
	if e != nil {
		return "", e
	}
	var r subscriptionCompletionReceipt
	if cleanupJournalStrict(raw, &r) != nil || r.Version != 1 || r.Identity != want || (r.Input.Version != 1 && r.Input.Version != 2) || r.HistoryID == "" || r.HistorySHA == "" || r.Check.Event != "subscribe.completion.check" || r.Check.Cancel {
		return "", completionEvidenceError()
	}
	if r.Stage != "notify_pending" && r.Stage != "notified" && r.Stage != "complete" {
		return "", completionEvidenceError()
	}
	if tokenHash(string(mustCleanSeedJSON(r.Input))) != want.InputSHA || tokenHash(string(mustCleanSeedJSON(r.Input.Source))) != want.SourceSHA || tokenHash(string(mustCleanSeedJSON(r.Input.Accepted))) != want.AcceptedSHA {
		return "", completionEvidenceError()
	}
	if r.Input.Version == 1 {
		var s Subscription
		if len(r.Input.Accepted) == 0 || r.Input.Ordinary != nil || cleanupJournalStrict(r.Input.Source.Subscription, &s) != nil || !s.BestVersion {
			return "", completionEvidenceError()
		}
	} else if e = p.validateOrdinaryCompletionInput(ctx, q, r); e != nil {
		return "", e
	}
	var source struct {
		ID      string `json:"id"`
		Context string `json:"source_context_sha"`
	}
	if json.Unmarshal(r.Input.Source.Subscription, &source) != nil || source.ID != want.SubscriptionID || !json.Valid(r.Input.Source.Subscribe) || !json.Valid(r.Input.Source.Meta) || !json.Valid(r.Input.Source.MediaInfo) || !json.Valid(r.Input.Source.ProviderInfo) || !json.Valid(r.Input.Source.Business) {
		return "", completionEvidenceError()
	}
	if source.Context != "" {
		origin, e := p.readSubscriptionCompletionSealed(ctx, q, "source_subscription_origin", want.SubscriptionID)
		if e != nil || tokenHash(string(origin)) != source.Context || !bytes.Equal(origin, r.Input.Source.Origin) {
			return "", completionEvidenceError()
		}
	} else if len(r.Input.Source.Origin) > 0 && string(r.Input.Source.Origin) != "null" {
		return "", completionEvidenceError()
	}
	seen := map[string]bool{}
	for _, accepted := range r.Input.Accepted {
		if accepted.ID == "" || seen[accepted.ID] {
			return "", completionEvidenceError()
		}
		seen[accepted.ID] = true
		fact, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_quality_accepted", accepted.ID)
		if e != nil || !bytes.Equal(fact, accepted.Raw) {
			return "", completionEvidenceError()
		}
		var f struct {
			Version                                              int
			Intent, Hash, Subscription, SnapshotSHA, Disposition string
			Revision                                             int
			Resource                                             json.RawMessage
		}
		if cleanupJournalStrict(fact, &f) != nil || f.Version != 1 || f.Intent != accepted.ID || f.Subscription != want.SubscriptionID || f.Revision < 1 || f.Disposition != "accepted" && f.Disposition != "accepted_duplicate" {
			return "", completionEvidenceError()
		}
		proof, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_quality_download", "intent:"+f.Intent)
		if e != nil || tokenHash(string(proof)) != f.SnapshotSHA {
			return "", completionEvidenceError()
		}
		var snapshot qualityDownloadSnapshot
		var native Subscription
		if cleanupJournalStrict(proof, &snapshot) != nil || cleanupJournalStrict(r.Input.Source.Subscription, &native) != nil || snapshot.Kind != "intent" || snapshot.ID != f.Intent || snapshot.SubscriptionID != f.Subscription || snapshot.Revision != f.Revision || !sameHash(snapshot.Hash, f.Hash) || snapshot.MediaKey != mediaKey(native.Media) || snapshot.Season != native.Season || snapshot.SourceContextSHA != source.Context || !bytes.Equal(mustCleanSeedJSON(snapshot.Resource), mustCleanSeedJSON(f.Resource)) {
			return "", completionEvidenceError()
		}
		// The original downloader receipt and accepted event must still exist;
		// an observed task list or an orphaned quality fact cannot supply them.
		post, e := p.readSubscriptionCompletionSealed(ctx, q, "download_post_add", f.Intent)
		if e != nil {
			return "", e
		}
		var postReceipt struct {
			Version                   int
			Intent, Hash, SourceError string
			SourceReturned            bool
		}
		if json.Unmarshal(post, &postReceipt) != nil || postReceipt.Version != 1 || postReceipt.Intent != f.Intent || !sameHash(postReceipt.Hash, f.Hash) || !postReceipt.SourceReturned || postReceipt.SourceError != "" {
			return "", completionEvidenceError()
		}
		added, e := p.readSubscriptionCompletionSealed(ctx, q, "download_added", domainEventID("download.accepted", f.Intent))
		if e != nil {
			return "", e
		}
		var d struct {
			ID   string `json:"download_id"`
			Kind string `json:"download_kind"`
			Hash string `json:"hash"`
		}
		if json.Unmarshal(added, &d) != nil || d.ID != f.Intent || d.Kind != "intent" || !sameHash(d.Hash, f.Hash) {
			return "", completionEvidenceError()
		}
	}
	history, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_history", r.HistoryID)
	if e != nil || tokenHash(string(history)) != r.HistorySHA {
		return "", completionEvidenceError()
	}
	var kind, state string
	var payload []byte
	if e = q.QueryRowContext(ctx, `SELECT kind,state,payload FROM jobs WHERE id=?`, want.JobID).Scan(&kind, &state, &payload); e != nil || kind != "subscription_completion" {
		return "", completionEvidenceError()
	}
	var job struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(payload, &job) != nil || job.ID != want.ID {
		return "", completionEvidenceError()
	}
	if requireRunning && r.Stage == "notify_pending" && state != "running" {
		return "", completionEvidenceError()
	}
	return r.Stage, nil
}
func (p *PluginStore) subscriptionCompletionNoticeIdentityTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity) error {
	_, e := p.subscriptionCompletionNoticeStageTx(ctx, q, want)
	return e
}

// The route compares its complete canonical business payload against this
// immutable input, rather than accepting arbitrary text under a valid ID.
func (p *PluginStore) subscriptionCompletionNoticeInputTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity) (json.RawMessage, error) {
	if _, e := p.subscriptionCompletionNoticeStageTx(ctx, q, want); e != nil {
		return nil, e
	}
	raw, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_completion", want.ID)
	if e != nil {
		return nil, e
	}
	var r subscriptionCompletionReceipt
	if cleanupJournalStrict(raw, &r) != nil {
		return nil, completionEvidenceError()
	}
	return append(json.RawMessage(nil), r.Input.Source.Business...), nil
}

func (p *PluginStore) subscriptionCompletionNoticeReadInputTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity) (json.RawMessage, error) {
	if _, e := p.readSubscriptionCompletionNoticeStageTx(ctx, q, want, false); e != nil {
		return nil, e
	}
	raw, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_completion", want.ID)
	if e != nil {
		return nil, e
	}
	var receipt subscriptionCompletionReceipt
	if cleanupJournalStrict(raw, &receipt) != nil {
		return nil, completionEvidenceError()
	}
	return append(json.RawMessage(nil), receipt.Input.Source.Business...), nil
}

func (a *App) subscriptionCompletionNoticeStageTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity) (string, error) {
	return a.pluginStore.subscriptionCompletionNoticeStageTx(ctx, q, want)
}
func (a *App) subscriptionCompletionNoticeIdentityTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity) error {
	return a.pluginStore.subscriptionCompletionNoticeIdentityTx(ctx, q, want)
}
func (a *App) subscriptionCompletionNoticeInputTx(ctx context.Context, q subscriptionCompletionReader, want subscriptionCompletionIdentity) (json.RawMessage, error) {
	return a.pluginStore.subscriptionCompletionNoticeInputTx(ctx, q, want)
}
