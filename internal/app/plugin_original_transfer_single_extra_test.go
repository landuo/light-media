package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pinned do_transfer._plan_file_items returns only the selected extra and
// inherits the unique main's metadata; it never transfers that main again.
func TestOriginalSingleExtraManualActualSDK(t *testing.T) {
	for _, ext := range []string{".zh.srt", ".flac"} {
		t.Run(ext, func(t *testing.T) {
			a, object, original, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			main := original.Files[0].Source
			extra := strings.TrimSuffix(main, ".mkv") + ext
			importWrite(t, extra, []byte("owned single extra"))
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
			cfg["result"] = nil
			raw, _ := json.Marshal(cfg)
			if _, e := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
				t.Fatal(e)
			}
			if e := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
				t.Fatal(e)
			}
			input := manualOrganizeRequest{MinFilesize: 10, Paths: []string{extra}, MediaType: "movie", TMDBID: 123, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", Scraping: importFlag(false), Preview: true}
			out, e := a.manualOrganize(ctx, input)
			if e != nil || len(out.Items) != 1 || out.Failed != 0 || out.Items[0].Source != extra {
				t.Fatal("fixed single-extra preview", out, e)
			}
			before := ownedModuleRead(t, object)
			input.Preview = false
			out, e = a.manualOrganize(ctx, input)
			if e != nil || out.Submitted != 1 || out.Failed != 0 {
				t.Fatal("single extra actual submission", out, e)
			}
			var plan ImportPlan
			if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); e != nil || len(plan.Files) != 1 || plan.Files[0].Source != extra {
				t.Fatal(plan, e)
			}
			job := overwriteStart(t, a, plan)
			if e = a.runImport(ctx, job, plan.ID, nil); e != nil {
				t.Fatal(e)
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
				t.Fatal("source only one physical task", before.Calls, after.Calls)
			}
			var final ImportPlan
			if e = a.Store.Get(ctx, "import_plan", plan.ID, &final); e != nil || final.State != "complete" {
				t.Fatal(final, e)
			}
			src, se := os.Stat(extra)
			dst, de := os.Stat(final.Files[0].Target)
			if se != nil || de != nil || !os.SameFile(src, dst) {
				t.Fatal("actual extra hardlink", se, de)
			}
			if _, e = os.Stat(original.Files[0].Target); !os.IsNotExist(e) {
				t.Fatal("must not transfer main", e)
			}
			for _, query := range []string{"SELECT COUNT(*) FROM records WHERE kind='asset'", "SELECT COUNT(*) FROM asset_slots", "SELECT COUNT(*) FROM reservations"} {
				var n int
				if e = a.Store.DB.QueryRow(query).Scan(&n); e != nil || n != 0 {
					t.Fatal("no false video inventory", query, n, e)
				}
			}
			var owned managedFile
			if e = a.Store.Get(ctx, "managed_file", tokenHash(final.Files[0].Target), &owned); e != nil || !auxiliaryMaterialKind(owned.Kind) {
				t.Fatal("independent material ownership", owned, e)
			}
			if strings.HasSuffix(ext, "srt") && !strings.HasSuffix(filepath.Base(final.Files[0].Target), ".srt") {
				t.Fatal(final.Files[0])
			}
			if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); e != nil {
				t.Fatal(e)
			}
			history, e := a.transferHistoryItem(ctx, final.Files[0].ID)
			if e != nil {
				t.Fatal(e)
			}
			oldTarget := final.Files[0].Target
			if e = os.MkdirAll(filepath.Join(a.Settings().Roots[0].Path, "again"), 0750); e != nil {
				t.Fatal(e)
			}
			input = manualOrganizeRequest{HistoryIDs: []string{history.ID}, FromHistory: true, TargetPath: filepath.Join(a.Settings().Roots[0].Path, "again"), Mode: "hardlink", Scraping: importFlag(false), Preview: true}
			preview, e := a.manualOrganize(ctx, input)
			if e != nil || preview.Failed != 0 || len(preview.Items) != 1 {
				t.Fatal("single-history preview", preview, e)
			}
			if _, e = os.Stat(oldTarget); e != nil {
				t.Fatal("preview retained old extra", e)
			}
			input.Preview = false
			out, e = a.manualOrganize(ctx, input)
			if e != nil || out.Submitted != 1 || out.Failed != 0 {
				t.Fatal("single-history resubmit", out, e)
			}
			if _, e = os.Stat(oldTarget); !os.IsNotExist(e) {
				t.Fatal("fixed ordinary manual cleans selected destination only", e)
			}
			if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); e != nil {
				t.Fatal(e)
			}
			job = overwriteStart(t, a, plan)
			if e = a.runImport(ctx, job, plan.ID, nil); e != nil {
				t.Fatal("single-history real run", e)
			}
			if _, e = os.Stat(original.Files[0].Target); !os.IsNotExist(e) {
				t.Fatal("history must not transfer main", e)
			}

		})
	}
}

