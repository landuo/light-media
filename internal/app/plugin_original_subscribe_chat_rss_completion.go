package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Called after the acquisition receipt validates the intent's exact parent.
// A recovered receipt still belongs to the root while that root can resume.
func (a *App) deferOriginalChatRSSQualityCompletionTx(ctx context.Context, tx *sql.Tx, in Intent, s Subscription) (bool, error) {
	if in.SubscriptionAcquisition == "" || !in.LatestResource || !s.BestVersion || s.Revision != in.SubscriptionRevision || !originalChatLatestActive(s) {
		return false, nil
	}
	parent, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, in.SubscriptionAcquisition))
	if err != nil {
		return false, err
	}
	switch parent.State {
	case "queued", "running", "retry_wait", "waiting_paused":
		return a.originalChatRSSOwnsSearch(ctx, tx, parent, s)
	default:
		return false, nil
	}
}

// The worker commits root success and any preparation in one transaction.
// RSS matching re-reads the subscription after the batch before finishing it.
func (a *App) finishOriginalChatRSSQualityRootTx(ctx context.Context, tx *sql.Tx, j Job) error {
	var input originalChatLatestJob
	if json.Unmarshal(j.Payload, &input) != nil || !input.RSSFallback {
		return nil
	}
	var current Subscription
	if err := qualityRecord(ctx, tx, "subscription", input.ID, &current); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if current.Revision != input.Revision || !originalChatLatestActive(current) || current.SourceState == "P" || !current.BestVersion || !subscriptionQualityState(current).Complete() {
		return nil
	}
	if owns, err := a.originalChatRSSOwnsSearch(ctx, tx, j, current); err != nil || !owns {
		return err
	}
	ctx = context.WithValue(ctx, jobTraceKey{}, jobTrace{Job: j.ID, Request: j.RequestID})
	return a.enqueueQualityCompletionForTx(ctx, tx, current, j.WorkflowID)
}
