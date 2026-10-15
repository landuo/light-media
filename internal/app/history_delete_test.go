package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/download"
)

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
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); e != nil {
		t.Fatal(e)
	}
	item, e := a.transferHistoryItem(context.Background(), plan.Files[0].ID)
	if e != nil {
		t.Fatal(e)
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
	for _, source := range []bool{false, true} {
		for _, target := range []bool{false, true} {
			t.Run(map[bool]string{false: "keep-source", true: "delete-source"}[source]+"/"+map[bool]string{false: "keep-target", true: "delete-target"}[target], func(t *testing.T) {
				a, item := historyRemovalFixture(t)
				if _, e := a.deleteTransferHistory(context.Background(), item, historyDeleteRequest{Confirm: true, DeleteSource: source, DeleteTarget: target}); e != nil {
					t.Fatal(e)
				}
				for path, removed := range map[string]bool{item.Source: source, item.Target: target} {
					_, e := os.Stat(path)
					if removed != os.IsNotExist(e) {
						t.Fatal("wrong file choice", path, removed, e)
					}
				}
			})
		}
	}
}

func TestTransferHistoryDeletionRejectsChangedOrDownloaderSourceBeforeTargetDeletion(t *testing.T) {
	for _, mode := range []string{"changed", "downloader-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			if mode == "changed" {
				importWrite(t, item.Source, []byte("changed synthetic source"))
			} else {
				a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:1"}, DownloadPath: filepath.Dir(item.Source)}}
			}
			if _, e := a.deleteTransferHistory(context.Background(), item, historyDeleteRequest{Confirm: true, DeleteSource: true, DeleteTarget: true}); e == nil {
				t.Fatal("unsafe source deletion accepted")
			}
			for _, path := range []string{item.Source, item.Target} {
				if _, e := os.Stat(path); e != nil {
					t.Fatal("preflight failure changed files", e)
				}
			}
		})
	}
}

func TestTransferHistoryDeletionResumesQuarantinedSource(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := context.Background()
	snapshot, e := snapshotPath(item.Source)
	if e != nil {
		t.Fatal(e)
	}
	receipt := historySourceRemoval{HistoryID: item.ID, State: "deleting", File: assetRemovalFile{Path: item.Source, Snapshot: snapshot, Kind: "source", Quarantine: ".light-media-remove-test.part"}}
	if e = a.Store.Put(ctx, "history_source_removal", item.ID, receipt); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(item.Source, filepath.Join(filepath.Dir(item.Source), receipt.File.Quarantine)); e != nil {
		t.Fatal(e)
	}
	if _, e = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteSource: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(filepath.Dir(item.Source), receipt.File.Quarantine)); !os.IsNotExist(e) {
		t.Fatal("unfinished deletion not resumed", e)
	}
	if _, e = os.Stat(item.Target); e != nil {
		t.Fatal("resuming source removal touched target", e)
	}
}

func TestTransferHistoryDeletionPreservesActualSeedingSource(t *testing.T) {
	a, item := historyRemovalFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "seeding synthetic", "save_path": filepath.Dir(item.Source), "progress": 1.0, "state": "uploading"}})
		default:
			t.Errorf("unexpected mutation or request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qbittorrent", BaseURL: server.URL}}}
	if _, e := a.deleteTransferHistory(context.Background(), item, historyDeleteRequest{Confirm: true, DeleteSource: true, DeleteTarget: true}); e == nil {
		t.Fatal("actual downloader source deletion accepted")
	}
	for _, path := range []string{item.Source, item.Target} {
		if _, e := os.Stat(path); e != nil {
			t.Fatal("seeding protection changed file", path, e)
		}
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
	if _, e := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true}); fileCode(e) != "HISTORY_TARGET_CHANGED" {
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
	if _, e = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true}); e != nil {
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
