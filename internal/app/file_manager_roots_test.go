package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFileManagerGrantsCurrentAndExplicitRoots(t *testing.T) {
	a, parent, explicit := importTestApp(t)
	base := filepath.Dir(parent)
	generated := func(name string) Root {
		t.Helper()
		path := filepath.Join(base, name)
		importWrite(t, filepath.Join(path, "movie.mkv"), []byte("video"))
		r, _, err := physicalTargetRoot(nil, path, "mixed")
		if err != nil {
			t.Fatal(err)
		}
		a.config.Roots = append(a.config.Roots, r)
		return r
	}
	retired, current, defaultRoot := generated("retired"), generated("current"), generated("default")
	custom := generated("custom")
	a.config.Roots[len(a.config.Roots)-1].FNOSLibraryGUID = "explicit-guid"
	a.config.DefaultRoot = defaultRoot.ID
	a.config.Directories = []DirectoryRule{{ID: "legacy", RootID: current.ID, SourcePath: parent}}
	a.invalidateSettings()
	for _, path := range []string{parent, explicit, current.Path, defaultRoot.Path, custom.Path} {
		root := fileBrowserRoot(t, a, path)
		if _, err := a.fileManagerRoot(root.ID, path); err != nil {
			t.Fatalf("current grant %s: %v", path, err)
		}
	}
	// History and background modules still have the stored identity. Neither
	// the main file browser nor a saved root ID may use it to grant a write.
	historical := fileBrowserRoot(t, a, retired.Path)
	if slices.ContainsFunc(a.fileManagerRoots(), func(r browserRoot) bool { return r.ID == historical.ID }) {
		t.Fatal("retired destination exposed as a browser grant")
	}
	req := browserOperationRequest{RootID: historical.ID, Action: "delete", Path: retired.Path, Paths: []string{filepath.Join(retired.Path, "movie.mkv")}}
	if _, err := a.previewBrowserOperation(t.Context(), req); fileCode(err) != "FILE_ROOT_REJECTED" {
		t.Fatalf("retired destination granted writes: %v", err)
	}
	req.RootID = containerBrowserRootID
	if _, err := a.previewBrowserOperation(t.Context(), req); fileCode(err) != "FILE_ROOT_REJECTED" {
		t.Fatalf("container root granted writes: %v", err)
	}
}

func TestFileManagerOverlappingAllowedRootsAndMounts(t *testing.T) {
	a, parent, _ := importTestApp(t)
	oldSource := filepath.Join(parent, "old")
	currentSource := filepath.Join(parent, "current")
	mount := filepath.Join(parent, "other", "mounted")
	for _, path := range []string{oldSource, currentSource, mount} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A former source can be stored before the parent is granted. A current
	// source under a previously authorized parent need not be in AllowedRoots.
	a.config.AllowedRoots = []string{oldSource, parent}
	a.config.Directories = []DirectoryRule{{ID: "active", SourcePath: currentSource}}
	t.Setenv("LIGHT_MEDIA_MEDIA_ROOTS", mount)
	a.invalidateSettings()
	p := a.fileManagerProtection()
	if reason := p.reason(oldSource); reason != "" {
		t.Fatalf("former source under authorized parent still blocked: %s", reason)
	}
	for _, path := range []string{parent, currentSource, mount, filepath.Dir(mount)} {
		if p.reason(path) == "" {
			t.Fatalf("current boundary %s unprotected", path)
		}
	}
	if _, err := a.previewBrowserOperation(t.Context(), browserOperationRequest{
		RootID: fileBrowserRoot(t, a, parent).ID, Action: "rename", Path: parent, Paths: []string{oldSource}, Name: "renamed",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileManagerMountInfoAndBoundariesThroughBindAlias(t *testing.T) {
	mountInfo := `30 21 8:1 /dir /media/Cloud\040Disk rw - ext4 /dev/test rw`
	if got := fileManagerMountPaths(mountInfo); !slices.Equal(got, []string{"/media/Cloud Disk"}) {
		t.Fatalf("decoded mount paths: %v", got)
	}
	// The deployment exposes the same volume at two paths.
	p := fileManagerBoundaries{appDir: "/config", roots: []string{"/media/mounted"}, libraries: []string{"/media/current"}, identity: func(path string) (mountedDirectoryID, error) {
		if path == "/media" || path == "/second" {
			return mountedDirectoryID{10, 20}, nil
		}
		return mountedDirectoryID{}, os.ErrNotExist
	}}
	grants := []browserRoot{{Path: "/media"}, {Path: "/second/retired"}}
	if !p.hasAuthorizedParent(grants, "/second/retired") || p.hasAuthorizedParent(grants, "/media") {
		t.Fatal("retired source must be covered by the aliased parent, but the outer grant must remain a boundary")
	}
	for _, path := range []string{"/second/mounted", "/second/current", "/second"} {
		if p.reason(path) == "" {
			t.Fatalf("unprotected physical alias %s", path)
		}
	}
	if reason := p.reason("/second/retired"); reason != "" {
		t.Fatalf("retired sibling treated as current boundary: %s", reason)
	}
}
