package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeMovedDirectoryHistoryFixture(t *testing.T) (*App, transferHistoryItem, importReceipt) {
	t.Helper()
	a, source, target := importTestApp(t)
	nativeMoveFixture(t, a)
	disc := filepath.Join(source, "History.Move.2024.BluRay")
	importWrite(t, filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts"), []byte("native moved history disc"))
	importWrite(t, filepath.Join(disc, "BDMV", "index.bdmv"), []byte("native moved history index"))
	if err := os.Mkdir(filepath.Join(disc, "CERTIFICATE"), 0750); err != nil {
		t.Fatal(err)
	}
	out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{disc}, MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "move", Scraping: importFlag(false)})
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatalf("actual directory move submission: %+v %v", out, err)
	}
	plan, job := historyUpsertAttempt(t, a, out.Items[0].JobID)
	runEpisodeGroupJob(t, a, job)
	item, err := a.transferHistoryItem(t.Context(), plan.Files[0].ID)
	if err != nil || item.Status != "success" || item.Mode != "move" {
		t.Fatalf("actual directory move history: %+v %v", item, err)
	}
	var receipt importReceipt
	if err = a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.DirectoryMoveState != "completed" || receipt.MoveSourceSnapshot == nil || receipt.SourceSnapshot != planSnapshot(plan.Files[0]) {
		t.Fatalf("missing actual native empty-container evidence: %+v %v", receipt, err)
	}
	current, entries, err := snapshotOriginalDirectory(disc)
	if err != nil || current != *receipt.MoveSourceSnapshot || current == receipt.SourceSnapshot || !sameInode(current, receipt.SourceSnapshot) {
		t.Fatal("move did not freeze its own empty-container source change", current, receipt, err)
	}
	for _, entry := range entries {
		if !entry.Directory {
			t.Fatal("native move left a file in the source tree", entry)
		}
	}
	return a, item, receipt
}

func TestImportMoveDirectoryHistoryReconcilesMissingSourceOutsideCurrentRoots(t *testing.T) {
	a, item, _ := nativeMovedDirectoryHistoryFixture(t)
	if err := os.RemoveAll(item.Source); err != nil {
		t.Fatal(err)
	}
	// Removing an old source setting must not block a no-delete reconciliation
	// of the exact directory move which already completed.
	a.config.AllowedRoots = nil
	_, exists, err := a.historySourceForRemoval(t.Context(), item)
	if err != nil || exists {
		t.Fatal("completed missing directory was treated as a fresh deletion", exists, err)
	}
	importWrite(t, filepath.Join(item.Source, "new.mkv"), []byte("unrelated new source"))
	if _, _, err = a.historySourceForRemoval(t.Context(), item); err == nil {
		t.Fatal("absence proof granted deletion of recreated directory")
	}
}

func TestImportMoveDirectoryHistoryDeletesOnlyFrozenEmptySource(t *testing.T) {
	for _, branch := range []string{"source", "both", "new-file", "wrong-receipt"} {
		t.Run(branch, func(t *testing.T) {
			a, item, receipt := nativeMovedDirectoryHistoryFixture(t)
			if branch == "new-file" {
				importWrite(t, filepath.Join(item.Source, "BDMV", "STREAM", "new-download.m2ts"), []byte("new file is not owned by completed move"))
			}
			if branch == "wrong-receipt" {
				receipt.SourceSnapshot.Inode++
				if err := a.Store.Put(t.Context(), "import_receipt", receipt.ID, receipt); err != nil {
					t.Fatal(err)
				}
			}
			deleteTarget := branch != "source"
			_, err := a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteSource: true, DeleteTarget: deleteTarget})
			blocked := branch == "new-file" || branch == "wrong-receipt"
			if blocked {
				if fileCode(err) != "FILE_SNAPSHOT_CHANGED" {
					t.Fatal("changed source or mismatched publication proof lost its original guard", err)
				}
				if _, e := os.Stat(item.Source); e != nil {
					t.Fatal("guarded source tree was removed", e)
				}
				if page := historyParityReadPage(t, a, ""); page.Total != 1 || page.Items[0].ID != item.ID {
					t.Fatal("guarded deletion hid history", page)
				}
			} else {
				if err != nil {
					t.Fatal("actual empty moved source deletion", err)
				}
				if _, e := os.Lstat(item.Source); !os.IsNotExist(e) {
					t.Fatal("explicit source deletion retained the empty disc", e)
				}
				if page := historyParityReadPage(t, a, ""); page.Total != 0 {
					t.Fatal("successful deletion retained history", page)
				}
			}
			video := filepath.Join(item.Target, "BDMV", "STREAM", "00001.m2ts")
			if deleteTarget && !blocked {
				if _, err := os.Lstat(item.Target); !os.IsNotExist(err) {
					t.Fatal("both selection retained target", err)
				}
			} else if got, err := os.ReadFile(video); err != nil || string(got) != "native moved history disc" {
				t.Fatal("source check or source-only deletion damaged target", err)
			}
			if branch == "new-file" {
				got, err := os.ReadFile(filepath.Join(item.Source, "BDMV", "STREAM", "new-download.m2ts"))
				if err != nil || string(got) != "new file is not owned by completed move" {
					t.Fatal("new source content was not preserved", err)
				}
			}
		})
	}
}

