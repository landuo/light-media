package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// These reviewer-owned cases use the production SDK executable and real SQLite.
// Source expectations come from independently selected inputs to the actual
// pinned Python Oper, not from the Go projection under review.
func independentHistoryEvidence(t *testing.T, name string, value any) {
	t.Helper()
	root := os.Getenv("HISTORY_INDEPENDENT_EVIDENCE")
	if root == "" {
		t.Fatal("HISTORY_INDEPENDENT_EVIDENCE required")
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, name), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func independentHistorySource(t *testing.T, name string, input any) []map[string]any {
	t.Helper()
	root := os.Getenv("HISTORY_INDEPENDENT_EVIDENCE")
	dir, err := os.MkdirTemp(root, name+"-")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config")
	if err = os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(input, "", "  ")
	in, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json")
	if err = os.WriteFile(in, raw, 0600); err != nil {
		t.Fatal(err)
	}
	python, driver, source := os.Getenv("HISTORY_SOURCE_PYTHON"), os.Getenv("HISTORY_SOURCE_DRIVER"), os.Getenv("HISTORY_SOURCE_ROOT")
	if python == "" || driver == "" || source == "" {
		t.Fatal("real source runner bindings required")
	}
	cmd := exec.Command(python, driver, in, out)
	cmd.Env = []string{"HOME=" + config, "CONFIG_DIR=" + config, "PATH=/usr/bin:/bin", "PYTHONPATH=" + source + ":" + filepath.Dir(driver), "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1", "DB_TYPE=sqlite", "LANG=C.UTF-8", "TZ=Asia/Shanghai"}
	log, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(dir, "run.log"), log, 0600)
	if err != nil {
		t.Fatalf("source execution: %v %s", err, log)
	}
	raw, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Observations []map[string]any `json:"observations"`
		Network      []any            `json:"network_attempts"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || len(result.Network) != 0 {
		t.Fatalf("source invalid: %v", err)
	}
	for _, observation := range result.Observations {
		if observation["error"] != nil {
			t.Fatalf("source case error: %v", observation)
		}
	}
	for _, observation := range result.Observations {
		if observation["method"] == "add_success" || observation["method"] == "add_fail" {
			returned, ok := observation["returned"].(map[string]any)
			if !ok {
				t.Fatal("source producer returned no row")
			}
			date, ok := returned["date"].(string)
			before, bok := observation["before_local_date"].(string)
			after, aok := observation["after_local_date"].(string)
			if !ok || !bok || !aok || date < before || date > after || len(date) != 19 {
				t.Fatal("source completion date outside observed local interval")
			}
		}
	}
	t.Logf("independent actual source observations: %s", dir)
	return result.Observations
}

func independentHistoryRunFailure(t *testing.T, a *App, job Job) error {
	t.Helper()
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	current, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	failure := a.safeRun(context.Background(), current)
	if failure == nil {
		t.Fatal("controlled native collision did not fail")
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='failed',error_message=? WHERE id=?`, failure.Error(), job.ID); err != nil {
		t.Fatal(err)
	}
	return failure
}

