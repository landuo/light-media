package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

// The source search waits for its download attempts and completion callback.
// Native jobs preserve that order for this chat action through their own parent
// links. Unrelated scheduled work does not decide the search result.
func (a *App) settleOriginalChatSubscriptionSearch(ctx context.Context, s Subscription, root string) error {
	return a.settleOriginalChatSubscriptionWork(ctx, s, root, false)
}

func (a *App) settleOriginalChatSubscriptionWork(ctx context.Context, s Subscription, root string, rss bool) error {
	downloads, err := a.originalChatSearchChildren(ctx, root, "download", "subscription_download")
	if err != nil {
		return err
	}
	parents := []string{root}
	var result error
	for _, child := range downloads {
		acceptedParents, err := a.settleOriginalChatSearchDownload(ctx, s, root, child, rss)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result = errors.Join(result, err)
		parents = append(parents, acceptedParents...)
	}
	for _, parent := range parents {
		err := a.settleOriginalChatSearchCompletion(ctx, s, root, parent)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result = errors.Join(result, err)
	}
	return result
}

func (a *App) originalChatSearchChildren(ctx context.Context, parent, kind, alternate string) ([]Job, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs
		WHERE kind IN (?,?) AND json_extract(payload,'$._parent_job_id')=? ORDER BY created_at,id`, kind, alternate, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (a *App) awaitOriginalChatSearchChild(ctx context.Context, child Job) (Job, error) {
	state, err := a.awaitOriginalChatSubscriptionJob(ctx, child.ID)
	if err != nil {
		return Job{}, err
	}
	job, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, child.ID))
	if err != nil {
		return Job{}, err
	}
	if job.State != state || job.Kind != child.Kind || job.WorkflowID != child.WorkflowID || !bytes.Equal(job.Payload, child.Payload) {
		return Job{}, bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的后续任务已变化")
	}
	return job, nil
}

func (a *App) settleOriginalChatSearchDownload(ctx context.Context, s Subscription, root string, child Job, rss bool) ([]string, error) {
	job, err := a.awaitOriginalChatSearchChild(ctx, child)
	if err != nil {
		return nil, err
	}
	var payload ordinarySearchDownloadPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.ID == "" || job.ParentJobID != root {
		return nil, bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的下载来源无法核对")
	}
	var in Intent
	if err := a.Store.Get(ctx, "intent", payload.ID, &in); err != nil {
		return nil, err
	}
	if in.ID != payload.ID || in.SubscriptionID != s.ID || in.SubscriptionRevision != s.Revision {
		return nil, bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的下载意图已变化")
	}
	if rss && (!in.LatestResource || (job.Kind == "subscription_download") != s.BestVersion || job.Kind == "download" && payload.Root != root) {
		return nil, bad("SUBSCRIPTION_LATEST_FAILED", "RSS 下载任务与匹配来源不符")
	}
	if job.State == "succeeded" {
		switch in.State {
		case "accepted", "downloading", "downloaded", "imported":
			var raw []byte
			if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("download.accepted", in.ID)).Scan(&raw); err != nil {
				return nil, err
			}
			var event DomainEvent
			if json.Unmarshal(raw, &event) != nil || event.IntentID != in.ID || event.JobID != job.ID || event.WorkflowID != job.WorkflowID {
				return nil, bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的下载接收记录无法核对")
			}
			if _, err := a.downloadAddedSnapshot(ctx, event); err != nil {
				return nil, err
			}
			if job.Kind == "subscription_download" {
				receiptJob, err := a.awaitOriginalChatSearchAcquisitionReceipt(ctx, root, job, in)
				if err != nil {
					return []string{job.ID}, err
				}
				return []string{job.ID, receiptJob.ID}, nil
			}
			return []string{job.ID}, nil
		}
	}
	// Only a recorded no-effect Add result counts as a finished search without
	// a download. An interrupted, uncertain or otherwise reviewable write does not.
	if job.State == "failed" && job.Kind == "download" && job.ErrorCode == "REVIEW_SUBSCRIPTION_DOWNLOAD_REJECTED" && in.State == "failed" {
		confirmed, err := a.confirmedOrdinarySubscriptionDownloadRejection(ctx, job, in)
		if err != nil || confirmed {
			return nil, err
		}
	}
	if job.State == "failed" && job.ErrorCode == "REVIEW_DOWNLOAD_REJECTED" {
		if !rss && job.Kind == "download" && in.State == "planned" {
			// Historical rows combine preflight NotStarted with definitive refusal.
			// Both prove no Add effect, while only refusal merits a cooldown.
			return nil, nil
		}
		if job.Kind == "subscription_download" && in.State == "failed" && in.SubscriptionAcquisition == root {
			batch, err := a.readAcquisition(ctx, a.Store.DB, root)
			if err != nil {
				return nil, err
			}
			if batch.Subscription.ID == s.ID && batch.Subscription.Revision == s.Revision {
				for _, step := range batch.Steps {
					if step.Phase == "rejected" && step.ChildID == job.ID && step.IntentID == in.ID && step.IntentSHA == acquisitionIntentSHA(in) {
						return nil, nil
					}
				}
			}
		}
	}
	return nil, bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的下载结果尚未确认")
}

func (a *App) awaitOriginalChatSearchAcquisitionReceipt(ctx context.Context, root string, child Job, in Intent) (Job, error) {
	// A recovering receipt worker keeps its own trace when queuing completion.
	// The sealed receipt binds that worker to this exact root, child and Add.
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	receipt, err := a.readAcquisitionReceiptTx(ctx, tx, in)
	if err != nil {
		return Job{}, err
	}
	if receipt.Phase != "accepted" || receipt.RootID != root || receipt.ChildID != child.ID || receipt.ChildSHA != tokenHash(string(child.Payload)) {
		return Job{}, acquisitionEvidence()
	}
	receiptJob, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, receipt.JobID))
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	receiptJob, err = a.awaitOriginalChatSearchChild(ctx, receiptJob)
	if err != nil {
		return Job{}, err
	}
	if receiptJob.State != "succeeded" {
		return Job{}, bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的下载回执未处理完毕")
	}
	return receiptJob, nil
}

func (a *App) settleOriginalChatSearchCompletion(ctx context.Context, s Subscription, root, parent string) error {
	preparations, err := a.originalChatSearchChildren(ctx, parent, "subscription_completion_prepare", "subscription_completion_prepare")
	if err != nil {
		return err
	}
	pausedRoot := ""
	if s.State == "paused" {
		pausedRoot = root
	}
	for _, child := range preparations {
		var payload subscriptionCompletionPreparationPayload
		if json.Unmarshal(child.Payload, &payload) != nil || payload.ID != s.ID || payload.Revision != s.Revision || payload.PausedSearchJob != pausedRoot {
			return bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的完成判定来源无法核对")
		}
		job, err := a.awaitOriginalChatSearchChild(ctx, child)
		if err != nil {
			return err
		}
		if job.State != "succeeded" {
			return bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的完成判定未执行完毕")
		}
		completions, err := a.originalChatSearchChildren(ctx, job.ID, "subscription_completion", "subscription_completion")
		if err != nil {
			return err
		}
		for _, completion := range completions {
			if err := a.awaitOriginalChatSearchCompletion(ctx, s, completion); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) awaitOriginalChatSearchCompletion(ctx context.Context, s Subscription, child Job) error {
	job, err := a.awaitOriginalChatSearchChild(ctx, child)
	if err != nil {
		return err
	}
	var payload struct{ ID string }
	if job.State != "succeeded" || json.Unmarshal(job.Payload, &payload) != nil || payload.ID == "" {
		return bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索的完成通知未执行完毕")
	}
	raw, err := a.pluginStore.readSubscriptionCompletionSealed(ctx, a.Store.DB, "subscription_completion", payload.ID)
	if err != nil {
		return err
	}
	var receipt subscriptionCompletionReceipt
	if cleanupJournalStrict(raw, &receipt) != nil || receipt.Stage != "complete" || receipt.Identity.ID != payload.ID || receipt.Identity.JobID != job.ID || receipt.Identity.SubscriptionID != s.ID || receipt.Identity.Revision != s.Revision {
		return completionEvidenceError()
	}
	return nil
}
