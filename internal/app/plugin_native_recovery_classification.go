package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"

	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
)

type pluginNativeRecoveryClass uint8

const (
	pluginNativeUnknownBlocked pluginNativeRecoveryClass = iota
	pluginNativeCompleted
	pluginNativeNoEffectsRetry
)

type pluginNativeRecoveryEvidence struct {
	Class      pluginNativeRecoveryClass
	Kind       string
	Digest     [sha256.Size]byte
	Diagnostic string
}

// This classifier never changes a run, completes an attempt, or grants a lease.
// Its caller must persist a nonce-bound decision and revalidate at the claim TX.
func (j *pluginRunAttemptJournal) classifyNativeRecoveryTx(ctx context.Context, tx *sql.Tx, head pluginRunAttemptStored) (pluginNativeRecoveryEvidence, error) {
	out := pluginNativeRecoveryEvidence{Class: pluginNativeUnknownBlocked}
	run, err := j.runForAdmission(ctx, tx, head.claim.Admission)
	if err != nil {
		return out, err
	}
	if run.State == "canceled" {
		kind, digest, found, e := j.autoBackupCanceledEvidenceTx(ctx, tx, head, run)
		if e != nil {
			return out, e
		}
		if found {
			return pluginNativeRecoveryEvidence{Class: pluginNativeCompleted, Kind: kind, Digest: digest}, nil
		}
		return out, nil
	}
	if run.State == "succeeded" || run.State == "failed" {
		kind, digest, found, e := j.nativeRecoveryEvidenceTx(ctx, tx, head, run)
		if e != nil {
			return out, e
		}
		if found {
			return pluginNativeRecoveryEvidence{Class: pluginNativeCompleted, Kind: kind, Digest: digest}, nil
		}
	}
	if head.claim.Admission.PluginID == "AutoBackup" && run.Version == "2.1.4" {
		// Preserve a native diagnosis only after authenticating this attempt's
		// exact deferred receipt and rechecking the private root in this TX.
		// A mutable run error label never grants this classification or retry.
		record, sealed, e := j.p.readDeferred(ctx, tx, run)
		if e != nil {
			return out, e
		}
		if record.Plugin != "AutoBackup" || record.Version != "2.1.4" || (record.Phase != "started" && record.Phase != "uncertain") {
			return out, nil
		}
		_, _, e = j.p.autoBackupRead(ctx, tx, run.InstanceID, run.Generation)
		if autoBackupErrorCode(e) == "AUTO_BACKUP_EVIDENCE_MISSING" {
			out.Kind = "auto-backup-unknown/v1"
			out.Diagnostic = "AUTO_BACKUP_EVIDENCE_MISSING"
			out.Digest = sha256.Sum256(sealed)
			return out, nil
		}
		return out, e
	}
	if head.claim.Admission.PluginID == "IYUUAutoSeed" && run.Version == "2.19" {
		record, sealed, e := j.p.readDeferred(ctx, tx, run)
		if e != nil {
			return out, e
		}
		if record.Plugin != "IYUUAutoSeed" || record.Version != "2.19" || (record.Phase != "started" && record.Phase != "uncertain") {
			return out, nil
		}
		_, found, e := j.p.iyuuDeferredOutcomeTx(ctx, tx, run, record)
		if code := iyuuRecoveryErrorCode(e); code != "" {
			out.Kind = "iyuu-unknown/v1"
			out.Diagnostic = code
			out.Digest = sha256.Sum256(sealed)
			return out, nil
		}
		if e != nil {
			return out, e
		}
		if found {
			return out, nil
		}
		out.Kind = "iyuu-unknown/v1"
		out.Diagnostic = "PLUGIN_DEFERRED_UNCERTAIN"
		out.Digest = sha256.Sum256(sealed)
		return out, nil
	}
	if head.claim.Admission.PluginID != "SubscribeGroup" || run.Version != "2.8.7" || (run.Action != "event" && run.Action != "init" && run.Action != "reload") {
		return out, nil
	}
	return j.subscribeRetryEvidenceTx(ctx, tx, run)
}

