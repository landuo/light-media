package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (p *PluginStore) subscriptionAddReceipt(ctx context.Context, q subscriptionCompletionReader, want subscriptionAddIdentity, running bool) (subscriptionAddReceipt, error) {
	var out subscriptionAddReceipt
	if want.ID == "" || want.JobID == "" || !pluginStorageDigest.MatchString(want.InputSHA) {
		return out, subscriptionAddEvidenceError()
	}
	raw, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_add", want.ID)
	if e != nil {
		return out, e
	}
	if cleanupJournalStrict(raw, &out) != nil || out.Version != 1 || out.Identity != want || out.Input.Version != 1 || want.InputSHA != tokenHash(string(mustCleanSeedJSON(out.Input))) || out.Input.Origin.SourceAddID != want.ID || !bytes.Equal(out.Input.Username, out.Input.Origin.Username) {
		return out, subscriptionAddEvidenceError()
	}
	if out.Stage != "admitted" && out.Stage != "saved" && out.Stage != "added" && out.Stage != "statistics_pending" && out.Stage != "complete" {
		return out, subscriptionAddEvidenceError()
	}
	var kind, state string
	var payload []byte
	if e = q.QueryRowContext(ctx, `SELECT kind,state,payload FROM jobs WHERE id=?`, want.JobID).Scan(&kind, &state, &payload); e != nil || kind != "subscription_add" {
		return out, subscriptionAddEvidenceError()
	}
	var ref struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(payload, &ref) != nil || ref.ID != want.ID || (running && state != "running") {
		return out, subscriptionAddEvidenceError()
	}
	if out.Stage != "admitted" && (out.Saved == nil || out.Saved.ID == "" || out.Saved.Revision < 1 || mediaKey(out.Saved.Media) != mediaKey(out.Input.Request.Media)) {
		return out, subscriptionAddEvidenceError()
	}
	if out.Saved != nil {
		if !out.Existing && out.Saved.SourceContextSHA != tokenHash(string(mustCleanSeedJSON(out.Input.Origin))) {
			return out, subscriptionAddEvidenceError()
		}
		if out.Saved.SourceContextSHA != "" {
			origin, e := p.readSubscriptionCompletionSealed(ctx, q, "source_subscription_origin", out.Saved.ID)
			if e != nil || tokenHash(string(origin)) != out.Saved.SourceContextSHA {
				return out, subscriptionAddEvidenceError()
			}
			if !out.Existing && !bytes.Equal(origin, mustCleanSeedJSON(out.Input.Origin)) {
				return out, subscriptionAddEvidenceError()
			}
		}
	}
	if out.Stage == "admitted" && (out.Saved != nil || out.EventID != "" || len(out.NoticeResult) != 0) {
		return out, subscriptionAddEvidenceError()
	}
	if out.Stage == "added" || out.Stage == "statistics_pending" || out.Stage == "complete" {
		if out.EventID != domainEventID("subscription.added", want.ID) || len(out.NoticeResult) == 0 {
			return out, subscriptionAddEvidenceError()
		}
		addedRaw, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_added", out.EventID)
		if e != nil {
			return out, e
		}
		var added subscriptionAddedSnapshot
		if cleanupJournalStrict(addedRaw, &added) != nil || added.SubscriptionID != out.Saved.ID || added.Revision != out.Saved.Revision || mediaKey(added.Media) != mediaKey(out.Saved.Media) || !bytes.Equal(added.Username, out.Input.Username) {
			return out, subscriptionAddEvidenceError()
		}
		var eventRaw []byte
		if e = q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, out.EventID).Scan(&eventRaw); e != nil {
			return out, subscriptionAddEvidenceError()
		}
		var event DomainEvent
		if json.Unmarshal(eventRaw, &event) != nil || event.Type != "subscription.added" || event.EventID != out.EventID || event.SubscriptionID != out.Saved.ID {
			return out, subscriptionAddEvidenceError()
		}
	}
	return out, nil
}
func (a *App) guardUISubscriptionAddMutationTx(ctx context.Context, tx *sql.Tx, origin *sourceSubscriptionOrigin, inputSHA string) error {
	scope, ok := ctx.Value(subscriptionAddExecutionKey{}).(*subscriptionAddExecution)
	if !ok || scope == nil || scope.a != a || origin == nil || origin.SourceAddID != scope.Identity.ID || origin.Version != 1 || origin.Channel != "" || origin.Source != "" || (origin.State != "N" && origin.State != "S") {
		return subscriptionAddEvidenceError()
	}
	r, e := a.pluginStore.subscriptionAddReceipt(ctx, tx, scope.Identity, true)
	if e != nil || r.Stage != "admitted" || !bytes.Equal(mustCleanSeedJSON(r.Input.Origin), mustCleanSeedJSON(origin)) {
		return subscriptionAddEvidenceError()
	}
	request := r.Input.Request
	request.source = &r.Input.Origin
	if inputSHA != tokenHash(string(mustCleanSeedJSON(larkSourceSubscriptionInput(request)))) {
		return subscriptionAddEvidenceError()
	}
	var state string
	var generation int
	if e = tx.QueryRowContext(ctx, `SELECT state,generation FROM jobs WHERE id=?`, scope.Job.ID).Scan(&state, &generation); e != nil || state != "running" || generation != scope.Job.Generation {
		return subscriptionAddEvidenceError()
	}
	return nil
}
func (a *App) commitUISubscriptionSaveTx(ctx context.Context, tx *sql.Tx, inputSHA string, saved Subscription, existing bool) error {
	scope, ok := ctx.Value(subscriptionAddExecutionKey{}).(*subscriptionAddExecution)
	if !ok {
		return nil
	}
	if scope == nil || scope.a != a {
		return subscriptionAddEvidenceError()
	}
	r, e := a.pluginStore.subscriptionAddReceipt(ctx, tx, scope.Identity, true)
	if e != nil {
		return e
	}
	if e = a.guardUISubscriptionAddMutationTx(ctx, tx, &r.Input.Origin, inputSHA); e != nil {
		return e
	}
	r.Existing = existing
	r.Saved = &saved
	r.Stage = "saved"
	return a.writeQualitySealedTx(ctx, tx, "subscription_add", r.Identity.ID, r)
}
func (a *App) admitUISubscriptionAdd(ctx context.Context, input subscriptionAddInput) (subscriptionAddIdentity, Job, error) {
	var identity subscriptionAddIdentity
	identity.ID = id("subscription_add_")
	input.Origin.SourceAddID = identity.ID
	identity.InputSHA = tokenHash(string(mustCleanSeedJSON(input)))
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return identity, Job{}, e
	}
	defer tx.Rollback()
	job, e := enqueueTx(ctx, tx, "subscription_add", map[string]string{"id": identity.ID}, identity.ID)
	if e != nil {
		return identity, job, e
	}
	identity.JobID = job.ID
	r := subscriptionAddReceipt{Version: 1, Identity: identity, Input: input, Stage: "admitted"}
	if e = a.writeQualitySealedTx(ctx, tx, "subscription_add", identity.ID, r); e != nil {
		return identity, job, e
	}
	// Inline source async_add owns this real job first. A cold process recovers
	// it through the same generic worker, not a second independent operation.
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1,lease_until=? WHERE id=? AND state='queued'`, time.Now().Add(2*time.Minute).Unix(), job.ID); e != nil {
		return identity, job, e
	}
	job.State, job.Generation = "running", 1
	return identity, job, tx.Commit()
}
func (a *App) runUISubscriptionAdd(ctx context.Context, j Job, key string) error {
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	var seed subscriptionAddReceipt
	if e := a.readQualitySealed(ctx, a.Store.DB, "subscription_add", key, &seed); e != nil {
		return e
	}
	if seed.Identity.ID != key || seed.Identity.JobID != j.ID {
		return subscriptionAddEvidenceError()
	}
	r, e := a.pluginStore.subscriptionAddReceipt(ctx, a.Store.DB, seed.Identity, true)
	if e != nil {
		return e
	}
	if r.Stage == "complete" {
		return nil
	}
	scope := &subscriptionAddExecution{a: a, Identity: r.Identity, Job: j}
	ctx = context.WithValue(ctx, subscriptionAddExecutionKey{}, scope)
	if r.Stage == "admitted" {
		request := r.Input.Request
		request.source = &r.Input.Origin
		_, cause := a.saveSubscription(ctx, "", request)
		// Saving and its exact source receipt share one TX. Even a lost Commit ACK
		// can only consume the already saved result; it cannot repeat creation.
		r, e = a.pluginStore.subscriptionAddReceipt(ctx, a.Store.DB, r.Identity, true)
		if e != nil {
			return e
		}
		if r.Stage == "admitted" {
			if cause != nil {
				return cause
			}
			return subscriptionAddEvidenceError()
		}
	}
	if r.Stage == "saved" {
		result, e := a.postSubscriptionAddNotice(ctx, r.Identity, r.Input.Business)
		if e != nil {
			return e
		}
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = a.requireSubscriptionAddNoticeTx(ctx, tx, r.Identity, r.Input.Business, result); e != nil {
			return e
		}
		current, e := a.pluginStore.subscriptionAddReceipt(ctx, tx, r.Identity, true)
		if e != nil || current.Stage != "saved" {
			return subscriptionAddEvidenceError()
		}
		event := DomainEvent{EventID: domainEventID("subscription.added", r.Identity.ID), Type: "subscription.added", MediaID: mediaKey(r.Saved.Media), SubscriptionID: r.Saved.ID, Purpose: "subscription", OccurredAt: now()}
		snapshot := subscriptionAddedSnapshot{SubscriptionID: r.Saved.ID, Revision: r.Saved.Revision, Media: r.Input.Request.Media, Username: r.Input.Username}
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_added", event.EventID, snapshot); e != nil {
			return e
		}
		if e = recordDomainEventTx(ctx, tx, event); e != nil {
			return e
		}
		current.EventID, current.NoticeResult, current.Stage = event.EventID, result, "added"
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_add", key, current); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		r = current
	}
	if r.Stage == "added" || r.Stage == "statistics_pending" {
		return a.awaitUISubscriptionAddStatistics(ctx, j, r)
	}
	return subscriptionAddEvidenceError()
}

func (a *App) createUISubscription(ctx context.Context, in SubscriptionRequest, username string) (Subscription, error) {
	ctx, end, e := a.beginSubscriptionStatistics(ctx)
	if e != nil {
		return Subscription{}, e
	}
	defer end()
	input, e := a.buildUISubscriptionAdd(ctx, in, username)
	if e != nil {
		return Subscription{}, e
	}
	identity, job, e := a.admitUISubscriptionAdd(ctx, input)
	if e != nil {
		return Subscription{}, e
	}
	cause := a.runUISubscriptionAdd(ctx, job, identity.ID)
	state, code := "succeeded", ""
	if cause != nil {
		state, code = "retry_wait", pluginErrorCodeInternal(cause)
		if code == "PLUGIN_ADAPTER_UNAVAILABLE" {
			state = "failed"
		}
	}
	_, finishErr := a.finishJob(context.WithoutCancel(ctx), job, state, code, "网页新增订阅原始业务流程", time.Now().Add(time.Minute).Unix())
	var r subscriptionAddReceipt
	e = a.readQualitySealed(context.WithoutCancel(ctx), a.Store.DB, "subscription_add", identity.ID, &r)
	if cause != nil {
		return Subscription{}, cause
	}
	if finishErr != nil {
		return Subscription{}, finishErr
	}
	if e != nil {
		return Subscription{}, e
	}
	if r.Stage != "complete" || r.Saved == nil {
		return Subscription{}, subscriptionAddEvidenceError()
	}
	return *r.Saved, nil
}
