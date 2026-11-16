package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportSoftlinkScrapeWritesBesideLibraryLink(t *testing.T) {
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Linked","original_title":"Linked","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
	input := filepath.Join(source, "Scrape.Linked.2024.mkv")
	importWrite(t, input, []byte("softlink scrape video"))
	plan, err := a.previewImport(t.Context(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "softlink", Scraping: importFlag(true), Files: []ImportFileRequest{{Path: input}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatal(plan, err)
	}
	job := overwriteStart(t, a, plan)
	if err = a.runImport(t.Context(), job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	if changed, e := a.finishJob(t.Context(), job, "succeeded", "", "", time.Now().Unix()); e != nil || !changed {
		t.Fatal("finish actual import batch", changed, e)
	}
	scrape, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
	if err != nil {
		t.Fatal("actual scrape job", err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, scrape.ID); err != nil {
		t.Fatal(err)
	}
	scrape, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, scrape.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.runScrape(t.Context(), scrape, plan.Files[0].AssetID); err != nil {
		t.Fatal("real link scrape", err)
	}
	target := plan.Files[0].Target
	if _, err = os.Stat(strings.TrimSuffix(target, ".mkv") + ".nfo"); err != nil {
		t.Fatal("library link NFO missing", err)
	}
	if _, err = os.Lstat(strings.TrimSuffix(input, ".mkv") + ".nfo"); !os.IsNotExist(err) {
		t.Fatal("scrape wrote beside referent", err)
	}
	if link, err := os.Readlink(target); err != nil || link != input {
		t.Fatal("scrape replaced media link", link, err)
	}
}

func TestImportSoftlinkInPlaceReorganizeKeepsOwnedEntries(t *testing.T) {
	a, _, old, contents := softlinkLifecycleFixture(t, "softlink")
	oldFiles := softlinkLifecycleOwned(t, a, old)
	plan, err := a.previewImport(t.Context(), ImportRequest{Manual: true, MediaType: "movie", TMDBID: 123, RootID: old.RootID, Mode: "softlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: old.Path}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || plan.ReorganizeAssetID != old.ID {
		t.Fatalf("in-place link preparation %+v %v", plan, err)
	}
	job, err := a.executeImport(t.Context(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	if err = a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	var replacement Asset
	if err = a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &replacement); err != nil || replacement.SourcePath != old.SourcePath {
		t.Fatal("resource provenance", replacement, err)
	}
	for _, file := range oldFiles {
		var current managedFile
		if err = a.Store.Get(t.Context(), "managed_file", tokenHash(file.Path), &current); err != nil || current.Snapshot != file.Snapshot || !sameSoftlinkProof(current.Softlink, file.Softlink) || current.AssetID != replacement.ID {
			t.Fatal("in-place reorg changed owned entry", current, err)
		}
	}
	for path, data := range contents {
		if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
			t.Fatal("in-place reorg changed referent", path, e)
		}
	}
}

func TestImportSoftlinkEntryPublicationAndColdRecovery(t *testing.T) {
	for _, branch := range []string{"success", "published-sql-recovery"} {
		t.Run(branch, func(t *testing.T) {
			a, source, target := importTestApp(t)
			input, output := filepath.Join(source, "linked.mkv"), filepath.Join(target, "linked.mkv")
			data := []byte("actual softlink referent media")
			importWrite(t, input, data)
			original, err := snapshotPath(input)
			if err != nil {
				t.Fatal(err)
			}
			if branch != "success" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER softlink_publish_fail BEFORE UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.state')='published' BEGIN SELECT RAISE(FAIL,'synthetic softlink publication SQL failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := a.publishImport(t.Context(), "softlink-owned", input, output, original, "softlink", t.Context().Err)
			if branch != "success" {
				if err == nil {
					t.Fatal("SQL failure ignored")
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER softlink_publish_fail`); err != nil {
					t.Fatal(err)
				}
				config, dir := a.Settings(), a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				a.config = config
				receipt, err = a.publishImport(t.Context(), "softlink-owned", input, output, original, "softlink", t.Context().Err)
			}
			if err != nil || receipt.State != "published" || receipt.Softlink == nil || receipt.StageSnapshot == original || sameInode(receipt.StageSnapshot, original) {
				t.Fatalf("entry ownership %+v %v", receipt, err)
			}
			if actual, text, err := snapshotSoftlinkEntry(output); err != nil || actual != receipt.StageSnapshot || text != input {
				t.Fatal("link material", actual, text, err)
			}
			if _, err = snapshotPath(output); err == nil {
				t.Fatal("generic no-follow path inspection accepted link")
			}
			if current, err := snapshotPath(input); err != nil || current != original {
				t.Fatal("source changed", current, err)
			}
			if got, err := os.ReadFile(output); err != nil || !bytes.Equal(got, data) {
				t.Fatal("link byte view", err)
			}
			if err = os.Remove(input); err != nil {
				t.Fatal(err)
			}
			if _, err = snapshotOwnedTransferPath(output, receipt.StageSnapshot, receipt.Softlink, false); err != nil {
				t.Fatal("owned broken entry cannot be inspected for deletion", err)
			}
			if _, err = snapshotOwnedTransferPath(output, receipt.StageSnapshot, receipt.Softlink, true); err == nil {
				t.Fatal("broken link counted as available media")
			}
		})
	}
}

func TestImportSoftlinkActualSDKVideoSubtitleAndAssetRecovery(t *testing.T) {
	for _, branch := range []string{"success", "asset-sql-recovery"} {
		t.Run(branch, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			nativeMoveFixture(t, a)
			observer := installOwnedTransferObserver(t, a, nil, nil)
			actorPID := func(app *App) int {
				r := ownedInputAPI(t, app, "GET", "/api/v1/plugin/"+observer+"/state", "")
				var value struct{ PID int }
				if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &value) != nil || value.PID <= 0 || value.PID == os.Getpid() {
					t.Fatal("expected independent SDK process", r.Code)
				}
				return value.PID
			}
			firstPID := actorPID(a)
			video, subtitle := filepath.Join(source, "Linked.2024.mkv"), filepath.Join(source, "Linked.2024.zh.srt")
			contents := map[string][]byte{video: []byte("linked video SDK material"), subtitle: []byte("linked subtitle SDK material")}
			for path, data := range contents {
				importWrite(t, path, data)
			}
			plan, err := a.previewImport(t.Context(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "softlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: video}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatalf("softlink plan %+v %v", plan, err)
			}
			var meta importMetadata
			if err = a.Store.Get(t.Context(), "import_meta", plan.ID, &meta); err != nil || len(meta.Sidecars[plan.Files[0].ID]) != 1 {
				t.Fatal("subtitle plan", meta, err)
			}
			if branch != "success" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER softlink_asset_fail BEFORE INSERT ON library_asset WHEN NEW.record_kind='asset' BEGIN SELECT RAISE(FAIL,'synthetic softlink asset failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			job := overwriteStart(t, a, plan)
			err = a.runImport(t.Context(), job, plan.ID, nil)
			if branch != "success" {
				if err == nil {
					t.Fatal("asset failure ignored")
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER softlink_asset_fail`); err != nil {
					t.Fatal(err)
				}
				// Drain observer delivery before graceful close. This tests cold
				// asset recovery, not loss/replay of in-flight event broadcasts.
				a.wg.Wait()
				config, dir := a.Settings(), a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.Close() })
				a.config = config
				if err = a.initializeOriginalPlugins(t.Context()); err != nil {
					t.Fatal(err)
				}
				if actorPID(a) == firstPID {
					t.Fatal("cold recovery retained old SDK process")
				}
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if err != nil {
					t.Fatal(err)
				}
				err = a.runImport(t.Context(), job, plan.ID, nil)
			}
			if err != nil {
				t.Fatal("real softlink import", err)
			}
			if err = a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
				t.Fatal(err)
			}
			var asset Asset
			if err = a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &asset); err != nil || asset.State != "present" || asset.Size != int64(len(contents[video])) {
				t.Fatal("real media size/asset", asset, err)
			}
			for path, target := range map[string]string{video: plan.Files[0].Target, subtitle: meta.Sidecars[plan.Files[0].ID][0].Target} {
				var owned managedFile
				if err = a.Store.Get(t.Context(), "managed_file", tokenHash(target), &owned); err != nil || owned.Softlink == nil || owned.Softlink.Source != path {
					t.Fatal("stored ownership", owned, err)
				}
				if _, err = snapshotOwnedTransferPath(target, owned.Snapshot, owned.Softlink, true); err != nil {
					t.Fatal(err)
				}
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, contents[path]) {
					t.Fatal("softlink changed source", err)
				}
			}
			if err = a.checkAsset(t.Context(), asset.ID); err != nil {
				t.Fatal("owned link asset check", err)
			}
			a.wg.Wait()
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &calls) != nil {
				t.Fatal("SDK observations", response.Code)
			}
			completed := map[string]int{}
			for _, call := range calls {
				if call.Type != "transfer.complete" && call.Type != "transfer.subtitle.complete" {
					continue
				}
				var out struct {
					Info struct {
						Success bool
						Method  string         `json:"transfer_type"`
						Target  map[string]any `json:"target_item"`
					} `json:"transferinfo"`
				}
				if err = json.Unmarshal(call.Data, &out); err != nil || !out.Info.Success || out.Info.Method != "softlink" || out.Info.Target["path"] == nil {
					t.Fatalf("SDK softlink result %s %v", call.Data, err)
				}
				completed[call.Type]++
			}
			if completed["transfer.complete"] != 1 || completed["transfer.subtitle.complete"] != 1 {
				t.Fatal("completion count", completed)
			}
		})
	}
}