func TestHistoryIndependentNativeRetryUpdatesFrozenRecord(t *testing.T) {
	a, _, object := ownedHistorySDKFixture(t, true)
	var sqliteVersion string
	if err := a.Store.DB.QueryRow(`SELECT sqlite_version()`).Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	independentHistoryEvidence(t, "target-runtime.json", map[string]any{"sqlite_version": sqliteVersion, "timezone": os.Getenv("TZ"), "isolation": "newTestApp private SQLite; owned loopback artifact; native synthetic files"})
	root := t.TempDir()
	source, target := filepath.Join(root, "input"), filepath.Join(root, "output")
	for _, d := range []string{source, target} {
		if err := os.Mkdir(d, 0750); err != nil {
			t.Fatal(err)
		}
	}
	a.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "root", Path: target, Kind: "mixed"}}}
	identity := importFixture(t, a, "movie")
	identity.Title = "Independent Ω"
	identity.IMDbID = "tt3141592"
	identity.TVDBID = sharedHistoryPtr(int64(271828))
	importCache(t, a, "media:movie:123", identity)
	path := filepath.Join(source, "Independent.2024.mkv")
	content := []byte("independent native copy: αβ\n")
	importWrite(t, path, content)
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
		t.Fatal("not a normal observer-free native producer")
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	collision := []byte("independent collision retained")
	importWrite(t, plan.Files[0].Target, collision)
	failure := independentHistoryRunFailure(t, a, job)
	first := ownedHistoryRead(t, object, &path, nil, nil, false)
	if first.Result == nil || first.Result.Status == nil || *first.Result.Status {
		t.Fatalf("missing initial failure: %+v", first)
	}
	independentHistoryEvidence(t, "native-first-failure.json", first)
	// Move our own collision aside (do not delete it); retry the supported same
	// plan execution after the physical conflict is resolved.
	savedCollision := plan.Files[0].Target + ".independent-collision"
	if err = os.Rename(plan.Files[0].Target, savedCollision); err != nil {
		t.Fatal(err)
	}
	// Separate completed-at seconds so a stale failure timestamp cannot pass.
	time.Sleep(1100 * time.Millisecond)
	retryBefore := time.Now().Format("2006-01-02 15:04:05")
	retry, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	queued := ownedHistoryRead(t, object, &path, nil, nil, false)
	independentHistoryEvidence(t, "native-retry-queued.json", queued)
	reorgRunJob(t, a, retry)
	retryAfter := time.Now().Format("2006-01-02 15:04:05")
	for p, want := range map[string][]byte{path: content, plan.Files[0].Target: content, savedCollision: collision} {
		got, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatalf("physical bytes %s: %v", p, e)
		}
	}
	fresh := ownedHistoryRead(t, object, &path, nil, nil, false)
	if fresh.Result == nil || fresh.Result.ID != first.Result.ID || fresh.Result.Date == nil || *fresh.Result.Date < retryBefore || *fresh.Result.Date > retryAfter {
		t.Error("retry lost stable history handle or failed to freeze its actual completion date")
	}
	destination := ownedHistoryRead(t, object, nil, &plan.Files[0].Target, nil, true)
	independentHistoryEvidence(t, "native-retry-complete.json", map[string]any{"src": fresh, "dest": destination, "first_job": job.ID, "retry_job": retry.ID})
	fileitem := func(p string) map[string]any {
		info, e := os.Stat(p)
		if e != nil {
			t.Fatal(e)
		}
		name := filepath.Base(p)
		ext := strings.TrimPrefix(filepath.Ext(name), ".")
		return map[string]any{"path": p, "storage": "local", "type": "file", "name": name, "basename": strings.TrimSuffix(name, "."+ext), "extension": ext, "size": info.Size(), "modify_time": float64(info.ModTime().UnixNano()) / 1e9}
	}
	producer := func(success bool) map[string]any {
		tr := map[string]any{"message": failure.Error(), "file_list": []string{}}
		if success {
			tr = map[string]any{"target_item": fileitem(plan.Files[0].Target), "file_list": []string{path}}
		}
		return map[string]any{"meta_path": path, "meta": map[string]any{"type": "电影"}, "fileitem": fileitem(path), "media": map[string]any{"source": "themoviedb", "type": "电影", "title": "Independent Ω", "year": "2024", "tmdb_id": 123, "imdb_id": "tt3141592", "tvdb_id": 271828, "category": ""}, "transferinfo": tr}
	}
	observations := independentHistorySource(t, "native-retry-source", map[string]any{"operations": []any{
		map[string]any{"case": "failed-normal-copy", "family": "transfer", "method": "add_fail", "arguments": map[string]any{"mode": "copy"}, "producer": producer(false)},
		map[string]any{"case": "successful-normal-copy-retry", "family": "transfer", "method": "add_success", "arguments": map[string]any{"mode": "copy"}, "producer": producer(true)},
		map[string]any{"case": "read-retry-source", "family": "transfer", "method": "get_by_src", "arguments": map[string]any{"src": path}},
		map[string]any{"case": "read-retry-destination", "family": "transfer", "method": "get_by_dest", "arguments": map[string]any{"dest": plan.Files[0].Target}},
	}})
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
	cold.wg.Wait()
	obj, ok := cold.pluginRunningInstances.Load("OwnedHistory")
	if !ok {
		t.Fatal("cold worker missing")
	}
	coldRead := ownedHistoryRead(t, obj.(*originalPluginInstance), &path, nil, nil, false)
	independentHistoryEvidence(t, "native-retry-cold.json", coldRead)
	if coldRead.PID == fresh.PID || !reflect.DeepEqual(coldRead.Result, fresh.Result) {
		t.Error("cold restart lost durable row or reused process")
	}
	if queued.Result == nil || !reflect.DeepEqual(queued.Result, first.Result) {
		t.Error("queued retry hid or changed last completed failure")
	}
	for i, read := range []ownedHistorySDKSnapshot{fresh, destination, coldRead} {
		want := observations[2+i%2]["returned"].(map[string]any)
		raw, _ := json.Marshal(read.Result)
		var got map[string]any
		_ = json.Unmarshal(raw, &got)
		if got == nil {
			t.Errorf("successful retry lookup %d returned null", i)
			continue
		}
		want["id"] = got["id"]
		want["date"] = got["date"]
		if !reflect.DeepEqual(want, got) {
			t.Errorf("successful retry lookup %d retains wrong frozen state: source=%s target=%s", i, mustHistoryJSON(want), mustHistoryJSON(got))
		}
	}
}

