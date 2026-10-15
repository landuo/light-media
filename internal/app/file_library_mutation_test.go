package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestFileLibraryUnmanagedEntriesCanBeRenamedAndQuarantined(t *testing.T) {
	a, _, target := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, target)
	name := filepath.Join(target, "unregistered.txt")
	importWrite(t, name, []byte("ordinary unregistered library file"))
	plan, err := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "rename", RootID: root.ID, Path: target, Paths: []string{name}, Name: "renamed.txt"})
	if err != nil {
		t.Fatal("unregistered library entry cannot be renamed", err)
	}
	if _, err = a.confirmBrowserOperation(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(target, "renamed.txt")
	plan, err = a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: target, Paths: []string{renamed}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.confirmBrowserOperation(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(plan.Items[0].Target)
	if err != nil || string(data) != "ordinary unregistered library file" {
		t.Fatal("unregistered file was not preserved in quarantine", err)
	}
	if reason := a.browserStaticProtection(target); reason == "" {
		t.Fatal("configured library root lost protection")
	}
	if reason := a.browserStaticProtection(a.Dir); reason == "" {
		t.Fatal("application data directory lost protection")
	}
}

func TestFileLibraryManagedVideosSourcesAndSidecarsRemainProtected(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	asset := Asset{ID: "active", RootID: "root", State: "present", Path: filepath.Join(target, "Show", "Season 1", "episode.mkv"), SourcePath: filepath.Join(source, "episode.mkv"), Media: catalog.Media{ID: 12, MediaType: "tv"}}
	if err := a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	owned := managedFile{Path: filepath.Join(target, "Show", "episode.nfo"), RootID: "root", AssetID: asset.ID, Kind: "nfo", MediaKey: mediaKey(asset.Media)}
	shared := managedFile{Path: filepath.Join(target, "Show", "poster.jpg"), RootID: "root", Kind: "image", MediaKey: mediaKey(asset.Media)}
	for _, file := range []managedFile{owned, shared} {
		if err := a.Store.Put(ctx, "managed_file", tokenHash(file.Path), file); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{asset.Path, asset.SourcePath, owned.Path, shared.Path, filepath.Join(target, "Show")} {
		err := a.browserMutationAllowed(ctx, path)
		if fileCode(err) != "FILE_IN_USE" || !strings.Contains(err.Error(), "整理历史") {
			t.Fatalf("active owned path lost protection: %s: %v", path, err)
		}
	}
}

func TestFileLibraryInactiveOwnershipDoesNotPermanentlyLockOrdinaryFiles(t *testing.T) {
	for _, state := range []string{"removed", "superseded"} {
		t.Run(state, func(t *testing.T) {
			a, source, target := importTestApp(t)
			ctx := context.Background()
			asset := Asset{ID: "old", RootID: "root", State: state, Path: filepath.Join(target, "Old", "video.mkv"), SourcePath: filepath.Join(source, "old.mkv"), Media: catalog.Media{ID: 24, MediaType: "movie"}}
			if err := a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
				t.Fatal(err)
			}
			owned := managedFile{Path: filepath.Join(target, "Old", "video.nfo"), RootID: "root", AssetID: asset.ID, Kind: "nfo", MediaKey: mediaKey(asset.Media)}
			shared := managedFile{Path: filepath.Join(target, "Old", "poster.jpg"), RootID: "root", Kind: "image", MediaKey: mediaKey(asset.Media)}
			for _, file := range []managedFile{owned, shared} {
				if err := a.Store.Put(ctx, "managed_file", tokenHash(file.Path), file); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{asset.Path, asset.SourcePath, owned.Path, shared.Path} {
				if err := a.browserMutationAllowed(ctx, path); err != nil {
					t.Fatalf("inactive ownership still locks ordinary path: %s: %v", path, err)
				}
			}
		})
	}
}

func TestFileLibraryOwnershipAddedAfterPreviewStopsConfirmation(t *testing.T) {
	a, _, target := importTestApp(t)
	ctx := context.Background()
	root := fileBrowserRoot(t, a, target)
	path := filepath.Join(target, "later-managed.nfo")
	importWrite(t, path, []byte("sidecar protected before confirmation"))
	plan, err := a.previewBrowserOperation(ctx, browserOperationRequest{Action: "delete", RootID: root.ID, Path: target, Paths: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(ctx, "asset", "new-owner", Asset{ID: "new-owner", State: "present", Path: filepath.Join(target, "video.mkv")}); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(ctx, "managed_file", tokenHash(path), managedFile{Path: path, AssetID: "new-owner", Kind: "nfo"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.confirmBrowserOperation(ctx, plan.ID); fileCode(err) != "FILE_IN_USE" {
		t.Fatal("ownership added after preview was ignored", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "sidecar protected before confirmation" {
		t.Fatal("protected sidecar changed", err)
	}
}
