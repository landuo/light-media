package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"light-media/internal/subscriptionquality"
)

type subscriptionCompletionCheckEvent struct{ Subscribe, MediaInfo, Meta json.RawMessage }

func (a *App) enqueueSubscriptionCompletionPreparationTx(ctx context.Context, tx *sql.Tx, in Intent) error {
	if in.SubscriptionID == "" {
		return nil
	}
	var s Subscription
	if e := qualityRecord(ctx, tx, "subscription", in.SubscriptionID, &s); errors.Is(e, sql.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	if s.State != "active" || s.SourceState == "P" || s.BestVersion && !subscriptionQualityState(s).Complete() {
		return nil
	}
	return enqueueSubscriptionCompletionPreparationForTx(ctx, tx, s, in.WorkflowID)
}

func (a *App) completionAcceptedClosure(ctx context.Context, q qualityReader, s Subscription) ([]subscriptionCompletionAccepted, error) {
	rows, e := q.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind='subscription_quality_accepted' ORDER BY id`)
	if e != nil {
		return nil, e
	}
	type record struct {
		id     string
		sealed []byte
	}
	records := []record{}
	for rows.Next() {
		var r record
		if e = rows.Scan(&r.id, &r.sealed); e != nil {
			rows.Close()
			return nil, e
		}
		records = append(records, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []subscriptionCompletionAccepted{}
	resources := []subscriptionquality.Resource{}
	state := subscriptionQualityState(s)
	state.CurrentPriority, state.EpisodePriority, state.Note = nil, nil, nil
	for _, r := range records {
		raw, e := a.pluginStore.unseal("subscription_quality_accepted", "system", r.id, r.sealed)
		if e != nil {
			return nil, e
		}
		var fact subscriptionQualityAccepted
		if cleanupJournalStrict(raw, &fact) != nil || fact.Version != 1 || fact.Intent != r.id {
			return nil, completionEvidenceError()
		}
		if fact.Subscription != s.ID || fact.Disposition != "accepted" && fact.Disposition != "accepted_duplicate" {
			continue
		}
		p, e := a.qualitySnapshot(ctx, q, "intent", r.id, fact.Hash)
		if e != nil || p == nil {
			return nil, completionEvidenceError()
		}
		if p.MediaKey != mediaKey(s.Media) || p.Season != s.Season {
			continue
		}
		if fact.SnapshotSHA != tokenHash(string(mustCleanSeedJSON(p))) || p.Revision != fact.Revision || p.SubscriptionID != fact.Subscription || p.SourceContextSHA != s.SourceContextSHA || !bytes.Equal(mustCleanSeedJSON(fact.Resource), mustCleanSeedJSON(p.Resource)) {
			return nil, completionEvidenceError()
		}
		var intent Intent
		if e = qualityRecord(ctx, q, "intent", fact.Intent, &intent); e != nil {
			return nil, completionEvidenceError()
		}
		post, e := a.sourcePostAddReceipt(ctx, q, intent)
		if e != nil || !post.SourceReturned || post.SourceError != "" || post.Mode == "existing_hash" || post.Duplicate != (fact.Disposition == "accepted_duplicate") || !sameHash(post.Hash, fact.Hash) {
			return nil, completionEvidenceError()
		}
		var added downloadAddedSnapshot
		if e = a.readQualitySealed(ctx, q, "download_added", domainEventID("download.accepted", fact.Intent), &added); e != nil || added.DownloadID != fact.Intent || added.DownloadKind != "intent" || !sameHash(added.Hash, fact.Hash) || added.TMDBID != s.Media.ID || added.MediaType != s.Media.MediaType {
			return nil, completionEvidenceError()
		}
		resource := fact.Resource
		if state.Type == subscriptionquality.TV {
			// None selects the whole torrent. Rebind its authenticated acquired
			// scope to today's targets: an old full pack cannot cover episodes
			// added by a later edit, but still proves a smaller current scope.
			targets := state.Targets()
			resource.ConfirmedFullCoverage = resource.ConfirmedFullCoverage && state.FullSeason(resource) && len(targets) > 0
			for _, episode := range targets {
				resource.ConfirmedFullCoverage = resource.ConfirmedFullCoverage && slices.Contains(p.Episodes, episode) && (len(resource.SelectedEpisodes) == 0 || slices.Contains(resource.SelectedEpisodes, episode))
			}
		}
		resources = append(resources, resource)
		out = append(out, subscriptionCompletionAccepted{ID: r.id, Raw: append(json.RawMessage(nil), raw...)})
	}
	if s.BestVersion && (len(out) == 0 || !state.Accepted(resources, 0).Complete()) {
		return nil, bad("SUBSCRIPTION_COMPLETION_FACTS_MISSING", "最高优先级尚缺完整的实际下载事实，未写历史或删除订阅")
	}
	return out, nil
}
func equalQualityEpisodes(a, b []int) bool {
	x, y := qualityCopyNote(a), qualityCopyNote(b)
	sort.Ints(x)
	sort.Ints(y)
	if len(x) != len(y) {
		return false
	}
	for i, n := range x {
		if n != y[i] {
			return false
		}
	}
	return true
}

func (a *App) prepareSubscriptionCompletion(ctx context.Context, j Job, key string, revision int) error {
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	if e := requireSubscriptionPreparationJob(ctx, a.Store.DB, j, key, revision); e != nil {
		return e
	}
	var s Subscription
	if e := a.Store.Get(ctx, "subscription", key, &s); errors.Is(e, sql.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	if s.Revision != revision || s.State != "active" || s.SourceState == "P" {
		return nil
	}
	if s.BestVersion && !subscriptionQualityState(s).Complete() {
		return nil
	}
	var resolved *standardSubscriptionRecognition
	if !s.BestVersion {
		var e error
		s, resolved, e = a.refreshOrdinaryCompletionScopeResolved(ctx, s)
		if e != nil {
			return e
		}
	}
	accepted, e := a.completionAcceptedClosure(ctx, a.Store.DB, s)
	if e != nil {
		return e
	}
	var basis *subscriptionCompletionOrdinaryBasis
	if !s.BestVersion {
		var complete bool
		basis, complete, e = a.ordinaryCompletionBasisWithExistence(ctx, a.Store.DB, s, accepted, originalExistenceFromRecognition(resolved))
		if e != nil || !complete {
			return e
		}
	}
	subscribe, e := a.subscriptionSourceProjection(ctx, s)
	if e != nil {
		return e
	}
	source, e := a.buildSubscriptionCompletionBusinessResolved(ctx, s, subscribe, resolved)
	if e != nil {
		return e
	} // Missing source MediaInfo/Notice never deletes a row.
	if !bytes.Equal(source.Subscription, mustCleanSeedJSON(s)) || !bytes.Equal(source.Subscribe, subscribe) {
		return completionEvidenceError()
	}
	check, e := a.sendStandardSubscriptionCompletionCheck(ctx, subscriptionCompletionCheckEvent{Subscribe: source.Subscribe, MediaInfo: source.MediaInfo, Meta: source.Meta})
	if e != nil {
		return e
	}
	if check.Cancel {
		return a.recordSubscriptionCheck(ctx, s, "完成被 "+check.Source+" 否决："+check.Reason, nil, time.Time{})
	}
	input := subscriptionCompletionInput{Version: 1, Source: source, Accepted: accepted}
	if basis != nil {
		input.Version, input.Ordinary = 2, basis
	}
	identity := subscriptionCompletionIdentity{ID: "subscription_complete_" + tokenHash(s.ID + ":" + tokenHash(string(mustCleanSeedJSON(input))))[:40], InputSHA: tokenHash(string(mustCleanSeedJSON(input))), SourceSHA: tokenHash(string(mustCleanSeedJSON(source))), AcceptedSHA: tokenHash(string(mustCleanSeedJSON(accepted))), SubscriptionID: s.ID, Revision: s.Revision}
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = requireSubscriptionPreparationJob(ctx, tx, j, key, revision); e != nil {
		return e
	}
	var current Subscription
	if e = qualityRecord(ctx, tx, "subscription", s.ID, &current); errors.Is(e, sql.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	if !bytes.Equal(mustCleanSeedJSON(current), source.Subscription) || current.SourceState == "P" {
		return bad("SUBSCRIPTION_COMPLETION_CHANGED", "订阅在完成判定期间已变化")
	}
	actual, e := a.completionAcceptedClosure(ctx, tx, current)
	if e != nil {
		return e
	}
	if !bytes.Equal(mustCleanSeedJSON(actual), mustCleanSeedJSON(accepted)) {
		return completionEvidenceError()
	}
	if basis != nil {
		fresh, complete, e := a.ordinaryCompletionBasisWithExistence(ctx, tx, current, actual, originalExistenceFromRecognition(resolved))
		if e != nil {
			return e
		}
		if !complete || !bytes.Equal(mustCleanSeedJSON(fresh), mustCleanSeedJSON(basis)) {
			return completionEvidenceError()
		}
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_completion_ordinary", identity.ID, basis); e != nil {
			return e
		}
	}
	history, e := subscriptionSourceHistory(subscribe, identity.ID)
	if e != nil {
		return e
	}
	// Original order: synchronous check, history, delete, then the durable job
	// sends the real template notification before publishing SubscribeComplete.
	if e = a.writeQualitySealedTx(ctx, tx, "subscription_history", identity.ID, history); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='subscription' AND id=?`, s.ID); e != nil {
		return e
	}
	job, e := enqueueTx(ctx, tx, "subscription_completion", map[string]string{"id": identity.ID}, j.WorkflowID)
	if e != nil {
		return e
	}
	identity.JobID = job.ID
	receipt := subscriptionCompletionReceipt{Version: 1, Identity: identity, Input: input, Stage: "notify_pending", HistoryID: identity.ID, HistorySHA: tokenHash(string(history)), Check: check}
	if e = a.writeQualitySealedTx(ctx, tx, "subscription_completion", identity.ID, receipt); e != nil {
		return e
	}
	if e = a.writeQualitySealedTx(ctx, tx, "source_chain_event", identity.ID, struct {
		Type   string
		Input  subscriptionCompletionCheckEvent
		Result subscriptionCompletionCheckResult
	}{"subscribe.completion.check", subscriptionCompletionCheckEvent{source.Subscribe, source.MediaInfo, source.Meta}, check}); e != nil {
		return e
	}
	return tx.Commit()
}

