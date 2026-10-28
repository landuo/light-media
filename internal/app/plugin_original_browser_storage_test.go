package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOriginalBrowserStorageActualHTTPAndSDKFixedCaller(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/browser-storage-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name   string
			Plugin json.RawMessage
			Output []struct {
				Name, Path string
				Size       int64
				ModifyTime float64 `json:"modify_time"`
			}
			Fail     bool
			CoreFail bool `json:"core_fail"`
			Status   int
			Calls    []struct {
				Role   string
				Kwargs json.RawMessage
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("fixed browser StorageChain/caller oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			if sample.CoreFail && os.Geteuid() == 0 {
				t.Skip("owned chmod read-denial does not apply to root; core exception dispatcher separately replayed")
			}
			a, cookie, object := ownedModuleFixture(t, true)
			folder := t.TempDir()
			file := filepath.Join(folder, "File10.mkv")
			if os.WriteFile(file, []byte("abc"), 0640) != nil || os.Chtimes(file, time.Unix(10, 0), time.Unix(10, 0)) != nil || os.Chtimes(folder, time.Unix(42, 0), time.Unix(42, 0)) != nil {
				t.Fatal("owned directory setup")
			}
			settings := a.Settings()
			settings.AllowedRoots, settings.Roots = []string{folder}, nil
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			mapped := bytes.ReplaceAll(sample.Plugin, []byte("/owned/"), []byte(folder+"/"))
			method := "module_execute"
			if sample.Fail {
				method = "module_fail"
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"list_files": method}, "result": json.RawMessage(mapped)})
			saveOwnedModule(t, a, cookie, object, string(config))
			before := ownedModuleRead(t, object)
			if sample.CoreFail {
				if os.Chmod(folder, 0) != nil {
					t.Fatal("owned permission fault")
				}
				defer os.Chmod(folder, 0750)
			}
			root := fileBrowserRoot(t, a, folder)
			query := url.Values{"root_id": {root.ID}, "path": {folder}, "sort": {"name"}, "order": {"asc"}}
			response := callTest(t, a.Handler(), "GET", "/api/files/list?"+query.Encode(), "", cookie, "")
			if response.Code != sample.Status {
				t.Fatal("fixed source browser result", response.Code, sample.Status, response.Body.String())
			}
			if response.Code == 200 {
				var result struct {
					Data struct{ Items []browserEntry }
				}
				if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Data.Items) != len(sample.Output) {
					t.Fatal("actual file-browser projection", response.Body.String())
				}
				for i, expected := range sample.Output {
					actual := result.Data.Items[i]
					if actual.Name != expected.Name || actual.Path != folder+expected.Path[len("/owned"):] || actual.Size != expected.Size || actual.Modified != time.Unix(int64(expected.ModifyTime), 0).UTC().Format(time.RFC3339) {
						t.Fatal("source list/order/field consumption", actual, expected)
					}
				}
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			var input struct {
				Args   []json.RawMessage
				Kwargs map[string]json.RawMessage
			}
			if after.Calls[method]-before.Calls[method] != 1 || json.Unmarshal(after.Inputs[method], &input) != nil || len(input.Args) != 0 || len(input.Kwargs) != 2 || string(input.Kwargs["recursion"]) != "false" {
				t.Fatal("actual SDK source list signature", string(after.Inputs[method]))
			}
			var call map[string]json.RawMessage
			if json.Unmarshal(input.Kwargs["fileitem"], &call) != nil || string(call["path"]) != string(mustCleanSeedJSON(folder+"/")) || string(call["basename"]) != string(mustCleanSeedJSON(filepath.Base(folder))) || string(call["modify_time"]) != "42" {
				t.Fatal("actual source local FileItem constructor", string(input.Kwargs["fileitem"]))
			}
		})
	}
}

func TestOriginalBrowserNaturalKeysActualSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/browser-storage-source-oracle.json")
	var oracle struct {
		Keys map[string]json.RawMessage `json:"natural_keys"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Keys) != 7 {
		t.Fatal("fixed natural key oracle", err)
	}
	for name, want := range oracle.Keys {
		parts, err := originalBrowserNaturalKey(name)
		if err != nil {
			t.Fatal(name, err)
		}
		actual := []any{}
		for _, part := range parts {
			if part.Number != nil {
				actual = append(actual, json.Number(part.Number.String()))
			} else {
				actual = append(actual, part.Text)
			}
		}
		got, _ := json.Marshal(actual)
		if !originalBaseJSONEqual(got, want) {
			t.Fatal("fixed Python natural key", name, string(got), string(want))
		}
	}
	if _, err := originalBrowserNaturalKey("²"); err == nil {
		t.Fatal("source int(nondecimal digit) must fail")
	}
}
