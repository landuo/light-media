package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

// A stuck source file is repaired by dropping its observation and freeing the
// slot held by an already-finished download. Nothing else is touched.
func TestRepairStrandedSourceClearsStuckState(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	base := t.TempDir()
	source := filepath.Join(base, "download")
	library := filepath.Join(base, "lib")
	for _, dir := range []string{source, library} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	rule := DirectoryRule{ID: "directory_repair", Name: "修复测试", Enabled: true, MonitorType: "monitor", MediaType: "tv", Mode: "hardlink", SourcePath: source, LibraryPath: library, RootID: "root_probe"}
	if err := a.saveSettings(ctx, Settings{AllowedRoots: []string{base}, Roots: []Root{{ID: "root_probe", Name: "probe", Path: library, Kind: "tv"}}, Directories: []DirectoryRule{rule}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "show.S01E02.mkv")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	key := directoryFileID(rule.ID, path)
	if err := a.Store.Put(ctx, "directory_file", key, DirectoryObservedFile{
		ID: key, DirectoryID: rule.ID, Path: path, State: "error",
		Reason: "该影片或季集已有其他下载或整理占用", LastSeen: now(),
	}); err != nil {
		t.Fatal(err)
	}
	// A finished download holds the slot.
	if err := a.Store.Put(ctx, "intent", "dl_done", Intent{ID: "dl_done", State: "downloaded", Media: catalog.Media{ID: 1, MediaType: "tv"}, Season: 1, Episodes: []int{2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO reservations(media_key,season,episode,intent_id) VALUES(?,?,?,?)`,
		"tv:1", 1, 2, "dl_done"); err != nil {
		t.Fatal(err)
	}
	// An unfinished download keeps its slot.
	if err := a.Store.Put(ctx, "intent", "dl_live", Intent{ID: "dl_live", State: "downloading", Media: catalog.Media{ID: 1, MediaType: "tv"}, Season: 1, Episodes: []int{3}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO reservations(media_key,season,episode,intent_id) VALUES(?,?,?,?)`,
		"tv:1", 1, 3, "dl_live"); err != nil {
		t.Fatal(err)
	}

	out, err := a.repairStrandedSource(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Observations != 1 || out.Reservations != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	if err := a.Store.Get(ctx, "directory_file", key, &DirectoryObservedFile{}); err == nil {
		t.Fatal("the stranded observation must be gone")
	}
	var left int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE media_key='tv:1'`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("an unfinished download must keep its slot, left=%d", left)
	}
}

// Paths outside every monitored root are refused: the repair is a maintenance
// action on monitored sources, not a general file primitive.
func TestRepairStrandedSourceRefusesForeignPath(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	base := t.TempDir()
	source := filepath.Join(base, "download")
	library := filepath.Join(base, "lib")
	for _, dir := range []string{source, library} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.saveSettings(ctx, Settings{AllowedRoots: []string{base}, Roots: []Root{{ID: "root_probe2", Name: "probe2", Path: library, Kind: "tv"}}, Directories: []DirectoryRule{{ID: "directory_repair2", Name: "修复测试2", Enabled: true, MonitorType: "monitor", MediaType: "tv", Mode: "hardlink", SourcePath: source, LibraryPath: library, RootID: "root_probe2"}}}); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	path := filepath.Join(outside, "stray.mkv")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.repairStrandedSource(ctx, path); err == nil {
		t.Fatal("a path outside every monitored root must be refused")
	}
}
