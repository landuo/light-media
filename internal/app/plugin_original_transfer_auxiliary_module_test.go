package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalTransferAuxiliaryModuleNativePrecedence(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-naming-order-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name  string
			Trace []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"subtitle", "audio"} {
		for _, outcome := range []string{"none", "false", "model_true_effect"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
				ctx := context.Background()
				ext := ".srt"
				if kind == "audio" {
					ext = ".flac"
				}
				input := strings.TrimSuffix(first.Files[0].Source, ".mkv") + ext
				importWrite(t, input, []byte("owned auxiliary bytes"))
				plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: first.Files[0].Source}}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatal(plan, err)
				}
				var meta importMetadata
				if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil || len(meta.Sidecars[plan.Files[0].ID]) != 1 {
					t.Fatal(meta, err)
				}
				method := "module_execute"
				mainOutcome := "none"
				cfg["result"] = nil
				if outcome == "false" {
					cfg["transfer_results"] = map[string]any{input: false}
				}
				if outcome == "model_true_effect" {
					method = "module_transfer"
					mainOutcome = outcome
					mapped := filepath.Join(a.Settings().Roots[0].Path, "Independent module", "Mapped"+ext)
					model := func(target string) any {
						return map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "target_item": originalTransferPreviewItem(target, "file", 22), "need_scrape": false}}
					}
					cfg["transfer_targets"] = map[string]string{plan.Files[0].Source: plan.Files[0].Target, input: mapped}
					cfg["transfer_results"] = map[string]any{plan.Files[0].Source: model(plan.Files[0].Target), input: model(mapped)}
				}
				cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": method}
				cfg["event_mutations"] = map[string]any{"transfer.rename": map[string]any{"updated": true, "updated_str": "Deferred native/Named.mkv"}}
				cfg["rename_source_extension"] = true
				encoded, _ := json.Marshal(cfg)
				if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
					t.Fatal(err)
				}
				if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
					t.Fatal(err)
				}
				var before struct {
					Trace []string `json:"transfer_trace"`
				}
				if err = object.readSession(ctx, "snapshot", &before); err != nil || len(before.Trace) != 0 {
					t.Fatal("preparation emitted native names", before, err)
				}
				job := overwriteStart(t, a, plan)
				err = a.runImport(ctx, job, plan.ID, nil)
				if (err != nil) != (outcome == "false") {
					t.Fatal("fixed ordinary dispatch outcome", err)
				}
				var after struct {
					Trace []string `json:"transfer_trace"`
				}
				if err = object.readSession(ctx, "snapshot", &after); err != nil {
					t.Fatal(err)
				}
				want := []string{}
				for _, key := range []string{mainOutcome, outcome + "_" + kind} {
					found := false
					for _, sample := range oracle.Cases {
						if sample.Name == key {
							for _, call := range sample.Trace {
								if call == "plugin" {
									call = method
								}
								want = append(want, call)
							}
							found = true
							break
						}
					}
					if !found {
						t.Fatal("fixed source case absent", key)
					}
				}
				if !reflect.DeepEqual(after.Trace, want) {
					t.Fatal("each file storage→module→native-only name", after.Trace, want)
				}
				if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
					t.Fatal(err)
				}
				actual := meta.Sidecars[plan.Files[0].ID][0]
				if outcome == "none" && actual.Target != filepath.Join(a.Settings().Roots[0].Path, "Deferred native", "Named"+ext) {
					t.Fatal("actual native auxiliary name", actual)
				}
				if outcome == "model_true_effect" && actual.Target != filepath.Join(a.Settings().Roots[0].Path, "Independent module", "Mapped"+ext) {
					t.Fatal("actual returned auxiliary target", actual)
				}
				info, e := os.Stat(actual.Target)
				if outcome == "false" {
					if !os.IsNotExist(e) {
						t.Fatal("false wrote auxiliary", e)
					}
				} else {
					src, e2 := os.Stat(input)
					if e != nil || e2 != nil || !os.SameFile(src, info) {
						t.Fatal("real selected auxiliary hardlink", e, e2)
					}
					var actualPlan ImportPlan
					if e = a.Store.Get(ctx, "import_plan", plan.ID, &actualPlan); e != nil {
						t.Fatal(e)
					}
					if e = a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, actualPlan, meta); e != nil {
						t.Fatal("actual sealed auxiliary target", e)
					}
				}
			})
		}
	}
}
