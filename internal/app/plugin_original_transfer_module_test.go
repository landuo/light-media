package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOriginalTransferModuleActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-module-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name           string
			Plugin         json.RawMessage
			Effect, Throws bool
			Result         []json.RawMessage
			Exception      *string
			TargetExists   bool `json:"target_exists"`
			NativeCalls    int  `json:"native_calls"`
			Callbacks      []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("fixed transfer oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			ctx := context.Background()
			source, target := t.TempDir(), t.TempDir()
			settings := a.Settings()
			settings.AllowedRoots = []string{source}
			settings.Roots = []Root{{ID: "root", Name: "Owned", Path: target, Kind: "movie"}}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(source, "Owned.2024.mkv")
			if err := os.WriteFile(input, []byte("owned transfer content"), 0640); err != nil {
				t.Fatal(err)
			}
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			method, mode := "module_execute", "no_effect"
			if sample.Effect {
				method, mode = "module_transfer", "effect"
			}
			if sample.Throws {
				if sample.Effect {
					mode = "effect_then_throw"
				} else {
					method = "module_fail"
				}
			}
			config := map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "transfer": method}, "media_result": full, "result": sample.Plugin, "storage_root": source, "storage_mode": mode, "transfer_root": target}
			encoded, _ := json.Marshal(config)
			saveOwnedModule(t, a, cookie, object, string(encoded))
			plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("real source preview", plan, err)
			}
			config["transfer_target"] = plan.Files[0].Target
			encoded, _ = json.Marshal(config)
			saveOwnedModule(t, a, cookie, object, string(encoded))
			before := ownedModuleRead(t, object)
			job := overwriteStart(t, a, plan)
			err = a.runImport(ctx, job, plan.ID, nil)
			success := len(sample.Result) > 0 && string(sample.Result[0]) == "true" && sample.Name != "model_true_without_effect"
			if (err == nil) != success {
				t.Fatal("actual source result branch + required physical verification", err, sample.Result, sample.Exception)
			}
			actual, statErr := os.Stat(plan.Files[0].Target)
			if (statErr == nil) != sample.TargetExists {
				t.Fatal("source real target side effect", statErr, sample.TargetExists)
			}
			if !success && !sample.TargetExists {
				var held int
				if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE intent_id=?`, plan.ID).Scan(&held); err != nil || held != 0 {
					t.Fatal("terminal unpublished result must allow a new explicit operation", held, err)
				}
			}
			if statErr == nil {
				src, e := os.Stat(input)
				if e != nil || !os.SameFile(src, actual) {
					t.Fatal("real hardlink identity", e)
				}
			}
			var count int
			if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&count); e != nil || count != map[bool]int{true: 1, false: 0}[success] {
				t.Fatal("actual asset registration", count, e)
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			if after.Calls[method]-before.Calls[method] != 1 {
				t.Fatal("real Source module once", before.Calls, after.Calls)
			}
			var call struct {
				Args   []json.RawMessage
				Kwargs map[string]json.RawMessage
			}
			if json.Unmarshal(after.Inputs[method], &call) != nil || len(call.Args) != 0 || len(call.Kwargs) != 14 {
				t.Fatal("exact transfer signature", string(after.Inputs[method]))
			}
			for _, key := range []string{"fileitem", "meta", "mediainfo", "target_directory", "target_storage", "target_path", "transfer_type", "episodes_info", "scrape", "library_type_folder", "library_category_folder", "source_oper", "target_oper", "preview"} {
				if _, ok := call.Kwargs[key]; !ok {
					t.Fatal("missing source keyword", key)
				}
			}
			if string(call.Kwargs["transfer_type"]) != `"link"` || string(call.Kwargs["preview"]) != "false" || string(call.Kwargs["episodes_info"]) != "null" {
				t.Fatal("source exact scalar inputs", string(after.Inputs[method]))
			}
			var snapshot struct {
				TransferEvents []json.RawMessage `json:"transfer_events"`
			}
			if e := object.readSession(ctx, "snapshot", &snapshot); e != nil {
				t.Fatal(e)
			}
			wantEvents := 0
			if len(sample.Callbacks) > 0 && sample.Callbacks[0] == "TransferInfo" && sample.Name != "model_true_without_effect" {
				wantEvents = 1
			}
			if len(snapshot.TransferEvents) != wantEvents {
				t.Fatal("source typed callback only", len(snapshot.TransferEvents), wantEvents, snapshot.TransferEvents)
			}
			if wantEvents == 1 {
				var event plugins.MutableEvent
				var payload struct {
					TransferInfo map[string]any `json:"transferinfo"`
				}
				if json.Unmarshal(snapshot.TransferEvents[0], &event) != nil || json.Unmarshal(event.Data, &payload) != nil || payload.TransferInfo["need_notify"] != false || payload.TransferInfo["need_scrape"] != false {
					t.Fatal("actual source TransferInfo defaults consumed", string(snapshot.TransferEvents[0]))
				}
			}
			// A second execution of the same frozen operation must not repeat a plugin
			// write, including false/invalid return values that already changed files.
			if sample.Name == "false" {
				config["modules"] = map[string]string{"recognize_media": "module_search_media"}
				encoded, _ := json.Marshal(config)
				saveOwnedModule(t, a, cookie, object, string(encoded))
			}
			retry := a.runImport(ctx, job, plan.ID, nil)
			if (retry == nil) != success {
				t.Fatal("stable recovery outcome", retry, err)
			}
			a.wg.Wait()
			if last := ownedModuleRead(t, object); last.Calls[method] != after.Calls[method] {
				t.Fatal("same frozen job replayed module")
			}
		})
	}
}

func originalTransferModuleFixture(t *testing.T, mode, workerMode string) (*App, *originalPluginInstance, ImportPlan, map[string]any) {
	t.Helper()
	a, cookie, object := ownedModuleFixture(t, true)
	ctx := context.Background()
	source, target := t.TempDir(), t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = []string{source}
	settings.Roots = []Root{{ID: "root", Path: target, Kind: "movie"}}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(source, "Owned.2024.mkv")
	if err := os.WriteFile(input, []byte("owned transfer content"), 0640); err != nil {
		t.Fatal(err)
	}
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "transfer": "module_transfer"}, "media_result": full, "result": map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": false, "private": "owned-private-transfer-result"}}, "storage_root": source, "storage_mode": "effect", "transfer_root": target, "transfer_mode": workerMode}
	encoded, _ := json.Marshal(config)
	saveOwnedModule(t, a, cookie, object, string(encoded))
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: mode, Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatal(plan, err)
	}
	config["transfer_target"] = plan.Files[0].Target
	encoded, _ = json.Marshal(config)
	saveOwnedModule(t, a, cookie, object, string(encoded))
	return a, object, plan, config
}

func TestOriginalTransferModuleCopyAndOwnershipGuards(t *testing.T) {
	for _, sample := range []struct{ name, mode, worker, code string }{{"actual_copy", "copy", "copy", ""}, {"changed_bytes", "copy", "corrupt", "REVIEW_COPY_CONTENT"}, {"copy_is_not_selected_link", "hardlink", "copy", "REVIEW_PLUGIN_TRANSFER_METHOD"}, {"different_result_target", "hardlink", "", "REVIEW_PLUGIN_TRANSFER_TARGET"}} {
		t.Run(sample.name, func(t *testing.T) {
			a, object, plan, config := originalTransferModuleFixture(t, sample.mode, sample.worker)
			ctx := context.Background()
			if sample.name == "different_result_target" {
				config["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "target_item": map[string]any{"storage": "local", "path": filepath.Join(filepath.Dir(plan.Files[0].Target), "Other.mkv")}}}
				encoded, _ := json.Marshal(config)
				if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
					t.Fatal(err)
				}
				if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
					t.Fatal(err)
				}
			}
			job := overwriteStart(t, a, plan)
			err := a.runImport(ctx, job, plan.ID, nil)
			if fileCode(err) != sample.code {
				t.Fatal("actual integrity guard", fileCode(err), sample.code, err)
			}
			if _, err := os.Stat(plan.Files[0].Source); err != nil {
				t.Fatal("source preserved", err)
			}
			var assets int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&assets); err != nil || assets != map[bool]int{true: 1, false: 0}[sample.code == ""] {
				t.Fatal("no unverified asset", assets, err)
			}
			var rows []byte
			if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='import_receipt' AND id=?`, plan.ID+"/"+plan.Files[0].ID).Scan(&rows); err != nil || bytes.Contains(rows, []byte("owned-private-transfer-result")) {
				t.Fatal("private module result encrypted", err)
			}
		})
	}
}

