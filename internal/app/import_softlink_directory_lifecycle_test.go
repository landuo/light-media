package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func runSoftlinkDirectoryJob(t *testing.T, a *App, id string) Job {
	t.Helper()
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	job, err := a.jobByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(a.traceContext(t.Context(), job.WorkflowID, job.ID, job.RequestID), job); err != nil {
		t.Fatal("actual worker", job.Kind, err)
	}
	if changed, err := a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal("actual worker finish", job.Kind, err)
	} else if !changed {
		// organize_file hands the same durable job to its import phase with
		// a new generation; run that phase just as the worker claims it next.
		current, e := a.jobByID(context.Background(), job.ID)
		if e == nil && job.Kind == "organize_file" && current.Kind == "import" && current.State == "queued" {
			return runSoftlinkDirectoryJob(t, a, id)
		}
		if e != nil || current.State != "succeeded" {
			t.Fatal("worker was not successfully finalized", current, e)
		}
	}
	return job
}

func softlinkDirectoryLifecycleFixture(t *testing.T, scrape, linkedNFO bool) (*App, ImportPlan, Asset, map[string][]byte) {
	t.Helper()
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"测试影片","original_title":"Test Film","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
	directory := filepath.Join(source, "Owned.2024.BluRay")
	contents := map[string][]byte{filepath.Join(directory, "BDMV", "STREAM", "00001.m2ts"): []byte("directory native stream"), filepath.Join(directory, "BDMV", "index.bdmv"): []byte("index"), filepath.Join(directory, "tiny.txt"): []byte("x")}
	if linkedNFO {
		contents[filepath.Join(directory, filepath.Base(directory)+".nfo")] = []byte("<movie>original source NFO</movie>")
	}
	for path, data := range contents {
		importWrite(t, path, data)
	}
	out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{directory}, TargetPath: a.Settings().Roots[0].Path, Mode: "softlink", MediaType: "movie", TMDBID: 123, Renaming: importFlag(false), Scraping: importFlag(scrape)})
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatal("actual manual directory", out, err)
	}
	runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
	var plan ImportPlan
	if err = a.Store.Get(t.Context(), "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 {
		t.Fatal(plan, err)
	}
	var asset Asset
	if err = a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &asset); err != nil || asset.State != "present" {
		t.Fatal(asset, err)
	}
	return a, plan, asset, contents
}

func TestImportSoftlinkDirectoryHistoryDeletion(t *testing.T) {
	for _, branch := range []string{"target", "broken", "both"} {
		t.Run(branch, func(t *testing.T) {
			a, _, asset, contents := softlinkDirectoryLifecycleFixture(t, false, false)
			missing := filepath.Join(asset.SourcePath, "BDMV", "STREAM", "00001.m2ts")
			if branch == "broken" {
				if err := os.Remove(missing); err != nil {
					t.Fatal(err)
				}
			}
			page := historyParityReadPage(t, a, "")
			if page.Total != 1 {
				t.Fatal("one directory history", page)
			}
			item := page.Items[0]
			if _, err := a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: true, DeleteSource: branch == "both"}); err != nil {
				t.Fatal("actual directory history delete", err)
			}
			if _, err := os.Lstat(asset.Path); !os.IsNotExist(err) {
				t.Fatal("directory target remains", err)
			}
			for path, data := range contents {
				got, err := os.ReadFile(path)
				if branch == "both" || branch == "broken" && path == missing {
					if !os.IsNotExist(err) {
						t.Fatal("explicit removed source remains", path, err)
					}
				} else if err != nil || !bytes.Equal(got, data) {
					t.Fatal("target deletion changed source", path, err)
				}
			}
		})
	}
}

