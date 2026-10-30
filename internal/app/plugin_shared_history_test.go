package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func ownedHistoryRead(t *testing.T, object *originalPluginInstance, source, dest, storage *string, byDest bool) ownedHistorySDKSnapshot {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"src": source, "dest": dest, "storage": storage, "by_destination": byDest})
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "snapshot", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out ownedHistorySDKSnapshot
	if err = json.Unmarshal(result.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.PID <= 0 || out.PID == os.Getpid() || !out.StaleDenied {
		t.Fatalf("not a distinct scoped SDK worker: %+v", out)
	}
	return out
}

func TestSharedHistoryNativeProducerWorkerQueries(t *testing.T) {
	a, cookie, object := ownedHistorySDKFixture(t, true)
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	for _, dir := range []string{source, target} {
		if err := os.Mkdir(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	a.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "root", Path: target, Kind: "mixed"}}}
	identity := importFixture(t, a, "movie")
	identity.TVDBID = sharedHistoryPtr(int64(456))
	identity.IMDbID = "tt1234567"
	importCache(t, a, "media:movie:123", identity)
	path := filepath.Join(source, "Owned.2024.mkv")
	importWrite(t, path, []byte("owned file bytes"))
	scrape := false
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	var meta importMetadata
	if err = a.Store.Get(context.Background(), "import_meta", plan.ID, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.OriginalTransfer) != 0 || a.originalTransferHasHandlers() {
		t.Fatal("history enabled extension path/observer")
	}
	if got := ownedHistoryRead(t, object, &path, nil, nil, false).Result; got != nil {
		t.Fatal("preview leaked as history", got)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := ownedHistoryRead(t, object, &path, nil, nil, false).Result; got != nil {
		t.Fatal("queued job leaked as history", got)
	}
	before := time.Now().Format("2006-01-02 15:04:05")
	reorgRunJob(t, a, job)
	after := time.Now().Format("2006-01-02 15:04:05")
	got := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	if got == nil || got.Source == nil || *got.Source != path || got.Destination == nil || *got.Destination != plan.Files[0].Target || got.TVDBID == nil || *got.TVDBID != 456 || got.Status == nil || !*got.Status {
		t.Fatalf("native producer record incomplete: %+v", got)
	}
	if got.Date == nil || *got.Date < before || *got.Date > after {
		t.Fatalf("date was not frozen at producer completion: %+v", got)
	}
	sharedHistoryCompareActualSourceProducer(t, path, plan.Files[0].Target, got)
	for _, storage := range []*string{nil, sharedHistoryPtr(""), sharedHistoryPtr("local")} {
		byDest := ownedHistoryRead(t, object, nil, got.Destination, storage, true).Result
		if !reflect.DeepEqual(byDest, got) {
			t.Fatalf("destination lookup differs: %+v != %+v", byDest, got)
		}
	}
	for _, value := range []string{path + "/", filepath.Join(source, "missing"), "", " /owned "} {
		if row := ownedHistoryRead(t, object, &value, nil, nil, false).Result; row != nil {
			t.Fatal("query cleaned or broadened path", value, row)
		}
	}
	for _, storage := range []string{"LOCAL", "excluded-storage"} {
		if row := ownedHistoryRead(t, object, &path, nil, &storage, false).Result; row != nil {
			t.Fatal("storage match was not exact")
		}
	}
	// Cached metadata and the live file may change; history is a frozen DB fact.
	identity.Title = "Changed later"
	identity.TVDBID = sharedHistoryPtr(int64(999))
	importCache(t, a, "media:movie:123", identity)
	if err = os.Chtimes(path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if reread := ownedHistoryRead(t, object, &path, nil, nil, false).Result; !reflect.DeepEqual(reread, got) {
		t.Fatal("live metadata/stat changed history")
	}
	if err = a.Store.Get(context.Background(), "import_meta", plan.ID, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.OriginalTransfer) != 0 || !meta.SharedHistory[plan.Files[0].ID].Final {
		t.Fatal("producer toggled callbacks or failed to persist history")
	}

	// Clones share the authoritative host history, but not worker identity/callers.
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHistory/clone", `{"suffix":"Copy","version":"99.9"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	cloneValue, ok := a.pluginRunningInstances.Load("OwnedHistorycopy")
	if !ok {
		t.Fatal("clone worker absent")
	}
	clone := cloneValue.(*originalPluginInstance)
	cloneRead := ownedHistoryRead(t, clone, &path, nil, nil, false)
	if !reflect.DeepEqual(cloneRead.Result, got) || cloneRead.PID == ownedHistoryRead(t, object, &path, nil, nil, false).PID {
		t.Fatal("clone confused process identity or host history")
	}
	oldPID := ownedHistoryRead(t, object, &path, nil, nil, false).PID
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, ok := cold.pluginRunningInstances.Load("OwnedHistory")
	if !ok {
		t.Fatal("cold worker absent")
	}
	coldRead := ownedHistoryRead(t, current.(*originalPluginInstance), &path, nil, nil, false)
	if !reflect.DeepEqual(coldRead.Result, got) || coldRead.PID == oldPID {
		t.Fatal("cold SQLite/worker read changed frozen history")
	}
	object = current.(*originalPluginInstance)
	raw, _ := json.Marshal(got)
	t.Logf("worker PID=%d native job=%s record=%s", ownedHistoryRead(t, object, &path, nil, nil, false).PID, job.ID, raw)
}

func TestSharedHistoryCapabilityDenied(t *testing.T) {
	_, _, object := ownedHistorySDKFixture(t, false)
	raw := json.RawMessage(`{"method":"history.transfer.get_by_src","payload":{"src":"/owned/missing"}}`)
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", raw, nil)
	if err == nil || len(result.Result) > 0 {
		t.Fatalf("undeclared capability passed: %s %v", result.Result, err)
	}
}

func TestSharedHistoryRejectsMalformedReadRequest(t *testing.T) {
	_, _, object := ownedHistorySDKFixture(t, true)
	for _, payload := range []string{`{}`, `null`, `[]`, `{"src":12}`, `{"src":[],"storage":"local"}`, `{"src":"/owned","sql":"SELECT 1"}`} {
		call := json.RawMessage(`{"method":"history.transfer.get_by_src","payload":` + payload + `}`)
		result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", call, nil)
		if err == nil || len(result.Result) > 0 {
			t.Fatalf("invalid input accepted %s: %s %v", payload, result.Result, err)
		}
	}
}

func TestSharedHistoryLegacyUnknownFieldsRemainNull(t *testing.T) {
	a, _, object := ownedHistorySDKFixture(t, true)
	file := PlanFile{ID: "owned-legacy-file", Source: "/owned/legacy.mkv", Target: "/owned/target.mkv", Status: "imported", Method: "copy"}
	plan := ImportPlan{ID: "owned-legacy-plan", Files: []PlanFile{file}, CreatedAt: "2020-01-01T00:00:00Z"}
	if err := a.Store.Put(context.Background(), "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	job, err := a.enqueue(context.Background(), "import", map[string]string{"id": plan.ID}, "owned-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	row := ownedHistoryRead(t, object, &file.Source, nil, nil, false).Result
	if row == nil || row.Date != nil || row.Title != nil || row.Category != nil || string(row.SourceFileItem) != "null" || string(row.DestinationFileItem) != "null" || row.Seasons != nil {
		t.Fatalf("legacy unknown values fabricated: %+v", row)
	}
	if row.Source == nil || *row.Source != file.Source || row.Destination == nil || *row.Destination != file.Target {
		t.Fatal("legacy durable path facts lost")
	}
}

func TestSharedHistoryNativeFailureIsDurable(t *testing.T) {
	a, _, object := ownedHistorySDKFixture(t, true)
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	for _, dir := range []string{source, target} {
		if err := os.Mkdir(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	a.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "root", Path: target, Kind: "mixed"}}}
	identity := importFixture(t, a, "movie")
	identity.TVDBID = sharedHistoryPtr(int64(456))
	identity.IMDbID = "tt1234567"
	importCache(t, a, "media:movie:123", identity)
	path := filepath.Join(source, "Owned.2024.mkv")
	importWrite(t, path, []byte("native failure source retained"))
	scrape := false
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A legitimate concurrent local writer created a new destination after plan
	// preparation. The existing native ownership guard, not a test mock, rejects it.
	importWrite(t, plan.Files[0].Target, []byte("owned fixture collision must be retained"))
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Format("2006-01-02 15:04:05")
	failure := a.safeRun(context.Background(), job)
	if failure == nil {
		t.Fatal("native target collision was accepted")
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='failed',error_message=? WHERE id=?`, failure.Error(), job.ID); err != nil {
		t.Fatal(err)
	}
	row := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	if row == nil || row.Status == nil || *row.Status || row.Error == nil || *row.Error == "" || row.Date == nil || *row.Date < before || row.Destination != nil || string(row.Files) != "[]" {
		t.Fatalf("native failure history not frozen: %+v failure=%v", row, failure)
	}
	if raw, err := os.ReadFile(plan.Files[0].Target); err != nil || string(raw) != "owned fixture collision must be retained" {
		t.Fatal("native collision damaged target", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "native failure source retained" {
		t.Fatal("native collision damaged source", err)
	}
	var metadata importMetadata
	if err = a.Store.Get(context.Background(), "import_meta", plan.ID, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.OriginalTransfer) != 0 || !metadata.SharedHistory[plan.Files[0].ID].Final {
		t.Fatal("failure enabled observers or lost snapshot")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := cold.pluginRunningInstances.Load("OwnedHistory")
	if got := ownedHistoryRead(t, current.(*originalPluginInstance), &path, nil, nil, false).Result; !reflect.DeepEqual(got, row) {
		t.Fatal("cold failure record differs")
	}
}

func TestSharedHistoryActualClaimGenerationAndStaleAttempt(t *testing.T) {
	a, _, object := ownedHistorySDKFixture(t, true)
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	for _, dir := range []string{source, target} {
		if err := os.Mkdir(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	a.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "root", Path: target, Kind: "mixed"}}}
	importFixture(t, a, "movie")
	path := filepath.Join(source, "Owned.2024.mkv")
	content := []byte("actual claim generation source")
	importWrite(t, path, content)
	scrape := false
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.claim(context.Background())
	if err != nil || first.ID != submitted.ID || first.Generation <= submitted.Generation {
		t.Fatal("native claim did not advance generation", first, err)
	}
	importWrite(t, plan.Files[0].Target, []byte("owned claim collision"))
	failure := a.safeRun(context.Background(), first)
	if failure == nil {
		t.Fatal("collision passed")
	}
	changed, err := a.finishJob(context.Background(), first, "failed", "IMPORT_CONFLICT", failure.Error(), time.Now().Unix())
	if err != nil || !changed {
		t.Fatal("native failure completion", err)
	}
	failed := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	if err = os.Rename(plan.Files[0].Target, plan.Files[0].Target+".owned-collision"); err != nil {
		t.Fatal(err)
	}
	first.State = "failed"
	accepted, err := a.retryOriginalScrapeJob(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := accepted.RowsAffected(); n != 1 {
		t.Fatal("native retry rejected")
	}
	if pending := ownedHistoryRead(t, object, &path, nil, nil, false).Result; !reflect.DeepEqual(pending, failed) {
		t.Fatal("same-job queued retry hid previous facts")
	}
	second, err := a.claim(context.Background())
	if err != nil || second.ID != first.ID || second.Generation <= first.Generation {
		t.Fatal("retry claim generation not unique", second, err)
	}
	type outcome struct {
		current bool
		err     error
	}
	results := make(chan outcome, 2)
	go func() { results <- outcome{false, a.safeRun(context.Background(), first)} }()
	go func() { results <- outcome{true, a.safeRun(context.Background(), second)} }()
	for range 2 {
		r := <-results
		if r.current && r.err != nil {
			t.Fatal("current execution failed", r.err)
		}
	}
	changed, err = a.finishJob(context.Background(), second, "succeeded", "", "", time.Now().Unix())
	if err != nil || !changed {
		t.Fatal("native success completion", err)
	}
	row := ownedHistoryRead(t, object, &path, nil, nil, false).Result
	if row == nil || row.Status == nil || !*row.Status || row.Error != nil || row.Destination == nil {
		t.Fatalf("stale attempt overrode successful current record: %+v", row)
	}
	before, _ := json.Marshal(row)
	if err = a.safeRun(context.Background(), first); err != nil && !originalTransferInterrupted(err) {
		t.Fatal(err)
	}
	after, _ := json.Marshal(ownedHistoryRead(t, object, &path, nil, nil, false).Result)
	if string(before) != string(after) {
		t.Fatal("late stale execution rewrote checkpoint")
	}
	var metadata importMetadata
	if err = a.Store.Get(context.Background(), "import_meta", plan.ID, &metadata); err != nil {
		t.Fatal(err)
	}
	snapshot := metadata.SharedHistory[plan.Files[0].ID]
	if snapshot.JobID != second.ID || snapshot.Generation != second.Generation {
		t.Fatalf("persisted checkpoint identity differs: %+v", snapshot)
	}
	if data, err := os.ReadFile(plan.Files[0].Target); err != nil || string(data) != string(content) {
		t.Fatal("physical native copy bytes", err)
	}
	t.Logf("real claim + retry generations %d -> %d, concurrent stale/current and late stale stable", first.Generation, second.Generation)
}
