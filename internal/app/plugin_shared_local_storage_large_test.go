package app

import (
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedLocalStorageLargeListAndConstructorDefaults(t *testing.T) {
	a, _, p := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	var source struct {
		Large struct {
			Count      int  `json:"count"`
			NameLength int  `json:"large_name_length"`
			Exists     bool `json:"large_name_exists"`
		} `json:"large"`
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/ordinary-source.json")
	if e != nil || json.Unmarshal(raw, &source) != nil {
		t.Fatal("source supplement", e)
	}
	for i := 0; i < source.Large.Count; i++ {
		if e := sharedLocalStorageFixtureWrite(t, filepath.Join(root, fmt.Sprintf("item-%05d.mkv", i)), []byte("x"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	out, e := sharedLocalStorageCall(t, p, plugins.LocalStorageListCapability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": root})})
	if e != nil {
		t.Fatal(e)
	}
	listBytes := len(out)
	var rows []*plugins.ModuleFileItem
	if e = json.Unmarshal(out, &rows); e != nil || len(rows) != source.Large.Count {
		t.Fatal("list truncated to browser cap", len(rows), e)
	}
	seen := map[string]bool{}
	for _, item := range rows {
		var name string
		if e := json.Unmarshal(item.Fields["name"], &name); e != nil || seen[name] {
			t.Fatal("duplicate or invalid source item")
		}
		seen[name] = true
	}
	for i := 0; i < source.Large.Count; i++ {
		if !seen[fmt.Sprintf("item-%05d.mkv", i)] {
			t.Fatal("native list omitted real item", i)
		}
	}
	input := sharedLocalStorageItemInput(t, map[string]any{"path": filepath.Join(root, "item-00000.mkv"), "name": strings.Repeat("x", source.Large.NameLength)})
	out, e = sharedLocalStorageCall(t, p, plugins.LocalStorageExistsCapability, plugins.LocalStorageRequest{FileItem: input})
	if e != nil || string(out) != "true" || !source.Large.Exists {
		t.Fatal("source 700KiB FileItem narrowed", e, string(out))
	}
	// ReadDir(-1) is the physical order source SystemUtils observes, not sorted
	// browser UI order. All entries here are regular files, so compare exactly.
	f, e := os.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	entries, e := f.ReadDir(-1)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	for i, entry := range entries {
		var name string
		_ = json.Unmarshal(rows[i].Fields["name"], &name)
		if name != entry.Name() {
			t.Fatal("native directory order changed", i, name, entry.Name())
		}
	}
	var defaults struct {
		Defaults []json.RawMessage `json:"defaults"`
	}
	raw, e = os.ReadFile("../../testdata/plugin-local-storage-20261030/ordinary-source.json")
	if e != nil || json.Unmarshal(raw, &defaults) != nil {
		t.Fatal(e)
	}
	for i, v := range []any{map[string]any{}, map[string]any{"path": nil, "storage": nil, "children": nil}} {
		item := sharedLocalStorageItemInput(t, v)
		assertSharedLocalJSON(t, mustCleanSeedJSON(item), defaults.Defaults[i])
	}
	t.Logf("actual source-sized list entries=%d logical_bytes=%d; FileItem name=%d", len(rows), listBytes, source.Large.NameLength)
}
