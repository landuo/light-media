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

func TestImportSoftlinkActualSDKSelectedSourceStorage(t *testing.T) {
	for _, branch := range []string{"success", "cold-stage", "copy-is-not-softlink", "false"} {
		t.Run(branch, func(t *testing.T) {
			a, source, target := importTestApp(t)
			observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(source), "storage_mode": branch})
			nativeMoveFixture(t, a)
			video := filepath.Join(source, "Storage.Linked.2024.mkv")
			contents := map[string][]byte{video: []byte("actual source actor softlink video")}
			positive := branch == "success" || branch == "cold-stage"
			if positive {
				contents[filepath.Join(source, "Storage.Linked.2024.zh.srt")] = []byte("actual source actor subtitle")
			}
			for path, data := range contents {
				importWrite(t, path, data)
			}
			plan, err := a.previewImport(t.Context(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "softlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: video}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("selected actor preview", plan, err)
			}
			var meta importMetadata
			if err = a.Store.Get(t.Context(), "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			frozen := map[string]*plugins.ModuleFileItem{video: meta.OriginalTransfer[plan.Files[0].ID].Item}
			files := []PlanFile{plan.Files[0]}
			for i, side := range meta.Sidecars[plan.Files[0].ID] {
				files = append(files, originalTransferSideFile(plan.Files[0], side, i))
				if side.Original == nil {
					t.Fatal("subtitle context missing")
				}
				frozen[side.Source] = side.Original.Item
			}
			pid := func(app *App) int {
				t.Helper()
				r := ownedInputAPI(t, app, "GET", "/api/v1/plugin/"+observer+"/state", "")
				var out struct{ PID int }
				if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.PID <= 0 || out.PID == os.Getpid() {
					t.Fatal("independent SDK actor", r.Code)
				}
				return out.PID
			}
			firstPID := pid(a)
			job := overwriteStart(t, a, plan)
			if branch == "cold-stage" {
				key := plan.ID + "/" + plan.Files[0].ID
				_, err = a.Store.DB.Exec(`CREATE TRIGGER storage_softlink_pause AFTER UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND NEW.id='` + key + `' AND json_extract(NEW.payload,'$.state')='planned' AND length(json_extract(NEW.payload,'$.storage_softlink.result'))>0 BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id='` + job.ID + `'; END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			err = a.runImport(t.Context(), job, plan.ID, nil)
			if branch == "cold-stage" {
				if !originalTransferInterrupted(err) {
					t.Fatal("actual stage did not stop before snapshot commit", err)
				}
				var receipt importReceipt
				if err = a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.State != "planned" || receipt.StageSnapshot != (importSnapshot{}) || receipt.StorageSoftlink == nil || len(receipt.StorageSoftlink.Result) == 0 {
					t.Fatal("actual recoverable actor result", receipt, err)
				}
				stage := filepath.Join(filepath.Dir(receipt.Target), receipt.Stage)
				oldStamp, _, err := snapshotSoftlinkEntry(stage)
				if err != nil {
					t.Fatal("real actor-created stage", err)
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER storage_softlink_pause`); err != nil {
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
					t.Fatal("old actor reused after cold reopen")
				}
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = a.jobByID(context.Background(), job.ID)
				if err != nil {
					t.Fatal(err)
				}
				err = a.runImport(t.Context(), job, plan.ID, nil)
				if err == nil {
					var published importReceipt
					if e := a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+plan.Files[0].ID, &published); e != nil || published.StageSnapshot != oldStamp {
						t.Fatal("stage recovery replaced actor inode", published, e)
					}
				}
			}
			if positive && err != nil || !positive && err == nil {
				t.Fatal("real storage result", branch, err)
			}
			if !positive {
				var assets int
				if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM library_asset WHERE 1=1`).Scan(&assets); e != nil || assets != 0 {
					t.Fatal("counterfeit actor got asset", assets, e)
				}
				if _, e := os.Lstat(plan.Files[0].Target); !os.IsNotExist(e) {
					t.Fatal("failed actor stage was published", e)
				}
			} else {
				for _, file := range files {
					var receipt importReceipt
					if e := a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+file.ID, &receipt); e != nil || receipt.State != "published" || receipt.StorageSoftlink == nil || receipt.StorageSoftlink.InstanceID != observer || receipt.StorageSoftlink.Key != "owned-local" || receipt.Softlink == nil || receipt.SHA256 != "" {
						t.Fatal("actual actor ownership", receipt, e)
					}
					if _, e := snapshotOwnedTransferPath(file.Target, receipt.StageSnapshot, receipt.Softlink, true); e != nil {
						t.Fatal(e)
					}
				}
			}
			for path, data := range contents {
				if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
					t.Fatal("softlink actor changed source", path, e)
				}
			}
			a.wg.Wait()
			r := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &calls) != nil {
				t.Fatal("actual SDK observations", r.Code)
			}
			counts := map[string]int{}
			for _, call := range calls {
				if call.Type != "storage.softlink" {
					continue
				}
				var operation plugins.StorageOperationCall
				if e := json.Unmarshal(call.Data, &operation); e != nil || operation.FileItem == nil {
					t.Fatal("storage signature", e)
				}
				var path string
				if e := json.Unmarshal(operation.FileItem.Fields["path"], &path); e != nil || !storageMoveSameFileItem(operation.FileItem, frozen[path]) {
					t.Fatal("frozen FileItem changed", path, e)
				}
				if operation.Operation != "softlink" || operation.NewName != nil || operation.Path == "" || filepath.Dir(operation.PublicationPath) != filepath.Dir(operation.Path) || !within(target, operation.Path) {
					t.Fatal("source softlink logical/staged path", operation)
				}
				counts[path]++
			}
			for path := range contents {
				if counts[path] != 1 {
					t.Fatal("source actor missing or repeated", path, counts)
				}
			}
		})
	}
}
