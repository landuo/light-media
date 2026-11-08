package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"light-media/internal/download"
	"light-media/internal/releasemeta"
)

const ordinarySubscriptionDownloadRejectionCode = "REVIEW_SUBSCRIPTION_DOWNLOAD_REJECTED"
const ordinarySubscriptionDownloadRejectionKind = "subscription_download_rejection"

type ordinarySubscriptionDownloadRejection struct {
	Version, Generation                         int
	Child, Parent, IntentSHA, Hash, Fingerprint string
}

func validOrdinarySubscriptionFailureFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (a *App) legacyOrdinarySubscriptionRejectionFingerprint(ctx context.Context, in Intent, site SiteConfig) (string, error) {
	snapshot, err := a.loadDownloadContext(ctx, "download_preview", in.PreviewID, in.Hash)
	if err != nil {
		return "", err
	}
	if snapshot == nil {
		return "", nil // Pre-context rows can still add, without inventing rejection evidence.
	}
	if in.SiteID != site.ID || in.LatestResource != (snapshot.LatestResource != nil) {
		return "", acquisitionEvidence()
	}
	if fingerprint := snapshot.OrdinaryFailureFingerprint; fingerprint != "" {
		if !validOrdinarySubscriptionFailureFingerprint(fingerprint) {
			return "", acquisitionEvidence()
		}
		return fingerprint, nil
	}
	var meta *releasemeta.MetaObject
	if len(snapshot.SourceMeta) > 0 {
		meta, err = releasemeta.DecodeMetaObjectState(snapshot.SourceMeta)
	} else {
		parser, parseErr := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: snapshot.Options})
		if parseErr != nil {
			return "", parseErr
		}
		meta, err = parser.ParseTitle(snapshot.Title, snapshot.Subtitle, nil)
	}
	if err != nil {
		return "", err
	}
	resource := in.ResourceID
	if proof := snapshot.LatestResource; proof != nil && proof.Mode == "rss" {
		if _, err := a.originalChatLatestProofResource(proof, site, in.ResourceID); err != nil {
			return "", err
		}
		material, err := a.originalHostReadSiteMaterial(ctx, site, in.ResourceID)
		if err != nil {
			return "", err
		}
		resource = material.FailureResourceKey
		if resource == "" {
			resource = acquisitionResourceURLKey(material.URL)
		}
	}
	return a.subscriptionFailureFingerprint(ctx, Subscription{Media: in.Media}, site.ID, resource, meta)
}