func TestImportSoftlinkDirectoryReorganize(t *testing.T) {
	for _, branch := range []string{"history-new-path", "in-place"} {
		t.Run(branch, func(t *testing.T) {
			a, _, old, contents := softlinkDirectoryLifecycleFixture(t, false, false)
			page := historyParityReadPage(t, a, "")
			if page.Total != 1 {
				t.Fatal(page)
			}
			var before managedFile
			if err := a.Store.Get(t.Context(), "managed_file", tokenHash(old.Path), &before); err != nil {
				t.Fatal(err)
			}
			request := manualOrganizeRequest{Mode: "softlink", MediaType: "movie", TMDBID: 123, Renaming: importFlag(false), Scraping: importFlag(false)}
			if branch == "history-new-path" {
				request.HistoryIDs = []string{page.Items[0].ID}
				request.TargetPath = filepath.Join(filepath.Dir(old.Path), "next")
				if err := os.MkdirAll(request.TargetPath, 0750); err != nil {
					t.Fatal(err)
				}
			} else {
				request.Paths = []string{old.Path}
				request.TargetPath = filepath.Dir(old.Path)
			}
			out, err := a.manualOrganize(t.Context(), request)
			if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
				t.Fatal("directory reorganization", branch, out, err)
			}
			runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
			var plan ImportPlan
			if err = a.Store.Get(t.Context(), "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 {
				t.Fatal(plan, err)
			}
			var current Asset
			if err = a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &current); err != nil || current.SourcePath != old.SourcePath {
				t.Fatal("directory resource provenance", current, err)
			}
			var owned managedFile
			if err = a.Store.Get(t.Context(), "managed_file", tokenHash(current.Path), &owned); err != nil || owned.Softlink == nil || owned.Softlink.Directory == nil {
				t.Fatal(owned, err)
			}
			if _, err = snapshotOwnedTransferPath(current.Path, owned.Snapshot, owned.Softlink, true); err != nil {
				t.Fatal(err)
			}
			if branch == "history-new-path" {
				if _, err = os.Lstat(old.Path); !os.IsNotExist(err) {
					t.Fatal("old directory remains", err)
				}
			} else if current.Path != old.Path || owned.Snapshot != before.Snapshot || !sameSoftlinkProof(owned.Softlink, before.Softlink) {
				t.Fatal("in-place reorganization replaced directory entries")
			}
			for _, member := range owned.Softlink.Directory.Members {
				if member.Softlink != nil && !within(old.SourcePath, member.Softlink.Source) {
					t.Fatal("link depends on deleted old library directory", member)
				}
			}
			for path, data := range contents {
				if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
					t.Fatal("directory reorg changed source", path, e)
				}
			}
			page = historyParityReadPage(t, a, "")
			if page.Total != 1 || page.Items[0].Source != old.SourcePath {
				t.Fatal("stable directory source history", page)
			}
		})
	}
}

