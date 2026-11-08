package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/catalog"
)

func TestTVDefaultNamingMatchesMoviePilotWithoutYear(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	series := catalog.Media{ID: 123, MediaType: "tv", Title: "Example: Series"}
	file := PlanFile{Source: filepath.Join(source, "Example.Series.S00E03-E04.MKV"),
		Season: 0, Episodes: []int{3, 4}, Part: "part1"}
	importWrite(t, file.Source, []byte("synthetic special"))
	media, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"name":"Example: Series","original_name":"Example: Series","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := a.originalTransferContextWithMedia(ctx, series, file, media)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(target, "Example： Series", "Season 0", "Example： Series - S00E03-E04-part1 - 第 3-4 集.MKV")
	native, err := a.originalTVImportTarget(ctx, series, file, target, file.Source)
	if err != nil || native != want {
		t.Fatalf("ordinary naming = %q, want %q, error %v", native, want, err)
	}
	module, err := a.originalTransferNativeRename(ctx, series, file, target, projection, nil, false)
	if err != nil || module != want {
		t.Fatalf("module naming = %q, want %q, error %v", module, want, err)
	}
}

// Fixed __handle_transfer chooses both storage operations, then calls transfer;
// the native FileManager provider alone enters TransHandler naming. Source
// dispatcher outcomes come from the pinned executable oracle, not this bridge.
func TestOriginalTransferExecutionNamesOnlyNativeFallback(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-naming-order-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name        string
			NativeCalls int `json:"native_calls"`
			Trace       []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"model_true_effect", "false", "none"} {
		t.Run(kind, func(t *testing.T) {
			core := -1
			var sourceTrace []string
			for _, sample := range oracle.Cases {
				if sample.Name == kind {
					core = sample.NativeCalls
					sourceTrace = sample.Trace
				}
			}
			if core < 0 {
				t.Fatal("missing fixed source case")
			}
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			var before struct {
				Trace []string `json:"transfer_trace"`
			}
			if err := object.readSession(ctx, "snapshot", &before); err != nil || len(before.Trace) != 0 {
				t.Fatal("executable preparation invoked native transfer events", before, err)
			}
			method := "module_transfer"
			if kind != "model_true_effect" {
				method = "module_execute"
				cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": method}
				cfg["result"] = nil
				if kind == "false" {
					cfg["result"] = false
				}
			}
			cfg["event_mutations"] = map[string]any{"transfer.rename": map[string]any{"updated": true, "updated_str": "Only native fallback/Actual native.mkv"}}
			encoded, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			err := a.runImport(ctx, job, plan.ID, nil)
			if (err != nil) != (kind == "false") {
				t.Fatal("fixed source result", err)
			}
			var after struct {
				Trace []string `json:"transfer_trace"`
			}
			if err := object.readSession(ctx, "snapshot", &after); err != nil {
				t.Fatal(err)
			}
			want := append([]string(nil), sourceTrace...)
			for i, name := range want {
				if name == "plugin" {
					want[i] = method
				}
			}
			if !reflect.DeepEqual(after.Trace, want) {
				t.Fatal("fixed storage/module/native-name order", after.Trace, want)
			}
			if core != 0 {
				var actual ImportPlan
				if err := a.Store.Get(ctx, "import_plan", plan.ID, &actual); err != nil {
					t.Fatal(err)
				}
				expected := filepath.Join(a.Settings().Roots[0].Path, "Only native fallback", "Actual native.mkv")
				if actual.Files[0].Target != expected {
					t.Fatal("native event result not adopted", actual.Files[0])
				}
				if _, err := os.Stat(expected); err != nil {
					t.Fatal("actual native publication", err)
				}
				if _, err := os.Stat(plan.Files[0].Target); !os.IsNotExist(err) {
					t.Fatal("prepared default path unexpectedly published", err)
				}
			}
		})
	}
}
