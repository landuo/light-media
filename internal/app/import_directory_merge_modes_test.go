package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func sharedModeSDKCalls(t *testing.T, a *App, observer, operation string) []plugins.StorageOperationCall {
	t.Helper()
	a.wg.Wait()
	r := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
	var rows []struct {
		Type string
		Data json.RawMessage
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &rows) != nil {
		t.Fatal("actor observations", r.Code)
	}
	out := []plugins.StorageOperationCall{}
	for _, row := range rows {
		if row.Type == "storage."+operation {
			var call plugins.StorageOperationCall
			if e := json.Unmarshal(row.Data, &call); e != nil {
				t.Fatal(e)
			}
			out = append(out, call)
		}
	}
	return out
}
func sharedDirectoryPolicyCalls(t *testing.T, a *App, observer string, wantIntercepts int) {
	t.Helper()
	a.wg.Wait()
	r := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
	var rows []struct{ Type string }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &rows) != nil {
		t.Fatal("directory policy observations", r.Code)
	}
	intercepts := 0
	for _, row := range rows {
		if row.Type == "transfer.overwrite.check" {
			t.Fatal("directory entered ordinary-file overwrite policy")
		}
		if row.Type == "transfer.intercept" {
			intercepts++
		}
	}
	if intercepts != wantIntercepts {
		t.Fatal("directory acquired per-leaf intercepts", intercepts, wantIntercepts)
	}
}
func sharedTwoLeafContents() map[string][]byte {
	return map[string][]byte{"BDMV/STREAM/00001.m2ts": []byte("owned stream"), "BDMV/index.bdmv": []byte("owned index")}
}

