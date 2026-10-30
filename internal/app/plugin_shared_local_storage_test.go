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
	"time"

	"light-media/internal/plugins"
)

var sharedLocalStorageAll = []string{plugins.LocalStorageExistsCapability, plugins.LocalStorageListCapability, plugins.LocalStorageDeleteCapability}

func sharedLocalStorageRoot(t *testing.T, a *App) string {
	t.Helper()
	root := t.TempDir()
	s := a.Settings()
	s.AllowedRoots = []string{root}
	if e := a.saveSettings(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	return root
}
func sharedLocalStorageItemInput(t *testing.T, value any) *plugins.ModuleFileItem {
	t.Helper()
	item, e := plugins.NewModuleFileItem(value)
	if e != nil {
		t.Fatal(e)
	}
	return item
}
func sharedLocalStorageSeed(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"dir.z", "dir.a", ".hidden", "@eaDir", "empty", "tree"} {
		if e := os.Mkdir(filepath.Join(root, name), 0755); e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range [][2]string{{"z.mkv", "z"}, {"a.TXT", "abc"}, {".dot", "d"}, {"plain", "p"}, {"dir.z/nested.mkv", "nested"}, {"dir.a/child.nfo", "nfo"}, {".hidden/invisible.mkv", "x"}, {"@eaDir/invisible.mkv", "x"}, {"tree/child", "tree"}} {
		if e := sharedLocalStorageFixtureWrite(t, filepath.Join(root, v[0]), []byte(v[1]), 0644); e != nil {
			t.Fatal(e)
		}
	}
	_ = filepath.Walk(root, func(path string, _ os.FileInfo, e error) error {
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Chtimes(path, time.Unix(1700000000, 0), time.Unix(1700000000, 0)); e != nil {
			t.Fatal(e)
		}
		return nil
	})
}
func assertSharedLocalJSON(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var g, w any
	if e := json.Unmarshal(got, &g); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(want, &w); e != nil {
		t.Fatal(e)
	}
	// Separate directory iteration order is tested from the same physical tree;
	// compare each complete source-produced FileItem here across fresh roots.
	normalize := func(v any) any {
		if list, ok := v.([]any); ok {
			sort.Slice(list, func(i, j int) bool {
				a, _ := list[i].(map[string]any)
				b, _ := list[j].(map[string]any)
				return a["path"].(string) < b["path"].(string)
			})
			return list
		}
		return v
	}
	if !reflect.DeepEqual(normalize(g), normalize(w)) {
		t.Fatalf("full source result differs\ngot %s\nwant %s", got, want)
	}
}
func TestSharedLocalStorageOrdinarySourceContract(t *testing.T) {
	a, _, p := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	sharedLocalStorageSeed(t, root)
	var oracle struct {
		Operations []struct {
			Case   string          `json:"case"`
			Method string          `json:"method"`
			Input  json.RawMessage `json:"input"`
			Kwargs struct {
				Recursion bool `json:"recursion"`
			} `json:"kwargs"`
			Returned json.RawMessage `json:"returned"`
			Error    json.RawMessage `json:"error"`
		} `json:"operations"`
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/ordinary-source.json")
	if e != nil {
		t.Fatal(e)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "$ROOT", root))
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, op := range oracle.Operations {
		t.Run(op.Case, func(t *testing.T) {
			method := map[string]string{"exists": plugins.LocalStorageExistsCapability, "list_files": plugins.LocalStorageListCapability, "delete_file": plugins.LocalStorageDeleteCapability}[op.Method]
			item := sharedLocalStorageItemInput(t, op.Input)
			got, e := sharedLocalStorageCall(t, p, method, plugins.LocalStorageRequest{FileItem: item, Recursion: op.Kwargs.Recursion})
			if len(op.Error) > 0 && string(op.Error) != "null" {
				if e == nil {
					t.Fatal("source error accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			assertSharedLocalJSON(t, got, op.Returned)
		})
	}
}
func TestSharedLocalStorageOrdinaryConsumer(t *testing.T) {
	a, _, p := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	path := filepath.Join(root, "Consumer.2026.mkv")
	if e := sharedLocalStorageFixtureWrite(t, path, []byte("owned synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := p.callOriginalMethod(context.Background(), "consumer", mustCleanSeedJSON(map[string]any{"path": strings.TrimSuffix(path, ".mkv")}))
	if e != nil || string(result.Result) != "true" {
		t.Fatal("actual typed consumer", e, string(result.Result))
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("consumer did not remove real file", e)
	}
	for i := 0; i < 2; i++ {
		var snap map[string]json.RawMessage
		if e := p.readSession(context.Background(), "snapshot", &snap); e != nil {
			t.Fatal(e)
		}
		if string(snap["stale_denied"]) != "true" {
			t.Fatal("old call authority reused")
		}
	}
}
