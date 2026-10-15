package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedBindAliasesUseDirectoryIdentityWithoutUserMappings(t *testing.T) {
	// Synthetic stat identities model Docker exposing one media volume twice.
	ids := map[string]mountedDirectoryID{}
	for path, inode := range map[string]uint64{"/media": 1, "/media/download": 2, "/media/download/anime": 3, "/vol2/1000/media": 1, "/vol2/1000/media/download": 2, "/vol2/1000/media/download/anime": 3, "/vol2/1000/media/download/anime/release": 4, "/vol2/1000/media/music": 5} {
		ids[path] = mountedDirectoryID{Device: 7, Inode: inode}
	}
	stat := func(path string) (mountedDirectoryID, error) {
		if id, ok := ids[path]; ok {
			return id, nil
		}
		return mountedDirectoryID{}, os.ErrNotExist
	}
	for _, test := range []struct {
		candidate, want string
		relevant        bool
	}{{"/vol2/1000/media/download/anime", "/media/download/anime", true}, {"/vol2/1000/media/download/anime/release", "/media/download/anime/release", true}, {"/vol2/1000/media/download", "/media/download", true}, {"/vol2/1000/media/music", "", false}, {"/9kg", "", false}} {
		got, ok := sharedDirectoryPathWithIdentity("/media/download/anime", test.candidate, stat)
		if got != test.want || ok != test.relevant {
			t.Fatalf("physical alias %s: got %q,%v want %q,%v", test.candidate, got, ok, test.want, test.relevant)
		}
	}
}

func TestDirectoryIdentityResolverDoesNotFollowSymlinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, link := filepath.Join(base, "directory"), filepath.Join(base, "link")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err = mountedDirectoryIdentity(link); err == nil {
		t.Fatal("symbolic link accepted as mount identity")
	}
}

func TestSharedBindContainmentMatchesDownloadAndManualFileButRejectsParentAndSibling(t *testing.T) {
	ids := map[string]mountedDirectoryID{}
	for path, inode := range map[string]uint64{"/media": 1, "/media/download": 2, "/media/download/anime": 3, "/vol2/1000/media": 1, "/vol2/1000/media/download": 2, "/vol2/1000/media/download/anime": 3, "/vol2/1000/media/download/anime/release": 4, "/vol2/1000/media/download/music": 5} {
		ids[path] = mountedDirectoryID{Device: 7, Inode: inode}
	}
	identity := func(path string) (mountedDirectoryID, error) {
		if id, ok := ids[path]; ok {
			return id, nil
		}
		return mountedDirectoryID{}, os.ErrNotExist
	}
	for _, test := range []struct {
		path      string
		contained bool
	}{{"/vol2/1000/media/download/anime", true}, {"/vol2/1000/media/download/anime/release", true}, {"/vol2/1000/media/download/anime/release/Episode.S01E01.mkv", true}, {"/vol2/1000/media/download", false}, {"/vol2/1000/media/download/music", false}, {"/media/download/anime-other", false}} {
		if got := sharedDirectoryContainsWithIdentity("/media/download/anime", test.path, identity); got != test.contained {
			t.Fatalf("physical rule containment for %s: got %v, want %v", test.path, got, test.contained)
		}
	}
}
