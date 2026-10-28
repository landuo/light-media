package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalBrowserMutationActualSDKSourceAndFiles(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/browser-mutation-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Action, Method, Mode string
			Value                json.RawMessage
			Success              bool
			SourceExists         bool `json:"source_exists"`
			TargetExists         bool `json:"target_exists"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 27 {
		t.Fatal("fixed real storage oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Action+"/"+sample.Mode, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			ctx := context.Background()
			folder := t.TempDir()
			old := filepath.Join(folder, "Owned.mkv")
			target := filepath.Join(folder, "New")
			if err := os.WriteFile(old, []byte("owned content"), 0640); err != nil {
				t.Fatal(err)
			}
			settings := a.Settings()
			settings.AllowedRoots, settings.Roots = []string{folder}, nil
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			method, mode := "module_execute", "no_effect"
			switch sample.Mode {
			case "effect_true", "effect_false", "effect_none":
				method, mode = "module_storage", "effect"
			case "effect_throw":
				method, mode = "module_storage", "effect_then_throw"
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{sample.Method: method}, "result": sample.Value, "storage_root": folder, "storage_action": sample.Action, "storage_mode": mode})
			saveOwnedModule(t, a, cookie, object, string(config))
			before := ownedModuleRead(t, object)
			request := browserOperationRequest{Action: sample.Action, RootID: fileBrowserRoot(t, a, folder).ID, Path: folder, Name: "New"}
			if sample.Action != "mkdir" {
				request.Paths = []string{old}
			}
			payload, _ := json.Marshal(request)
			preview := callTest(t, a.Handler(), "POST", "/api/files/operation-preview", string(payload), cookie, "")
			var out struct{ Data browserOperation }
			if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &out) != nil || out.Data.ID == "" {
				t.Fatal("preview", preview.Body.String())
			}
			confirm, _ := json.Marshal(map[string]any{"plan_id": out.Data.ID, "confirm": true})
			result := callTest(t, a.Handler(), "POST", "/api/files/operation-confirm", string(confirm), cookie, "")
			// Source accepts a bare true without touching storage. The existing host
			// requires the promised physical effect; this explicit adaptation is stricter.
			wantStatus := 400
			if sample.Success && sample.Mode != "true_no_effect" {
				wantStatus = 200
			}
			if result.Code != wantStatus {
				t.Fatal("source truth and physical effect", result.Code, wantStatus, result.Body.String())
			}
			_, oldErr := os.Stat(old)
			_, targetErr := os.Stat(target)
			if (oldErr == nil) != sample.SourceExists || (targetErr == nil) != sample.TargetExists {
				t.Fatal("fixed source physical effects", oldErr, targetErr, sample.SourceExists, sample.TargetExists)
			}
			var saved browserOperation
			if err := a.Store.Get(ctx, "file_operation", out.Data.ID, &saved); err != nil {
				t.Fatal(err)
			}
			if sample.Action == "delete" && !sample.SourceExists {
				if !saved.Items[0].Done {
					t.Fatal("actual removal unrecorded")
				}
				if saved.Items[0].Unrecoverable {
					if _, err := a.restoreBrowserOperation(ctx, saved.ID); fileCode(err) != "FILE_RESTORE_UNAVAILABLE" {
						t.Fatal("plugin direct deletion cannot promise restore", err)
					}
				} else if _, err := os.Stat(saved.Items[0].Target); err != nil {
					t.Fatal("native recycle bytes", err)
				}
			}
			after := ownedModuleRead(t, object)
			if after.Calls[method]-before.Calls[method] != 1 {
				t.Fatal("actual SDK call", after, before)
			}
			var input struct {
				Args   []json.RawMessage
				Kwargs map[string]json.RawMessage
			}
			if json.Unmarshal(after.Inputs[method], &input) != nil || len(input.Args) != 0 {
				t.Fatal("ordinary module kwargs", string(after.Inputs[method]))
			}
			count := 2
			if sample.Action == "delete" {
				count = 1
			}
			if len(input.Kwargs) != count {
				t.Fatal("exact StorageChain kwargs", string(after.Inputs[method]))
			}
			var item struct{ Path, Storage, Type string }
			if json.Unmarshal(input.Kwargs["fileitem"], &item) != nil || item.Storage != "local" {
				t.Fatal(item)
			}
			wantPath := old
			if sample.Action == "mkdir" {
				wantPath = folder + "/"
			}
			if item.Path != wantPath {
				t.Fatal(item, wantPath)
			}
			retry := callTest(t, a.Handler(), "POST", "/api/files/operation-confirm", string(confirm), cookie, "")
			if retry.Code != result.Code {
				t.Fatal("a failed module effect must not become success on retry", result.Body.String(), retry.Body.String())
			}
			if last := ownedModuleRead(t, object); last.Calls[method] != after.Calls[method] {
				t.Fatal("same journal replayed plugin")
			}
		})
	}
}
