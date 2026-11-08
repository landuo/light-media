package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

type ordinarySearchDownloadKey struct{}

type ordinarySearchDownloadScope struct {
	app    *App
	parent Job
	child  Job
	intent string
}

type ordinarySearchDownloadPayload struct {
	ID   string `json:"id"`
	Root string `json:"_ordinary_search_attempt"`
}

func ordinarySearchDownloadReview() error {
	return bad("REVIEW_ORDINARY_SEARCH_DOWNLOAD", "订阅搜索的下载结果无法确认，已保留原任务供核对")
}

func (a *App) ordinarySearchParentTx(ctx context.Context, tx *sql.Tx, parent Job, owner Subscription) (bool, error) {
	actual, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, parent.ID))
	if err != nil {
		return false, err
	}
	var payload originalChatPausedSearchPayload
	if actual.Kind != "subscription" || actual.State != "running" || actual.Generation != parent.Generation || actual.WorkflowID != parent.WorkflowID ||
		!bytes.Equal(actual.Payload, parent.Payload) || json.Unmarshal(actual.Payload, &payload) != nil || payload.ID != owner.ID || payload.Revision == nil || *payload.Revision != owner.Revision ||
		owner.BestVersion || !a.subscriptionSearchState(ctx, owner) {
		return false, ordinarySearchDownloadReview()
	}
	if originalChatManualSubscriptionJob(actual) {
		return false, nil
	}
	rss, err := a.originalChatRSSOwnsSearch(ctx, tx, actual, owner)
	if err != nil {
		return false, err
	}
	if !rss {
		return false, ordinarySearchDownloadReview()
	}
	return true, nil
}

