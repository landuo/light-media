package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Server-created proof: a manual file retry can continue its completed download
// without releasing that download's slots or losing asset provenance. Preview
// only records proof; intent binding and enqueue change in one transaction.
type downloadImportRetry struct {
	IntentID     string `json:"intent_id"`
	SourcePlanID string `json:"source_plan_id"`
	Fingerprint  string `json:"fingerprint"`
}

func findDownloadImportRetry(ctx context.Context, q qualityReader, owner string, plan ImportPlan, file PlanFile) (*downloadImportRetry, error) {
	var intent Intent
	if err := qualityRecord(ctx, q, "intent", owner, &intent); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if intent.ID != owner || intent.State != "downloaded" || intent.ImportPlanID == "" || intent.RootID != plan.RootID || mediaKey(intent.Media) != mediaKey(plan.Media) || intent.ChatWholeUnscoped || intent.Purpose == "quality" {
		return nil, nil
	}
	// A partial pack may have several failed files. Earlier failed plans remain
	// evidence after another file is retried; no active attempt may be replaced.
	var active bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs j INDEXED BY jobs_due CROSS JOIN records p ON p.kind='import_plan' AND p.id=json_extract(j.payload,'$.id') WHERE j.kind='import' AND j.state IN ('queued','running','retry_wait','waiting_paused') AND json_extract(p.payload,'$.intent_id')=?)`, owner).Scan(&active); err != nil || active {
		return nil, err
	}
	var old ImportPlan
	if err := qualityRecord(ctx, q, "import_plan", intent.ImportPlanID, &old); err != nil {
		return nil, err
	}
	if !failedDownloadSource(old, file.Source) {
		var raw []byte
		err := q.QueryRowContext(ctx, `SELECT p.payload FROM records p WHERE p.kind='import_plan' AND json_extract(p.payload,'$.intent_id')=? AND json_extract(p.payload,'$.state') IN ('partial','failed','superseded') AND EXISTS(SELECT 1 FROM json_each(p.payload,'$.files') f WHERE json_extract(f.value,'$.source')=? AND json_extract(f.value,'$.status')='error') ORDER BY json_extract(p.payload,'$.created_at') DESC LIMIT 1`, owner, file.Source).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &old) != nil {
			return nil, nil
		}
	}
	if old.IntentID != owner || old.RootID != plan.RootID || mediaKey(old.Media) != mediaKey(plan.Media) {
		return nil, nil
	}
	for _, episode := range importEpisodes(file.Episodes) {
		var reserved string
		err := q.QueryRowContext(ctx, `SELECT intent_id FROM reservations WHERE media_key=? AND season=? AND episode=?`, mediaKey(plan.Media), file.Season, episode).Scan(&reserved)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if reserved != owner {
			return nil, nil
		}
		allowed, err := failedImportSourceUnpublished(ctx, q, old, file, episode, true)
		if err != nil || !allowed {
			return nil, err
		}
	}
	return &downloadImportRetry{IntentID: owner, SourcePlanID: old.ID, Fingerprint: downloadReviewFingerprint(intent)}, nil
}

func failedDownloadSource(plan ImportPlan, source string) bool {
	if plan.State != "partial" && plan.State != "failed" && plan.State != "superseded" {
		return false
	}
	for _, file := range plan.Files {
		if file.Source == source && file.Status == "error" {
			return true
		}
	}
	return false
}

func confirmDownloadImportRetry(ctx context.Context, tx *sql.Tx, plan ImportPlan, proof downloadImportRetry) error {
	if !plan.Manual || len(plan.Files) != 1 || plan.IntentID != proof.IntentID {
		return bad("REVIEW_IMPORT_RETRY", "下载重整范围已变化，请重新提交")
	}
	current, err := findDownloadImportRetry(ctx, tx, proof.IntentID, plan, plan.Files[0])
	if err != nil {
		return err
	}
	if current == nil || *current != proof {
		return bad("REVIEW_IMPORT_RETRY", "原下载或整理状态已变化，请重新提交")
	}
	var intent Intent
	if err := qualityRecord(ctx, tx, "intent", proof.IntentID, &intent); err != nil {
		return err
	}
	var previous ImportPlan
	if err := qualityRecord(ctx, tx, "import_plan", intent.ImportPlanID, &previous); err != nil {
		return err
	}
	previous.State = "superseded"
	if err := txPut(ctx, tx, "import_plan", previous.ID, previous); err != nil {
		return err
	}
	intent.ImportPlanID, intent.ImportMode, intent.Error, intent.UpdatedAt = plan.ID, plan.Mode, "", now()
	return txPut(ctx, tx, "intent", intent.ID, intent)
}