func subscriptionSourceHistory(subscribe json.RawMessage, id string) (json.RawMessage, error) {
	value, e := qualityJSONValue(subscribe)
	if e != nil {
		return nil, e
	}
	row, ok := value.(map[string]any)
	if !ok {
		return nil, completionEvidenceError()
	}
	out := map[string]any{}
	for _, key := range []string{"name", "year", "type", "keyword", "tmdbid", "imdbid", "tvdbid", "doubanid", "bangumiid", "anilistid", "mediaid", "media_source", "media_id", "season", "poster", "backdrop", "vote", "description", "filter", "include", "exclude", "quality", "resolution", "effect", "total_episode", "start_episode", "username", "sites", "best_version", "best_version_full", "episode_priority", "save_path", "search_imdbid", "custom_words", "media_category", "filter_groups", "episode_group"} {
		if value, exists := row[key]; exists {
			out[key] = value
		}
	}
	out["id"], out["date"] = id, sourceSubscriptionDate(now())
	return mustCleanSeedJSON(out), nil
}

func (a *App) runSubscriptionCompletion(ctx context.Context, j Job, key string) error {
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	var receipt subscriptionCompletionReceipt
	if e := a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", key, &receipt); e != nil {
		return e
	}
	if receipt.Identity.ID != key || receipt.Identity.JobID != j.ID {
		return completionEvidenceError()
	}
	if receipt.Stage == "complete" {
		return a.subscriptionCompletionNoticeIdentityTx(ctx, a.Store.DB, receipt.Identity)
	}
	if receipt.Stage != "notify_pending" {
		return completionEvidenceError()
	}
	result, e := a.postSubscriptionCompletionRawNotice(ctx, receipt.Identity, receipt.Input.Source.Business)
	if e != nil {
		return e
	}
	// Reuse the existing statistics worker lock so a newly committed child
	// cannot execute its HTTP call before the source's complete event has
	// been submitted. Acquire before the SQL connection, as that worker does.
	a.subscriptionStats.mu.Lock()
	defer a.subscriptionStats.mu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = a.requireSubscriptionCompletionRawNoticeTx(ctx, tx, receipt.Identity, receipt.Input.Source.Business, result); e != nil {
		return e
	}
	var current subscriptionCompletionReceipt
	if e = a.readQualitySealed(ctx, tx, "subscription_completion", key, &current); e != nil {
		return e
	}
	if current.Identity != receipt.Identity || current.Stage != "notify_pending" || !bytes.Equal(mustCleanSeedJSON(current.Input), mustCleanSeedJSON(receipt.Input)) {
		return completionEvidenceError()
	}
	payload := struct {
		SubscribeID string          `json:"subscribe_id"`
		Subscribe   json.RawMessage `json:"subscribe_info"`
		MediaInfo   json.RawMessage `json:"mediainfo"`
	}{receipt.Identity.SubscriptionID, receipt.Input.Source.Subscribe, receipt.Input.Source.MediaInfo}
	if e = a.writeQualitySealedTx(ctx, tx, "source_subscribe_complete", key, payload); e != nil {
		return e
	}
	eventCtx := context.WithValue(ctx, subscriptionCompletionEventKey{}, &subscriptionCompletionEventGrant{a: a, Identity: receipt.Identity, Business: receipt.Input.Source.Business, Result: result})
	if e = recordDomainEventTx(eventCtx, tx, DomainEvent{SchemaVersion: 1, EventID: domainEventID("subscription.completed", key), Type: "subscription.completed", OccurredAt: now(), WorkflowID: j.WorkflowID, JobID: j.ID, MediaID: sourceSubscriptionMediaKey(receipt.Input.Source.Subscription), SubscriptionID: receipt.Identity.SubscriptionID, Purpose: "subscription"}); e != nil {
		return e
	}
	if e = a.enqueueSubscriptionCompletionStatsTx(ctx, tx, receipt.Identity, receipt.Input.Source.Subscribe, receipt.Input.Source.MediaInfo); e != nil {
		return e
	}
	current.Stage = "complete"
	if e = a.writeQualitySealedTx(ctx, tx, "subscription_completion", key, current); e != nil {
		return e
	}

	if e = tx.Commit(); e != nil {
		return e
	}
	// The source emits only after history/delete and post_message returned.
	// Reuse the actual frozen full source objects, never reconstruct MediaInfo
	// from a compact catalog row or replay this notification from the outbox.
	a.emitStandardBusinessEvent(ctx, "subscribe.complete", payload)
	return nil
}
func sourceSubscriptionMediaKey(raw json.RawMessage) string {
	var row struct {
		Media struct {
			ID   int64  `json:"id"`
			Type string `json:"media_type"`
		} `json:"media"`
	}
	if json.Unmarshal(raw, &row) != nil {
		return ""
	}
	return row.Media.Type + ":" + jsonNumber(row.Media.ID)
}
