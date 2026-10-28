package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOriginalTransferPreviewActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-preview-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name         string
			Plugin       json.RawMessage
			Throws       bool
			Result       []json.RawMessage
			Trace        []string
			PreviewItems []struct{ Target *string } `json:"preview_items"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 10 {
		t.Fatal("fixed preview oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			ctx := context.Background()
			source, library := t.TempDir(), t.TempDir()
			settings := a.Settings()
			settings.AllowedRoots = []string{source}
			settings.Roots = []Root{{ID: "root", Name: "Owned", Path: library, Kind: "movie"}}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(source, "Source.mkv")
			target := filepath.Join(library, "Owned (2024)", "Source.mkv")
			if err := os.WriteFile(input, []byte("owned preview content"), 0640); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("preexisting target"), 0640); err != nil {
				t.Fatal(err)
			}
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			method := "module_execute"
			if sample.Throws {
				method = "module_fail"
			}
			config := map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "transfer": method}, "media_result": full, "result": json.RawMessage(strings.ReplaceAll(string(sample.Plugin), "{root}", library)), "event_mutations": map[string]any{"transfer.rename": map[string]any{"updated": true, "updated_str": "Owned (2024)/Source.mkv"}}}
			encoded, _ := json.Marshal(config)
			saveOwnedModule(t, a, cookie, object, string(encoded))
			var before, after struct {
				TransferTrace []string                   `json:"transfer_trace"`
				Calls         map[string]int             `json:"calls"`
				Inputs        map[string]json.RawMessage `json:"inputs"`
			}
			if err := object.readSession(ctx, "snapshot", &before); err != nil {
				t.Fatal(err)
			}
			plan, err := a.previewImport(ctx, ImportRequest{originalModulePreview: true, MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if err != nil || len(plan.Files) != 1 {
				t.Fatal(plan, err)
			}
			success := len(sample.Result) > 0 && string(sample.Result[0]) == "true"
			if (plan.Files[0].Status == "ready") != success {
				t.Fatal("fixed source preview result", sample.Result, plan.Files)
			}
			if success {
				want := ""
				if len(sample.PreviewItems) > 0 && sample.PreviewItems[0].Target != nil {
					want = strings.ReplaceAll(*sample.PreviewItems[0].Target, "{root}", library)
				}
				if plan.Files[0].Target != want {
					t.Fatal("actual preview callback target", plan.Files[0].Target, want)
				}
			}
			if err := object.readSession(ctx, "snapshot", &after); err != nil {
				t.Fatal(err)
			}
			trace := after.TransferTrace[len(before.TransferTrace):]
			wantTrace := []string{"storage.operation", "storage.operation", method}
			for _, kind := range sample.Trace {
				if kind == "native_preview" {
					wantTrace = append(wantTrace, "transfer.rename.build", "transfer.rename")
				}
			}
			if !reflect.DeepEqual(trace, wantTrace) {
				t.Fatal("storage/module/native rename order", trace, wantTrace)
			}
			var call struct{ Kwargs map[string]json.RawMessage }
			if json.Unmarshal(after.Inputs[method], &call) != nil || len(call.Kwargs) != 14 || string(call.Kwargs["preview"]) != "true" {
				t.Fatal("same 14 keywords + explicit preview", string(after.Inputs[method]))
			}
			if after.Calls["transfer_event"] != before.Calls["transfer_event"] {
				t.Fatal("preview sent normal callback", after.Calls)
			}
			for _, kind := range []string{"import_receipt", "asset", "job"} {
				var count int
				if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind=?`, kind).Scan(&count); err != nil || count != 0 {
					t.Fatal("preview publication/history", kind, count, err)
				}
			}
			var count int
			if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations`).Scan(&count); err != nil || count != 0 {
				t.Fatal("preview media slot", count, err)
			}
			for path, want := range map[string]string{input: "owned preview content", target: "preexisting target"} {
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Fatal("preview changed actual files", path, string(got), err)
				}
			}
			entries, err := os.ReadDir(library)
			if err != nil || len(entries) != 1 {
				t.Fatal("unexpected preview directory effect", entries, err)
			}
		})
	}
}

// The existing Go preparation API produces an executable frozen plan, unlike
// MoviePilot's transfer endpoint with preview=true. Keep its auxiliary-file
// contract; the source preview flag belongs to the manual-organize endpoint.
func TestOriginalTransferPublicPreparationRetainsSidecars(t *testing.T) {
	a, cookie, object := ownedModuleFixture(t, true)
	ctx := context.Background()
	source, library := t.TempDir(), t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = []string{source}
	settings.Roots = []Root{{ID: "root", Path: library, Kind: "movie"}}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(source, "Owned.2024.mkv")
	for path, text := range map[string]string{input: "owned video", filepath.Join(source, "Owned.2024.zh.srt"): "1\n00:00:00,000 --> 00:00:01,000\nOwned subtitle\n"} {
		if err := os.WriteFile(path, []byte(text), 0640); err != nil {
			t.Fatal(err)
		}
	}
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media"}, "media_result": full})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	body, _ := json.Marshal(ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	r := callTest(t, a.Handler(), http.MethodPost, "/api/import/preview", string(body), cookie, "")
	var response struct{ Data ImportPlan }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || len(response.Data.Files) != 1 || response.Data.Files[0].Status != "ready" {
		t.Fatal("real frozen preparation", r.Code, r.Body.String())
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", response.Data.ID, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Sidecars[response.Data.Files[0].ID]) != 1 {
		t.Fatal("public executable plan lost its real subtitle", meta.Sidecars)
	}
}