// A private refresh child uses the same immutable batch row that created its
// preview. Its own encrypted snapshot cannot grant access to another batch.
func (a *App) guardOrdinarySearchRSSIntent(ctx context.Context, parent Job, owner Subscription, in Intent, source *downloadContextSnapshot) error {
	batch := originalChatLatestFromContext(ctx)
	var root originalChatLatestJob
	if batch == nil || batch.Mode != "rss" || json.Unmarshal(parent.Payload, &root) != nil || batch.id != root.LatestBatch ||
		source == nil || source.LatestResource == nil || !sameHash(source.Hash, in.Hash) ||
		!((source.DownloadKind == "intent" && source.DownloadID == in.ID) || (source.DownloadKind == "download_preview" && source.DownloadID == in.PreviewID)) {
		return ordinarySearchDownloadReview()
	}
	if err := verifyRSSMagnetContext(in, source); err != nil {
		return err
	}
	site, err := a.originalChatLatestIntentSite(owner, in)
	if err != nil {
		return err
	}
	proof, err := a.originalChatLatestSelect(batch, site, in.ResourceID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(proof, source.LatestResource) {
		return ordinarySearchDownloadReview()
	}
	return nil
}

func (a *App) ordinarySearchChildMarker(ctx context.Context, tx *sql.Tx, owner Subscription, intent Intent, source *downloadContextSnapshot) (string, error) {
	scope, _ := ctx.Value(ordinarySearchDownloadKey{}).(ordinarySearchDownloadScope)
	if scope.app == nil {
		return "", nil
	}
	trace, _ := ctx.Value(jobTraceKey{}).(jobTrace)
	if scope.app != a || scope.intent != "" || trace.Job != scope.parent.ID || !intent.Auto || intent.SubscriptionAcquisition != "" || intent.SubscriptionID != owner.ID || intent.SubscriptionRevision != owner.Revision || intent.ChatDownload {
		return "", ordinarySearchDownloadReview()
	}
	rss, err := a.ordinarySearchParentTx(ctx, tx, scope.parent, owner)
	if err != nil {
		return "", err
	}
	if intent.LatestResource != rss {
		return "", ordinarySearchDownloadReview()
	}
	if rss {
		if err := a.guardOrdinarySearchRSSIntent(ctx, scope.parent, owner, intent, source); err != nil {
			return "", err
		}
	}
	return scope.parent.ID, nil
}

func (a *App) confirmOrdinarySearchDownload(ctx context.Context, parent Job, preview string) (Intent, error) {
	ctx = context.WithValue(ctx, jobTraceKey{}, jobTrace{Job: parent.ID, Request: parent.RequestID})
	ctx = context.WithValue(ctx, ordinarySearchDownloadKey{}, ordinarySearchDownloadScope{app: a, parent: parent})
	return a.confirmDownload(ctx, preview, false)
}

func (a *App) guardOrdinarySearchDownload(ctx context.Context, child Job, in Intent) error {
	var payload ordinarySearchDownloadPayload
	if json.Unmarshal(child.Payload, &payload) != nil || payload.Root == "" {
		return nil
	}
	scope, _ := ctx.Value(ordinarySearchDownloadKey{}).(ordinarySearchDownloadScope)
	if scope.app != a || scope.intent != in.ID || payload.ID != in.ID || scope.child.ID != child.ID || scope.child.Generation != child.Generation || scope.parent.ID != payload.Root || child.ParentJobID != payload.Root {
		return ordinarySearchDownloadReview()
	}
	if in.LatestResource {
		if err := a.jobCancelled(ctx, scope.parent); err != nil {
			return err
		}
		return a.jobCancelled(ctx, child)
	}
	return nil
}

func (a *App) deferOrdinarySearchDownloadCompletion(ctx context.Context, in Intent) bool {
	scope, _ := ctx.Value(ordinarySearchDownloadKey{}).(ordinarySearchDownloadScope)
	return scope.app == a && scope.intent == in.ID && scope.child.ID != ""
}

func (a *App) guardOrdinarySearchDownloadWrite(ctx context.Context, child Job, in Intent) error {
	if !a.deferOrdinarySearchDownloadCompletion(ctx, in) {
		return nil
	}
	scope := ctx.Value(ordinarySearchDownloadKey{}).(ordinarySearchDownloadScope)
	if in.LatestResource {
		source, err := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
		if err != nil {
			return err
		}
		if source == nil || source.LatestResource == nil {
			return ordinarySearchDownloadReview()
		}
		if _, err := a.originalChatLatestIntentResource(ctx, in, source.LatestResource); err != nil {
			return err
		}
	}
	if err := a.jobCancelled(ctx, scope.parent); err != nil {
		return err
	}
	if err := a.jobCancelled(ctx, child); err != nil {
		return err
	}
	return a.guardSubscriptionContext(ctx)
}

func (a *App) resumeOrdinarySearchDownloads(ctx context.Context, parent Job, sub Subscription) (int, map[string]bool, error) {
	children, err := a.originalChatSearchChildren(ctx, parent.ID, "download", "download")
	if err != nil {
		return 0, nil, err
	}
	accepted := 0
	rejected := map[string]bool{}
	for _, child := range children {
		var payload ordinarySearchDownloadPayload
		if json.Unmarshal(child.Payload, &payload) != nil || payload.Root != parent.ID || payload.ID == "" {
			return accepted, nil, ordinarySearchDownloadReview()
		}
		ok, err := a.runOrdinarySearchDownload(ctx, parent, sub, payload.ID)
		if err != nil {
			return accepted, nil, err
		}
		if ok {
			accepted++
			continue
		}
		var receipt ordinarySubscriptionDownloadRejection
		if err := a.readQualitySealed(ctx, a.Store.DB, ordinarySubscriptionDownloadRejectionKind, payload.ID, &receipt); err != nil {
			return accepted, nil, err
		}
		rejected[receipt.Fingerprint] = true
	}
	return accepted, rejected, nil
}

func (a *App) runOrdinarySearchDownload(ctx context.Context, parent Job, sub Subscription, intentID string) (bool, error) {
	child, in, run, err := a.claimOrdinarySearchDownload(ctx, parent, sub, intentID)
	if err != nil {
		return false, err
	}
	if !run {
		if child.State == "failed" {
			confirmed, err := a.confirmedOrdinarySubscriptionDownloadRejection(ctx, child, in)
			if err != nil || confirmed {
				return false, err
			}
			return false, ordinarySearchDownloadReview()
		}
		return true, nil // The claim verified an existing durable acceptance.
	}
	childCtx := a.traceContext(ctx, child.WorkflowID, child.ID, child.RequestID)
	childCtx = context.WithValue(childCtx, ordinarySearchDownloadKey{}, ordinarySearchDownloadScope{app: a, parent: parent, child: child, intent: intentID})
	err = a.safeRun(childCtx, child)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if e := a.Store.Get(ctx, "intent", intentID, &in); e != nil {
		return false, e
	}
	actual, e := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, child.ID))
	if e != nil {
		return false, e
	}
	if actual.Kind != child.Kind || actual.WorkflowID != child.WorkflowID || !bytes.Equal(actual.Payload, child.Payload) {
		return false, ordinarySearchDownloadReview()
	}
	if actual.Generation != child.Generation || actual.State != "running" {
		if actual.Generation == child.Generation && actual.State == "failed" {
			confirmed, err := a.confirmedOrdinarySubscriptionDownloadRejection(ctx, actual, in)
			if err != nil || confirmed {
				return false, err
			}
		}
		return false, ordinarySearchDownloadReview()
	}
	if err != nil {
		// No fallback follows transport loss, plugin invocation or an unproved
		// return. The reserved target remains attached to this exact intent.
		_, finishErr := a.finishJob(ctx, child, "failed", pluginErrorCodeInternal(ordinarySearchDownloadReview()), a.clean(err.Error()), time.Now().Unix())
		return false, errors.Join(ordinarySearchDownloadReview(), finishErr)
	}
	if err := a.ordinarySearchDownloadAccepted(ctx, a.Store.DB, child, in); err != nil {
		return false, err
	}
	changed, err := a.finishJob(ctx, child, "succeeded", "", "", time.Now().Unix())
	if err != nil {
		return false, err
	}
	if !changed {
		return false, ordinarySearchDownloadReview()
	}
	return true, nil
}

