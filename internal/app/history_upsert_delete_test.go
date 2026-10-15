package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func historyUpsertDeleteFixture(t *testing.T) (*App, transferHistoryItem, transferHistoryItem, string) {
	t.Helper()
	a, source, old := reorgFixture(t)
	ctx := context.Background()
	page := historyParityReadPage(t, a, "")
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("initial history=%+v", page)
	}
	first := page.Items[0]
	target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "upsert-delete-library")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	scrape := false
	plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: old.Media.MediaType, TMDBID: old.Media.ID, TargetPath: target, Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: source}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("replacement preview=%+v err=%v", plan, err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", plan.ID))
	latest, err := a.transferHistoryItem(ctx, first.ID)
	if err != nil || latest.ID != first.ID || latest.executionFileID() != plan.Files[0].ID || latest.PlanID != plan.ID {
		t.Fatalf("latest history did not retain its stable identity: first=%+v latest=%+v err=%v", first, latest, err)
	}
	if _, err = os.Stat(old.Path); !os.IsNotExist(err) {
		t.Fatal("old managed target was not cleaned", err)
	}
	// A different file can appear at the old target after a completed reorganization.
	// The current history must never authorize deleting it.
	importWrite(t, old.Path, []byte("foreign replacement at previous target"))
	return a, first, latest, old.Path
}

func TestHistoryUpsertDeleteUsesLatestExecutionAndPreservesOldTarget(t *testing.T) {
	a, first, latest, oldTarget := historyUpsertDeleteFixture(t)
	ctx := context.Background()
	var original ImportPlan
	if err := a.Store.Get(ctx, "import_plan", first.PlanID, &original); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(original)
	if _, err := a.deleteTransferHistory(ctx, latest, historyDeleteRequest{Confirm: true, FileID: latest.executionFileID(), DeleteSource: true, DeleteTarget: true}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{latest.Source, latest.Target} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("latest execution file was not deleted", path, err)
		}
	}
	if content, err := os.ReadFile(oldTarget); err != nil || string(content) != "foreign replacement at previous target" {
		t.Fatal("old foreign target was touched", err)
	}
	var receipt historySourceRemoval
	if err := a.Store.Get(ctx, "history_source_removal", latest.executionFileID(), &receipt); err != nil || receipt.HistoryID != latest.executionFileID() || receipt.State != "completed" {
		t.Fatal("source receipt was not tied to the latest execution", receipt, err)
	}
	var hidden struct {
		FileID       string `json:"file_id"`
		ExecutionKey string `json:"execution_key"`
	}
	if err := a.Store.Get(ctx, "transfer_history_hidden", latest.ID, &hidden); err != nil || hidden.FileID != latest.executionFileID() || hidden.ExecutionKey != latest.ExecutionKey {
		t.Fatal("history tombstone did not retain the execution cutoff", hidden, err)
	}
	if page := historyParityReadPage(t, a, ""); page.Total != 0 {
		t.Fatal("deletion exposed an earlier attempt", page)
	}
	if err := a.Store.Get(ctx, "import_plan", first.PlanID, &original); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(original)
	if string(after) != string(before) {
		t.Fatal("deletion rewrote the prior audit plan")
	}
}

func TestHistoryUpsertDeleteRejectsStaleDialogWithoutTouchingFiles(t *testing.T) {
	for _, choice := range []historyDeleteRequest{
		{Confirm: true},
		{Confirm: true, DeleteSource: true},
		{Confirm: true, DeleteTarget: true},
		{Confirm: true, DeleteSource: true, DeleteTarget: true},
	} {
		t.Run(map[bool]string{false: "keep-source", true: "delete-source"}[choice.DeleteSource]+"/"+map[bool]string{false: "keep-target", true: "delete-target"}[choice.DeleteTarget], func(t *testing.T) {
			a, first, latest, oldTarget := historyUpsertDeleteFixture(t)
			ctx := context.Background()
			choice.FileID = first.executionFileID()
			if _, err := a.deleteTransferHistory(ctx, latest, choice); fileCode(err) != "HISTORY_CHANGED" {
				t.Fatal("stale dialog was not rejected", err)
			}
			for _, path := range []string{latest.Source, latest.Target, oldTarget} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("stale deletion touched a file", path, err)
				}
			}
			var hidden map[string]any
			if err := a.Store.Get(ctx, "transfer_history_hidden", latest.ID, &hidden); err == nil {
				t.Fatal("stale dialog hid the updated record")
			}
		})
	}
}

func TestHistoryUpsertDeleteNeverResumesPreviousExecutionSourceReceipt(t *testing.T) {
	a, first, latest, oldTarget := historyUpsertDeleteFixture(t)
	ctx := context.Background()
	stamp, err := snapshotPath(latest.Source)
	if err != nil {
		t.Fatal(err)
	}
	quarantine := ".light-media-remove-previous-execution.part"
	quarantinedPath := filepath.Join(filepath.Dir(latest.Source), quarantine)
	if err = os.Rename(latest.Source, quarantinedPath); err != nil {
		t.Fatal(err)
	}
	previous := historySourceRemoval{HistoryID: first.executionFileID(), State: "deleting", File: assetRemovalFile{Path: latest.Source, Snapshot: stamp, Kind: "source", Quarantine: quarantine}}
	if err = a.Store.Put(ctx, "history_source_removal", first.executionFileID(), previous); err != nil {
		t.Fatal(err)
	}
	if _, err = a.deleteTransferHistory(ctx, latest, historyDeleteRequest{Confirm: true, FileID: latest.executionFileID(), DeleteSource: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(quarantinedPath); err != nil {
		t.Fatal("latest deletion resumed an older source removal", err)
	}
	for _, path := range []string{latest.Target, oldTarget} {
		if _, err = os.Stat(path); err != nil {
			t.Fatal("source-only deletion touched a target", path, err)
		}
	}
	var actual historySourceRemoval
	if err = a.Store.Get(ctx, "history_source_removal", first.executionFileID(), &actual); err != nil || actual != previous {
		t.Fatal("previous execution receipt was altered", actual, err)
	}
	if err = a.Store.Get(ctx, "history_source_removal", latest.executionFileID(), &actual); err == nil {
		t.Fatal("nonexistent latest source unexpectedly created a removal receipt")
	}
}

func TestHistoryUpsertDeleteOldClientCannotDeleteUpdatedFiles(t *testing.T) {
	a, _, latest, oldTarget := historyUpsertDeleteFixture(t)
	ctx := context.Background()
	for _, choice := range []historyDeleteRequest{
		{Confirm: true, DeleteSource: true},
		{Confirm: true, DeleteTarget: true},
		{Confirm: true, DeleteSource: true, DeleteTarget: true},
	} {
		if _, err := a.deleteTransferHistory(ctx, latest, choice); fileCode(err) != "HISTORY_CHANGED" {
			t.Fatal("old client could delete files from a newer execution", err)
		}
	}
	for _, path := range []string{latest.Source, latest.Target, oldTarget} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("old client deletion touched a file", path, err)
		}
	}
	if page := historyParityReadPage(t, a, ""); page.Total != 1 {
		t.Fatal("old client deletion hid the updated history", page)
	}
}
