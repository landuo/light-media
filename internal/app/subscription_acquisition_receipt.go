package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
)

// Receipt jobs only settle a normally returned SourcePostAdd result. They have
// no downloader capability and never re-check mutable subscription policy to
// decide whether the already-issued external operation actually happened.
type subscriptionAcquisitionReceipt struct {
	Version                                                                                            int
	IntentID, IntentSHA, RootID, StepSHA, ChildID, ChildSHA, JobID, JobSHA, WorkflowID, PostSHA, Phase string
	ParentState, ChildState, Disposition, CompletedAt                                                  string
	ParentGeneration                                                                                   int
}
type acquisitionReceiptExecutionKey struct{}
type acquisitionReceiptExecution struct {
	a       *App
	job     Job
	receipt subscriptionAcquisitionReceipt
}

func acquisitionRootSatisfied(r subscriptionAcquisition) bool {
	state := subscriptionQualityState(r.Subscription)
	wanted := []int{}
	if r.Subscription.Media.MediaType == "tv" {
		if state.Full() {
			wanted = state.Targets()
		} else {
			for _, ep := range state.Pending() {
				if slices.Contains(r.Request.Episodes, ep) {
					wanted = append(wanted, ep)
				}
			}
		}
	}
	accepted := false
	covered := map[int]bool{}
	for _, step := range r.Steps {
		if step.Phase != "accepted" {
			continue
		}
		accepted = true
		episodes := step.Input.Selected
		if len(episodes) == 0 {
			episodes = step.Input.Episodes
		}
		for _, ep := range episodes {
			covered[ep] = true
		}
	}
	if !accepted {
		return false
	}
	if r.Subscription.Media.MediaType == "movie" {
		return true
	}
	if len(wanted) == 0 {
		return false
	}
	for _, ep := range wanted {
		if !covered[ep] {
			return false
		}
	}
	return true
}
func (a *App) acquisitionReceiptInputTx(ctx context.Context, tx *sql.Tx, in Intent) (subscriptionAcquisition, int, Job, error) {
	r, e := a.readAcquisition(ctx, tx, in.SubscriptionAcquisition)
	if errors.Is(e, sql.ErrNoRows) {
		return r, 0, Job{}, acquisitionEvidence()
	} else if e != nil {
		return r, 0, Job{}, e
	}
	parent, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, r.ID))
	if e != nil {
		return r, 0, Job{}, e
	}
	if parent.Kind != "subscription" || tokenHash(string(parent.Payload)) != r.JobSHA || parent.WorkflowID != r.JobWorkflowID {
		return r, 0, Job{}, acquisitionEvidence()
	}
	for i, step := range r.Steps {
		if step.IntentID != in.ID {
			continue
		}
		if step.Phase != "confirmed" && step.Phase != "accepted" || step.IntentSHA != acquisitionIntentSHA(in) || r.Subscription.ID != in.SubscriptionID || r.Subscription.Revision != in.SubscriptionRevision {
			return r, 0, Job{}, acquisitionEvidence()
		}
		child, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, step.ChildID))
		if e != nil {
			return r, 0, Job{}, e
		}
		var payload struct {
			ID string `json:"id"`
		}
		if child.Kind != "subscription_download" || child.ParentJobID != r.ID || child.WorkflowID != in.WorkflowID || json.Unmarshal(child.Payload, &payload) != nil || payload.ID != in.ID {
			return r, 0, Job{}, acquisitionEvidence()
		}
		return r, i, child, nil
	}
	return r, 0, Job{}, acquisitionEvidence()
}
func (a *App) readAcquisitionReceiptTx(ctx context.Context, tx *sql.Tx, in Intent) (subscriptionAcquisitionReceipt, error) {
	var out subscriptionAcquisitionReceipt
	if e := a.readQualitySealed(ctx, tx, "subscription_acquisition_receipt", in.ID, &out); e != nil {
		return out, e
	}
	r, ordinal, child, e := a.acquisitionReceiptInputTx(ctx, tx, in)
	if e != nil {
		return out, e
	}
	post, e := a.sourcePostAddReceipt(ctx, tx, in)
	if e != nil {
		return out, e
	}
	if !post.SourceReturned || post.SourceError != "" || post.Mode == "existing_hash" || out.Version != 1 || out.IntentID != in.ID || out.IntentSHA != acquisitionIntentSHA(in) || out.RootID != r.ID || out.StepSHA != r.Steps[ordinal].InputSHA || out.ChildID != child.ID || out.ChildSHA != tokenHash(string(child.Payload)) || out.WorkflowID != in.WorkflowID || out.PostSHA != tokenHash(string(mustCleanSeedJSON(post))) || out.Phase != "prepared" && out.Phase != "accepted" {
		return out, acquisitionEvidence()
	}
	if out.Phase == "accepted" && r.Steps[ordinal].Phase != "accepted" {
		return out, acquisitionEvidence()
	}
	job, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, out.JobID))
	if e != nil {
		return out, e
	}
	var payload struct {
		ID       string `json:"id"`
		Root     string `json:"root"`
		InputSHA string `json:"input_sha"`
		PostSHA  string `json:"post_sha"`
	}
	if job.Kind != "subscription_acquisition_receipt" || job.WorkflowID != out.WorkflowID || job.ParentJobID != child.ID || tokenHash(string(job.Payload)) != out.JobSHA || json.Unmarshal(job.Payload, &payload) != nil || payload.ID != in.ID || payload.Root != r.ID || payload.InputSHA != out.StepSHA || payload.PostSHA != out.PostSHA {
		return out, acquisitionEvidence()
	}
	return out, nil
}

