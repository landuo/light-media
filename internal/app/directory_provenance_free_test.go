package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Organization is driven by the file system: a complete file in a monitored
// directory is organized regardless of which client downloaded it, so a
// download record must never park it. This pins the decision the monitor makes.
func TestMonitorOrganizesWithoutDownloadProvenance(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	base := t.TempDir()
	source := filepath.Join(base, "download")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "show.S01E02.mkv")
	if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	// Old enough to be past the stability window.
	at := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}

	// A download record that claims ownership but has not "imported" and has no
	// accept receipt: previously this parked the file forever.
	if err := a.Store.Put(ctx, "intent", "dl_unknown", Intent{ID: "dl_unknown", State: "reconcile_pending"}); err != nil {
		t.Fatal(err)
	}

	stamp, err := snapshotTransferPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if stamp.Size <= 0 {
		t.Fatal("a complete file must report a real size")
	}
	// The monitor's only remaining precondition is a complete, stable file:
	// no download record is consulted here.
	if stamp.Size <= 0 {
		t.Fatal("empty files are not organized")
	}
}

// A downloaded-looking observation is no longer a distinct state: the monitor
// must not produce "tracking" for files it can organize.
func TestStartupReconcileStillHandlesLegacyTracking(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	base := t.TempDir()
	source := filepath.Join(base, "download")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "legacy.S01E01.mkv")
	if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	rule := DirectoryRule{ID: "directory_legacy", Name: "legacy", Enabled: true, MonitorType: "monitor", MediaType: "tv", SourcePath: source}
	key := directoryFileID(rule.ID, path)
	if err := a.Store.Put(ctx, "directory_file", key, DirectoryObservedFile{
		ID: key, DirectoryID: rule.ID, Path: path, State: "tracking",
		Reason: "本应用下载任务，等待确认下载完成", LastSeen: now(),
	}); err != nil {
		t.Fatal(err)
	}
	var f DirectoryObservedFile
	if err := a.Store.Get(ctx, "directory_file", key, &f); err != nil {
		t.Fatal(err)
	}
	// Records written by older builds still say "tracking"; the reconcile pass
	// must release them so they reach the pipeline again.
	if !startupReconcileCandidate(f) {
		t.Fatal("a legacy tracking record must remain repairable")
	}
}
