package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func storagePublicationSDKPID(t *testing.T, a *App, observer string) int {
	t.Helper()
	r := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/state", "")
	var state struct{ PID int }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &state) != nil || state.PID <= 0 || state.PID == os.Getpid() {
		t.Fatal("independent SDK actor", r.Code)
	}
	return state.PID
}
func storagePublicationSDKCalls(t *testing.T, a *App, observer string) []plugins.StorageOperationCall {
	t.Helper()
	a.wg.Wait()
	r := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
	var rows []struct {
		Type string
		Data json.RawMessage
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &rows) != nil {
		t.Fatal("actual observations", r.Code)
	}
	calls := []plugins.StorageOperationCall{}
	for _, row := range rows {
		if row.Type != "storage.copy" && row.Type != "storage.link" {
			continue
		}
		var call plugins.StorageOperationCall
		if err := json.Unmarshal(row.Data, &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}
func storagePublicationColdReopen(t *testing.T, a *App, observer string, job Job) (*App, Job) {
	t.Helper()
	pid := storagePublicationSDKPID(t, a, observer)
	a.wg.Wait()
	settings, dir := a.Settings(), a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.config = settings
	if err = reopened.initializeOriginalPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if storagePublicationSDKPID(t, reopened, observer) == pid {
		t.Fatal("SDK process survived cold reopen")
	}
	if _, err = reopened.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = scanJob(reopened.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	return reopened, job
}

// One returned-before-staged cold point through each actual entry: ordinary
// copy and directory hardlink. These exercise real SDK storage objects, not
// manually fabricated results or cloned receipts.
func TestStorageCopyLinkActualSDKColdStage(t *testing.T) {
	for _, directory := range []bool{false, true} {
		method, name := "copy", "ordinary-copy"
		if directory {
			method, name = "hardlink", "directory-hardlink"
		}
		t.Run(name, func(t *testing.T) {
			a, sourceRoot, library := importTestApp(t)
			observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(sourceRoot), "storage_mode": "directory-names"})
			nativeMoveFixture(t, a)
			source := filepath.Join(sourceRoot, "Storage.2024.mkv")
			contents := map[string][]byte{source: []byte("selected source actor original video")}
			if directory {
				source = filepath.Join(sourceRoot, "Storage.2024.BluRay")
				contents = map[string][]byte{filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"): []byte("owned stream"), filepath.Join(source, "BDMV", "index.bdmv"): []byte("owned index")}
			}
			for path, data := range contents {
				importWrite(t, path, data)
			}
			var plan ImportPlan
			var job Job
			var err error
			if directory {
				out, e := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{source}, TargetPath: library, Mode: method, MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
				if e != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
					t.Fatal("directory enqueue", out, e)
				}
				if err = a.Store.Get(t.Context(), "import_plan", out.Items[0].PlanID, &plan); err != nil {
					t.Fatal(err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, out.Items[0].JobID); err != nil {
					t.Fatal(err)
				}
				job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, out.Items[0].JobID))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				plan, err = a.previewImport(t.Context(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: method, Scraping: importFlag(false), Files: []ImportFileRequest{{Path: source}}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatal("preview", plan, err)
				}
				job = overwriteStart(t, a, plan)
			}
			key := plan.ID + "/" + plan.Files[0].ID
			selector := "NEW.id='" + key + "'"
			if directory {
				selector = "NEW.id LIKE '" + key + "/directory/%'"
			}
			_, err = a.Store.DB.Exec(`CREATE TRIGGER storage_publication_pause AFTER UPDATE ON records WHEN NEW.kind='import_receipt' AND ` + selector + ` AND json_extract(NEW.payload,'$.state')='planned' AND length(json_extract(NEW.payload,'$.storage_publication.result'))>0 BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id='` + job.ID + `'; END`)
			if err != nil {
				t.Fatal(err)
			}
			run := func(app *App, j Job) error {
				if directory {
					return app.safeRun(t.Context(), j)
				}
				return app.runImport(t.Context(), j, plan.ID, nil)
			}
			err = run(a, job)
			if !originalTransferInterrupted(err) {
				t.Fatal("real returned stage did not pause", err)
			}
			childKey := key
			var parent importReceipt
			if directory {
				if err = a.Store.Get(t.Context(), "import_receipt", key, &parent); err != nil || len(parent.DirectoryFiles) != 1 {
					t.Fatal("actual directory prefix", parent, err)
				}
				childKey = key + "/directory/" + tokenHash(parent.DirectoryFiles[0])
			}
			var child importReceipt
			if err = a.Store.Get(t.Context(), "import_receipt", childKey, &child); err != nil || child.State != "planned" || child.StageSnapshot != (importSnapshot{}) || child.StoragePublication == nil || len(child.StoragePublication.Result) == 0 {
				t.Fatal("sealed pre-staged receipt", child, err)
			}
			if child.StoragePublication.Method != method || child.StoragePublication.InstanceID != observer {
				t.Fatal("actual call identity", child.StoragePublication)
			}
			var result json.RawMessage
			if err = a.Store.Unseal(child.StoragePublication.Result, &result); err != nil || string(result) != "true" {
				t.Fatal("actual sealed result", string(result), err)
			}
			stage := filepath.Join(filepath.Dir(child.Target), child.Stage)
			stamp, err := snapshotPath(stage)
			if err != nil || stamp != child.StoragePublication.ResultSnapshot {
				t.Fatal("returned entry binding", stamp, err)
			}
			info, err := os.Stat(stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`DROP TRIGGER storage_publication_pause`); err != nil {
				t.Fatal(err)
			}
			a, job = storagePublicationColdReopen(t, a, observer, job)
			if err = run(a, job); err != nil {
				t.Fatal("cold result recovery", err)
			}
			if _, err = a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.Get(t.Context(), "import_receipt", childKey, &child); err != nil || child.State != "published" || child.StageSnapshot != stamp {
				t.Fatal("stage inode was not reused", child, err)
			}
			after, err := os.Stat(child.Target)
			if err != nil || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) {
				t.Fatal("actor entry attributes changed", err)
			}
			if _, err = os.Lstat(stage); !os.IsNotExist(err) {
				t.Fatal("published stage retained", err)
			}
			if directory {
				if err = a.Store.Get(t.Context(), "import_receipt", key, &parent); err != nil || parent.State != "published" || len(parent.DirectoryCompleted) != len(contents) || parent.directoryTarget("BDMV/STREAM/00001.m2ts") != "BluRay/Streams/feature.m2ts" {
					t.Fatal("directory mapping/completion", parent, err)
				}
			}
			calls := storagePublicationSDKCalls(t, a, observer)
			if len(calls) != len(contents) {
				t.Fatal("source actor replay/missing", len(calls), len(contents))
			}
			seen := map[string]bool{}
			for _, call := range calls {
				var path string
				if call.FileItem == nil || json.Unmarshal(call.FileItem.Fields["path"], &path) != nil || seen[path] {
					t.Fatal("source actor FileItem/replay", call)
				}
				seen[path] = true
				var receipt importReceipt
				id := key
				if directory {
					relative, e := filepath.Rel(source, path)
					if e != nil {
						t.Fatal(e)
					}
					id = key + "/directory/" + tokenHash(relative)
				}
				if e := a.Store.Get(t.Context(), "import_receipt", id, &receipt); e != nil || receipt.StoragePublication == nil {
					t.Fatal(receipt, e)
				}
				var frozen plugins.ModuleFileItem
				if e := a.Store.Unseal(receipt.StoragePublication.FileItem, &frozen); e != nil || !storageMoveSameFileItem(call.FileItem, &frozen) {
					t.Fatal("FileItem fidelity", e)
				}
				if call.Operation != originalTransferType(method) || call.PublicationPath != filepath.Join(filepath.Dir(receipt.Target), receipt.Stage) {
					t.Fatal("actual logical/stage args", call)
				}
				if method == "copy" && (call.Path != filepath.Dir(receipt.Target) || call.NewName == nil || *call.NewName != filepath.Base(receipt.Target)) || method == "hardlink" && (call.Path != receipt.Target || call.NewName != nil) {
					t.Fatal("StorageBase signature", call)
				}
				if data, e := os.ReadFile(receipt.Target); e != nil || !bytes.Equal(data, contents[path]) {
					t.Fatal("actual copied/linked bytes", e)
				}
			}
			for path, data := range contents {
				if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
					t.Fatal("source mutated", e)
				}
			}
		})
	}
}

