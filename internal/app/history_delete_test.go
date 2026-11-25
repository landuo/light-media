package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/download"
)

// A history request must not enumerate a daemon's unrelated tasks, even when it
// manages the selected download's source directory.
func historyUnrelatedDownloader(t *testing.T, a *App, item transferHistoryItem) {
	t.Helper()
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("history deletion contacted qB: %s %s", r.Method, r.URL.Path)
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "save_path": "/unmapped/downloader", "state": "uploading", "progress": 1},
				map[string]any{"hash": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "save_path": filepath.Dir(item.Source), "state": "uploading", "progress": 1},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(qb.Close)
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qbittorrent", BaseURL: qb.URL}, SharedPaths: true, DownloadPath: a.config.Roots[0].Path}}
	a.invalidateSettings()
}

func TestHistoryTargetDeletionFinishesWithUnmappedDownloaderDirectory(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := t.Context()
	a.config.Directories = []DirectoryRule{{ID: "media", Name: "媒体目录", SourcePath: filepath.Dir(item.Source), LibraryPath: a.config.Roots[0].Path, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "none", Enabled: true}}
	historyUnrelatedDownloader(t, a, item)
	sourceBefore, err := os.Stat(item.Source)
	if err != nil {
		t.Fatal(err)
	}
	season := filepath.Dir(item.Target)
	if season == a.config.Roots[0].Path {
		t.Fatal("fixture needs a library subdirectory")
	}
	sidecar := filepath.Join(season, "season.nfo")
	importWrite(t, sidecar, []byte("season metadata"))
	// Resume the exact production sequence: the first request removed the
	// library entry, then stopped during parent cleanup before hiding the row.
	if err := a.removeHistoricalTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(item.Target); !os.IsNotExist(err) {
		t.Fatalf("expected completed target removal: %v", err)
	}
	payload, err := json.Marshal(historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/", bytes.NewReader(payload))
	if _, err = a.transferHistoryAPI(req, "transfers/"+item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(item.Target); !os.IsNotExist(err) {
		t.Fatalf("library entry still present: %v", err)
	}
	sourceAfter, err := os.Stat(item.Source)
	if err != nil || !os.SameFile(sourceBefore, sourceAfter) {
		t.Fatalf("download source changed: %v", err)
	}
	if _, err := os.Stat(season); !os.IsNotExist(err) {
		t.Fatalf("expected MP to remove the parent containing only metadata: %v", err)
	}
	if _, err := a.transferHistoryItem(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted history row: %v", err)
	}
	var state historyParentCleanup
	var encoded []byte
	if err := a.Store.Get(ctx, "history_parent_cleanup", item.executionFileID()+"/target", &encoded); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Unseal(encoded, &state); err != nil || !state.Completed {
		t.Fatalf("expected terminal parent cleanup receipt: %v", err)
	}
}

func historyRemovalFixture(t *testing.T) (*App, transferHistoryItem) {
	t.Helper()
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	input := filepath.Join(source, "History (2024).mkv")
	importWrite(t, input, []byte("history delete synthetic"))
	plan, e := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input}}})
	if e != nil {
		t.Fatal(e)
	}
	job, e := a.executeImport(context.Background(), plan.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID); e != nil {
		t.Fatal(e)
	}
	job.State, job.Generation = "running", 1
	if e = a.runImport(context.Background(), job, plan.ID, nil); e != nil {
		t.Fatal(e)
	}
	if finished, err := a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil || !finished {
		t.Fatalf("fixture import did not finish: %v", err)
	}
	var item transferHistoryItem
	for _, candidate := range historyParityReadPage(t, a, "page_size=1000").Items {
		if candidate.FileID == plan.Files[0].ID {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		t.Fatal("history row not found for fixture file")
	}
	return a, item
}

func TestTransferHistoryDeletionDefaultsToRecordOnly(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := context.Background()
	if _, e := a.deleteTransferHistory(ctx, item, historyDeleteRequest{}); e == nil {
		t.Fatal("missing confirmation accepted")
	}
	if _, e := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true}); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{item.Source, item.Target} {
		if _, e := os.Stat(path); e != nil {
			t.Fatal("record removal touched file", path, e)
		}
	}
	var plan ImportPlan
	if e := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil {
		t.Fatal("audit plan lost", e)
	}
	out, e := a.transferHistory(ctx, httptest.NewRequest("GET", "/history/transfers", nil))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(out)
	var page struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(b, &page)
	if page.Total != 0 {
		t.Fatal("deleted row remained", string(b))
	}
}

func TestTransferHistoryDeletionHonorsIndependentFileChoices(t *testing.T) {
	for _, tc := range []struct {
		name           string
		source, target bool
	}{
		{"record_only", false, false},
		{"source_and_record", true, false},
		{"library_and_record", false, true},
		{"both_files_and_record", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			historyUnrelatedDownloader(t, a, item)
			payload, err := json.Marshal(historyDeleteRequest{Confirm: true, DeleteSource: tc.source, DeleteTarget: tc.target, FileID: item.executionFileID()})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodDelete, "/", bytes.NewReader(payload))
			if _, err := a.transferHistoryAPI(req, "transfers/"+item.ID); err != nil {
				t.Fatal(err)
			}
			for path, removed := range map[string]bool{item.Source: tc.source, item.Target: tc.target} {
				_, err := os.Stat(path)
				if removed && !os.IsNotExist(err) || !removed && err != nil {
					t.Fatalf("file %s, expected removed=%t: %v", path, removed, err)
				}
			}
			if _, err := a.transferHistoryItem(t.Context(), item.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("expected deleted history row: %v", err)
			}
		})
	}
}

