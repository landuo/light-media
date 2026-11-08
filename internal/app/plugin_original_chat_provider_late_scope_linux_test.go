package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func assertOriginalLarkProviderLateScope(t *testing.T, a *App, source, library, file string, lateEpisodes, completed *atomic.Bool, details, material, adds, fileReads *atomic.Int32) {
	t.Helper()
	ctx := t.Context()
	var intentID string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID); err != nil {
		t.Fatal("accepted unscoped intent unavailable", err)
	}
	var before Intent
	if err := a.Store.Get(ctx, "intent", intentID, &before); err != nil || before.State != "accepted" ||
		!before.ChatWholeUnscoped || before.Season != 0 || len(before.Episodes) != 0 {
		t.Fatalf("invalid precondition for recovery: state=%s season=%d episodes=%v err=%v", before.State, before.Season, before.Episodes, err)
	}
	input := filepath.Join(source, file)
	importWrite(t, input, bytes.Repeat([]byte{1}, 1024))
	lateEpisodes.Store(true)
	completed.Store(true)
	if err := a.pollDownloads(ctx); err != nil {
		t.Fatal(err)
	}

	var after Intent
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := a.Store.Get(ctx, "intent", intentID, &after); err != nil {
			t.Fatal(err)
		}
		if after.State == "imported" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after.State != "imported" || after.ImportPlanID == "" || after.Error != "" || acquisitionIntentSHA(before) != acquisitionIntentSHA(after) {
		t.Fatalf("late scope did not publish from the immutable whole Add: state=%s error=%s plan=%s", after.State, after.Error, after.ImportPlanID)
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", after.ImportPlanID, &plan); err != nil || len(plan.Files) != 1 ||
		plan.Files[0].Source != input || plan.Files[0].Season != 1 || !slices.Equal(plan.Files[0].Episodes, []int{7, 8}) {
		t.Fatalf("actual import did not bind the recovered S01E07–E08: files=%v err=%v", plan.Files, err)
	}
	if _, err := a.readCompletedUnscopedPlan(ctx, a.Store.DB, after, plan); err != nil {
		t.Fatal("missing frozen completion authorization", err)
	}
	var assetID string
	var count int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT asset_id, count(*) FROM asset_slots WHERE media_key='tv:anilist:777' AND season=1 AND episode IN (7,8)
		GROUP BY asset_id`).Scan(&assetID, &count); err != nil || count != 2 {
		t.Fatal("published slot lost the exact provider", err)
	}
	var asset Asset
	if err := a.Store.Get(ctx, "asset", assetID, &asset); err != nil || asset.IntentID != intentID || asset.SourcePath != input || !within(library, asset.Path) {
		t.Fatalf("wrong physical asset: asset=%v err=%v", asset.ID, err)
	}
	original, sourceErr := os.Stat(input)
	linked, libraryErr := os.Stat(asset.Path)
	if sourceErr != nil || libraryErr != nil || !os.SameFile(original, linked) ||
		details.Load() != 3 || material.Load() != 1 || adds.Load() != 1 || fileReads.Load() != 1 {
		t.Fatalf("expected one new exact provider lookup and one physical hardlink without replay: source=%v library=%v details=%d material=%d add=%d files=%d",
			sourceErr, libraryErr, details.Load(), material.Load(), adds.Load(), fileReads.Load())
	}
	t.Log("the same exact provider supplied S01E07–E08 only at completion; the host verified the archived torrent file, saved a separate frozen import scope and hardlinked it without changing or replaying the accepted Add")
}
