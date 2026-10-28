package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestOriginalTransferCollectionActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-collection-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name      string
			Parent    json.RawMessage
			Plugin    json.RawMessage
			Native    []map[string]any
			Output    []map[string]any
			Exception *string
			Trace     []struct{ Method string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 18 {
		t.Fatal("fixed storage/single-file planner oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			primary := plan.Files[0]
			source := filepath.Dir(primary.Source)
			project := func(value any) json.RawMessage {
				encoded, _ := json.Marshal(value)
				return json.RawMessage(strings.ReplaceAll(string(encoded), "{root}/source", source))
			}
			// All provider metadata refer to owned, physically existing files;
			// metadata alone never grants a publication or filesystem snapshot.
			entries := append([]map[string]any(nil), sample.Native...)
			var tuple struct {
				Items []map[string]any `json:"__plugin_module_tuple"`
			}
			_ = json.Unmarshal(sample.Plugin, &tuple)
			entries = append(entries, tuple.Items...)
			created := map[string]bool{primary.Source: true}
			for _, item := range entries {
				path := strings.ReplaceAll(sourceText(item["path"]), "{root}/source", source)
				if path == "" || created[path] {
					continue
				}
				created[path] = true
				importWrite(t, path, []byte("owned sibling"))
				if sample.Name == "large_extra" && strings.HasSuffix(path, ".flac") {
					if err := os.Truncate(path, 70*1024*1024); err != nil {
						t.Fatal(err)
					}
				}
			}
			cfg["modules"] = map[string]string{"get_parent_item": "module_storage_parent", "list_files": "module_storage_siblings"}
			cfg["storage_parent"], cfg["storage_siblings"] = project(sample.Parent), project(sample.Plugin)
			encoded, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			before := ownedModuleRead(t, object)
			actual, err := a.sourceImportSidecarsChecked(ctx, primary, nil, false)
			if (err != nil) != (sample.Exception != nil) {
				t.Fatal("fixed collection error branch", err, sample.Exception)
			}
			if err == nil {
				want := []string{}
				for _, item := range sample.Output[1:] {
					want = append(want, strings.ReplaceAll(sourceText(item["path"]), "{root}/source", source))
				}
				got := []string{}
				for _, item := range actual {
					got = append(got, item.Source)
					stat, err := os.Stat(item.Source)
					if err != nil || stat.Size() != item.Snapshot.Size || item.Item == nil {
						t.Fatal("real snapshot plus original FileItem", item, err)
					}
				}
				if !reflect.DeepEqual(got, want) {
					native := false
					for _, call := range sample.Trace {
						native = native || call.Method == "native_list"
					}
					// The source oracle supplies a controlled native listing. Actual
					// APFS/ext4 order is a separate LocalStorage boundary; terminal
					// tuple cases below compare exact provider order without sorting.
					if native {
						sort.Strings(got)
						sort.Strings(want)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal("fixed selected-main matching, provider order, de-duplication and exclusions", got, want)
					}
				}
				if sample.Name == "metadata_name" && string(actual[0].Item.Fields["name"]) != `"Owned.2024.zh.srt"` {
					t.Fatal("source FileItem name/extension must survive actual path validation", actual[0])
				}
			}
			after := ownedModuleRead(t, object)
			wantParent, wantList := 0, 0
			for _, call := range sample.Trace {
				if call.Method == "get_parent_item" {
					wantParent++
				}
				if call.Method == "list_files" {
					wantList++
				}
			}
			if after.Calls["module_storage_parent"]-before.Calls["module_storage_parent"] != wantParent || after.Calls["module_storage_siblings"]-before.Calls["module_storage_siblings"] != wantList {
				t.Fatal("actual SDK call sequence", before.Calls, after.Calls, sample.Trace)
			}
			if wantList != 0 {
				var call struct{ Kwargs map[string]json.RawMessage }
				if json.Unmarshal(after.Inputs["module_storage_siblings"], &call) != nil || len(call.Kwargs) != 2 || string(call.Kwargs["recursion"]) != "false" || sample.Name == "scalar_parent" && string(call.Kwargs["fileitem"]) != "true" {
					t.Fatal("fixed list_files kwargs / parent passed through", string(after.Inputs["module_storage_siblings"]))
				}
			}
		})
	}
}
