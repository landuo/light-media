package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestOriginalHistoryOrdinaryDirectoryFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/history-ordinary-directory-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name      string
			Other     map[string]string
			Result    bool
			Remaining []string
			Trace     []struct{ Method string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("fixed directory cleanup source", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, _, object := ownedModuleFixture(t, true)
			ctx := context.Background()
			root := t.TempDir()
			settings := a.Settings()
			settings.AllowedRoots = []string{root}
			if sample.Name == "ordinary-directory-root" {
				settings.AllowedRoots = append(settings.AllowedRoots, filepath.Join(root, "A/B/Disc"))
			}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "A/B/Disc")
			for relative, content := range sample.Other {
				importWrite(t, filepath.Join(root, relative), []byte(content))
			}
			cfg := map[string]any{"enabled": true, "modules": map[string]string{"any_files": "module_execute"}, "result": nil}
			if sample.Name == "ordinary-directory-veto" {
				cfg["modules"] = map[string]string{"any_files": "module_execute", "delete_file": "module_storage_delete"}
				cfg["storage_mode"] = "no_effect"
				cfg["storage_root"] = root
				cfg["result"] = false
			}
			config, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, config); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			stamp, err := snapshotTransferPath(path)
			if err != nil {
				t.Fatal(err)
			}
			item, err := originalLocalFileItem(path)
			if err != nil {
				t.Fatal(err)
			}
			parent, err := importDir(filepath.Dir(path), false)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			selected := assetRemovalFile{Path: path, Snapshot: stamp, Quarantine: ".light-media-remove-owned-directory.part"}
			before := ownedModuleRead(t, object)
			err = a.originalHistoryStorageDelete(ctx, &selected, parent, func() error { return a.Store.Put(ctx, "owned_directory_removal", "fixture", selected) }, item)
			if (err == nil) != sample.Result {
				t.Fatal("fixed ordinary directory return", sample.Result, err)
			}
			if !sample.Result && !originalHistoryDeleteRejected(err) {
				t.Fatal("fixed false deletion", err)
			}
			if err == nil && !selected.DirectoryRetained {
				if err := a.removeHistoryQuarantine(ctx, &selected, parent, func() error { return a.Store.Put(ctx, "owned_directory_removal", "fixture", selected) }); err != nil {
					t.Fatal(err)
				}
				initial, _ := json.Marshal(item)
				if err := a.originalHistoryCleanupParents(ctx, "owned-directory", path, initial, selected.DirectoryParentOnly); err != nil {
					t.Fatal(err)
				}
			}
			var remaining []string
			err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if path != root {
					relative, _ := filepath.Rel(root, path)
					remaining = append(remaining, filepath.ToSlash(relative))
				}
				return nil
			})
			if remaining == nil {
				remaining = []string{}
			}
			sort.Strings(remaining)
			if err != nil || !reflect.DeepEqual(remaining, sample.Remaining) {
				t.Fatal("fixed directory effects", remaining, sample.Remaining, err)
			}
			after := ownedModuleRead(t, object)
			want := 0
			for _, call := range sample.Trace {
				if call.Method == "any_files" {
					want++
				}
			}
			if got := after.Calls["module_execute"] - before.Calls["module_execute"]; got != want {
				t.Fatal("fixed ordinary any_files extension count", got, want)
			}
		})
	}
}
