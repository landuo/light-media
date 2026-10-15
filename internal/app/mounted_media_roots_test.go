package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMountedMediaRootsAuthorizeExplicitVolumesAndRejectConfigOrHostRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media, data := filepath.Join(base, "media"), filepath.Join(base, "config")
	if err = os.MkdirAll(media, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	c := Settings{}
	if err = mountedMediaRoots(&c, data, media+":"+media+"/"); err != nil || len(c.AllowedRoots) != 1 || c.AllowedRoots[0] != media {
		t.Fatalf("explicit media volume missing or duplicated: %+v %v", c.AllowedRoots, err)
	}
	for _, path := range []string{"/", "/config", data, base, filepath.Join(base, "missing"), "relative/path"} {
		if err := mountedMediaRoots(&Settings{}, data, path); err == nil {
			t.Fatalf("unsafe or unavailable deployment root accepted: %s", path)
		}
	}
	link := filepath.Join(base, "media-link")
	if err = os.Symlink(media, link); err != nil {
		t.Fatal(err)
	}
	if err := mountedMediaRoots(&Settings{}, data, link); err == nil {
		t.Fatal("symlink mount root accepted")
	}
}
