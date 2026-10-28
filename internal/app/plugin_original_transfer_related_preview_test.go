package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

// Fixed manual_transfer plans main + related tasks and appends a fallback row
// when a raw false never reaches _preview_callback (transfer.py:3538-3653).
func TestOriginalTransferRelatedManualPreviewActualSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-related-preview-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name   string
			Result []json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 3 {
		t.Fatal("fixed manual preview loop oracle", err)
	}
	for _, kind := range []string{"native", "raw-false-subtitle", "typed-veto-subtitle"} {
		t.Run(kind, func(t *testing.T) {
			var batch struct {
				Summary struct{ Total, Failed int }
				Items   []struct {
					Source                 string
					Target, Title, Message *string
					Success                bool
				}
			}
			found := false
			for _, sample := range oracle.Cases {
				if sample.Name == kind {
					if len(sample.Result) != 2 || json.Unmarshal(sample.Result[1], &batch) != nil {
						t.Fatal("fixed preview batch", sample.Name)
					}
					found = true
					break
				}
			}
			if !found {
				t.Fatal("fixed related preview case missing", kind)
			}
			a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			input := first.Files[0].Source
			library := a.Settings().Roots[0].Path
			subtitle := strings.TrimSuffix(input, ".mkv") + ".zh.srt"
			audio := strings.TrimSuffix(input, ".mkv") + ".flac"
			importWrite(t, subtitle, []byte("owned preview subtitle"))
			importWrite(t, audio, []byte("owned preview audio"))
			expected := map[string]string{input: filepath.Join(library, "Owned (2024)", "Owned (2024).mkv"), subtitle: filepath.Join(library, "Owned (2024)", "Owned (2024).default.chi.zh-cn.srt"), audio: filepath.Join(library, "Owned (2024)", "Owned (2024).flac")}
			for _, target := range expected {
				importWrite(t, target, []byte("preexisting destination must stay"))
			}
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
			cfg["result"] = nil
			if kind == "raw-false-subtitle" {
				cfg["transfer_results"] = map[string]any{subtitle: false}
			}
			if kind == "typed-veto-subtitle" {
				cfg["transfer_results"] = map[string]any{subtitle: map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned subtitle veto"}}}
			}
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{input}, MediaType: "movie", TMDBID: 123, TargetPath: library, Mode: "hardlink", Scraping: importFlag(false), Preview: true})
			if err != nil || out.Total != batch.Summary.Total || out.Failed != batch.Summary.Failed || len(out.Items) != len(batch.Items) || out.Submitted != 0 {
				t.Fatal("independent per-task preview/fallback rows", out, err)
			}
			if out.Items[0].Source != input {
				t.Fatal("source plans main before related tasks", out.Items)
			}
			seen := map[string]bool{}
			for _, item := range out.Items {
				seen[item.Source] = true
				matched := false
				for _, original := range batch.Items {
					if strings.ReplaceAll(original.Source, "{root}/source", filepath.Dir(input)) != item.Source {
						continue
					}
					matched = true
					target := ""
					if original.Target != nil {
						target = strings.ReplaceAll(*original.Target, "{root}/library", library)
					}
					if item.Target != target || (item.Status == "ready") != original.Success || (item.Media.Title != "") != (original.Title != nil) {
						t.Fatal("actual fixed source preview callback/fallback fields", item, original)
					}
				}
				if !matched {
					t.Fatal("unexpected preview task", item.Source)
				}
				if item.Source == subtitle && kind != "native" {
					if item.Status != "failed" || item.Target != "" {
						t.Fatal("source failed preview destination", item)
					}
					if kind == "raw-false-subtitle" && item.Media.Title != "" || kind == "typed-veto-subtitle" && (item.Media.Title != "Owned" || item.Error != "owned subtitle veto") {
						t.Fatal("raw fallback and concrete model callback differ", item)
					}
				} else if item.Status != "ready" || item.Target != expected[item.Source] {
					t.Fatal("source native preview result", item, expected[item.Source])
				}
				if _, err := a.executeImport(ctx, item.PlanID, nil); fileCode(err) != "PLUGIN_PREVIEW_ONLY" {
					t.Fatal("explicit preview is not an executable preparation", err)
				}
			}
			if !seen[input] || !seen[subtitle] || !seen[audio] {
				t.Fatal("related preview identity", seen)
			}
			for _, target := range expected {
				if data, e := os.ReadFile(target); e != nil || string(data) != "preexisting destination must stay" {
					t.Fatal("preview probed/published into an existing destination", target, e)
				}
			}
			var observed struct {
				Trace []string `json:"transfer_trace"`
				Calls map[string]int
			}
			if err := object.readSession(ctx, "snapshot", &observed); err != nil {
				t.Fatal(err)
			}
			wantTrace := 15
			if kind != "native" {
				wantTrace = 13
			}
			if len(observed.Trace) != wantTrace || observed.Calls["module_execute"] != 3 || observed.Calls["transfer_event"] != 0 {
				t.Fatal("every related preview storage/module/native-only name with no normal callback", observed)
			}
			for _, record := range []string{"import_receipt", "asset", "original_transfer_callback"} {
				var count int
				if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind=?`, record).Scan(&count); e != nil || count != 0 {
					t.Fatal("preview published persistent execution", record, count, e)
				}
			}
			var jobs int
			if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='import'`).Scan(&jobs); e != nil || jobs != 0 {
				t.Fatal("preview created job", jobs, e)
			}
		})
	}
}
