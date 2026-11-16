package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func finishSharedDirectoryFixture(t *testing.T, mode string, scrape ...bool) (*App, *originalPluginInstance, ImportPlan, Asset, sharedDirectoryFixture) {
	t.Helper()
	a, object, plan, job, fixture := prepareSharedDirectoryImport(t, mode, scrape...)
	if err := a.runImport(t.Context(), job, plan.ID, nil); err != nil {
		t.Fatal("actual merge import", err)
	}
	if _, err := a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	var asset Asset
	if err := a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &asset); err != nil {
		t.Fatal(err)
	}
	return a, object, plan, asset, fixture
}
func checkSharedPreserved(t *testing.T, fixture sharedDirectoryFixture) {
	t.Helper()
	root, err := importDir(fixture.Target, false)
	if err != nil {
		t.Fatal("existing root removed", err)
	}
	info, err := root.Stat()
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(stamp, fixture.RootSnapshot) {
		t.Fatal("existing root replaced", err)
	}
	for relative, before := range fixture.Preserved {
		if now, e := snapshotPath(filepath.Join(fixture.Target, relative)); e != nil || now != before {
			t.Fatal("unrelated member touched", relative, e)
		}
	}
	if info, e := os.Stat(filepath.Join(fixture.Target, "unrelated", "empty")); e != nil || !info.IsDir() {
		t.Fatal("unrelated empty directory removed", e)
	}
}

