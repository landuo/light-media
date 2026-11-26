package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImportReceiptProjectionCommitsWithParent(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	const key = "plan_test/disc"
	receipt := func(state, path string) map[string]any {
		return map[string]any{"state": state, "directory_members": map[string]any{path: map[string]string{"asset_id": "asset_test"}}}
	}
	if err := store.Put(ctx, "import_receipt", key, receipt("staged", "keep.m2ts")); err != nil {
		t.Fatal(err)
	}
	var id, parent, unused int
	var detail string
	if err := store.DB.QueryRowContext(ctx, "EXPLAIN QUERY PLAN DELETE FROM import_receipt_member WHERE receipt_id=?", key).Scan(&id, &parent, &unused, &detail); err != nil || !strings.Contains(detail, "SEARCH import_receipt_member USING INDEX import_receipt_member_receipt") {
		t.Fatal("member lookup", detail, err)
	}
	checkOriginal := func() {
		t.Helper()
		var got struct{ State string }
		var path string
		if err := store.Get(ctx, "import_receipt", key, &got); err != nil || got.State != "staged" {
			t.Fatal("receipt after rollback", got, err)
		}
		if err := store.DB.QueryRowContext(ctx, "SELECT json_extract(payload,'$.path') FROM import_receipt_member WHERE receipt_id=?", key).Scan(&path); err != nil || path != "keep.m2ts" {
			t.Fatal("member after rollback", path, err)
		}
	}
	if _, err := store.DB.Exec("CREATE TRIGGER reject_member BEFORE INSERT ON import_receipt_member BEGIN SELECT RAISE(ABORT,'rejected insert'); END"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "import_receipt", key, receipt("published", "new.m2ts")); err == nil {
		t.Fatal("expected failed member insertion")
	}
	checkOriginal()
	if _, err := store.DB.Exec("CREATE TRIGGER keep_member BEFORE DELETE ON import_receipt_member BEGIN SELECT RAISE(ABORT,'rejected delete'); END"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "import_receipt", key); err == nil {
		t.Fatal("expected failed member deletion")
	}
	checkOriginal()
}

func TestOriginalUnchangedTargetOnlyWritesReceipt(t *testing.T) {
	a, src, _, _, library := ownedNativeSourceDirectories(t)
	source := filepath.Join(src, "Original.Movie.2024.mkv")
	importWrite(t, source, []byte("original video"))
	ctx := t.Context()
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, TargetPath: library,
		Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: source}}})
	if err != nil || plan.DirectorySourceVersion != 2 {
		t.Fatal("directory authorization fixture", plan.DirectorySourceVersion, err)
	}
	job := overwriteStart(t, a, plan)
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec("CREATE TRIGGER keep_plan BEFORE UPDATE ON import_plan BEGIN SELECT RAISE(ABORT,'unexpected plan rewrite'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec("CREATE TRIGGER keep_authorization BEFORE UPDATE ON plugin_record WHEN NEW.kind='native_directory_selection' BEGIN SELECT RAISE(ABORT,'unexpected reseal'); END"); err != nil {
		t.Fatal(err)
	}
	receipt := originalTransferPlannedReceipt(plan, plan.Files[0])
	receipt.OriginalNamingResolved = true
	if err := a.transitionOriginalTransferTarget(ctx, job, &plan, 0, meta, receipt, false); err != nil {
		t.Fatal(err)
	}
	var saved importReceipt
	if err := a.Store.Get(ctx, "import_receipt", receipt.ID, &saved); err != nil || !saved.OriginalNamingResolved || saved.Target != plan.Files[0].Target {
		t.Fatal("confirmed target was not saved", saved, err)
	}
	if err := a.Store.Delete(ctx, "native_directory_selection", plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.transitionOriginalTransferTarget(ctx, job, &plan, 0, meta, receipt, false); err == nil {
		t.Fatal("same target accepted without its sealed directory authorization")
	}
}