func (a *App) rejectOrdinarySubscriptionDownload(ctx context.Context, j Job, in Intent, origin string, cause error) error {
	if !download.MutationRejected(cause) || !isSubscriptionAcquisitionSource(origin) || !in.Auto || in.SubscriptionID == "" || in.SubscriptionAcquisition != "" {
		return acquisitionEvidence()
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	child, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if err != nil {
		return err
	}
	var input struct{ ID string }
	if child.Kind != "download" || child.State != "running" || child.Generation != j.Generation || child.ParentJobID == "" || child.WorkflowID != in.WorkflowID || !slices.Equal(child.Payload, j.Payload) || json.Unmarshal(child.Payload, &input) != nil || input.ID != in.ID {
		return acquisitionEvidence()
	}
	parent, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, child.ParentJobID))
	if err != nil {
		return err
	}
	var root originalChatPausedSearchPayload
	if parent.Kind != "subscription" || (parent.State != "running" && parent.State != "succeeded") || json.Unmarshal(parent.Payload, &root) != nil || root.ID != in.SubscriptionID || root.Revision == nil || *root.Revision != in.SubscriptionRevision {
		return acquisitionEvidence()
	}
	var current Intent
	if err := qualityRecord(ctx, tx, "intent", in.ID, &current); err != nil {
		return err
	}
	if current.State != "submitting" || current.SourceModuleStarted || current.SourcePostAddVersion != 1 || !sameHash(current.Hash, in.Hash) || acquisitionIntentSHA(current) != acquisitionIntentSHA(in) {
		return acquisitionEvidence()
	}
	proof, err := a.qualitySnapshot(ctx, tx, "intent", in.ID, in.Hash)
	if err != nil {
		return err
	}
	var owner Subscription
	if err := qualityRecord(ctx, tx, "subscription", in.SubscriptionID, &owner); err != nil {
		return err
	}
	if proof == nil || proof.BestVersion || owner.BestVersion || !a.subscriptionSearchState(ctx, owner) || owner.Revision != in.SubscriptionRevision || proof.SubscriptionID != owner.ID || proof.Revision != owner.Revision || proof.SiteID != in.SiteID || proof.ResourceID != in.ResourceID || proof.SourceContextSHA != owner.SourceContextSHA {
		return acquisitionEvidence()
	}
	var staged downloadContextSnapshot
	if err := a.readQualitySealed(ctx, tx, "download_context", "intent:"+in.ID, &staged); err != nil {
		return err
	}
	fingerprint := staged.OrdinaryFailureFingerprint
	if staged.DownloadID != in.ID || staged.DownloadKind != "intent" || !sameHash(staged.Hash, in.Hash) || !validOrdinarySubscriptionFailureFingerprint(fingerprint) {
		return acquisitionEvidence()
	}
	message := a.clean(cause.Error())
	current.State, current.Error, current.UpdatedAt = "failed", message, now()
	if err := txPut(ctx, tx, "intent", current.ID, current); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, current.ID); err != nil {
		return err
	}
	if err := a.recordSubscriptionFailureTx(ctx, tx, origin, fingerprint, message); err != nil {
		return err
	}
	receipt := ordinarySubscriptionDownloadRejection{Version: 1, Generation: child.Generation, Child: child.ID, Parent: parent.ID, IntentSHA: acquisitionIntentSHA(current), Hash: current.Hash, Fingerprint: fingerprint}
	if err := a.writeQualitySealedTx(ctx, tx, ordinarySubscriptionDownloadRejectionKind, current.ID, receipt); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='failed',run_at=?,lease_until=0,error_code=?,error_message=?,updated_at=? WHERE id=? AND generation=? AND state='running'`, time.Now().Unix(), ordinarySubscriptionDownloadRejectionCode, message, now(), child.ID, child.Generation)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return acquisitionEvidence()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), child.WorkflowID, child.ID, "ERROR", child.Kind, ordinarySubscriptionDownloadRejectionCode, message); err != nil {
		return err
	}
	if err := recordDomainEventTx(ctx, tx, DomainEvent{SchemaVersion: 1, EventID: domainEventID("job.failed", fmt.Sprintf("%s:%d", child.ID, child.Attempt)), Type: "job.failed", OccurredAt: now(), WorkflowID: child.WorkflowID, JobID: child.ID, ParentJobID: child.ParentJobID, CausationID: child.RequestID, Purpose: materialPurpose}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.notifyFailed(ctx, child, message)
	a.log("ERROR", child.Kind, ordinarySubscriptionDownloadRejectionCode, message, child.WorkflowID, child.ID)
	return jobErr(ordinarySubscriptionDownloadRejectionCode, cause)
}

// The durable receipt distinguishes a native refusal from a pre-Add error or
// an unknown outcome. Only that exact failed child is safe to skip in a batch.
func (a *App) confirmedOrdinarySubscriptionDownloadRejection(ctx context.Context, child Job, in Intent) (bool, error) {
	if child.ErrorCode != ordinarySubscriptionDownloadRejectionCode {
		return false, nil
	}
	var receipt ordinarySubscriptionDownloadRejection
	if err := a.readQualitySealed(ctx, a.Store.DB, ordinarySubscriptionDownloadRejectionKind, in.ID, &receipt); err != nil {
		return false, err
	}
	var input struct{ ID string }
	if receipt.Version != 1 || receipt.Generation != child.Generation || receipt.Child != child.ID || receipt.Parent == "" || receipt.Parent != child.ParentJobID || receipt.IntentSHA != acquisitionIntentSHA(in) || !sameHash(receipt.Hash, in.Hash) || !validOrdinarySubscriptionFailureFingerprint(receipt.Fingerprint) || child.Kind != "download" || child.State != "failed" || child.WorkflowID != in.WorkflowID || in.State != "failed" || !in.Auto || in.SubscriptionID == "" || in.SubscriptionAcquisition != "" || json.Unmarshal(child.Payload, &input) != nil || input.ID != in.ID {
		return false, acquisitionEvidence()
	}
	return true, nil
}