func TestSharedDirectoryActualMergeDeletionLifecycle(t *testing.T) {
	for _, branch := range []string{"history-target", "asset-hardlink", "history-both", "history-cold"} {
		t.Run(branch, func(t *testing.T) {
			mode := "copy"
			if branch == "asset-hardlink" {
				mode = "hardlink"
			}
			a, object, plan, asset, fixture := finishSharedDirectoryFixture(t, mode)
			// A real delete_file provider removes only the scoped leaf passed to it.
			cfg := map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": filepath.Dir(a.Settings().Roots[0].Path), "storage_mode": "effect", "result": true}
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			item, err := a.transferHistoryItem(t.Context(), plan.Files[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if branch == "history-cold" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER shared_delete_final BEFORE UPDATE ON history_target_receipt WHEN NEW.record_kind='history_target_removal' AND json_extract(NEW.payload,'$.File.deleted')=1 BEGIN SELECT RAISE(FAIL,'shared final commit interrupted'); END`); err != nil {
					t.Fatal(err)
				}
			}
			remove := func() error {
				if branch == "asset-hardlink" {
					preview, e := a.previewAssetRemoval(t.Context(), asset)
					if e != nil {
						return e
					}
					return a.removeAsset(t.Context(), asset.ID, preview.ID, true, false)
				}
				_, e := a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: true, DeleteSource: branch == "history-both"})
				return e
			}
			err = remove()
			a.wg.Wait()
			var calls struct {
				Calls []json.RawMessage `json:"storage_calls"`
			}
			if e := object.readSession(t.Context(), "snapshot", &calls); e != nil {
				t.Fatal(e)
			}
			seen := map[string]int{}
			for _, raw := range calls.Calls {
				var call struct {
					Kwargs struct {
						Item *plugins.ModuleFileItem `json:"fileitem"`
					}
				}
				if json.Unmarshal(raw, &call) != nil || call.Kwargs.Item == nil {
					t.Fatal("actual provider FileItem", string(raw))
				}
				var path string
				_ = json.Unmarshal(call.Kwargs.Item.Fields["path"], &path)
				path = filepath.Clean(path)
				if path == fixture.Target {
					t.Fatal("provider received shared root deletion authority")
				}
				if within(fixture.Target, path) {
					relative, _ := filepath.Rel(fixture.Target, path)
					seen[relative]++
				}
			}
			if len(seen) != len(fixture.Contents) {
				t.Fatal("actual leaf provider dispatch", seen, err)
			}
			for relative := range fixture.Contents {
				if seen[relative] != 1 {
					t.Fatal("member provider repeated or skipped", relative, seen)
				}
			}
			if branch == "history-cold" {
				if err == nil {
					t.Fatal("actual final persistence did not fail")
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER shared_delete_final`); err != nil {
					t.Fatal(err)
				}
				a.wg.Wait()
				settings, dir := a.Settings(), a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.Close() })
				a.config = settings
				if err = a.initializeOriginalPlugins(t.Context()); err != nil {
					t.Fatal(err)
				}
				object = originalBaseSDKLoaded(t, a, ownedModuleID)
				err = remove()
				if after := ownedModuleRead(t, object); after.Calls["module_storage_delete"] != 0 {
					t.Fatal("cold recovery replayed provider", after.Calls)
				}
			}
			if err != nil {
				t.Fatal("member-scoped removal", err)
			}
			checkSharedPreserved(t, fixture)
			for relative := range fixture.Contents {
				if _, e := os.Lstat(filepath.Join(fixture.Target, relative)); !os.IsNotExist(e) {
					t.Fatal("owned leaf retained", relative, e)
				}
			}
			if branch == "history-both" {
				if _, e := os.Lstat(fixture.Source); !os.IsNotExist(e) {
					t.Fatal("explicit source deletion missing", e)
				}
			} else if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
				t.Fatal("target deletion changed source", now, e)
			}
			if branch != "asset-hardlink" {
				var receipt historyTargetRemoval
				if e := a.Store.Get(t.Context(), "history_target_removal", item.executionFileID(), &receipt); e != nil || !receipt.File.Deleted || receipt.File.SharedDirectory == nil || len(receipt.File.SharedMembers) != len(fixture.Contents) {
					t.Fatal("durable subset deletion", receipt, e)
				}
				if e := verifySharedDirectoryRemoved(receipt.File); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestSharedDirectoryActualMergeMetadataColdRebase(t *testing.T) {
	a, _, plan, asset, fixture := finishSharedDirectoryFixture(t, "copy", true)
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=? LIMIT 1`, asset.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER shared_metadata_final BEFORE UPDATE ON managed_file WHEN NEW.record_kind='managed_file' AND NEW.id='` + tokenHash(asset.Path) + `' BEGIN SELECT RAISE(FAIL,'shared metadata commit interrupted'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(t.Context(), job); err == nil {
		t.Fatal("metadata terminal commit did not fail")
	}
	var journal originalMetadataDirectory
	if e := a.Store.Get(t.Context(), "original_metadata_directory", asset.ID, &journal); e != nil || len(journal.Writes) == 0 {
		t.Fatal("actual metadata write absent", e, err)
	}
	nfo := filepath.Join(asset.Path, filepath.Base(asset.Path)+".nfo")
	before, err := snapshotPath(nfo)
	if err != nil {
		t.Fatal("actual NFO", err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER shared_metadata_final`); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	settings, dir := a.Settings(), a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.config = settings
	if err = a.initializeOriginalPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverOriginalMetadataDirectory(t.Context(), asset); err != nil {
		t.Fatal("subset metadata cold rebase", err)
	}
	var owned managedFile
	if err = a.Store.Get(t.Context(), "managed_file", tokenHash(asset.Path), &owned); err != nil || owned.SharedDirectory == nil || len(owned.SharedDirectory.Members) != len(fixture.Contents)+1 {
		t.Fatal("metadata scoped ownership", owned, err)
	}
	if after, e := snapshotPath(nfo); e != nil || after != before {
		t.Fatal("recovery rewrote actual NFO", e)
	}
	if _, err = a.snapshotAssetMedia(t.Context(), asset); err != nil {
		t.Fatal("media/inventory subset", err)
	}
	if err = a.checkAsset(t.Context(), asset.ID); err != nil {
		t.Fatal("asset presence subset", err)
	}
	var receipt importReceipt
	if err = a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.StageSnapshot != owned.Snapshot || !sameSharedDirectoryProof(receipt.SharedDirectory, owned.SharedDirectory) {
		t.Fatal("metadata owner/receipt atomic rebase", err)
	}
	checkSharedPreserved(t, fixture)
	// Finish the interrupted job before exercising ordinary target deletion.
	if _, err = a.finishJob(t.Context(), job, "failed", "test", "verified metadata recovery", 0); err != nil {
		t.Fatal(err)
	}
	item, err := a.transferHistoryItem(t.Context(), plan.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(nfo); !os.IsNotExist(err) {
		t.Fatal("real owned metadata retained", err)
	}
	checkSharedPreserved(t, fixture)
}

func TestSharedDirectoryActualMergeHistoryReorganize(t *testing.T) {
	a, _, plan, old, fixture := finishSharedDirectoryFixture(t, "copy")
	item, err := a.transferHistoryItem(t.Context(), plan.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(a.Settings().Roots[0].Path, "next")
	if err = os.Mkdir(target, 0750); err != nil {
		t.Fatal(err)
	}
	out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{HistoryIDs: []string{item.ID}, FromHistory: true, TargetPath: target, Mode: "copy", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatal("shared-root history reorganization", out, err)
	}
	runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
	checkSharedPreserved(t, fixture)
	var next ImportPlan
	if err = a.Store.Get(t.Context(), "import_plan", out.Items[0].PlanID, &next); err != nil {
		t.Fatal(err)
	}
	var current Asset
	if err = a.Store.Get(t.Context(), "asset", next.Files[0].AssetID, &current); err != nil || current.SourcePath != old.SourcePath || current.Path == old.Path {
		t.Fatal("reorganization source ownership", current, err)
	}
	for relative, data := range fixture.Contents {
		if got, e := os.ReadFile(filepath.Join(current.Path, relative)); e != nil || string(got) != string(data) {
			t.Fatal("real new leaf", relative, e)
		}
	}
	if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
		t.Fatal("source changed", now, e)
	}
}