func TestTransferHistoryDeletionKeepsChangedSourceAfterDeletingTarget(t *testing.T) {
	a, item := historyRemovalFixture(t)
	if err := os.Remove(item.Source); err != nil {
		t.Fatal(err)
	}
	const replacement = "changed synthetic source"
	importWrite(t, item.Source, []byte(replacement))
	if _, err := a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, DeleteSource: true, DeleteTarget: true, FileID: item.executionFileID()}); fileCode(err) != "FILE_SNAPSHOT_CHANGED" {
		t.Fatalf("expected changed-file error: %v", err)
	}
	if got, err := os.ReadFile(item.Source); err != nil || string(got) != replacement {
		t.Fatalf("replacement source was changed: %v", err)
	}
	if _, err := os.Stat(item.Target); !os.IsNotExist(err) {
		t.Fatalf("destination should already have been removed: %v", err)
	}
	if _, err := a.transferHistoryItem(t.Context(), item.ID); err != nil {
		t.Fatalf("failed source deletion must retain history: %v", err)
	}
}

func TestTransferHistoryDeletionResumesQuarantinedSource(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := context.Background()
	snapshot, e := snapshotPath(item.Source)
	if e != nil {
		t.Fatal(e)
	}
	receipt := historySourceRemoval{HistoryID: item.executionFileID(), State: "deleting", File: assetRemovalFile{Path: item.Source, Snapshot: snapshot, Kind: "source", Quarantine: ".light-media-remove-test.part"}}
	if e = a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(item.Source, filepath.Join(filepath.Dir(item.Source), receipt.File.Quarantine)); e != nil {
		t.Fatal(e)
	}
	if _, e = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteSource: true, FileID: item.executionFileID()}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(filepath.Dir(item.Source), receipt.File.Quarantine)); !os.IsNotExist(e) {
		t.Fatal("unfinished deletion not resumed", e)
	}
	if _, e = os.Stat(item.Target); e != nil {
		t.Fatal("resuming source removal touched target", e)
	}
}

func TestTransferHistoryDeletionCannotRemoveReplacementAsset(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := context.Background()
	var owned managedFile
	if e := a.Store.Get(ctx, "managed_file", tokenHash(item.Target), &owned); e != nil {
		t.Fatal(e)
	}
	var replacement Asset
	if e := a.Store.Get(ctx, "asset", owned.AssetID, &replacement); e != nil {
		t.Fatal(e)
	}
	// Reimporting the same original can produce identical bytes/inodes at the
	// same path but a new asset owner. The old history must not authorize it.
	replacement.ID = "replacement-owner"
	if e := a.Store.Put(ctx, "asset", replacement.ID, replacement); e != nil {
		t.Fatal(e)
	}
	owned.AssetID = replacement.ID
	if e := a.Store.Put(ctx, "managed_file", tokenHash(item.Target), owned); e != nil {
		t.Fatal(e)
	}
	if _, e := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()}); fileCode(e) != "HISTORY_TARGET_CHANGED" {
		t.Fatal("old history authorized replacement deletion", e)
	}
	if _, e := os.Stat(item.Target); e != nil {
		t.Fatal("replacement file was removed", e)
	}
	if e := a.Store.Get(ctx, "asset", replacement.ID, &replacement); e != nil || replacement.State != "present" {
		t.Fatal("replacement state changed", replacement, e)
	}
}

func TestTransferHistoryDeletionResumesQuarantinedTarget(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := context.Background()
	var asset Asset
	if e := a.assetAtPath(ctx, item.Target, &asset); e != nil {
		t.Fatal(e)
	}
	preview, e := a.previewAssetRemoval(ctx, asset)
	if e != nil || len(preview.Files) != 1 {
		t.Fatal("preview", preview, e)
	}
	preview.State = "deleting"
	preview.Files[0].Quarantine = ".light-media-remove-target-resume.part"
	preview.Files[0].Quarantined = true
	quarantine := filepath.Join(filepath.Dir(item.Target), preview.Files[0].Quarantine)
	if e = os.Rename(item.Target, quarantine); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Put(ctx, "removal_preview", preview.ID, preview); e != nil {
		t.Fatal(e)
	}
	if _, e = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(quarantine); !os.IsNotExist(e) {
		t.Fatal("target receipt not resumed", e)
	}
	if _, e = os.Stat(item.Source); e != nil {
		t.Fatal("resuming target deletion touched source", e)
	}
	if e = a.Store.Get(ctx, "asset", asset.ID, &asset); e != nil || asset.State != "removed" {
		t.Fatal("target asset not finalized", asset, e)
	}
}