func TestStorageCopyLinkActualSDKRejectsCounterfeitAndUnreturned(t *testing.T) {
	for _, test := range []struct{ method, mode string }{{"copy", "link-is-not-copy"}, {"hardlink", "copy-is-not-link"}, {"copy", "missing-result"}} {
		t.Run(test.mode, func(t *testing.T) {
			a, sourceRoot, _ := importTestApp(t)
			observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(sourceRoot), "storage_mode": test.mode})
			nativeMoveFixture(t, a)
			source := filepath.Join(sourceRoot, "Storage.2024.mkv")
			importWrite(t, source, []byte("source content unchanged"))
			plan, err := a.previewImport(t.Context(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: test.method, Scraping: importFlag(false), Files: []ImportFileRequest{{Path: source}}})
			if err != nil || len(plan.Files) != 1 {
				t.Fatal(plan, err)
			}
			key := plan.ID + "/" + plan.Files[0].ID
			job := overwriteStart(t, a, plan)
			if test.mode == "missing-result" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER storage_publication_missing BEFORE UPDATE ON records WHEN NEW.kind='import_receipt' AND NEW.id='` + key + `' AND length(json_extract(NEW.payload,'$.storage_publication.result'))>0 BEGIN SELECT RAISE(FAIL,'result persistence interrupted'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.runImport(t.Context(), job, plan.ID, nil); err == nil {
				t.Fatal("unconfirmed/counterfeit actor succeeded")
			}
			if test.mode != "missing-result" && fileCode(err) != "REVIEW_PLUGIN_TRANSFER_METHOD" {
				t.Fatal("wrong mode accepted", err)
			}
			var receipt importReceipt
			if err = a.Store.Get(t.Context(), "import_receipt", key, &receipt); err != nil || receipt.StoragePublication == nil || receipt.State != "planned" {
				t.Fatal("real started receipt missing", receipt, err)
			}
			stage := filepath.Join(filepath.Dir(receipt.Target), receipt.Stage)
			before, err := snapshotPath(stage)
			if err != nil {
				t.Fatal("real actor stage", err)
			}
			if test.mode == "missing-result" {
				if len(receipt.StoragePublication.Result) != 0 {
					t.Fatal("failed persistence invented return")
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER storage_publication_missing`); err != nil {
					t.Fatal(err)
				}
				a, job = storagePublicationColdReopen(t, a, observer, job)
			}
			// No storage context: a native fallback would otherwise be available here.
			_, err = a.publishImport(t.Context(), key, source, receipt.Target, receipt.SourceSnapshot, test.method, t.Context().Err)
			if err == nil {
				t.Fatal("unconfirmed/counterfeit stage adopted")
			}
			if after, e := snapshotPath(stage); e != nil || after != before {
				t.Fatal("failed stage rewritten or deleted", e)
			}
			if _, e := os.Lstat(receipt.Target); !os.IsNotExist(e) {
				t.Fatal("failed stage published", e)
			}
			if calls := storagePublicationSDKCalls(t, a, observer); len(calls) != 1 {
				t.Fatal("source actor retried", len(calls))
			}
		})
	}
}