func TestImportSoftlinkDirectoryMetadataCommitColdRecovery(t *testing.T) {
	a, plan, asset, contents := softlinkDirectoryLifecycleFixture(t, true, false)
	scrape, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER softlink_metadata_fail BEFORE UPDATE ON managed_file WHEN NEW.record_kind='managed_file' AND NEW.id='` + tokenHash(asset.Path) + `' BEGIN SELECT RAISE(ABORT,'metadata commit fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, scrape.ID); err != nil {
		t.Fatal(err)
	}
	scrape, err = a.jobByID(context.Background(), scrape.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(t.Context(), scrape); err == nil {
		t.Fatal("metadata ownership commit did not fail")
	}
	nfo := filepath.Join(asset.Path, filepath.Base(asset.Path)+".nfo")
	if _, err = os.Stat(nfo); err != nil {
		t.Fatal("real NFO was not written before SQL failure", err)
	}
	var pending originalMetadataDirectory
	if err = a.Store.Get(t.Context(), "original_metadata_directory", asset.ID, &pending); err != nil || len(pending.Writes) == 0 {
		t.Fatal("durable metadata write proof", pending, err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER softlink_metadata_fail`); err != nil {
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
	runSoftlinkDirectoryJob(t, a, scrape.ID)
	var owned managedFile
	if err = a.Store.Get(t.Context(), "managed_file", tokenHash(asset.Path), &owned); err != nil {
		t.Fatal(err)
	}
	if _, err = snapshotOwnedTransferPath(asset.Path, owned.Snapshot, owned.Softlink, true); err != nil {
		t.Fatal("recovered directory proof", err)
	}
	var receipt importReceipt
	if err = a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.StageSnapshot != owned.Snapshot || !sameSoftlinkProof(receipt.Softlink, owned.Softlink) {
		t.Fatal("recovered owner/receipt", err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_original_directory WHERE id=?`, asset.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("journal not finalized", count, err)
	}
	for path, data := range contents {
		if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
			t.Fatal("recovery changed original tree", path, e)
		}
	}
}

func TestImportSoftlinkDirectoryScrapeRebasesManagedTree(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-nfo", true: "linked-nfo"}[existing], func(t *testing.T) {
			a, plan, asset, contents := softlinkDirectoryLifecycleFixture(t, true, existing)
			scrape, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
			if err != nil {
				t.Fatal("actual queued metadata", err)
			}
			runSoftlinkDirectoryJob(t, a, scrape.ID)
			nfo := filepath.Join(asset.Path, filepath.Base(asset.Path)+".nfo")
			if _, err = os.Stat(nfo); err != nil {
				t.Fatal("library NFO", err)
			}
			if existing {
				if _, err = os.Readlink(nfo); err != nil {
					t.Fatal("non-overwrite did not retain known source NFO link", err)
				}
				// Explicit overwrite of the same genuine inherited event exercises entry
				// exchange, not following/writing the source NFO.
				var payload map[string]json.RawMessage
				if err = json.Unmarshal(scrape.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				var event map[string]any
				if err = json.Unmarshal(payload["original_metadata_event"], &event); err != nil {
					t.Fatal(err)
				}
				event["overwrite"] = true
				next, err := a.enqueue(t.Context(), "scrape", map[string]any{"id": asset.ID, "original_metadata_event": event}, scrape.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				runSoftlinkDirectoryJob(t, a, next.ID)
				if _, err = os.Readlink(nfo); err == nil {
					t.Fatal("explicit NFO overwrite retained old link")
				}
			}
			var owned managedFile
			if err = a.Store.Get(t.Context(), "managed_file", tokenHash(asset.Path), &owned); err != nil || owned.Softlink == nil || owned.Softlink.Directory == nil {
				t.Fatal(owned, err)
			}
			relative, _ := filepath.Rel(asset.Path, nfo)
			if member, ok := owned.Softlink.Directory.Members[relative]; !ok || member.Softlink != nil {
				t.Fatal("NFO not registered as ordinary metadata", member, ok)
			}
			if _, err = snapshotOwnedTransferPath(asset.Path, owned.Snapshot, owned.Softlink, true); err != nil {
				t.Fatal("post-scrape directory proof", err)
			}
			var receipt importReceipt
			if err = a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.StageSnapshot != owned.Snapshot || !sameSoftlinkProof(receipt.Softlink, owned.Softlink) {
				t.Fatal("metadata receipt/owner atomic rebasing", err)
			}
			for path, data := range contents {
				if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
					t.Fatal("scrape changed original tree", path, e)
				}
			}
			if err = a.checkAsset(t.Context(), asset.ID); err != nil {
				t.Fatal("scraped directory inventory", err)
			}
			page := historyParityReadPage(t, a, "")
			if page.Total != 1 {
				t.Fatal(page)
			}
			if _, err = a.deleteTransferHistory(t.Context(), page.Items[0], historyDeleteRequest{Confirm: true, FileID: page.Items[0].executionFileID(), DeleteTarget: true}); err != nil {
				t.Fatal("delete rebased directory including metadata", err)
			}
			for path, data := range contents {
				if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
					t.Fatal("delete changed original tree", path, e)
				}
			}
		})
	}
}
