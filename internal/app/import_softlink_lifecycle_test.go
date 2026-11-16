package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func softlinkLifecycleFixture(t *testing.T, mode string) (*App, ImportPlan, Asset, map[string][]byte) {
	t.Helper()
	a, source, _ := importTestApp(t)
	nativeMoveFixture(t, a)
	video := filepath.Join(source, "Linked.2024.mkv")
	contents := map[string][]byte{video: []byte("native lifecycle video"), filepath.Join(source, "Linked.2024.zh.srt"): []byte("native lifecycle subtitle")}
	for path, data := range contents {
		importWrite(t, path, data)
	}
	plan, err := a.previewImport(t.Context(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: mode, Scraping: importFlag(false), Files: []ImportFileRequest{{Path: video}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatal("actual lifecycle preview", plan, err)
	}
	job, err := a.executeImport(t.Context(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	if err = a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	var asset Asset
	if err = a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &asset); err != nil || asset.State != "present" {
		t.Fatal("actual registered lifecycle asset", asset, err)
	}
	return a, plan, asset, contents
}

func softlinkLifecycleOwned(t *testing.T, a *App, asset Asset) []managedFile {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT payload FROM managed_file WHERE json_extract(payload,'$.asset_id')=?`, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var files []managedFile
	for rows.Next() {
		var raw []byte
		var file managedFile
		if err = rows.Scan(&raw); err != nil || json.Unmarshal(raw, &file) != nil {
			t.Fatal("real managed file", err)
		}
		files = append(files, file)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestImportSoftlinkHistoryDeletesOwnedEntries(t *testing.T) {
	for _, branch := range []string{"target", "both", "broken", "replaced"} {
		t.Run(branch, func(t *testing.T) {
			a, plan, asset, contents := softlinkLifecycleFixture(t, "softlink")
			owned := softlinkLifecycleOwned(t, a, asset)
			if len(owned) != 2 {
				t.Fatal("video/subtitle ownership missing", owned)
			}
			for _, file := range owned {
				if file.Softlink == nil {
					t.Fatal("softlink publication was not registered", file)
				}
				if _, err := snapshotOwnedTransferPath(file.Path, file.Snapshot, file.Softlink, true); err != nil {
					t.Fatal(err)
				}
			}
			video := plan.Files[0].Source
			if branch == "broken" {
				if err := os.Remove(video); err != nil {
					t.Fatal(err)
				}
			}
			var replacement importSnapshot
			if branch == "replaced" {
				// Retain the old inode elsewhere so immediate inode reuse cannot
				// turn a same-text replacement into an ambiguous test fixture.
				if err := os.Rename(asset.Path, asset.Path+".held"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(video, asset.Path); err != nil {
					t.Fatal(err)
				}
				var err error
				replacement, _, err = snapshotSoftlinkEntry(asset.Path)
				if err != nil {
					t.Fatal(err)
				}
			}
			page := historyParityReadPage(t, a, "")
			if page.Total != 2 {
				t.Fatal("independent video/subtitle histories", page)
			}
			bySource := map[string]transferHistoryItem{}
			for _, item := range page.Items {
				bySource[item.Source] = item
			}
			paths := []string{video}
			for path := range contents {
				if path != video {
					paths = append(paths, path)
				}
			}
			for _, path := range paths {
				item := bySource[path]
				if item.ID == "" {
					t.Fatal("missing actual source history", path)
				}
				_, err := a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: true, DeleteSource: branch == "both"})
				if branch == "replaced" {
					if err == nil {
						t.Fatal("same-text new link inode acquired old deletion authority")
					}
					if stamp, _, err := snapshotSoftlinkEntry(asset.Path); err != nil || stamp != replacement {
						t.Fatal("unowned replacement changed", err)
					}
					if got, err := os.ReadFile(video); err != nil || !bytes.Equal(got, contents[video]) {
						t.Fatal("replacement rejection touched referent", err)
					}
					if historyParityReadPage(t, a, "").Total != 2 {
						t.Fatal("rejected deletion hid history")
					}
					return
				}
				if err != nil {
					t.Fatal("actual owned link history deletion", path, err)
				}
				if _, err := os.Lstat(item.Target); !os.IsNotExist(err) {
					t.Fatal("link entry was not actually unlinked", err)
				}
			}
			for path, data := range contents {
				gone := branch == "both" || branch == "broken" && path == video
				if gone {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("explicit source deletion/absence", path, err)
					}
				} else if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
					t.Fatal("target-only deletion changed referent", path, err)
				}
			}
			if historyParityReadPage(t, a, "").Total != 0 {
				t.Fatal("successful deletion retained histories")
			}
		})
	}
}

func TestImportSoftlinkReorganizeFlattensOwnedLinkAndCleansEntries(t *testing.T) {
	a, first, old, contents := softlinkLifecycleFixture(t, "softlink")
	oldFiles := softlinkLifecycleOwned(t, a, old)
	root, err := a.importRoot(old.RootID)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root.Path, "Reorganized")
	if err = os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	plan, err := a.previewImport(t.Context(), ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "softlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: old.Path}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || plan.Files[0].Source != first.Files[0].Source {
		t.Fatal("owned link was not resolved before ordinary source planning", plan, err)
	}
	job, err := a.executeImport(t.Context(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	var replacement Asset
	if err = a.assetAtPath(t.Context(), plan.Files[0].Target, &replacement); err != nil {
		t.Fatal(err)
	}
	newFiles := softlinkLifecycleOwned(t, a, replacement)
	if len(newFiles) != len(oldFiles) {
		t.Fatal("subtitle reorganization lost ownership", newFiles, oldFiles)
	}
	for _, file := range newFiles {
		if file.Softlink == nil || contents[file.Softlink.Source] == nil {
			t.Fatal("replacement link chained through old library target", file)
		}
		if actual, err := os.Readlink(file.Path); err != nil || actual != file.Softlink.Source {
			t.Fatal("real replacement referent", actual, err)
		}
	}
	for _, oldFile := range oldFiles {
		if _, err := os.Lstat(oldFile.Path); err != nil {
			t.Fatal("old entry removed before cleanup transaction", err)
		}
	}
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", plan.ID))
	for _, oldFile := range oldFiles {
		if _, err := os.Lstat(oldFile.Path); !os.IsNotExist(err) {
			t.Fatal("old owned link entry survived cleanup", oldFile.Path, err)
		}
	}
	for _, file := range newFiles {
		if _, err := snapshotOwnedTransferPath(file.Path, file.Snapshot, file.Softlink, true); err != nil {
			t.Fatal("cleanup broke new link", err)
		}
		if got, err := os.ReadFile(file.Path); err != nil || !bytes.Equal(got, contents[file.Softlink.Source]) {
			t.Fatal("replacement no longer exposes original bytes", err)
		}
	}
	for path, data := range contents {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
			t.Fatal("cleanup removed link referent", path, err)
		}
	}
}

func TestImportSoftlinkReorganizeProtectsSoleOldEntity(t *testing.T) {
	a, first, old, contents := softlinkLifecycleFixture(t, "copy")
	source := first.Files[0].Source
	if resolved, err := a.resolveSoftlinkReorganizeSource(t.Context(), old.Path); err != nil || resolved != source {
		t.Fatal("existing original resource was not preferred", resolved, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	root, err := a.importRoot(old.RootID)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root.Path, "RejectedDanglingLink")
	if err = os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	_, err = a.previewImport(t.Context(), ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "softlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: old.Path}}})
	if fileCode(err) != "REVIEW_SOFTLINK_CLEANUP_DEPENDENCY" {
		t.Fatal("sole old entity was not protected before publication", err)
	}
	if got, err := os.ReadFile(old.Path); err != nil || !bytes.Equal(got, contents[source]) {
		t.Fatal("failed preflight removed sole old entity", err)
	}
	var current Asset
	if err = a.Store.Get(t.Context(), "asset", old.ID, &current); err != nil || current.State != "present" {
		t.Fatal("failed preflight replaced old ownership", current, err)
	}
}
