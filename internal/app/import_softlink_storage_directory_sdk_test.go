package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func TestDirectoryStorageSoftlinkActualSDKAndColdStageRecovery(t *testing.T) {
	for _, branch := range []string{"directory-names", "cold-stage", "copy-is-not-softlink"} {
		t.Run(branch, func(t *testing.T) {
			a, sourceRoot, library := importTestApp(t)
			observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(sourceRoot), "storage_mode": branch})
			nativeMoveFixture(t, a)
			source := filepath.Join(sourceRoot, "Storage.2024.BluRay")
			contents := map[string][]byte{filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"): []byte("owned stream"), filepath.Join(source, "BDMV", "index.bdmv"): []byte("owned index"), filepath.Join(source, "tiny.txt"): []byte("x")}
			for path, data := range contents {
				importWrite(t, path, data)
			}
			if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			before, _, err := snapshotOriginalDirectory(source)
			if err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{source}, TargetPath: library, Mode: "softlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
				t.Fatal("real directory queue", out, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(t.Context(), "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 {
				t.Fatal(plan, err)
			}
			key := plan.ID + "/" + plan.Files[0].ID
			if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, out.Items[0].JobID); err != nil {
				t.Fatal(err)
			}
			job, err := a.jobByID(context.Background(), out.Items[0].JobID)
			if err != nil {
				t.Fatal(err)
			}
			pid := func(app *App) int {
				t.Helper()
				r := ownedInputAPI(t, app, "GET", "/api/v1/plugin/"+observer+"/state", "")
				var state struct{ PID int }
				if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &state) != nil || state.PID <= 0 || state.PID == os.Getpid() {
					t.Fatal("independent source actor", r.Code)
				}
				return state.PID
			}
			firstPID := pid(a)
			if branch == "cold-stage" {
				_, err = a.Store.DB.Exec(`CREATE TRIGGER directory_softlink_actor_pause AFTER UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND NEW.id LIKE '` + key + `/directory/%' AND json_extract(NEW.payload,'$.state')='planned' AND length(json_extract(NEW.payload,'$.storage_softlink.result'))>0 BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id='` + job.ID + `'; END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			err = a.safeRun(t.Context(), job)
			var firstPath string
			var firstEntry importSnapshot
			if branch == "cold-stage" {
				if !originalTransferInterrupted(err) {
					t.Fatal("real actor result did not pause before staged confirmation", err)
				}
				var parent importReceipt
				if err = a.Store.Get(t.Context(), "import_receipt", key, &parent); err != nil || parent.DirectoryStorageSoftlink == nil || len(parent.DirectoryFiles) != 1 {
					t.Fatal("frozen source actor and prefix", parent, err)
				}
				var child importReceipt
				if err = a.Store.Get(t.Context(), "import_receipt", key+"/directory/"+tokenHash(parent.DirectoryFiles[0]), &child); err != nil || child.StorageSoftlink == nil || len(child.StorageSoftlink.Result) == 0 || child.StageSnapshot != (importSnapshot{}) {
					t.Fatal("persisted returned stage proof", child, err)
				}
				firstPath = child.Target
				firstEntry, _, err = snapshotSoftlinkEntry(filepath.Join(filepath.Dir(child.Target), child.Stage))
				if err != nil {
					t.Fatal("real stage", err)
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER directory_softlink_actor_pause`); err != nil {
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
				if pid(a) == firstPID {
					t.Fatal("actor process survived cold reopen")
				}
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = a.jobByID(context.Background(), job.ID)
				if err != nil {
					t.Fatal(err)
				}
				err = a.safeRun(t.Context(), job)
			}
			positive := branch != "copy-is-not-softlink"
			if positive && err != nil || !positive && err == nil {
				t.Fatal("actual directory actor result", branch, err)
			}
			if positive {
				if _, err = a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
					t.Fatal(err)
				}
				var parent importReceipt
				if err = a.Store.Get(t.Context(), "import_receipt", key, &parent); err != nil || parent.State != "published" || parent.Softlink == nil || parent.Softlink.Directory == nil || parent.DirectoryStorageSoftlink == nil || parent.DirectoryStorageSoftlink.InstanceID != observer || parent.DirectoryStorageSoftlink.Key != "owned-local" || len(parent.DirectoryCompleted) != len(contents) {
					t.Fatal("root actor ownership", parent, err)
				}
				if _, err = snapshotOwnedTransferPath(parent.Target, parent.StageSnapshot, parent.Softlink, true); err != nil {
					t.Fatal(err)
				}
				for _, relative := range parent.DirectoryFiles {
					var child importReceipt
					if err = a.Store.Get(t.Context(), "import_receipt", key+"/directory/"+tokenHash(relative), &child); err != nil || child.State != "published" || child.StorageSoftlink == nil {
						t.Fatal("child actor publication", child, err)
					}
					if child.Target == firstPath && child.StageSnapshot != firstEntry {
						t.Fatal("cold recovery replaced actual stage inode")
					}
				}
				if branch == "directory-names" {
					if parent.directoryTarget("BDMV/STREAM/00001.m2ts") != "BluRay/Streams/feature.m2ts" {
						t.Fatal("provider names/mapping lost", parent.DirectoryMembers)
					}
				}
				if parent.DirectoryStreamSize == nil || *parent.DirectoryStreamSize != 12 {
					t.Fatal("STREAM size lost", parent.DirectoryStreamSize)
				}
				var assets int
				if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM library_asset WHERE 1=1`).Scan(&assets); err != nil || assets != 1 {
					t.Fatal("directory split into child assets", assets, err)
				}
				page := historyParityReadPage(t, a, "")
				if page.Total != 1 {
					t.Fatal("one root history", page)
				}
				if _, err = os.Lstat(filepath.Join(parent.Target, "CERTIFICATE")); !os.IsNotExist(err) {
					t.Fatal("empty source directory was copied", err)
				}
			} else {
				var assets int
				if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM library_asset WHERE 1=1`).Scan(&assets); e != nil || assets != 0 {
					t.Fatal("copy actor got softlink asset", assets, e)
				}
			}
			if current, _, e := snapshotOriginalDirectory(source); e != nil || current != before {
				t.Fatal("source directory mutated", current, e)
			}
			for path, data := range contents {
				if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
					t.Fatal("source data changed", path, e)
				}
			}
			a.wg.Wait()
			var parent importReceipt
			if err = a.Store.Get(t.Context(), "import_receipt", key, &parent); err != nil {
				t.Fatal(err)
			}
			r := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &calls) != nil {
				t.Fatal("SDK observations", r.Code)
			}
			counts := map[string]int{}
			for _, call := range calls {
				if call.Type != "storage.softlink" {
					continue
				}
				var operation plugins.StorageOperationCall
				if err = json.Unmarshal(call.Data, &operation); err != nil || operation.FileItem == nil {
					t.Fatal(err)
				}
				var path string
				if err = json.Unmarshal(operation.FileItem.Fields["path"], &path); err != nil {
					t.Fatal(err)
				}
				relative, e := filepath.Rel(source, path)
				if e != nil {
					t.Fatal(e)
				}
				var frozen plugins.ModuleFileItem
				if e = a.Store.Unseal(parent.DirectoryMembers[relative].FileItem, &frozen); e != nil || !storageMoveSameFileItem(operation.FileItem, &frozen) {
					t.Fatal("complete frozen member FileItem changed", path, e)
				}
				if operation.NewName != nil || operation.Path != filepath.Join(parent.Target, parent.directoryTarget(relative)) || filepath.Dir(operation.PublicationPath) != filepath.Dir(operation.Path) {
					t.Fatal("logical/staged path contract", operation)
				}
				counts[path]++
			}
			expected := len(contents)
			if !positive {
				expected = 1
			}
			if len(counts) != expected {
				t.Fatal("actual actor members", counts, expected)
			}
			for path, n := range counts {
				if n != 1 {
					t.Fatal("actor repeated completed member", path, n)
				}
			}
		})
	}
}
