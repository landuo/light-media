package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
)

// A replacement parent lease can recover committed receipts, never infer a
// downloader outcome from List. A pending Add/selection with no normally
// returned receipt keeps its original generation and requires review.
func (a *App) advanceAcquisitionGenerationTx(ctx context.Context, tx *sql.Tx, j Job, r subscriptionAcquisition) (subscriptionAcquisition, error) {
	actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, r.ID))
	if e != nil {
		return r, e
	}
	if actual.ID != j.ID || actual.Kind != "subscription" || actual.State != "running" || actual.Generation != j.Generation || actual.Generation < r.Generation || tokenHash(string(actual.Payload)) != r.JobSHA || actual.WorkflowID != r.JobWorkflowID || !slices.Equal(actual.Payload, j.Payload) {
		return r, acquisitionEvidence()
	}
	if actual.Generation == r.Generation {
		return a.acquisitionScopeTx(ctx, tx, &r)
	}
	for _, step := range r.Steps {
		switch step.Phase {
		case "intent", "preview_rejected":
			if step.IntentID != "" || step.ChildID != "" {
				return r, acquisitionEvidence()
			}
		case "confirmed", "accepted", "rejected":
			var in Intent
			if e = qualityRecord(ctx, tx, "intent", step.IntentID, &in); e != nil {
				return r, e
			}
			child, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, step.ChildID))
			if e != nil {
				return r, e
			}
			if acquisitionIntentSHA(in) != step.IntentSHA || in.SubscriptionAcquisition != r.ID || child.Kind != "subscription_download" || child.ParentJobID != r.ID || child.WorkflowID != in.WorkflowID {
				return r, acquisitionEvidence()
			}
			switch step.Phase {
			case "accepted":
				if child.State != "succeeded" {
					return r, acquisitionEvidence()
				}
				if e = a.guardAcceptedDownloadProgressTx(ctx, tx, in); e != nil {
					return r, e
				}
			case "rejected":
				if in.State != "failed" || child.State != "failed" {
					return r, acquisitionEvidence()
				}
			case "confirmed":
				if in.State != "post_add" {
					return r, bad("REVIEW_SUBSCRIPTION_ACQUISITION", "下载器结果尚未确认，新执行代次未重试原操作")
				}
				post, e := a.sourcePostAddReceipt(ctx, tx, in)
				if e != nil || !post.SourceReturned || post.SourceError != "" {
					return r, bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后原操作尚未正常返回，新执行代次未重试")
				}
				if child.State != "running" && child.State != "queued" && child.State != "retry_wait" {
					return r, acquisitionEvidence()
				}
			}
		default:
			return r, acquisitionEvidence()
		}
	}
	r.Generation = actual.Generation
	if e = a.saveAcquisitionTx(ctx, tx, r); e != nil {
		return r, e
	}
	return a.acquisitionScopeTx(ctx, tx, &r)
}

// Receipt recovery precedes metadata/site queries. An already acknowledged Add
// does not become unknown merely because a tracker or metadata service is down
// after a process restart; no remote operation occurs in this continuation.
func (a *App) resumeKnownSubscriptionAcquisition(ctx context.Context, j Job) (bool, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	r, e := a.readAcquisition(ctx, tx, j.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	r, e = a.advanceAcquisitionGenerationTx(ctx, tx, j, r)
	if e != nil {
		return false, e
	}
	if r.Completed || acquisitionRootSatisfied(r) {
		for _, step := range r.Steps {
			if step.Phase != "accepted" {
				continue
			}
			var in Intent
			if e = qualityRecord(ctx, tx, "intent", step.IntentID, &in); e != nil {
				return false, e
			}
			if acquisitionIntentSHA(in) != step.IntentSHA {
				return false, acquisitionEvidence()
			}
			if e = a.guardAcceptedDownloadProgressTx(ctx, tx, in); e != nil {
				return false, e
			}
		}
		if !r.Completed {
			r.Completed = true
			if e = a.saveAcquisitionTx(ctx, tx, r); e != nil {
				return false, e
			}
		}
		if e = tx.Commit(); e != nil {
			return false, e
		}
		return true, nil
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	ctx, e = withResourceRecognition(ctx, r.Options)
	if e != nil {
		return false, e
	}
	ctx = context.WithValue(ctx, jobTraceKey{}, jobTrace{Job: r.ID})
	resumed := false
	for ordinal, step := range r.Steps {
		if step.Phase != "confirmed" {
			continue
		}
		var in Intent
		if e = a.Store.Get(ctx, "intent", step.IntentID, &in); e != nil {
			return resumed, e
		}
		if in.State != "post_add" {
			continue
		}
		// submitAcquisitionChild verifies the same sealed post-Add receipt inside
		// its transaction and only then runs runDownload's SQL acceptance branch.
		stepCtx := context.WithValue(ctx, subscriptionAcquisitionKey{}, &subscriptionAcquisitionGrant{a, &r, ordinal})
		if _, e = a.submitAcquisitionChild(stepCtx, in); e != nil {
			return resumed, e
		}
		resumed = true
	}
	return resumed, nil
}
