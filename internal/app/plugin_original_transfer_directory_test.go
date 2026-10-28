package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOriginalTransferDirectoryCollectorFixedSourceActualSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-directory-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name       string
			Initial    map[string]any
			Plugin     map[string]json.RawMessage
			NativeTree map[string][]map[string]any `json:"native_tree"`
			Output     []originalTransferCandidate
			Exception  *string
			Trace      []struct{ Role, Method, Path string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("fixed recursive source oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			root := filepath.Dir(plan.Files[0].Source)
			project := func(v any) json.RawMessage {
				b, _ := json.Marshal(v)
				return json.RawMessage(strings.ReplaceAll(string(b), "{root}", root))
			}
			var initial map[string]any
			_ = json.Unmarshal(project(sample.Initial), &initial)
			var tree map[string][]map[string]any
			_ = json.Unmarshal(project(sample.NativeTree), &tree)
			var configured map[string]json.RawMessage
			_ = json.Unmarshal(project(sample.Plugin), &configured)
			create := func(item map[string]any) {
				path := sourceText(item["path"])
				if sourceText(item["type"]) == "dir" {
					if err := os.MkdirAll(path, 0750); err != nil {
						t.Fatal(err)
					}
				} else {
					importWrite(t, path, []byte("owned directory candidate!"))
				}
			}
			create(initial)
			for dir, items := range tree {
				if err := os.MkdirAll(dir, 0750); err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					create(item)
				}
			}
			// Terminal tuple providers may return real files absent from the controlled
			// native listing. Their order is still the provider's exact order.
			for _, value := range configured {
				var tuple struct {
					Items []map[string]any `json:"__plugin_module_tuple"`
				}
				if json.Unmarshal(value, &tuple) == nil {
					for _, item := range tuple.Items {
						create(item)
					}
				}
			}
			cfg["modules"] = map[string]string{"get_file_item": "module_storage_tree", "list_files": "module_storage_tree"}
			cfg["storage_tree"] = configured
			encoded, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			actual, _, err := a.originalTransferCandidates(ctx, sourceText(initial["path"]))
			if (err != nil) != (sample.Exception != nil) {
				t.Fatal("source exception branch", err, sample.Exception)
			}
			if err == nil {
				var expected []originalTransferCandidate
				_ = json.Unmarshal(project(sample.Output), &expected)
				projection := func(items []originalTransferCandidate) []any {
					out := []any{}
					for _, item := range items {
						out = append(out, []any{filepath.Clean(sourceText(item.Item["path"])), sourceText(item.Item["type"]), sourceText(item.Item["name"]), item.Bluray})
					}
					return out
				}
				if !reflect.DeepEqual(projection(actual), projection(expected)) {
					t.Fatal("source collector exact order/unit/metadata", projection(actual), projection(expected))
				}
			}
			var snapshot struct {
				StorageCalls []struct{ Kwargs map[string]json.RawMessage } `json:"storage_calls"`
			}
			if err := object.readSession(ctx, "snapshot", &snapshot); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			want := []string{}
			for _, call := range snapshot.StorageCalls {
				var path string
				method := "get_file_item"
				if item, ok := call.Kwargs["fileitem"]; ok {
					var f struct{ Path string }
					_ = json.Unmarshal(item, &f)
					path = f.Path
					method = "list_files"
					if string(call.Kwargs["recursion"]) != "false" {
						t.Fatal("source nonrecursive storage wrapper", call)
					}
				} else {
					_ = json.Unmarshal(call.Kwargs["path"], &path)
					if string(call.Kwargs["storage"]) != `"local"` {
						t.Fatal("source storage argument", call)
					}
				}
				if len(call.Kwargs) != 2 {
					t.Fatal("source exact kwargs", call)
				}
				got = append(got, method+":"+filepath.Clean(path))
			}
			for _, call := range sample.Trace {
				if call.Role == "plugin" {
					want = append(want, call.Method+":"+filepath.Clean(strings.ReplaceAll(call.Path, "{root}", root)))
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("selected refresh once and recursive module sequence", got, want)
			}
		})
	}
}

func TestOriginalTransferDirectoryUsesStorageHost(t *testing.T) {
	a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	path := filepath.Dir(plan.Files[0].Source)
	cfg["modules"] = map[string]string{"get_file_item": "module_storage_tree", "list_files": "module_storage_tree"}
	cfg["storage_tree"] = map[string]any{"get_file_item:" + path: false}
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	result, err := a.manualOrganizeScan(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if files := result.(map[string]any)["files"].([]importScanFile); len(files) != 0 {
		t.Fatal("fixed get_item false must stop before listing or scanning native files", files)
	}
	if got := ownedModuleRead(t, object).Calls["module_storage_tree"]; got != 1 {
		t.Fatal("actual get_file_item call", got)
	}
}