func (j *pluginRunAttemptJournal) subscribeRetryEvidenceTx(ctx context.Context, tx *sql.Tx, run PluginRun) (pluginNativeRecoveryEvidence, error) {
	out := pluginNativeRecoveryEvidence{Class: pluginNativeUnknownBlocked}
	if run.State != "pending" && run.State != "reconcile_pending" && run.State != "running" {
		return out, nil
	}
	// Search by run globally. A mismatched/corrupt scope is NOT absent proof.
	var receiptCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_subscribe_group_operations WHERE run_id=?`, run.ID).Scan(&receiptCount); err != nil {
		return out, err
	}
	if receiptCount != 0 {
		return out, nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id='SubscribeGroup' AND i.version=? AND i.generation=? AND i.installed=1 AND i.desired_enabled=1 AND v.revision=?`, run.InstanceID, run.Version, run.Generation, run.ConfigRevision).Scan(&count); err != nil {
		return out, err
	}
	if count != 1 {
		return out, nil
	}
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, run.InstanceID).Scan(&sealed); err != nil {
		return out, err
	}
	raw, err := j.p.unseal("config", run.InstanceID, "main", sealed)
	if err != nil {
		return out, err
	}
	var inv plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	var config subscribegroup.Configuration
	if json.Unmarshal(run.Input, &inv) != nil || inv.Action != run.Action || !bytes.Equal(inv.Config, raw) || json.Unmarshal(raw, &config) != nil {
		return out, nil
	}
	if run.Action == "init" || run.Action == "reload" {
		// Clear flags, configuration revision, subscriptions and receipt all
		// commit in configureSubscribeGroup's one transaction; cache is memory.
		rawEvidence, _ := json.Marshal(struct {
			Run, Instance, Version, Action string
			Generation, Revision           int64
			Input, Config                  [sha256.Size]byte
		}{run.ID, run.InstanceID, run.Version, run.Action, run.Generation, run.ConfigRevision, sha256.Sum256(run.Input), sha256.Sum256(raw)})
		return pluginNativeRecoveryEvidence{Class: pluginNativeNoEffectsRetry, Kind: "subscribe-config-atomic/v1", Digest: sha256.Sum256(rawEvidence)}, nil
	}
	if !pluginStrictJSON(inv.Input, &input) || input.BatchID == "" || config.Clear || config.ClearHandle {
		return out, nil
	}
	var deliveries int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_event_deliveries WHERE batch_id=?`, input.BatchID).Scan(&deliveries); err != nil {
		return out, err
	}
	if deliveries != 1 {
		return out, nil
	}
	page, err := j.p.pluginBatchEventsFrom(ctx, tx, run, input.BatchID, 0, 2, run.State)
	if err != nil {
		return out, err
	}
	var handler string
	if err = tx.QueryRowContext(ctx, `SELECT handler_key FROM plugin_event_batches WHERE id=? AND run_id=?`, input.BatchID, run.ID).Scan(&handler); err != nil {
		return out, err
	}
	if page.Total != 1 || len(page.Events) != 1 || !page.Complete {
		return out, nil
	}
	event := page.Events[0]
	var snapshot any
	switch handler {
	case "added":
		if event.Type != "subscription.added" {
			return out, nil
		}
		snapshot, err = j.p.subscriptionAddedSnapshotFrom(ctx, tx, event)
	case "downloaded":
		if event.Type != "download.accepted" {
			return out, nil
		}
		snapshot, err = j.p.downloadAddedSnapshotFrom(ctx, tx, event)
	default:
		return out, nil
	}
	if err != nil {
		return out, err
	}
	// Added may refresh derived catalog cache before its transaction. Only the
	// subscription/intent/job/KV business mutation and its receipt are atomic.
	evidence, err := json.Marshal(struct {
		Run, Instance, Version, Action, Batch, Handler string
		Generation, Revision                           int64
		Input, Config                                  [sha256.Size]byte
		Event                                          DomainEvent
		Snapshot                                       any
	}{run.ID, run.InstanceID, run.Version, run.Action, input.BatchID, handler, run.Generation, run.ConfigRevision, sha256.Sum256(run.Input), sha256.Sum256(raw), event, snapshot})
	if err != nil {
		return out, err
	}
	return pluginNativeRecoveryEvidence{Class: pluginNativeNoEffectsRetry, Kind: "subscribe-event-atomic/v1", Digest: sha256.Sum256(evidence)}, nil
}