func TestSharedDirectorySoftlinkActualPrefixAndLifecycle(t *testing.T) {
	for _, branch := range []string{"success-metadata-delete", "occupied-prefix", "history-reorganize", "selected-source-cold"} {
		t.Run(branch, func(t *testing.T) {
			options := sharedDirectoryFixtureOptions{Contents: sharedTwoLeafContents(), MissingLeaf: true, Storage: branch == "selected-source-cold"}
			if branch == "occupied-prefix" {
				options.MissingLeaf = false
				options.OccupiedLeaf = "BDMV/index.bdmv"
			}
			a, _, plan, job, fixture := prepareSharedDirectoryImportConfigured(t, "softlink", options, branch == "success-metadata-delete")
			key := plan.ID + "/" + plan.Files[0].ID
			var occupied importSnapshot
			if branch == "occupied-prefix" {
				var e error
				occupied, e = snapshotPath(filepath.Join(fixture.Target, options.OccupiedLeaf))
				if e != nil {
					t.Fatal(e)
				}
			}
			if options.Storage {
				if _, e := a.Store.DB.Exec(`CREATE TRIGGER shared_soft_stage AFTER UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND NEW.id LIKE '` + key + `/directory/%' AND json_extract(NEW.payload,'$.state')='planned' AND length(json_extract(NEW.payload,'$.storage_softlink.result'))>0 BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id='` + job.ID + `'; END`); e != nil {
					t.Fatal(e)
				}
			}
			err := a.runImport(t.Context(), job, plan.ID, nil)
			var stageStamp importSnapshot
			var firstTarget string
			if options.Storage {
				if !originalTransferInterrupted(err) {
					t.Fatal("returned actual softlink stage did not pause", err)
				}
				var prefix importReceipt
				if e := a.Store.Get(t.Context(), "import_receipt", key, &prefix); e != nil || len(prefix.DirectoryFiles) != 1 || prefix.DirectoryStorageSoftlink == nil || prefix.SharedDirectory == nil || prefix.Softlink != nil {
					t.Fatal("shared actor prefix", prefix, e)
				}
				var child importReceipt
				if e := a.Store.Get(t.Context(), "import_receipt", key+"/directory/"+tokenHash(prefix.DirectoryFiles[0]), &child); e != nil || child.StorageSoftlink == nil || len(child.StorageSoftlink.Result) == 0 || child.StageSnapshot.Inode != 0 {
					t.Fatal("actual returned stage journal", child, e)
				}
				stageStamp, _, err = snapshotSoftlinkEntry(filepath.Join(filepath.Dir(child.Target), child.Stage))
				if err != nil {
					t.Fatal(err)
				}
				firstTarget = child.Target
				if calls := sharedModeSDKCalls(t, a, fixture.Observer, "softlink"); len(calls) != 1 || calls[0].Path != child.Target {
					t.Fatal("first selected actor call", calls)
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER shared_soft_stage`); err != nil {
					t.Fatal(err)
				}
				a, job = storagePublicationColdReopen(t, a, fixture.Observer, job)
				err = a.runImport(t.Context(), job, plan.ID, nil)
			}
			var receipt importReceipt
			if e := a.Store.Get(t.Context(), "import_receipt", key, &receipt); e != nil {
				t.Fatal(e)
			}
			wantIntercepts := 1
			if options.Storage {
				wantIntercepts = 2
			}
			sharedDirectoryPolicyCalls(t, a, fixture.Observer, wantIntercepts)
			if branch == "occupied-prefix" {
				if err == nil || receipt.State == "published" || len(receipt.DirectoryCompleted) != 1 || len(receipt.DirectoryFiles) != 2 || receipt.Softlink != nil || receipt.SharedDirectory == nil || len(receipt.SharedDirectory.Members) != 1 {
					t.Fatal("real unsuccessful prefix", receipt, err)
				}
				if now, e := snapshotPath(filepath.Join(fixture.Target, options.OccupiedLeaf)); e != nil || now != occupied {
					t.Fatal("occupied leaf predeleted or overwritten", now, e)
				}
				if text, e := os.Readlink(filepath.Join(fixture.Target, receipt.directoryTarget(receipt.DirectoryCompleted[0]))); e != nil || text != filepath.Join(fixture.Source, receipt.DirectoryCompleted[0]) {
					t.Fatal("successful prefix absent", text, e)
				}
				permit := receipt.DirectoryMerge.Children[options.OccupiedLeaf]
				if permit == nil || permit.Published || permit.Before == nil || *permit.Before != occupied {
					t.Fatal("occupied-leaf prefix record", permit)
				}
				if _, e := os.Lstat(filepath.Join(filepath.Dir(filepath.Join(fixture.Target, options.OccupiedLeaf)), permit.Backup)); !os.IsNotExist(e) {
					t.Fatal("softlink predeleted occupied leaf into a backup", e)
				}
				checkSharedPreserved(t, fixture)
				if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
					t.Fatal("failed softlink modified source", e)
				}
				return
			}
			if err != nil {
				t.Fatal("shared directory actual softlink", err)
			}
			if _, err = a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
				t.Fatal(err)
			}
			if receipt.OriginalSoftlinkDirectory == nil || receipt.OriginalSoftlinkDirectory.Permission.TargetAbsent || receipt.OriginalSoftlinkDirectory.Permission.Adopted {
				t.Fatal("existing root acquired whole-tree external permission", receipt.OriginalSoftlinkDirectory)
			}
			if receipt.State != "published" || receipt.Softlink != nil || receipt.SharedDirectory == nil || len(receipt.SharedDirectory.Members) != 2 || receipt.StageSnapshot.Size != 23 {
				t.Fatal("shared referent-sized proof", receipt)
			}
			for relative, member := range receipt.SharedDirectory.Members {
				if member.Softlink == nil || member.Softlink.Source != filepath.Join(fixture.Source, relative) {
					t.Fatal("actual leaf link proof", relative, member)
				}
				if filepath.Join(fixture.Target, relative) == firstTarget && member.Snapshot != stageStamp {
					t.Fatal("cold recovery replaced stage")
				}
				leaf, known, e := a.ownedDirectorySoftlinkMember(t.Context(), filepath.Join(fixture.Target, relative), true)
				if e != nil || !known || leaf.SharedDirectory != nil || leaf.SourceSnapshot != member.Softlink.SourceSnapshot {
					t.Fatal("exact derived shared link", leaf, known, e)
				}
			}
			if options.Storage {
				calls := sharedModeSDKCalls(t, a, fixture.Observer, "softlink")
				if len(calls) != 2 || calls[0].Path != firstTarget || calls[1].Path == firstTarget {
					t.Fatal("cold source actor repeated completed leaf", calls)
				}
			}
			if e := a.validateSharedSoftlinkPublication(t.Context(), receipt); e != nil {
				t.Fatal(e)
			}
			if e := a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); e != nil {
				t.Fatal(e)
			}
			var asset Asset
			if e := a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &asset); e != nil {
				t.Fatal(e)
			}
			item, e := a.transferHistoryItem(t.Context(), plan.Files[0].ID)
			if e != nil {
				t.Fatal(e)
			}
			if branch == "success-metadata-delete" {
				var scrapeID string
				if e = a.Store.DB.QueryRow(`SELECT id FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=? LIMIT 1`, asset.ID).Scan(&scrapeID); e != nil {
					t.Fatal(e)
				}
				runSoftlinkDirectoryJob(t, a, scrapeID)
				var owned managedFile
				if e = a.Store.Get(t.Context(), "managed_file", tokenHash(asset.Path), &owned); e != nil || owned.Softlink != nil || owned.SharedDirectory == nil || len(owned.SharedDirectory.Members) != 3 {
					t.Fatal("actual metadata subset", owned, e)
				}
				nfo := filepath.Join(asset.Path, filepath.Base(asset.Path)+".nfo")
				if info, e := os.Lstat(nfo); e != nil || !info.Mode().IsRegular() {
					t.Fatal("actual ordinary NFO", e)
				}
				if _, e = a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: true}); e != nil {
					t.Fatal("shared linked target removal", e)
				}
				for relative := range owned.SharedDirectory.Members {
					if _, e = os.Lstat(filepath.Join(asset.Path, relative)); !os.IsNotExist(e) {
						t.Fatal("owned target retained", relative, e)
					}
				}
			}
			if branch == "history-reorganize" {
				destination := filepath.Join(a.Settings().Roots[0].Path, "next")
				if e = os.Mkdir(destination, 0750); e != nil {
					t.Fatal(e)
				}
				out, e := a.manualOrganize(t.Context(), manualOrganizeRequest{HistoryIDs: []string{item.ID}, FromHistory: true, Mode: "softlink", TargetPath: destination, Scraping: importFlag(false)})
				if e != nil || out.Submitted != 1 || out.Failed != 0 {
					t.Fatal("shared softlink reorganization", out, e)
				}
				runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
			}
			checkSharedPreserved(t, fixture)
			if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
				t.Fatal("softlink consumer followed source", now, e)
			}
		})
	}
}

func TestSharedDirectoryMoveActualNativeAndSelectedCold(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "selected-source-cold"}[selected], func(t *testing.T) {
			a, _, plan, job, fixture := prepareSharedDirectoryImportConfigured(t, "move", sharedDirectoryFixtureOptions{Contents: sharedTwoLeafContents(), Storage: selected})
			key := plan.ID + "/" + plan.Files[0].ID
			if selected {
				if _, e := a.Store.DB.Exec(`CREATE TRIGGER shared_move_stage AFTER UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND NEW.id LIKE '` + key + `/directory/%' AND json_extract(NEW.payload,'$.state')='planned' AND length(json_extract(NEW.payload,'$.storage_move.result'))>0 BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id='` + job.ID + `'; END`); e != nil {
					t.Fatal(e)
				}
			}
			err := a.runImport(t.Context(), job, plan.ID, nil)
			var firstTarget string
			var stageStamp importSnapshot
			if selected {
				if !originalTransferInterrupted(err) {
					t.Fatal("actual move result did not pause", err)
				}
				var prefix importReceipt
				if e := a.Store.Get(t.Context(), "import_receipt", key, &prefix); e != nil || !prefix.DirectoryListed || prefix.DirectoryStorageMove == nil || len(prefix.DirectoryFiles) != 2 {
					t.Fatal("frozen complete move tree", prefix, e)
				}
				var child importReceipt
				if e := a.Store.Get(t.Context(), "import_receipt", key+"/directory/"+tokenHash(prefix.DirectoryFiles[0]), &child); e != nil || child.StorageMove == nil || len(child.StorageMove.Result) == 0 || child.StageSnapshot.Inode != 0 {
					t.Fatal("actual selected move stage", child, e)
				}
				stageStamp, err = snapshotPath(filepath.Join(filepath.Dir(child.Target), child.Stage))
				if err != nil {
					t.Fatal(err)
				}
				firstTarget = child.Target
				if _, e := os.Lstat(child.Source); !os.IsNotExist(e) {
					t.Fatal("actor did not really move source", e)
				}
				if calls := sharedModeSDKCalls(t, a, fixture.Observer, "move"); len(calls) != 1 || calls[0].Path != filepath.Dir(firstTarget) || calls[0].NewName == nil || *calls[0].NewName != filepath.Base(firstTarget) {
					t.Fatal("actual source move call", calls)
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER shared_move_stage`); err != nil {
					t.Fatal(err)
				}
				a, job = storagePublicationColdReopen(t, a, fixture.Observer, job)
				err = a.runImport(t.Context(), job, plan.ID, nil)
			}
			if err != nil {
				t.Fatal("shared actual move", err)
			}
			if _, err = a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
				t.Fatal(err)
			}
			var receipt importReceipt
			if err = a.Store.Get(t.Context(), "import_receipt", key, &receipt); err != nil || receipt.State != "published" || receipt.DirectoryMoveState != "completed" || receipt.MoveSourceSnapshot == nil || receipt.SharedDirectory == nil || receipt.Softlink != nil || len(receipt.DirectoryCompleted) != 2 {
				t.Fatal("actual whole-directory move completion", receipt, err)
			}
			for relative, data := range fixture.Contents {
				if _, e := os.Lstat(filepath.Join(fixture.Source, relative)); !os.IsNotExist(e) {
					t.Fatal("selected source retained", relative, e)
				}
				target := filepath.Join(fixture.Target, relative)
				if got, e := os.ReadFile(target); e != nil || string(got) != string(data) {
					t.Fatal("actual destination", relative, e)
				}
				if target == firstTarget {
					if now, e := snapshotPath(target); e != nil || now != stageStamp {
						t.Fatal("actual returned move stage replaced", e)
					}
				}
			}
			if selected {
				calls := sharedModeSDKCalls(t, a, fixture.Observer, "move")
				if len(calls) != 2 || calls[0].NewName == nil || calls[1].NewName == nil || filepath.Join(calls[0].Path, *calls[0].NewName) != firstTarget || filepath.Join(calls[1].Path, *calls[1].NewName) == firstTarget {
					t.Fatal("source move replayed after cold recovery", calls)
				}
			}
			checkSharedPreserved(t, fixture)
		})
	}
}