func TestOriginalTransferModuleAssetCommitFailureColdRecovery(t *testing.T) {
	testOriginalTransferModuleAssetCommitFailureColdRecovery(t, false)
}

func TestOriginalTransferModuleReturnedTargetColdRecovery(t *testing.T) {
	testOriginalTransferModuleAssetCommitFailureColdRecovery(t, true)
}

func TestOriginalTransferNativeNamingPublishedColdRecovery(t *testing.T) {
	testOriginalTransferModuleAssetCommitFailureColdRecovery(t, false, true)
}

func TestOriginalTransferAuxiliaryNativePublishedColdRecovery(t *testing.T) {
	testOriginalTransferModuleAssetCommitFailureColdRecovery(t, false, true, true)
}

func testOriginalTransferModuleAssetCommitFailureColdRecovery(t *testing.T, returnedTarget bool, nativeFallback ...bool) {
	a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	mapped := plan.Files[0].Target
	method := "module_transfer"
	if len(nativeFallback) > 0 && nativeFallback[0] {
		method = "module_execute"
		mapped = filepath.Join(a.Settings().Roots[0].Path, "Cold native path", "Actual native.mkv")
		cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": method}
		cfg["result"] = nil
		cfg["event_mutations"] = map[string]any{"transfer.rename": map[string]any{"updated": true, "updated_str": "Cold native path/Actual native.mkv"}}
		cfg["rename_source_extension"] = true
		raw, _ := json.Marshal(cfg)
		if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
			t.Fatal(err)
		}
		if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
			t.Fatal(err)
		}
	}
	if returnedTarget {
		mapped = filepath.Join(a.Settings().Roots[0].Path, "Cold returned path", "Actual returned.mkv")
		configureOriginalTransferTarget(t, a, object, cfg, mapped)
	}
	wantCalls, wantEvents := 1, 1
	var auxiliary string
	if len(nativeFallback) > 1 && nativeFallback[1] {
		auxiliary = filepath.Join(filepath.Dir(plan.Files[0].Source), "Owned.2024.srt")
		importWrite(t, auxiliary, []byte("owned cold subtitle"))
		var err error
		plan, err = a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: plan.Files[0].Source}}})
		if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
			t.Fatal("cold auxiliary preparation", plan, err)
		}
		wantCalls, wantEvents = 2, 2
	}
	job := overwriteStart(t, a, plan)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_module_asset_failure BEFORE INSERT ON records WHEN NEW.kind='asset' BEGIN SELECT RAISE(ABORT,'owned_module_asset_failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.runImport(ctx, job, plan.ID, nil); err == nil {
		t.Fatal("actual asset SQL fault not reached")
	}
	a.wg.Wait()
	before := ownedModuleRead(t, object)
	if before.Calls[method] != wantCalls {
		t.Fatal("actual external write", before.Calls)
	}
	var snapshot struct {
		Events []json.RawMessage `json:"transfer_events"`
		Trace  []string          `json:"transfer_trace"`
	}
	if err := object.readSession(ctx, "snapshot", &snapshot); err != nil || len(snapshot.Events) != wantEvents {
		t.Fatal("source callback follows physical publication/history before unrelated asset registration", len(snapshot.Events), wantEvents, err)
	}
	if history, e := a.transferHistoryItem(ctx, plan.Files[0].ID); e != nil || history.Status != "success" {
		t.Fatal("source history committed before asset SQL fault", history, e)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_module_asset_failure`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err := cold.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	value, ok := cold.pluginRunningInstances.Load(ownedModuleID)
	if !ok {
		t.Fatal("cold actual worker absent")
	}
	instance := value.(*originalPluginInstance)
	if after := ownedModuleRead(t, instance); after.PID == before.PID || after.Calls[method] != 0 {
		t.Fatal("cold process not replaced or premature replay")
	}
	if _, err := cold.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := cold.claim(ctx)
	if err != nil || recovered.ID != job.ID || recovered.Generation <= job.Generation {
		t.Fatal("cold claim", recovered, err)
	}
	if err := cold.runImport(ctx, recovered, plan.ID, nil); err != nil {
		t.Fatal("actual cold receipt adoption", err)
	}
	cold.wg.Wait()
	if last := ownedModuleRead(t, instance); last.Calls[method] != 0 {
		t.Fatal("published plugin file replayed after cold recovery", last.Calls)
	}
	if err := instance.readSession(ctx, "snapshot", &snapshot); err != nil || len(snapshot.Events) != 0 {
		t.Fatal("cold recovery must not replay committed callbacks", len(snapshot.Events), err)
	}
	if len(nativeFallback) > 0 && nativeFallback[0] {
		for _, kind := range snapshot.Trace {
			if kind == "transfer.rename.build" || kind == "transfer.rename" || kind == method {
				t.Fatal("published native recovery repeated naming/provider", snapshot.Trace)
			}
		}
	}
	var actual ImportPlan
	if err := cold.Store.Get(ctx, "import_plan", plan.ID, &actual); err != nil || actual.Files[0].Status != "imported" || actual.Files[0].Target != mapped {
		t.Fatal("actual recovered registration", actual, err)
	}
	if auxiliary != "" {
		var meta importMetadata
		if err := cold.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil || len(meta.Sidecars[actual.Files[0].ID]) != 1 {
			t.Fatal("cold independent auxiliary facts", meta, err)
		}
		side := meta.Sidecars[actual.Files[0].ID][0]
		if side.Target != filepath.Join(cold.Settings().Roots[0].Path, "Cold native path", "Actual native.srt") {
			t.Fatal("cold auxiliary name changed", side)
		}
		source, e := os.Stat(auxiliary)
		target, e2 := os.Stat(side.Target)
		if e != nil || e2 != nil || !os.SameFile(source, target) {
			t.Fatal("cold actual auxiliary hardlink", e, e2)
		}
	}
}

func TestOriginalTransferModuleFailureEndsWorkerWithoutAutomaticRetry(t *testing.T) {
	a, object, plan, config := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	config["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
	config["result"] = false
	encoded, _ := json.Marshal(config)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); a.worker(runCtx) }()
	defer func() { cancel(); <-done }()
	var actual Job
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		actual, err = scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
		if err != nil {
			t.Fatal(err)
		}
		if actual.State != "queued" && actual.State != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if actual.State != "failed" || actual.ErrorCode != "PLUGIN_TRANSFER_FAILED" || actual.Attempt != 1 {
		t.Fatal("fixed source task false ends this attempt and UI must recover", actual.State, actual.ErrorCode, actual.Attempt)
	}
	if after := ownedModuleRead(t, object); after.Calls["module_execute"] != 1 {
		t.Fatal("actual worker invocation", after.Calls)
	}
	var held int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE intent_id=?`, plan.ID).Scan(&held); err != nil || held != 0 {
		t.Fatal("rejected manual transfer must release its own unpublished slot for a new action", held, err)
	}
}