func TestOriginalSingleExtraInheritedMetadataFixedSource(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-module-host/transfer-single-extra-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name      string
			Current   map[string]any
			Listing   []map[string]any
			Parent    bool
			Inherited []struct {
				MainPath string `json:"main_path"`
			}
			Trace []string
		}
	}
	if e != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 5 {
		t.Fatal(e)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			root := filepath.Dir(plan.Files[0].Source)
			project := func(v any) json.RawMessage {
				r, _ := json.Marshal(v)
				return json.RawMessage(strings.ReplaceAll(string(r), "{root}/source", root))
			}
			path := strings.ReplaceAll(sourceText(sample.Current["path"]), "{root}/source", root)
			importWrite(t, path, []byte("owned extra"))
			for _, entry := range sample.Listing {
				p := strings.ReplaceAll(sourceText(entry["path"]), "{root}/source", root)
				if p != plan.Files[0].Source && p != path {
					importWrite(t, p, []byte("owned sibling"))
				}
			}
			cfg["modules"] = map[string]string{"get_parent_item": "module_storage_parent", "list_files": "module_storage_siblings"}
			cfg["storage_parent"] = false
			if sample.Parent {
				cfg["storage_parent"] = map[string]any{"storage": "local", "type": "dir", "path": root + "/"}
			}
			cfg["storage_siblings"] = project(map[string]any{"__plugin_module_tuple": sample.Listing})
			encoded, _ := json.Marshal(cfg)
			if _, e = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); e != nil {
				t.Fatal(e)
			}
			if e = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
				t.Fatal(e)
			}
			got, e := a.originalSingleExtraMetadataPath(ctx, path)
			if e != nil {
				t.Fatal(e)
			}
			want := path
			if len(sample.Inherited) > 0 {
				want = strings.ReplaceAll(sample.Inherited[0].MainPath, "{root}/source", root)
			}
			if got != want {
				t.Fatal("fixed unique/ambiguous/no-parent metadata inheritance", got, want)
			}
			state := ownedModuleRead(t, object)
			if state.Calls["module_storage_parent"] != 1 || state.Calls["module_storage_siblings"] != len(sample.Trace)-1 {
				t.Fatal("one sibling collection", state.Calls, sample.Trace)
			}
		})
	}
}

