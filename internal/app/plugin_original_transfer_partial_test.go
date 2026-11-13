package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalTransferPartialTasksFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-task-loop-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name              string
			Outcomes          []any
			Completed, Failed int
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("fixed manual task loop oracle", err)
	}
	for _, sample := range oracle.Cases {
		if sample.Name == "subtitle_exception" {
			continue
		} // Actual SQL callback interruption has a separate test.
		t.Run(sample.Name, func(t *testing.T) {
			a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			base := strings.TrimSuffix(first.Files[0].Source, ".mkv")
			importWrite(t, base+".srt", []byte("owned partial subtitle"))
			importWrite(t, base+".flac", []byte("owned partial audio"))
			plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: first.Files[0].Source}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("real preview", plan, err)
			}
			var meta importMetadata
			if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil || len(meta.Sidecars[plan.Files[0].ID]) != 2 {
				t.Fatal("real related tasks", err)
			}
			files := []PlanFile{plan.Files[0]}
			for i, side := range meta.Sidecars[plan.Files[0].ID] {
				files = append(files, originalTransferSideFile(plan.Files[0], side, i))
			}
			results := map[string]any{}
			for i, file := range files {
				if sample.Outcomes[i] == false {
					results[file.Source] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned rejection"}}
				}
			}
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
			cfg["result"], cfg["transfer_results"] = nil, results
			encoded, _ := json.Marshal(cfg)
			if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
				t.Fatal(err)
			}
			if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			if err = a.runImport(ctx, job, plan.ID, nil); err == nil {
				t.Fatal("partial source loop must report failure")
			}
			a.wg.Wait()
			if n := ownedModuleRead(t, object).Calls["module_execute"]; n != 3 {
				t.Fatalf("fixed loop continues after failure: calls=%d want=3", n)
			}
			var actual ImportPlan
			if err = a.Store.Get(ctx, "import_plan", plan.ID, &actual); err != nil || actual.State != "partial" {
				t.Fatal("partial persisted", actual, err)
			}
			for i, file := range files {
				stat, e := os.Stat(file.Target)
				if sample.Outcomes[i] == true {
					src, se := os.Stat(file.Source)
					if e != nil || se != nil || !os.SameFile(stat, src) {
						t.Fatal("successful task real hardlink", file.Source, e, se)
					}
					var owned managedFile
					if e = a.Store.Get(ctx, "managed_file", tokenHash(file.Target), &owned); e != nil || owned.Snapshot != planSnapshot(file) || owned.AssetID != actual.Files[0].AssetID {
						t.Fatal("successful independent file ownership", file.Source, owned, e)
					}
				} else if !os.IsNotExist(e) {
					t.Fatal("rejected task must not publish", file.Source, e)
				}
			}
			var successes, failures, assets, slots int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='original_transfer_callback' AND json_extract(payload,'$.plan_id')=? AND json_extract(payload,'$.file.status')='imported'`, plan.ID).Scan(&successes); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='original_transfer_callback' AND json_extract(payload,'$.plan_id')=? AND json_extract(payload,'$.file.status')='error'`, plan.ID).Scan(&failures); err != nil {
				t.Fatal(err)
			}
			if successes != sample.Completed || failures != sample.Failed {
				t.Fatal("source per-task history", successes, failures, sample.Completed, sample.Failed)
			}
			job.State, job.ErrorMessage = "failed", "owned rejection"
			projection, err := a.originalTransferJobResult(ctx, job)
			if err != nil || projection.Total != 3 || projection.Failed != sample.Failed {
				t.Fatal("actual job per-file summary", projection, err)
			}
			for i, item := range projection.Items {
				if (item.Status == "success") != (sample.Outcomes[i] == true) {
					t.Fatal("job result changed a successful sibling", item)
				}
			}
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&assets)
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots`).Scan(&slots)
			wantAsset := 0
			if sample.Outcomes[0] == true {
				wantAsset = 1
			}
			if assets != wantAsset || slots != wantAsset {
				t.Fatal("only real main publication enters media inventory", assets, slots, wantAsset)
			}
			before := ownedModuleRead(t, object).Calls["module_execute"]
			if err = a.runImport(ctx, job, plan.ID, nil); err == nil {
				t.Fatal("recovery must retain terminal failure")
			}
			if after := ownedModuleRead(t, object).Calls["module_execute"]; after != before {
				t.Fatal("recovery repeated physical task", before, after)
			}
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			fresh, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { fresh.Close() })
			if err = fresh.initializeOriginalPlugins(ctx); err != nil {
				t.Fatal(err)
			}
			if err = fresh.runImport(ctx, job, plan.ID, nil); err == nil {
				t.Fatal("cold recovery erased rejected task")
			}
			value, ok := fresh.pluginRunningInstances.Load(ownedModuleID)
			if !ok {
				t.Fatal("cold plugin missing")
			}
			if n := ownedModuleRead(t, value.(*originalPluginInstance)).Calls["module_execute"]; n != 0 {
				t.Fatal("cold recovery repeated actual provider", n)
			}
		})
	}
}

func TestOriginalTransferCallbackExceptionContinuesAndColdRecovers(t *testing.T) {
	a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	base := strings.TrimSuffix(first.Files[0].Source, ".mkv")
	importWrite(t, base+".srt", []byte("owned callback subtitle"))
	importWrite(t, base+".flac", []byte("owned callback audio"))
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: first.Files[0].Source}}})
	if err != nil {
		t.Fatal(err)
	}
	cfg["modules"], cfg["result"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}, nil
	encoded, _ := json.Marshal(cfg)
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
		t.Fatal(err)
	}
	if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	// A real per-task SQL exception, matching the pinned loop's exception
	// branch; only the first callback fails, so later callbacks must commit.
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_partial_callback BEFORE INSERT ON records WHEN NEW.kind='original_transfer_callback' AND json_extract(NEW.payload,'$.file.parent_file_id') IS NULL BEGIN SELECT RAISE(ABORT,'owned callback exception');END`); err != nil {
		t.Fatal(err)
	}
	job := overwriteStart(t, a, plan)
	if err = a.runImport(ctx, job, plan.ID, nil); err == nil {
		t.Fatal("actual callback exception absent")
	}
	a.wg.Wait()
	if n := ownedModuleRead(t, object).Calls["module_execute"]; n != 3 {
		t.Fatal("exception prevented remaining tasks", n)
	}
	var callbacks, managed int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='original_transfer_callback'`).Scan(&callbacks)
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='managed_file'`).Scan(&managed)
	if callbacks != 2 || managed != 3 {
		t.Fatal("independent callbacks and actual ownership", callbacks, managed)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_partial_callback`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })
	if err = fresh.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if err = fresh.jobCancelled(ctx, job); fileCode(err) != "JOB_CANCELLED" {
		t.Fatal("cold restart did not revoke the old execution generation", err)
	}
	recovered, err := fresh.claim(ctx)
	if err != nil || recovered.ID != job.ID || recovered.Generation <= job.Generation {
		t.Fatal("cold callback job was not durably reclaimed", recovered, err)
	}
	if err = fresh.runImport(ctx, recovered, plan.ID, nil); err != nil {
		t.Fatal("cold callback-only completion", err)
	}
	fresh.wg.Wait()
	value, ok := fresh.pluginRunningInstances.Load(ownedModuleID)
	if !ok {
		t.Fatal("cold plugin missing")
	}
	if n := ownedModuleRead(t, value.(*originalPluginInstance)).Calls["module_execute"]; n != 0 {
		t.Fatal("callback recovery repeated transfer", n)
	}
	_ = fresh.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='original_transfer_callback'`).Scan(&callbacks)
	var actual ImportPlan
	if err = fresh.Store.Get(ctx, "import_plan", plan.ID, &actual); err != nil || callbacks != 3 || actual.State != "complete" {
		t.Fatal("all callbacks durably completed", callbacks, actual.State, err)
	}
}
