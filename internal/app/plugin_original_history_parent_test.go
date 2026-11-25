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

func historyParentTestRule(source string) DirectoryRule {
	return DirectoryRule{ID: "downloads", Name: "Downloads", MediaType: "mixed", Mode: "hardlink", MonitorType: "none", SourcePath: source}
}

func TestHistoryAssociatedDirectoryUsesDeepestConfiguredPath(t *testing.T) {
	settings := Settings{
		AllowedRoots: []string{"/media"},
		Roots:        []Root{{ID: "legacy", Path: "/media/library"}},
		Directories: []DirectoryRule{
			{SourcePath: "/media/downloads/incoming", LibraryPath: "/media/library/TV"},
			{SourcePath: "/media/downloads", RootID: "legacy"},
		},
	}
	for input, want := range map[string]string{
		"/media/downloads/incoming/episode":     "/media/downloads/incoming",
		"/media/downloads/incoming-old/episode": "/media/downloads",
		"/media/library/TV/episode":             "/media/library/TV",
		"/media/library/film":                   "/media/library",
		"/media/other/episode":                  "",
	} {
		if got := historyAssociatedDirectory(settings, input); got != want {
			t.Errorf("directory for %s: got %q, want %q", input, got, want)
		}
	}
}

func TestHistoryParentCleanupKeepsRecreatedPath(t *testing.T) {
	a, _, _ := ownedModuleFixture(t, true)
	ctx := t.Context()
	root := t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = []string{root}
	settings.Directories = []DirectoryRule{historyParentTestRule(root)}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "Season", "episode.nfo")
	importWrite(t, old, []byte("first"))
	item, err := originalLocalFileItem(old)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	importWrite(t, old, []byte("replacement"))
	if err := a.originalHistoryCleanupParents(ctx, "replaced", old, raw); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(old); err != nil || string(b) != "replacement" {
		t.Fatalf("recreated file: %s, %v", b, err)
	}
}

func TestOriginalHistoryParentCleanupFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/history-parent-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name      string
			Other     map[string]string
			Result    bool
			Remaining []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("fixed StorageChain source cases", err)
	}
	for _, sample := range oracle.Cases {
		if sample.Name == "none-any-files" || sample.Name == "false-file-delete" {
			continue
		}
		t.Run(sample.Name, func(t *testing.T) {
			a, _, object := ownedModuleFixture(t, true)
			ctx := context.Background()
			root := t.TempDir()
			settings := a.Settings()
			settings.AllowedRoots = []string{root}
			if sample.Name != "no-associated-nonmedia" {
				settings.Directories = []DirectoryRule{historyParentTestRule(root)}
			}
			if sample.Name == "explicit-empty-extensions" {
				settings.RMTMediaExt, settings.RMTSubExt, settings.RMTAudioExt, settings.DownloadTmpExt = json.RawMessage(`[]`), json.RawMessage(`[]`), json.RawMessage(`[]`), json.RawMessage(`[]`)
			}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "A/B/Movie.mkv")
			importWrite(t, path, []byte("video"))
			for relative, content := range sample.Other {
				importWrite(t, filepath.Join(root, relative), []byte(content))
			}
			initial, err := originalLocalFileItem(path)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(initial)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			cfg := map[string]any{"enabled": true, "modules": map[string]string{}}
			switch sample.Name {
			case "false-parent":
				cfg["modules"], cfg["storage_parent"] = map[string]string{"get_parent_item": "module_storage_parent"}, false
			case "zero-any-files":
				cfg["modules"], cfg["result"] = map[string]string{"any_files": "module_execute"}, 0
			case "false-directory-delete":
				cfg["modules"], cfg["result"], cfg["storage_mode"], cfg["storage_root"] = map[string]string{"delete_file": "module_storage_delete"}, false, "no_effect", root
			}
			config, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, config); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			err = a.originalHistoryCleanupParents(ctx, "owned-parent", path, encoded)
			if (err == nil) != sample.Result {
				t.Fatal("fixed cleanup return", sample.Result, err)
			}
			if !sample.Result && !originalHistoryDeleteRejected(err) {
				t.Fatal("false directory storage result must be the actual module result", err)
			}
			remaining := []string{}
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
			sort.Strings(remaining)
			if err != nil || !reflect.DeepEqual(remaining, sample.Remaining) {
				t.Fatal("fixed filesystem effects", remaining, sample.Remaining, err)
			}
			before := ownedModuleRead(t, object)
			// Completed cleanup is a durable no-op in the current App.
			if sample.Result {
				if err := a.originalHistoryCleanupParents(ctx, "owned-parent", path, encoded); err != nil {
					t.Fatal("completed cleanup replay", err)
				}
				after := ownedModuleRead(t, object)
				if after.Calls["module_storage_parent"] != before.Calls["module_storage_parent"] || after.Calls["module_execute"] != before.Calls["module_execute"] {
					t.Fatal("completed parent callbacks repeated")
				}
				if sample.Name == "nonmedia-nfo" {
					dir := a.Dir
					if err := a.Close(); err != nil {
						t.Fatal(err)
					}
					fresh, err := New(Options{DataDir: dir})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { fresh.Close() })
					if err := fresh.originalHistoryCleanupParents(ctx, "owned-parent", path, encoded); err != nil {
						t.Fatal("actual cold parent receipt recovery", err)
					}
				}
			}
		})
	}
}

// The fixed empty-parent and nonmedia-nfo cases also traverse real installed
// modules. This catches directory FileItem's trailing slash at the SDK seam.
func TestOriginalHistoryParentCleanupActualSDKDirectory(t *testing.T) {
	for _, useClone := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "None-clone"}[useClone], func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			ctx := context.Background()
			root := t.TempDir()
			settings := a.Settings()
			settings.AllowedRoots = []string{root}
			settings.Directories = []DirectoryRule{historyParentTestRule(root)}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "A/B/Movie.mkv")
			importWrite(t, path, []byte("owned source"))
			importWrite(t, filepath.Join(root, "A/B/Movie.nfo"), []byte("owned nonmedia"))
			initial, err := originalLocalFileItem(path)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(initial)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			configure := func(object *originalPluginInstance, mode string, result any) {
				cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": root, "storage_mode": mode, "result": result})
				if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, cfg); err != nil {
					t.Fatal(err)
				}
				if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
					t.Fatal(err)
				}
			}
			var clone *originalPluginInstance
			if useClone {
				clone = ownedModuleClone(t, a, cookie, "parent")
				configure(object, "no_effect", nil)
				configure(clone, "effect", true)
			} else {
				configure(object, "effect", true)
			}
			before := ownedModuleRead(t, object)
			if err := a.originalHistoryCleanupParents(ctx, "owned-directory-sdk", path, raw); err != nil {
				t.Fatal("actual directory delete extension", err)
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_storage_delete"]-before.Calls["module_storage_delete"] != 2 {
				t.Fatal("both nested parents call ordinary delete_file", before.Calls, after.Calls)
			}
			if clone != nil && ownedModuleRead(t, clone).Calls["module_storage_delete"] != 2 {
				t.Fatal("source None must dispatch both directory deletions to the clone")
			}
			if _, err := os.Stat(filepath.Join(root, "A")); !os.IsNotExist(err) {
				t.Fatal("native files remain after actual directory module", err)
			}
			if _, err := os.Stat(root); err != nil {
				t.Fatal("associated root must remain", err)
			}
		})
	}
}
