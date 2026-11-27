package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestBrowserInventoryFollowsLibraryEntriesAcrossRenameDeleteAndRestore(t *testing.T) {
	a, downloads, library := importTestApp(t)
	ctx := context.Background()
	show := filepath.Join(library, "Old.Show%")
	video := filepath.Join(show, "episode.mkv")
	otherPath := filepath.Join(library, "Old.ShowX", "episode.mkv")
	source := filepath.Join(downloads, "release.mkv")
	importWrite(t, video, []byte("owned episode"))
	importWrite(t, otherPath, []byte("independent copy"))
	if err := os.Link(video, source); err != nil {
		t.Fatal(err)
	}
	media := catalog.Media{ID: 1201, MediaType: "tv"}
	asset := Asset{ID: "one", State: "present", Path: video, SourcePath: source, Season: 1, Episodes: []int{2}, Media: media, RootID: "root"}
	other := asset
	other.ID, other.Path = "two", otherPath
	for _, entry := range []Asset{asset, other} {
		if err := a.Store.Put(ctx, "asset", entry.ID, entry); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Store.DB.Exec("INSERT INTO asset_slots VALUES(?,?,?,?)", mediaKey(media), 1, 2, entry.ID); err != nil {
			t.Fatal(err)
		}
	}
	nfo := filepath.Join(show, "episode.nfo")
	importWrite(t, nfo, []byte("sidecar"))
	for _, v := range []struct{ Path, Kind string }{{video, "video"}, {nfo, "nfo"}} {
		snap, err := snapshotPath(v.Path)
		if err != nil {
			t.Fatal(err)
		}
		file := managedFile{AssetID: asset.ID, Path: v.Path, RootID: asset.RootID, Kind: v.Kind, Snapshot: snap}
		if err = a.Store.Put(ctx, "managed_file", tokenHash(v.Path), file); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Store.Put(ctx, "asset_identity", asset.ID, libraryIdentity{AssetID: asset.ID, Path: video, RootPath: library}); err != nil {
		t.Fatal(err)
	}

	getState := func(id, state, path string, slots int) {
		t.Helper()
		var got Asset
		var count int
		if err := a.Store.Get(ctx, "asset", id, &got); err != nil || got.State != state || got.Path != path {
			t.Fatalf("asset %s: %s %s, want %s %s: %v", id, got.State, got.Path, state, path, err)
		}
		if err := a.Store.DB.QueryRow("SELECT count(*) FROM asset_slots WHERE asset_id=?", id).Scan(&count); err != nil || count != slots {
			t.Fatalf("asset %s: %d slots, want %d: %v", id, count, slots, err)
		}
	}
	act := func(action, path, base, name string) browserOperation {
		t.Helper()
		root := fileBrowserRoot(t, a, base)
		plan, err := a.previewBrowserOperation(ctx, browserOperationRequest{Action: action, RootID: root.ID, Path: filepath.Dir(path), Paths: []string{path}, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		plan, err = a.confirmBrowserOperation(ctx, plan.ID)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	// Operating on another hardlink at the downloader path must never remove
	// a library slot or rewrite the original source recorded by transfer history.
	act("delete", source, downloads, "")
	getState("one", "present", video, 1)
	moved := filepath.Join(library, "renamed")
	newVideo := filepath.Join(moved, filepath.Base(video))
	act("rename", show, library, filepath.Base(moved))
	getState("one", "present", newVideo, 1)
	for _, old := range []string{video, nfo} {
		var file managedFile
		if err := a.Store.Get(ctx, "managed_file", tokenHash(old), &file); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		next := filepath.Join(moved, filepath.Base(old))
		if err := a.Store.Get(ctx, "managed_file", tokenHash(next), &file); err != nil || file.Path != next {
			t.Fatal("missing rekeyed owner", err)
		}
	}
	var proof libraryIdentity
	if err := a.Store.Get(ctx, "asset_identity", asset.ID, &proof); err != nil || proof.Path != newVideo {
		t.Fatal("missing renamed identity", err)
	}
	recycle := act("delete", moved, library, "")
	getState("one", "removed", newVideo, 0)
	getState("two", "present", otherPath, 1)
	if err := backfillAssetSlots(ctx, a.Store.DB); err != nil {
		t.Fatal(err)
	}
	getState("one", "removed", newVideo, 0)
	if _, err := a.restoreBrowserOperation(ctx, recycle.ID); err != nil {
		t.Fatal(err)
	}
	getState("one", "present", newVideo, 1)
	getState("two", "present", otherPath, 1)
	var owner managedFile
	if err := a.Store.Get(ctx, "managed_file", tokenHash(newVideo), &owner); err != nil || owner.AssetID != "one" {
		t.Fatal("ownership not restored", err)
	}
}

func TestBrowserInventoryReceiptSharesTransactionAndRestoresAfterRetry(t *testing.T) {
	a, _, library := importTestApp(t)
	ctx := context.Background()
	v := filepath.Join(library, "clip.mkv")
	importWrite(t, v, []byte("episode"))
	asset := Asset{ID: "owned", Path: v, State: "external", RootID: "root", Media: catalog.Media{ID: 42, MediaType: "movie"}}
	if err := a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec("INSERT INTO asset_slots VALUES(?,?,?,?)", mediaKey(asset.Media), 0, 0, asset.ID); err != nil {
		t.Fatal(err)
	}
	first := Asset{ID: "first", Path: filepath.Join(library, "first.mkv"), State: "present"}
	importWrite(t, first.Path, []byte("other"))
	if err := a.Store.Put(ctx, "asset", first.ID, first); err != nil {
		t.Fatal(err)
	}
	root := fileBrowserRoot(t, a, library)
	plan, err := a.previewBrowserOperation(ctx, browserOperationRequest{RootID: root.ID, Path: library, Paths: []string{first.Path, v}, Action: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Store.DB.Exec(`CREATE TRIGGER fail_browser_inventory BEFORE INSERT ON file_operation
 WHEN json_extract(NEW.payload,'$.items[1].done')=1 BEGIN SELECT RAISE(ABORT,'fixture interruption'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.confirmBrowserOperation(ctx, plan.ID); err == nil {
		t.Fatal("expected interrupted index transaction")
	}
	if _, err = os.Stat(plan.Items[1].Target); err != nil {
		t.Fatal("expected completed file effect", err)
	}
	var actual Asset
	if err = a.Store.Get(ctx, "asset", first.ID, &actual); err != nil || actual.State != "removed" {
		t.Fatal("completed batch item lost its index change", err)
	}
	if err = a.Store.Get(ctx, "asset", asset.ID, &actual); err != nil || actual.State != "external" {
		t.Fatal("partial index update escaped rollback", err)
	}
	if _, err = a.Store.DB.Exec("DROP TRIGGER fail_browser_inventory"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.confirmBrowserOperation(ctx, plan.ID); err != nil {
		t.Fatal("retry", err)
	}
	if _, err = a.confirmBrowserOperation(ctx, plan.ID); err != nil {
		t.Fatal("duplicate confirm", err)
	}
	newOwner := managedFile{AssetID: "replacement", Path: v, Kind: "video"}
	if err = a.Store.Put(ctx, "asset", "replacement", Asset{ID: "replacement", Path: newOwner.Path, State: "present"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.restoreBrowserOperation(ctx, plan.ID); err == nil {
		t.Fatal("restore overwrote replacement owner")
	}
	if _, err = os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatal("restored part of a batch before detecting a later conflict", err)
	}
	if _, err = os.Stat(v); !os.IsNotExist(err) {
		t.Fatal("changed physical file before detecting restore conflict", err)
	}
	if err = a.Store.Get(ctx, "asset", asset.ID, &actual); err != nil || actual.State != "removed" {
		t.Fatal("conflicting restore partially published", err)
	}
	if _, err = a.Store.DB.Exec("DELETE FROM library_asset WHERE id='replacement'"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.restoreBrowserOperation(ctx, plan.ID); err != nil {
		t.Fatal("retry restore", err)
	}
	if err = a.Store.Get(ctx, "asset", asset.ID, &actual); err != nil || actual.State != "external" {
		t.Fatal("prior state not restored", err)
	}
	if err = a.Store.Get(ctx, "asset", first.ID, &actual); err != nil || actual.State != "present" {
		t.Fatal("first item's private recovery evidence was lost", err)
	}
}

func TestBrowserInventoryRenameTreatsStaleDestinationAndConcurrentClaimSeparately(t *testing.T) {
	for _, afterPreview := range []bool{false, true} {
		a, _, library := importTestApp(t)
		ctx := context.Background()
		root := fileBrowserRoot(t, a, library)
		source, dest := filepath.Join(library, "source.mkv"), filepath.Join(library, "dest.mkv")
		importWrite(t, source, []byte("unregistered video"))
		claim := func() {
			asset := Asset{ID: "old", State: "present", Path: dest, Media: catalog.Media{MediaType: "movie", ID: 1}}
			if err := a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec("INSERT INTO asset_slots VALUES(?,?,?,?)", mediaKey(asset.Media), 0, 0, asset.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Put(ctx, "managed_file", tokenHash(dest), managedFile{Kind: "video", Path: dest, AssetID: asset.ID}); err != nil {
				t.Fatal(err)
			}
		}
		if !afterPreview {
			claim()
		}
		plan, err := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", Name: filepath.Base(dest), RootID: root.ID, Path: library, Paths: []string{source}})
		if err != nil {
			t.Fatal(err)
		}
		if afterPreview {
			claim()
		}
		_, err = a.confirmBrowserOperation(ctx, plan.ID)
		if afterPreview {
			if err == nil {
				t.Fatal("new claim was ignored")
			}
			if _, err = os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("moved the file before detecting target conflict", err)
			}
			if _, err = os.Stat(source); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err != nil {
			t.Fatal("old claim should not block an absent destination", err)
		}
		var asset Asset
		if err = a.Store.Get(ctx, "asset", "old", &asset); err != nil || asset.State != "removed" {
			t.Fatal("new file inherited stale asset", err)
		}
		var n int
		if err = a.Store.DB.QueryRow("SELECT count(*) FROM asset_slots WHERE asset_id='old'").Scan(&n); err != nil || n != 0 {
			t.Fatal("retired destination still occupies slot", n, err)
		}
		if err = a.Store.Get(ctx, "managed_file", tokenHash(dest), &managedFile{}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("new file inherited stale owner", err)
		}
	}
}

func TestBrowserInventoryReplacedSourceDoesNotCarryFormerIdentity(t *testing.T) {
	a, _, library := importTestApp(t)
	ctx := context.Background()
	source := filepath.Join(library, "before.mkv")
	importWrite(t, source, []byte("film A"))
	proof, err := snapshotPath(source)
	if err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: "a", Path: source, Size: proof.Size, State: "present", Media: catalog.Media{ID: 1, MediaType: "movie"}}
	if err = a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	owned := managedFile{AssetID: asset.ID, Path: source, Kind: "video", Snapshot: proof}
	if err = a.Store.Put(ctx, "managed_file", tokenHash(source), owned); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(source, source+".old"); err != nil {
		t.Fatal(err)
	}
	importWrite(t, source, []byte("film B"))
	root := fileBrowserRoot(t, a, library)
	plan, err := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", Name: "after.mkv", RootID: root.ID, Path: library, Paths: []string{source}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.confirmBrowserOperation(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	var actual Asset
	if err = a.Store.Get(ctx, "asset", asset.ID, &actual); err != nil || actual.Path != source || actual.State != "removed" {
		t.Fatal("previous identity moved to unrelated file", err)
	}
	if err = a.Store.Get(ctx, "managed_file", tokenHash(filepath.Join(library, "after.mkv")), &owned); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unrelated file gained former ownership", err)
	}
}

func TestBrowserInventoryAliasUsesDirectoryEntryNotFileInode(t *testing.T) {
	identity := func(dir string) (mountedDirectoryID, error) {
		switch dir {
		case "/library", "/shared/alias":
			return mountedDirectoryID{1, 2}, nil
		case "/downloads":
			return mountedDirectoryID{1, 3}, nil
		}
		return mountedDirectoryID{}, os.ErrNotExist
	}
	roots := []Root{{Path: "/library"}}
	parents := browserInventoryParents("/shared/alias/Season/episode.mkv", roots, identity)
	if len(parents) != 2 || parents[1] != "/library/Season" {
		t.Fatal("missed alias", parents)
	}
	parents = browserInventoryParents("/downloads/episode.mkv", roots, identity)
	if len(parents) != 1 {
		t.Fatal("classified another hardlink as the library entry", parents)
	}
}
