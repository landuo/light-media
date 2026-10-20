package app

import (
	"bytes"
	"context"
	"time"
)

func (a *App) validateUISubscriptionStatisticsSource(ctx context.Context, q pluginRemoveLinkJournalReader, r subscriptionStatsReceipt) error {
	in := r.Input
	if in.Mode != "await_async" || in.Operation != "add" || in.Added == nil || in.Lark != nil || in.Completion != nil || len(in.MediaInfo) != 0 || len(in.Subscribe) != 0 {
		return subscriptionStatsEvidenceError()
	}
	source, e := a.pluginStore.subscriptionAddReceipt(ctx, q, *in.Added, false)
	if e != nil || (source.Stage != "statistics_pending" && source.Stage != "complete") || source.StatisticsID != r.ID || !bytes.Equal(source.Input.Registration, in.Registration) {
		return subscriptionStatsEvidenceError()
	}
	var proof pluginNoticeV2Result
	if cleanupJournalStrict(source.NoticeResult, &proof) != nil || !proof.SourceReturned || proof.SourceError != "" {
		return subscriptionStatsEvidenceError()
	}
	// Read-only authentication does not require the parent to remain running.
	// The already admitted HTTP child may finish after the API disconnects.
	route, _, e := a.pluginStore.readNoticeRoute(ctx, q, proof.RouteID)
	if e != nil || !route.Complete || route.SourceError != "" || route.BusinessV2 == nil || route.BusinessV2.Binding.Added == nil || *route.BusinessV2.Binding.Added != source.Identity || route.Count != proof.Count || route.LastSHA != proof.LastSHA || route.BusinessV2.EnvelopeSHA != proof.EnvelopeSHA || route.BusinessV2.BusinessSHA != proof.BusinessSHA {
		return subscriptionStatsEvidenceError()
	}
	return nil
}
func (a *App) awaitUISubscriptionAddStatistics(ctx context.Context, j Job, r subscriptionAddReceipt) error {
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	if r.Stage == "added" {
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		current, e := a.pluginStore.subscriptionAddReceipt(ctx, tx, r.Identity, true)
		if e != nil {
			return e
		}
		if current.Stage != "added" {
			return subscriptionAddEvidenceError()
		}
		key, e := a.enqueueSubscriptionStatsTx(ctx, tx, subscriptionStatsInput{Operation: "add", Mode: "await_async", Added: &r.Identity, Registration: append([]byte(nil), r.Input.Registration...)}, j.WorkflowID)
		if e != nil {
			return e
		}
		current.Stage, current.StatisticsID = "statistics_pending", key
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_add", r.Identity.ID, current); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		r = current
	}
	if r.Stage != "statistics_pending" || r.StatisticsID == "" {
		return subscriptionAddEvidenceError()
	}
	var child subscriptionStatsReceipt
	if e := a.readQualitySealed(ctx, a.Store.DB, "subscription_stats", r.StatisticsID, &child); e != nil {
		return e
	}
	if child.Input.Added == nil || *child.Input.Added != r.Identity {
		return subscriptionStatsEvidenceError()
	}
	// The source awaits its helper. Claim this real admitted child, or await the
	// generic worker which claimed it first. There is still one durable intent.
	if child.Stage == "accepted" {
		result, e := a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='running',generation=generation+1,lease_until=?,updated_at=? WHERE id=? AND kind='subscription_stats' AND state IN ('queued','retry_wait')`, time.Now().Add(2*time.Minute).Unix(), now(), child.JobID)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if count == 1 {
			job, e := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, child.JobID))
			if e != nil {
				return e
			}
			cause := a.safeRun(ctx, job)
			state, code := "succeeded", ""
			if cause != nil {
				state, code = "retry_wait", pluginErrorCodeInternal(cause)
			}
			_, e = a.finishJob(context.WithoutCancel(ctx), job, state, code, "网页新增原异步统计请求", time.Now().Add(time.Minute).Unix())
			if e != nil {
				return e
			}
		}
	}
	for {
		if e := a.readQualitySealed(ctx, a.Store.DB, "subscription_stats", r.StatisticsID, &child); e != nil {
			return e
		}
		switch child.Stage {
		case "complete":
			if !child.Result.SourceReturned || child.Result.SourceError != "" {
				return &APIError{Code: "SUBSCRIPTION_STATISTICS_SOURCE_ERROR", Message: "原异步统计调用异常：" + child.Result.SourceError, Status: 500}
			}
			tx, e := a.Store.DB.BeginTx(ctx, nil)
			if e != nil {
				return e
			}
			defer tx.Rollback()
			current, e := a.pluginStore.subscriptionAddReceipt(ctx, tx, r.Identity, true)
			if e != nil {
				return e
			}
			if current.Stage != "statistics_pending" || current.StatisticsID != r.StatisticsID {
				return subscriptionAddEvidenceError()
			}
			var verified subscriptionStatsReceipt
			if e = a.readQualitySealed(ctx, tx, "subscription_stats", r.StatisticsID, &verified); e != nil || verified.Stage != "complete" || !bytes.Equal(mustCleanSeedJSON(verified), mustCleanSeedJSON(child)) {
				return subscriptionStatsEvidenceError()
			}
			if e = a.validateUISubscriptionStatisticsSource(ctx, tx, verified); e != nil {
				return e
			}
			current.Stage = "complete"
			if e = a.writeQualitySealedTx(ctx, tx, "subscription_add", r.Identity.ID, current); e != nil {
				return e
			}
			return tx.Commit()
		case "dependency":
			return bad("SUBSCRIPTION_ASYNC_STATS_DEPENDENCY", "网页新增通知与事件已保存，原异步统计依赖尚未就绪")
		case "unknown":
			return subscriptionStatsEvidenceError()
		case "accepted", "intent":
			var state string
			if e := a.Store.DB.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id=?`, child.JobID).Scan(&state); e != nil {
				return e
			}
			if state != "running" {
				return subscriptionStatsEvidenceError()
			}
		default:
			return subscriptionStatsEvidenceError()
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
