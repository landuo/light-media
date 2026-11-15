package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Filesystem-level ownership-mechanism test; the separate ActualMerge tests
// establish the publication and SDK provenance used by production consumers.
func TestSharedDirectoryScopedEmptyDirectoryCleanup(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-empty-removed", true: "new-nonempty-retained"}[foreign], func(t *testing.T) {
			a, _, library := importTestApp(t)
			root := filepath.Join(library, "shared")
			proof := &importSharedDirectoryProof{Directories: map[string]importSnapshot{}, CreatedDirectories: map[string]bool{"new": true}, Members: map[string]importSharedDirectoryMember{}}
			for _, relative := range []string{"existing/owned.mkv", "new/owned.mkv"} {
				path := filepath.Join(root, relative)
				importWrite(t, path, []byte("owned file"))
				stamp, e := snapshotPath(path)
				if e != nil {
					t.Fatal(e)
				}
				proof.Members[relative] = importSharedDirectoryMember{Snapshot: stamp}
			}
			for _, relative := range []string{"existing", "new"} {
				dir, e := importDir(filepath.Join(root, relative), false)
				if e != nil {
					t.Fatal(e)
				}
				info, e := dir.Stat()
				dir.Close()
				if e != nil {
					t.Fatal(e)
				}
				stamp, e := originalDirectoryIdentity(info)
				if e != nil {
					t.Fatal(e)
				}
				proof.Directories[relative] = sharedDirectoryIdentity(stamp)
			}
			dir, e := importDir(root, false)
			if e != nil {
				t.Fatal(e)
			}
			info, e := dir.Stat()
			dir.Close()
			if e != nil {
				t.Fatal(e)
			}
			stamp, e := originalDirectoryIdentity(info)
			if e != nil {
				t.Fatal(e)
			}
			proof.Root = sharedDirectoryIdentity(stamp)
			aggregate, e := snapshotSharedDirectory(root, importSnapshot{}, proof, true)
			if e != nil {
				t.Fatal(e)
			}
			file := assetRemovalFile{Path: root, Snapshot: aggregate, SharedDirectory: proof, Quarantine: ".light-media-remove-unit.part"}
			if foreign {
				importWrite(t, filepath.Join(root, "new", "foreign.nfo"), []byte("retained foreign metadata"))
			}
			persist := func() error { return a.Store.Put(t.Context(), "unit_shared_removal", "test", file) }
			if e = a.removeSharedDirectoryMembers(t.Context(), &file, persist); e != nil {
				t.Fatal(e)
			}
			if e = a.Store.Get(t.Context(), "unit_shared_removal", "test", &file); e != nil || !file.Deleted {
				t.Fatal("durable leaf completion", file, e)
			}
			if e = verifySharedDirectoryRemoved(file); e != nil {
				t.Fatal(e)
			}
			for _, relative := range []string{".", "existing"} {
				if info, e := os.Stat(filepath.Join(root, relative)); e != nil || !info.IsDir() {
					t.Fatal("existing directory removed", relative, e)
				}
			}
			if foreign {
				if got, e := os.ReadFile(filepath.Join(root, "new", "foreign.nfo")); e != nil || string(got) != "retained foreign metadata" {
					t.Fatal("unrelated new-directory member removed", e)
				}
			} else if _, e := os.Stat(filepath.Join(root, "new")); !os.IsNotExist(e) {
				t.Fatal("created empty directory retained", e)
			}
		})
	}
}