func TestImportMoveReorganizedLibrarySourceDeletionRequiresFrozenOwner(t *testing.T) {
	for _, branch := range []string{"missing-source-both", "missing-subtitle-source-both", "missing-owner-proof", "changed-old-root", "recreated-source"} {
		t.Run(branch, func(t *testing.T) {
			a, root, first, contents := nativeMoveHistoryFixture(t)
			var original transferHistoryItem
			extension := ".mkv"
			if branch == "missing-subtitle-source-both" {
				extension = ".srt"
			}
			for path, item := range first {
				if strings.HasSuffix(path, extension) {
					original = item
				}
			}
			nextRoot := filepath.Join(root, "HistoryDeleteSecondMove")
			if err := os.MkdirAll(nextRoot, 0750); err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{HistoryIDs: []string{original.ID}, FromHistory: true, TargetPath: nextRoot, Mode: "move", Scraping: importFlag(false)})
			if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
				t.Fatalf("actual history reorganization: %+v %v", out, err)
			}
			plan, job := historyUpsertAttempt(t, a, out.Items[0].JobID)
			runEpisodeGroupJob(t, a, job)
			var latest transferHistoryItem
			for _, candidate := range historyParityReadPage(t, a, "page_size=1000").Items {
				if candidate.Source == original.Target {
					latest = candidate
				}
			}
			if latest.ID == "" || latest.Status != "success" || latest.Source != original.Target || a.sourceAllowed(latest.Source) {
				t.Fatalf("fixture did not produce a managed library source outside download roots: %+v", latest)
			}
			if _, err = os.Lstat(latest.Source); !os.IsNotExist(err) {
				t.Fatal("second move did not remove its library source", err)
			}
			var meta importMetadata
			if err = a.Store.Get(t.Context(), "import_meta", plan.ID, &meta); err != nil || extension == ".mkv" && meta.Reorganize == nil {
				t.Fatal("real reorganization did not freeze its old ownership", err)
			}
			missing := branch == "missing-source-both" || branch == "missing-subtitle-source-both"
			if !missing {
				// Existing paths must never use the completed-move absence shortcut.
				importWrite(t, latest.Source, []byte("new file at former library source"))
			}
			switch branch {
			case "missing-owner-proof":
				meta.Reorganize = nil
				if err = a.Store.Put(t.Context(), "import_meta", plan.ID, &meta); err != nil {
					t.Fatal(err)
				}
			case "changed-old-root":
				found := false
				for index := range a.config.Roots {
					if a.config.Roots[index].ID == meta.Reorganize.Asset.RootID {
						a.config.Roots[index].Path = filepath.Join(root, "ChangedOldRoot")
						found = true
					}
				}
				if !found {
					t.Fatal("frozen old root was not configured")
				}
			}
			_, err = a.deleteTransferHistory(t.Context(), latest, historyDeleteRequest{Confirm: true, FileID: latest.executionFileID(), DeleteSource: true, DeleteTarget: true})
			if !missing {
				wantCode := "INVALID_SOURCE"
				if branch == "recreated-source" {
					wantCode = "FILE_SNAPSHOT_CHANGED"
				}
				if fileCode(err) != wantCode {
					t.Fatal("unproven or recreated library source was granted deletion permission", err)
				}
				if got, err := os.ReadFile(latest.Source); err != nil || string(got) != "new file at former library source" {
					t.Fatal("existing source used the missing-source shortcut", err)
				}
				if got, err := os.ReadFile(latest.Target); err != nil || !bytes.Equal(got, contents[original.Source]) {
					t.Fatal("rejected source authorization touched target", err)
				}
			} else {
				if err != nil {
					t.Fatal("delete both after a real move from a frozen library source", err)
				}
				if _, err = os.Lstat(latest.Target); !os.IsNotExist(err) {
					t.Fatal("selected target remained after delete both", err)
				}
			}
			for source, companion := range first {
				if companion.ID != original.ID {
					if got, err := os.ReadFile(companion.Target); err != nil || !bytes.Equal(got, contents[source]) {
						t.Fatal("library-source deletion touched unselected companion", err)
					}
				}
			}
		})
	}
}
