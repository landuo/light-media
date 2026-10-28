package artifacts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPackageResourceGenericCodeFilesAndOpenSnapshot(t *testing.T) {
	req, client, _, files := frontendFixture(t, "zip")
	installed, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := OpenPackageResource(req.Root, req.PluginID, "private.txt", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Reader.Close()
	if resource.Size != int64(len(files["private.txt"])) || resource.SHA256 != digest(files["private.txt"]) {
		t.Fatal("generic package resource metadata")
	}
	for _, name := range []string{"frontend/assets", "."} {
		if _, err := OpenPackageResource(req.Root, req.PluginID, name, req.Validate); !errors.Is(err, os.ErrPermission) {
			t.Fatal("directory not rejected", name, err)
		}
	}
	for _, name := range []string{"../private.txt", ".acquired.json", "missing.bin"} {
		if _, err := OpenPackageResource(req.Root, req.PluginID, name, req.Validate); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("nonresource path accepted", name, err)
		}
	}
	// An update replaces the pathname, but an already authorized open stream
	// continues to read the verified old inode. New opens revalidate current code.
	path := filepath.Join(installed.Command.Dir, "private.txt")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered new bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resource.Reader)
	if err != nil || !bytes.Equal(got, files["private.txt"]) {
		t.Fatal("open verified snapshot changed", err)
	}
	if _, err := OpenPackageResource(req.Root, req.PluginID, "private.txt", req.Validate); err == nil {
		t.Fatal("changed tree accepted by a new resource open")
	}
}
