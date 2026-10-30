package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// A legacy row has known native facts but no reconstructed source metadata.
// Real queue/claim/cancel transitions must leave it unchanged; only actual
// terminal native publication/failure may replace that checkpoint.
func TestSharedHistoryLegacyRetryLifecycle(t *testing.T) {
	a, _, object := ownedHistorySDKFixture(t, true)
	ctx := context.Background()
	root := t.TempDir()
	input, output := filepath.Join(root, "input"), filepath.Join(root, "output")
	for _, dir := range []string{input, output} {
		if err := os.Mkdir(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	a.config = Settings{AllowedRoots: []string{input}, Roots: []Root{{ID: "root", Path: output, Kind: "mixed"}}}
	importFixture(t, a, "movie")
	path := filepath.Join(input, "Owned.Legacy.2024.mkv")
	importWrite(t, path, []byte("owned legacy lifecycle bytes"))
	scrape := false
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	var meta importMetadata
	if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		t.Fatal(err)
	}
	meta.SharedHistory = nil // Persisted pre-slice format, before actual native work.
	if err = a.Store.Put(ctx, "import_meta", plan.ID, meta); err != nil {
		t.Fatal(err)
	}
	claim := func() Job {
		t.Helper()
		submitted, e := a.executeImport(ctx, plan.ID, nil)
		if e != nil {
			t.Fatal(e)
		}
		actual, e := a.claim(ctx)
		if e != nil || actual.ID != submitted.ID {
			t.Fatalf("real claim: %+v %v", actual, e)
		}
		return actual
	}
	finishFailure := func(job Job) {
		t.Helper()
		failure := a.safeRun(ctx, job)
		if failure == nil {
			t.Fatal("owned collision unexpectedly passed")
		}
		changed, e := a.finishJob(ctx, job, "failed", "IMPORT_CONFLICT", failure.Error(), time.Now().Unix())
		if e != nil || !changed {
			t.Fatal("failure completion", changed, e)
		}
	}
	first := claim()
	importWrite(t, plan.Files[0].Target, []byte("owned collision retained"))
	finishFailure(first)
	prior := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	if prior == nil || prior.Status == nil || *prior.Status || prior.Date != nil || string(prior.SourceFileItem) != "null" {
		t.Fatalf("legacy baseline: %+v", prior)
	}
	checkpoints := map[string]any{"legacy_failed": prior}
	queued, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if row := ownedHistoryRead(t, object, &path, nil, nil, false).Result; !reflect.DeepEqual(row, prior) {
		t.Fatal("queue changed legacy facts")
	}
	running, err := a.claim(ctx)
	if err != nil || running.ID != queued.ID {
		t.Fatal("retry claim", err)
	}
	row := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	checkpoints["running"] = row
	if !reflect.DeepEqual(row, prior) {
		t.Fatal("running changed legacy facts")
	}
	if _, err = a.cancelOrganizeQueueFile(ctx, plan.Files[0].ID); err != nil {
		t.Fatal(err)
	}
	row = ownedHistoryRead(t, object, &path, nil, nil, false).Result
	checkpoints["cancelled"] = row
	if !reflect.DeepEqual(row, prior) {
		t.Fatal("cancellation changed completed legacy facts")
	}
	if err = a.safeRun(ctx, running); err != nil && !originalTransferInterrupted(err) {
		t.Fatal("stale cancelled run", err)
	}
	if row = ownedHistoryRead(t, object, &path, nil, nil, false).Result; !reflect.DeepEqual(row, prior) {
		t.Fatal("cancelled stale execution rewrote history")
	}
	next := claim()
	finishFailure(next)
	failed := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	checkpoints["new_native_failure"] = failed
	if failed == nil || failed.Status == nil || *failed.Status || failed.Date == nil || failed.Destination != nil || failed.Error == nil || string(failed.SourceFileItem) != "null" {
		t.Fatalf("new failure missing known facts or fabricated legacy data: %+v", failed)
	}
	if err = os.Rename(plan.Files[0].Target, plan.Files[0].Target+".owned-conflict"); err != nil {
		t.Fatal(err)
	}
	last := claim()
	if row = ownedHistoryRead(t, object, &path, nil, nil, false).Result; !reflect.DeepEqual(row, failed) {
		t.Fatal("success pending changed prior failure")
	}
	if err = a.safeRun(ctx, last); err != nil {
		t.Fatal(err)
	}
	changed, err := a.finishJob(ctx, last, "succeeded", "", "", time.Now().Unix())
	if err != nil || !changed {
		t.Fatal("success completion", changed, err)
	}
	succeeded := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	checkpoints["new_native_success"] = succeeded
	if succeeded == nil || succeeded.Status == nil || !*succeeded.Status || succeeded.Date == nil || succeeded.Error != nil || succeeded.Destination == nil || string(succeeded.SourceFileItem) != "null" {
		t.Fatalf("native success checkpoint: %+v", succeeded)
	}
	if row = ownedHistoryRead(t, object, nil, succeeded.Destination, nil, true).Result; !reflect.DeepEqual(row, succeeded) {
		t.Fatal("destination differs")
	}
	if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.OriginalTransfer) != 0 || meta.SharedHistory[plan.Files[0].ID].JobID != last.ID {
		t.Fatal("observer enabled or checkpoint stale")
	}
	if data, e := os.ReadFile(plan.Files[0].Target); e != nil || string(data) != "owned legacy lifecycle bytes" {
		t.Fatal("native output", e)
	}
	if data, e := os.ReadFile(plan.Files[0].Target + ".owned-conflict"); e != nil || string(data) != "owned collision retained" {
		t.Fatal("owned conflict damaged", e)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	value, ok := cold.pluginRunningInstances.Load("OwnedHistory")
	if !ok {
		t.Fatal("cold SDK worker missing")
	}
	restored := ownedHistoryRead(t, value.(*originalPluginInstance), &path, nil, nil, false).Result
	checkpoints["cold_success"] = restored
	if !reflect.DeepEqual(restored, succeeded) {
		t.Fatal("cold lifecycle facts differ")
	}
	if dir := os.Getenv("HISTORY_OWNER_CHECKPOINT_EVIDENCE"); dir != "" {
		raw, e := json.MarshalIndent(checkpoints, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "legacy-lifecycle.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