func TestHistoryIndependentRawInputsAreNotRewritten(t *testing.T) {
	_, _, object := ownedHistorySDKFixture(t, true)
	tests := map[string]json.RawMessage{
		"invalid-utf8":               append(append([]byte(`{"src":"`), 0xff), []byte(`"}`)...),
		"unpaired-high-surrogate":    json.RawMessage(`{"src":"\ud800"}`),
		"duplicate-member":           json.RawMessage(`{"src":"/exact-first","src":"/exact-second"}`),
		"case-alias-member":          json.RawMessage(`{"src":"/exact-first","SRC":"/exact-second"}`),
		"unpaired-low-surrogate":     json.RawMessage(`{"src":"\udfff"}`),
		"duplicate-escaped-member":   json.RawMessage(`{"src":"first","\u0073rc":"second"}`),
		"duplicate-storage":          json.RawMessage(`{"src":"valid","storage":null,"storage":"local"}`),
		"dest-alias-member":          json.RawMessage(`{"dest":"first","DEST":"second"}`),
		"dest-low-surrogate-storage": json.RawMessage(`{"dest":"valid","storage":"\udfff"}`),
	}
	observations := map[string]any{}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			method := plugins.TransferHistoryBySourceCapability
			if strings.HasPrefix(name, "dest-") {
				method = plugins.TransferHistoryByDestinationCapability
			}
			call := append([]byte(`{"method":"`+method+`","payload":`), payload...)
			call = append(call, '}')
			result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", call, nil)
			observations[name] = map[string]any{"input_hex": fmt.Sprintf("%x", payload), "result": string(result.Result), "error": fmt.Sprint(err)}
			if err == nil {
				t.Errorf("raw ambiguous/malformed input accepted rather than rejected: result=%s", result.Result)
			}
		})
	}
	for name, payload := range map[string]json.RawMessage{
		"valid-surrogate-pair": json.RawMessage(`{"src":"/independent/\ud83d\ude00.mkv"}`),
		"literal-backslash-u":  json.RawMessage(`{"src":"/independent/\\ud800.mkv"}`),
	} {
		call := append([]byte(`{"method":"history.transfer.get_by_src","payload":`), payload...)
		call = append(call, '}')
		result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", call, nil)
		observations[name] = map[string]any{"result": string(result.Result), "error": fmt.Sprint(err)}
		if err != nil || string(result.Result) != "null" {
			t.Errorf("valid exact string rejected or worker damaged: %s: %s %v", name, result.Result, err)
		}
	}
	independentHistoryEvidence(t, "raw-inputs.json", observations)
}