func TestOriginalSingleExtraRetryActualSDK(t *testing.T) { originalFileRetryActualSDK(t, true) }
func TestOriginalMainRetryActualSDK(t *testing.T)        { originalFileRetryActualSDK(t, false) }
func originalFileRetryActualSDK(t *testing.T, auxiliary bool) {
	t.Helper()
	a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	extra := first.Files[0].Source
	if auxiliary {
		extra = strings.TrimSuffix(extra, ".mkv") + ".zh.srt"
		importWrite(t, extra, []byte("owned retry subtitle"))
	}
	cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute", "obtain_images": "module_images"}
	cfg["image_mode"] = "false"
	cfg["result"] = nil
	cfg["transfer_results"] = map[string]any{extra: map[string]any{"__plugin_transfer_info": map[string]any{"success": false, "message": "owned first rejection"}}}
	save := func() {
		raw, _ := json.Marshal(cfg)
		if _, e := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
			t.Fatal(e)
		}
		if e := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
			t.Fatal(e)
		}
	}
	save()
	out, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{extra}, MediaType: "movie", TMDBID: 123, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", Scraping: importFlag(false)})
	if e != nil || out.Submitted != 1 {
		t.Fatal(out, e)
	}
	var plan ImportPlan
	if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); e != nil {
		t.Fatal(e)
	}
	job := overwriteStart(t, a, plan)
	if e = a.runImport(ctx, job, plan.ID, nil); e == nil {
		t.Fatal("controlled first rejection")
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, job.ID); e != nil {
		t.Fatal(e)
	}
	history, e := a.transferHistoryItem(ctx, plan.Files[0].ID)
	if e != nil || !history.CanRetry {
		t.Fatal(history, e)
	}
	cfg["transfer_results"] = map[string]any{}
	save()
	before := ownedModuleRead(t, object)
	response, e := a.transferHistoryAPI(httptest.NewRequest("POST", "/api/history/transfers/"+history.ID+"/retry", strings.NewReader(`{"confirm":true}`)), "transfers/"+history.ID+"/retry")
	if e != nil {
		t.Fatal("fresh ordinary retry", e)
	}
	retried, ok := response.(Job)
	if !ok {
		t.Fatal("retry response", response)
	}
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(retried.Payload, &payload) != nil || payload.ID == plan.ID {
		t.Fatal("new operation, not stale failed receipt", retried)
	}
	var next ImportPlan
	if e = a.Store.Get(ctx, "import_plan", payload.ID, &next); e != nil {
		t.Fatal(e)
	}
	job = overwriteStart(t, a, next)
	if e = a.runImport(ctx, job, next.ID, nil); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	after := ownedModuleRead(t, object)
	if after.Calls["module_search_media"]-before.Calls["module_search_media"] != 1 || after.Calls["module_images"]-before.Calls["module_images"] != 1 || after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
		t.Fatal("one new recognition/images/transfer", before.Calls, after.Calls)
	}
	if auxiliary {
		if _, e = os.Stat(first.Files[0].Target); !os.IsNotExist(e) {
			t.Fatal("retry leaves main untransferred", e)
		}
	} else {
		src, e1 := os.Stat(extra)
		dst, e2 := os.Stat(first.Files[0].Target)
		if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
			t.Fatal("fresh main retry actual link", e1, e2)
		}
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); e != nil {
		t.Fatal(e)
	}
	latest, e := a.transferHistoryItem(ctx, history.ID)
	if e != nil || latest.ID != history.ID || latest.Status != "success" || latest.Source != extra {
		t.Fatal("stable independent history", latest, e)
	}

	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	fresh, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { fresh.Close() })
	if e = fresh.initializeOriginalPlugins(ctx); e != nil {
		t.Fatal(e)
	}
	if e = fresh.runImport(ctx, job, next.ID, nil); e != nil {
		t.Fatal("cold exact successful extra", e)
	}
	value, ok := fresh.pluginRunningInstances.Load(ownedModuleID)
	if !ok {
		t.Fatal("cold plugin missing")
	}
	state := ownedModuleRead(t, value.(*originalPluginInstance))
	if state.Calls["module_execute"] != 0 || state.Calls["module_images"] != 0 {
		t.Fatal("cold repeated writes", state.Calls)
	}
	afterCold, e := fresh.transferHistoryItem(ctx, history.ID)
	if e != nil || afterCold.Status != "success" || afterCold.Target != latest.Target {
		t.Fatal("cold stable auxiliary history", afterCold, e)
	}
}
