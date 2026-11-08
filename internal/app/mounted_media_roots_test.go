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

func TestBrushMountAuthorizesOnlyBrushFileChecks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, data := filepath.Join(base, "brushflow"), filepath.Join(base, "config")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIGHT_MEDIA_MEDIA_ROOTS", "")
	t.Setenv("LIGHT_MEDIA_BRUSH_ROOTS", root)
	a, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	file := filepath.Join(root, "torrent")
	if err := a.originalBrushDestinationAllowed(root); err != nil || !a.brushSourceAllowed(file) {
		t.Fatalf("brush path not allowed: %v", err)
	}
	if a.sourceAllowed(file) || a.brushSourceAllowed(root+"-other/torrent") {
		t.Fatal("brush mount granted access outside its intended scope")
	}
	link := filepath.Join(root, "escape")
	if err = os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	if err := a.originalBrushDestinationAllowed(link); err == nil {
		t.Fatal("brush path followed a symbolic link")
	}
}