func independentHistoryCanonical(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHistoryIndependentExactQuerySourceMatrix(t *testing.T) {
	oraclePath := os.Getenv("HISTORY_INDEPENDENT_QUERY_SOURCE")
	if oraclePath == "" {
		t.Fatal("fresh independent source oracle required")
	}
	raw, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Observations []struct {
			Case, Method        string
			Arguments, Returned json.RawMessage
			Error               json.RawMessage
		}
		Network []any `json:"network_attempts"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil || len(oracle.Network) != 0 || len(oracle.Observations) != 15 {
		t.Fatal("invalid actual source matrix", err)
	}
	a, _, object := ownedHistorySDKFixture(t, true)
	used := map[int]bool{}
	for _, obs := range oracle.Observations {
		if len(obs.Error) > 0 {
			t.Fatal("source query failed", string(obs.Error))
		}
		if string(obs.Returned) == "null" {
			continue
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(obs.Returned, &fields); err != nil {
			t.Fatal(err)
		}
		var sourceID int
		if err = json.Unmarshal(fields["id"], &sourceID); err != nil {
			t.Fatal(err)
		}
		if used[sourceID] {
			continue
		}
		used[sourceID] = true
		handle := fmt.Sprintf("independent-row-%d", sourceID)
		fields["id"], _ = json.Marshal(handle)
		encoded, _ := json.Marshal(fields)
		var row plugins.TransferHistoryRecord
		if err = json.Unmarshal(encoded, &row); err != nil {
			t.Fatal(err)
		}
		status := "imported"
		if row.Status != nil && !*row.Status {
			status = "error"
		}
		file := PlanFile{ID: handle, HistorySource: handle, Status: status}
		if row.Source != nil {
			file.Source = *row.Source
		}
		if row.Destination != nil {
			file.Target = *row.Destination
		}
		plan := ImportPlan{ID: "independent-plan-" + handle, Files: []PlanFile{file}, CreatedAt: time.Date(2020, 1, sourceID, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)}
		metadata := importMetadata{SharedHistory: map[string]sharedTransferHistorySnapshot{handle: {Row: row, Final: true}}}
		tx, e := a.Store.DB.BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = persistSharedHistoryPlan(context.Background(), tx, plan, metadata); e != nil {
			t.Fatal(e)
		}
		job, e := enqueueTx(context.Background(), tx, "import", map[string]any{"id": plan.ID}, "independent-query-fixture")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(`UPDATE jobs SET state='succeeded',created_at=? WHERE id=?`, plan.CreatedAt, job.ID); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	if len(used) != 4 {
		t.Fatal("source matrix did not expose all independent rows")
	}
	historyState := func(app *App) []string {
		rows, e := app.Store.DB.Query(`SELECT kind||'/'||id||':'||payload FROM records WHERE kind IN ('import_plan','import_meta','original_transfer_callback','transfer_history_hidden') ORDER BY kind,id`)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var s string
			if e = rows.Scan(&s); e != nil {
				t.Fatal(e)
			}
			out = append(out, s)
		}
		return out
	}
	before := historyState(a)
	observed := map[string]any{}
	run := func(phase string, obj *originalPluginInstance) {
		for _, obs := range oracle.Observations {
			t.Run(phase+"/"+obs.Case, func(t *testing.T) {
				method := plugins.TransferHistoryBySourceCapability
				if obs.Method == "get_by_dest" {
					method = plugins.TransferHistoryByDestinationCapability
				}
				request, _ := json.Marshal(map[string]any{"method": method, "payload": obs.Arguments})
				result, e := obj.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", request, nil)
				if e != nil {
					t.Fatal(e)
				}
				want := independentHistoryCanonical(t, obs.Returned)
				if want != nil {
					fields := want.(map[string]any)
					fields["id"] = "independent-row-" + string(fields["id"].(json.Number))
				}
				got := independentHistoryCanonical(t, result.Result)
				observed[phase+"/"+obs.Case] = got
				if !reflect.DeepEqual(want, got) {
					t.Errorf("actual source/target mismatch: source=%s target=%s", mustHistoryJSON(want), mustHistoryJSON(got))
				}
			})
		}
	}
	run("warm", object)
	if !reflect.DeepEqual(before, historyState(a)) {
		t.Error("read queries mutated authoritative history")
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
	cold.wg.Wait()
	v, ok := cold.pluginRunningInstances.Load("OwnedHistory")
	if !ok {
		t.Fatal("cold worker missing")
	}
	run("cold", v.(*originalPluginInstance))
	if !reflect.DeepEqual(before, historyState(cold)) {
		t.Error("cold read queries mutated authoritative history")
	}
	independentHistoryEvidence(t, "independent-query-target.json", observed)
}

func TestHistoryIndependentLegacyQueuedRetryPreservesKnownRecord(t *testing.T) {
	a, _, object := ownedHistorySDKFixture(t, true)
	ctx := context.Background()
	root := t.TempDir()
	input, output := filepath.Join(root, "input"), filepath.Join(root, "output")
	for _, p := range []string{input, output} {
		if err := os.Mkdir(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	a.config = Settings{AllowedRoots: []string{input}, Roots: []Root{{ID: "root", Path: output, Kind: "mixed"}}}
	identity := importFixture(t, a, "movie")
	path := filepath.Join(input, "Independent.Legacy.2024.mkv")
	importWrite(t, path, []byte("independent legacy format source"))
	scrape := false
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the pre-slice persisted metadata shape, then use the real native
	// producer and executeImport path rather than constructing a mock history DB.
	var meta importMetadata
	if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		t.Fatal(err)
	}
	meta.SharedHistory = nil
	if err = a.Store.Put(ctx, "import_meta", plan.ID, meta); err != nil {
		t.Fatal(err)
	}
	firstJob, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	importWrite(t, plan.Files[0].Target, []byte("legacy conflict retained"))
	failure := independentHistoryRunFailure(t, a, firstJob)
	before := ownedHistoryRead(t, object, &path, nil, nil, false)
	if before.Result == nil || before.Result.Date != nil || before.Result.Status == nil || *before.Result.Status {
		t.Fatal("legacy baseline failed", before)
	}
	source := independentHistorySource(t, "legacy-pending-source", map[string]any{"seed": map[string]any{"transferhistory": []any{map[string]any{"src": path, "src_storage": "local", "src_fileitem": nil, "dest": plan.Files[0].Target, "dest_storage": "local", "dest_fileitem": nil, "mode": "copy", "status": false, "errmsg": failure.Error(), "files": nil, "title": identity.Title, "year": "2024", "type": "电影", "tmdbid": 123, "media_source": "themoviedb", "media_id": "123"}}}, "operations": []any{
		map[string]any{"case": "before-next-transfer-completion", "family": "transfer", "method": "get_by_src", "arguments": map[string]any{"src": path}},
		map[string]any{"case": "query-again-no-source-write", "family": "transfer", "method": "get_by_src", "arguments": map[string]any{"src": path}},
	}})
	raw, _ := json.Marshal(before.Result)
	var baseline map[string]any
	_ = json.Unmarshal(raw, &baseline)
	expected := source[0]["returned"].(map[string]any)
	expected["id"] = baseline["id"]
	if !reflect.DeepEqual(expected, baseline) {
		t.Fatalf("legacy known/null facts not source-grounded: %s != %s", mustHistoryJSON(expected), mustHistoryJSON(baseline))
	}
	queued, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := ownedHistoryRead(t, object, &path, nil, nil, false)
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
	obj, ok := cold.pluginRunningInstances.Load("OwnedHistory")
	if !ok {
		t.Fatal("cold legacy worker missing")
	}
	restarted := ownedHistoryRead(t, obj.(*originalPluginInstance), &path, nil, nil, false)
	independentHistoryEvidence(t, "legacy-queued-retry.json", map[string]any{"before": before, "queued": after, "cold": restarted, "first_job": firstJob.ID, "queued_job": queued.ID})
	if !reflect.DeepEqual(before.Result, after.Result) || !reflect.DeepEqual(before.Result, restarted.Result) {
		t.Error("legacy completed known row disappears during queued retry or cold restart")
	}
}

func TestHistoryIndependentTypedSDKDoesNotRewriteInvalidStrings(t *testing.T) {
	observations := independentHistorySource(t, "typed-sdk-source", map[string]any{
		"seed": map[string]any{"transferhistory": []any{map[string]any{"src": "/independent/typed/�", "dest": "/independent/typed-dest/�", "src_storage": "�", "dest_storage": "�", "title": "valid replacement-character row", "status": true}}},
		"operations": []any{
			map[string]any{"case": "valid-replacement-src", "family": "transfer", "method": "get_by_src", "arguments": map[string]any{"src": "/independent/typed/�", "storage": "�"}},
			map[string]any{"case": "valid-replacement-dest", "family": "transfer", "method": "get_by_dest", "arguments": map[string]any{"dest": "/independent/typed-dest/�", "storage": "�"}},
		},
	})
	expected := observations[0]["returned"].(map[string]any)
	expected["id"] = "independent-typed-sdk-handle"
	raw, _ := json.Marshal(expected)
	var row plugins.TransferHistoryRecord
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	a, _, object := ownedHistorySDKFixture(t, true)
	file := PlanFile{ID: row.ID, HistorySource: row.ID, Source: *row.Source, Target: *row.Destination, Status: "imported"}
	plan := ImportPlan{ID: "independent-typed-sdk-plan", Files: []PlanFile{file}, CreatedAt: "2020-01-01T00:00:00Z"}
	metadata := importMetadata{SharedHistory: map[string]sharedTransferHistorySnapshot{row.ID: {Row: row, Final: true}}}
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = persistSharedHistoryPlan(context.Background(), tx, plan, metadata); err != nil {
		t.Fatal(err)
	}
	job, err := enqueueTx(context.Background(), tx, "import", map[string]any{"id": plan.ID}, "independent-typed-sdk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := ownedHistoryRead(t, object, row.Source, nil, row.SourceStorage, false).Result; !reflect.DeepEqual(got, &row) {
		t.Fatal("valid replacement-character positive control failed")
	}
	results := map[string]any{}
	for _, test := range []struct {
		name, key string
		value     []byte
		byDest    bool
	}{
		{"typed-invalid-source", "source_bytes_for_sdk_probe", bytes.ReplaceAll([]byte(*row.Source), []byte("�"), []byte{0xff}), false},
		{"typed-invalid-dest", "dest_bytes_for_sdk_probe", bytes.ReplaceAll([]byte(*row.Destination), []byte("�"), []byte{0xff}), true},
		{"typed-invalid-storage", "storage_bytes_for_sdk_probe", []byte{0xff}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The executable intentionally constructs invalid Go string bytes *inside*
			// its process, after JSON transport. This exercises the typed SDK rather
			// than the already-tested raw JSON host decoder.
			request := map[string]any{"src": row.Source, "dest": row.Destination, "storage": row.SourceStorage, "by_destination": test.byDest, test.key: test.value}
			input, _ := json.Marshal(request)
			result, e := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "snapshot", input, nil)
			results[test.name] = map[string]any{"input": request, "result": result.Result, "error": fmt.Sprint(e)}
			if e == nil || len(result.Result) > 0 {
				t.Errorf("typed SDK silently rewrote invalid bytes into another valid row: %s", result.Result)
			}
		})
	}
	if got := ownedHistoryRead(t, object, row.Source, nil, row.SourceStorage, false).Result; !reflect.DeepEqual(got, &row) {
		t.Fatal("SDK rejected valid replacement character after invalid inputs")
	}
	independentHistoryEvidence(t, "typed-sdk-inputs.json", results)
}