// Called in the exact transaction that first seals the actual one-use
// SourcePostAdd return. Without that receipt, no recovery job can be admitted.
func (a *App) preserveAcquisitionReceiptTx(ctx context.Context, tx *sql.Tx, in Intent, post sourcePostAddReceipt) error {
	if in.SubscriptionAcquisition == "" || !post.SourceReturned || post.SourceError != "" {
		return nil
	}
	if _, e := tx.ExecContext(ctx, `SAVEPOINT subscription_receipt_enqueue`); e != nil {
		return e
	}
	e := a.enqueueAcquisitionReceiptTx(ctx, tx, in, post)
	if e != nil {
		if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO subscription_receipt_enqueue`); rollbackErr != nil {
			return rollbackErr
		}
		if _, releaseErr := tx.ExecContext(ctx, `RELEASE subscription_receipt_enqueue`); releaseErr != nil {
			return releaseErr
		}
		// Preserve the one-use SourcePostAdd return even if its local scope record
		// was removed/corrupted. No recovery job or accepted fact is fabricated.
		return a.writeQualitySealedTx(ctx, tx, "subscription_acquisition_receipt_error", in.ID, struct{ Intent, Root, PostSHA, Code, Message string }{in.ID, in.SubscriptionAcquisition, tokenHash(string(mustCleanSeedJSON(post))), pluginErrorCodeInternal(e), a.clean(e.Error())})
	}
	_, e = tx.ExecContext(ctx, `RELEASE subscription_receipt_enqueue`)
	return e
}
func (a *App) enqueueAcquisitionReceiptTx(ctx context.Context, tx *sql.Tx, in Intent, post sourcePostAddReceipt) error {
	if in.SubscriptionAcquisition == "" {
		return nil
	}
	if !sourcePostAddReceiptValid(post, in) || !post.SourceReturned || post.SourceError != "" || post.Mode == "existing_hash" {
		return nil
	}
	r, ordinal, child, e := a.acquisitionReceiptInputTx(ctx, tx, in)
	if e != nil {
		return e
	}
	g, ok := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant)
	if !ok || g == nil || g.a != a || g.root == nil || g.root.ID != r.ID || g.ordinal != ordinal || g.ordinal >= len(g.root.Steps) || g.root.Steps[ordinal].InputSHA != r.Steps[ordinal].InputSHA {
		return acquisitionEvidence()
	}
	old, e := a.readAcquisitionReceiptTx(ctx, tx, in)
	if e == nil {
		if old.PostSHA != tokenHash(string(mustCleanSeedJSON(post))) {
			return acquisitionEvidence()
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	out := subscriptionAcquisitionReceipt{Version: 1, IntentID: in.ID, IntentSHA: acquisitionIntentSHA(in), RootID: r.ID, StepSHA: r.Steps[ordinal].InputSHA, ChildID: child.ID, ChildSHA: tokenHash(string(child.Payload)), WorkflowID: in.WorkflowID, PostSHA: tokenHash(string(mustCleanSeedJSON(post))), Phase: "prepared"}
	jobCtx := context.WithValue(ctx, jobTraceKey{}, jobTrace{Job: child.ID})
	job, e := enqueueTx(jobCtx, tx, "subscription_acquisition_receipt", map[string]string{"id": in.ID, "root": r.ID, "input_sha": out.StepSHA, "post_sha": out.PostSHA}, in.WorkflowID)
	if e != nil {
		return e
	}
	out.JobID, out.JobSHA = job.ID, tokenHash(string(job.Payload))
	return a.writeQualitySealedTx(ctx, tx, "subscription_acquisition_receipt", in.ID, out)
}
func (a *App) acceptAcquisitionReceiptTx(ctx context.Context, tx *sql.Tx, in Intent, j Job) error {
	r, ordinal, child, e := a.acquisitionReceiptInputTx(ctx, tx, in)
	if e != nil {
		return e
	}
	if child.ID != j.ID || tokenHash(string(j.Payload)) != tokenHash(string(child.Payload)) {
		return acquisitionEvidence()
	}
	receipt, e := a.readAcquisitionReceiptTx(ctx, tx, in)
	if e != nil {
		return e
	}
	post, ok := ctx.Value(sourcePostAddKey{}).(sourcePostAddReceipt)
	if !ok || !sourcePostAddReceiptValid(post, in) || !post.SourceReturned || post.SourceError != "" || tokenHash(string(mustCleanSeedJSON(post))) != receipt.PostSHA {
		return acquisitionEvidence()
	}
	grant, gok := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant)
	if gok && grant != nil && grant.a == a && grant.root != nil && grant.root.ID == r.ID && grant.ordinal == ordinal && ordinal < len(grant.root.Steps) && grant.root.Steps[ordinal].InputSHA == receipt.StepSHA {
		// The original producer may settle its irreversible, normally returned
		// receipt even when the parent was cancelled after the external action.
	} else {
		execution, xok := ctx.Value(acquisitionReceiptExecutionKey{}).(*acquisitionReceiptExecution)
		if !xok || execution == nil || execution.a != a || execution.receipt.JobID != receipt.JobID || execution.receipt.JobSHA != receipt.JobSHA {
			return acquisitionEvidence()
		}
		actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, receipt.JobID))
		if e != nil {
			return e
		}
		if actual.Kind != "subscription_acquisition_receipt" || actual.State != "running" || actual.Generation != execution.job.Generation || tokenHash(string(actual.Payload)) != receipt.JobSHA {
			return acquisitionEvidence()
		}
	}
	if e = a.guardAcceptedDownloadProgressTx(ctx, tx, in); e != nil {
		return e
	}
	if receipt.Phase == "accepted" {
		if r.Steps[ordinal].Phase != "accepted" {
			return acquisitionEvidence()
		}
		return nil
	}
	parent, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, r.ID))
	if e != nil {
		return e
	}
	var fact subscriptionQualityAccepted
	if e = a.readQualitySealed(ctx, tx, "subscription_quality_accepted", in.ID, &fact); e != nil {
		return e
	}
	receipt.Phase, receipt.ParentState, receipt.ParentGeneration, receipt.ChildState, receipt.Disposition, receipt.CompletedAt = "accepted", parent.State, parent.Generation, child.State, fact.Disposition, now()
	r.Steps[ordinal].Phase = "accepted"
	r.Completed = acquisitionRootSatisfied(r)
	if e = a.saveAcquisitionTx(ctx, tx, r); e != nil {
		return e
	}
	if e = a.writeQualitySealedTx(ctx, tx, "subscription_acquisition_receipt", in.ID, receipt); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='succeeded',lease_until=0,updated_at=? WHERE id=?`, now(), child.ID); e != nil {
		return e
	}
	// The primary producer just did the receipt job's actual local work in this
	// transaction. A concurrent claimed receipt worker finishes its own lease.
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='succeeded',lease_until=0,updated_at=? WHERE id=? AND state IN ('queued','retry_wait','waiting_paused')`, now(), receipt.JobID); e != nil {
		return e
	}
	if gok && grant != nil && grant.root != nil {
		*grant.root = r
	}
	return nil
}
func (a *App) runAcquisitionReceipt(ctx context.Context, j Job, key string) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		return e
	}
	if actual.Kind != "subscription_acquisition_receipt" || actual.State != "running" || actual.Generation != j.Generation {
		return acquisitionEvidence()
	}
	var in Intent
	if e = qualityRecord(ctx, tx, "intent", key, &in); e != nil {
		return e
	}
	receipt, e := a.readAcquisitionReceiptTx(ctx, tx, in)
	if e != nil {
		return e
	}
	if receipt.JobID != j.ID || receipt.JobSHA != tokenHash(string(j.Payload)) {
		return acquisitionEvidence()
	}
	if receipt.Phase == "accepted" {
		return a.guardAcceptedDownloadProgressTx(ctx, tx, in)
	}
	if in.State != "post_add" {
		return acquisitionEvidence()
	}
	post, e := a.sourcePostAddReceipt(ctx, tx, in)
	if e != nil {
		return e
	}
	_, _, child, e := a.acquisitionReceiptInputTx(ctx, tx, in)
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	in.State, in.Error, in.UpdatedAt = "accepted", "", now()
	ctx = sourcePostAddContext(ctx, post)
	ctx = context.WithValue(ctx, acquisitionReceiptExecutionKey{}, &acquisitionReceiptExecution{a, j, receipt})
	return a.acceptDownload(ctx, in, child)
}