func (a *App) claimOrdinarySearchDownload(ctx context.Context, parent Job, sub Subscription, intentID string) (Job, Intent, bool, error) {
	var child Job
	var in Intent
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return child, in, false, err
	}
	defer tx.Rollback()
	var owner Subscription
	if err = qualityRecord(ctx, tx, "subscription", sub.ID, &owner); err != nil {
		return child, in, false, err
	}
	rss, err := a.ordinarySearchParentTx(ctx, tx, parent, owner)
	if err != nil {
		return child, in, false, err
	}
	child, err = scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='download' AND json_extract(payload,'$.id')=? AND json_extract(payload,'$._ordinary_search_attempt')=?`, intentID, parent.ID))
	if err != nil {
		return child, in, false, err
	}
	if err = qualityRecord(ctx, tx, "intent", intentID, &in); err != nil {
		return child, in, false, err
	}
	if child.ParentJobID != parent.ID || child.WorkflowID != in.WorkflowID || in.ID != intentID || in.SubscriptionID != owner.ID || in.SubscriptionRevision != owner.Revision || !in.Auto || in.SubscriptionAcquisition != "" || in.LatestResource != rss || in.ChatDownload {
		return child, in, false, ordinarySearchDownloadReview()
	}
	if providerNativeMedia(owner.Media) && (mediaKey(in.Media) != mediaKey(owner.Media) || in.Season != owner.Season) {
		return child, in, false, ordinarySearchDownloadReview()
	}
	if rss {
		var source downloadContextSnapshot
		if err := a.readQualitySealed(ctx, tx, "download_context", "intent:"+in.ID, &source); err != nil {
			return child, in, false, err
		}
		if err := a.guardOrdinarySearchRSSIntent(ctx, parent, owner, in, &source); err != nil {
			return child, in, false, err
		}
	}
	if child.State == "failed" {
		return child, in, false, nil
	}
	accepted := in.State == "accepted" || in.State == "downloading" || in.State == "downloaded" || in.State == "imported"
	if accepted {
		if err = a.ordinarySearchDownloadAccepted(ctx, tx, child, in); err != nil {
			return child, in, false, err
		}
		if child.State == "succeeded" {
			return child, in, false, nil
		}
	}
	if child.State != "queued" {
		return child, in, false, ordinarySearchDownloadReview()
	}
	if !accepted && (in.State != "planned" || child.Generation != 0 || child.Attempt != 0) {
		if in.State != "post_add" || in.SourceModuleStarted {
			return child, in, false, ordinarySearchDownloadReview()
		}
		post, err := a.sourcePostAddReceipt(ctx, tx, in)
		if err != nil || !post.SourceReturned || post.SourceError != "" {
			return child, in, false, ordinarySearchDownloadReview()
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='running',attempt=attempt+1,generation=generation+1,lease_until=?,updated_at=? WHERE id=? AND generation=? AND attempt=? AND state='queued'`, time.Now().Add(10*time.Minute).Unix(), now(), child.ID, child.Generation, child.Attempt)
	if err != nil {
		return child, in, false, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return child, in, false, ordinarySearchDownloadReview()
	}
	child.State, child.Generation, child.Attempt = "running", child.Generation+1, child.Attempt+1
	return child, in, true, tx.Commit()
}

func (a *App) ordinarySearchDownloadAccepted(ctx context.Context, q qualityReader, child Job, in Intent) error {
	if in.State != "accepted" && in.State != "downloading" && in.State != "downloaded" && in.State != "imported" {
		return ordinarySearchDownloadReview()
	}
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("download.accepted", in.ID)).Scan(&raw); err != nil {
		return err
	}
	var event DomainEvent
	if json.Unmarshal(raw, &event) != nil || event.Type != "download.accepted" || event.EventID != domainEventID("download.accepted", in.ID) || event.IntentID != in.ID || event.JobID != child.ID || event.WorkflowID != child.WorkflowID {
		return ordinarySearchDownloadReview()
	}
	var accepted downloadAddedSnapshot
	if err := a.readQualitySealed(ctx, q, "download_added", event.EventID, &accepted); err != nil || accepted.DownloadID != in.ID || accepted.DownloadKind != "intent" || !sameHash(accepted.Hash, in.Hash) || accepted.TMDBID != in.Media.ID || accepted.MediaType != in.Media.MediaType || accepted.SiteID != in.SiteID {
		return ordinarySearchDownloadReview()
	}
	if providerNativeMedia(in.Media) {
		identity, err := accepted.mediaIdentity()
		if err != nil || identity != mediaKey(in.Media) || event.MediaID != identity {
			return ordinarySearchDownloadReview()
		}
	}
	if in.LatestResource && (accepted.Context == nil || accepted.Context.LatestResource == nil || accepted.Context.LatestResource.Mode != "rss" || verifyRSSMagnetContext(in, accepted.Context) != nil) {
		return ordinarySearchDownloadReview()
	}
	post, err := a.sourcePostAddReceipt(ctx, q, in)
	if err != nil || !post.SourceReturned || post.SourceError != "" {
		return ordinarySearchDownloadReview()
	}
	return nil
}
