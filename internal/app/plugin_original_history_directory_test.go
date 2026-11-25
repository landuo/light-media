package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestHistoryUnassociatedSoftlinkDirectoryKeepsLinkedNFO(t *testing.T) {
	a, sourceRoot, library := importTestApp(t)
	ctx := t.Context()
	source, target := filepath.Join(sourceRoot, "Movie"), filepath.Join(library, "Movie")
	sourceNFO, targetNFO := filepath.Join(source, "movie.nfo"), filepath.Join(target, "movie.nfo")
	importWrite(t, sourceNFO, []byte("<movie/>"))
	stamp, _, err := snapshotOriginalDirectory(source)
	if err != nil {
		t.Fatal(err)
	}
	file := PlanFile{ID: "ordinary", Source: source, Target: target, Method: "softlink", DirectoryDigest: stamp.Directory, Device: stamp.Device, Inode: stamp.Inode, ModNS: stamp.ModNS, Size: stamp.Size}
	receipt, err := a.publishOriginalDirectory(ctx, "test/ordinary", file, func() error { return nil })
	if err != nil || receipt.Softlink == nil || receipt.Softlink.Directory == nil || len(receipt.Softlink.Directory.Members) != 1 {
		t.Fatal("publish ordinary directory with one linked NFO", err)
	}
	asset := Asset{ID: "movie", Path: target, RootID: "root", Media: importFixture(t, a, "movie"), State: "present"}
	owner := managedFile{Path: target, RootID: asset.RootID, AssetID: asset.ID, MediaKey: mediaKey(asset.Media), Kind: "video", Snapshot: receipt.StageSnapshot, Softlink: receipt.Softlink}
	if err := a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "managed_file", tokenHash(target), owner); err != nil {
		t.Fatal(err)
	}
	parent, err := importDir(library, false)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	removal := assetRemovalFile{Path: target, Snapshot: owner.Snapshot, Softlink: owner.Softlink, Quarantine: ".light-media-remove-ordinary.part"}
	if err := a.originalHistorySoftlinkDelete(ctx, &removal, parent, func() error { return nil }, nil); err != nil || !removal.DirectoryRetained || removal.SourceOperation != "" {
		t.Fatalf("ordinary directory disposition retained=%v err=%v", removal.DirectoryRetained, err)
	}
	if actual, err := os.Readlink(targetNFO); err != nil || actual != sourceNFO {
		t.Fatalf("NFO link=%q err=%v", actual, err)
	}
	if data, err := os.ReadFile(sourceNFO); err != nil || string(data) != "<movie/>" {
		t.Fatalf("source NFO=%q err=%v", data, err)
	}
}

func TestHistoryOrdinaryDirectoryOutsideRulesRetainsNFO(t *testing.T) {
	a, _, _ := ownedModuleFixture(t, true)
	ctx := t.Context()
	root := t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = []string{root}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Movie")
	nfo := filepath.Join(dir, "movie.nfo")
	importWrite(t, nfo, []byte("keep"))
	stamp, err := snapshotTransferPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := originalHistoryManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := importDir(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, prepared := range []bool{false, true} {
		file := assetRemovalFile{Path: dir, Snapshot: stamp, Quarantine: ".light-media-remove-old.part"}
		if prepared {
			// A previous release prepared a delete but had not called storage.
			op := browserOperation{ID: "old", Action: "delete", State: "executing", Items: []browserOperationItem{{Source: dir, Target: filepath.Join(root, file.Quarantine), Manifest: manifest}}}
			sealed, err := a.Store.Seal(op)
			if err != nil {
				t.Fatal(err)
			}
			file.SourceOperation = base64.StdEncoding.EncodeToString(sealed)
			file.DirectoryParentOnly = true
		}
		err := a.originalHistoryStorageDelete(ctx, &file, parent, func() error { return nil }, nil)
		if err != nil || !file.DirectoryRetained {
			t.Fatalf("prepared=%v retained=%v err=%v", prepared, file.DirectoryRetained, err)
		}
		if _, err := os.Stat(nfo); err != nil {
			t.Fatal(err)
		}
	}
}

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
			associated := root
			if sample.Name == "ordinary-directory-root" {
				associated = filepath.Join(root, "A/B/Disc")
			}
			settings.Directories = []DirectoryRule{historyParentTestRule(associated)}
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
