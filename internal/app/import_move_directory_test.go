package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestImportMoveDirectoryCleanupRecovery(t *testing.T) {
	a, source, target := importTestApp(t)
	path := filepath.Join(source, "Disc")
	for name, data := range map[string]string{"BDMV/STREAM/00001.m2ts": "synthetic stream", "BDMV/index.bdmv": "synthetic index"} {
		importWrite(t, filepath.Join(path, name), []byte(data))
	}
	stamp, _, err := snapshotOriginalDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	file := PlanFile{ID: "disc", Source: path, Target: filepath.Join(target, "Disc"), Method: "move", DirectoryDigest: stamp.Directory, Device: stamp.Device, Inode: stamp.Inode, ModNS: stamp.ModNS, Size: stamp.Size}
	key := "directory-move/disc"
	ctx := context.Background()
	// Cancel between two member removals, not during a speculative race. All
	// targets are already published, and the source directory digest has changed.
	check := func() error {
		var parent importReceipt
		if a.Store.Get(ctx, "import_receipt", key, &parent) == nil && parent.DirectoryMoveState == "cleaning" {
			for _, relative := range parent.DirectoryFiles {
				var child importReceipt
				if a.Store.Get(ctx, "import_receipt", key+"/directory/"+tokenHash(relative), &child) == nil && child.Move != nil && child.Move.State == "completed" {
					return bad("JOB_CANCELLED", "synthetic cancel after first member move")
				}
			}
		}
		return nil
	}
	receipt, err := a.publishOriginalDirectory(ctx, key, file, check)
	if !originalTransferInterrupted(err) || receipt.DirectoryMoveState != "cleaning" || receipt.State != "published" {
		t.Fatalf("cleanup boundary %+v %v", receipt, err)
	}
	if current, _, e := snapshotOriginalDirectory(path); e != nil || current == stamp {
		t.Fatal("first source member was not moved", current, e)
	}
	config, dataDir := a.Settings(), a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.config = config
	receipt, err = a.publishOriginalDirectory(ctx, key, file, func() error { return nil })
	if err != nil || receipt.DirectoryMoveState != "completed" {
		t.Fatalf("cold directory recovery %+v %v", receipt, err)
	}
	for name, want := range map[string]string{"BDMV/STREAM/00001.m2ts": "synthetic stream", "BDMV/index.bdmv": "synthetic index"} {
		got, e := os.ReadFile(filepath.Join(file.Target, name))
		if e != nil || string(got) != want {
			t.Fatal("target content", name, string(got), e)
		}
		if _, e = os.Stat(filepath.Join(path, name)); !os.IsNotExist(e) {
			t.Fatal("source member remains", name, e)
		}
	}
	replacement := filepath.Join(path, "BDMV/index.bdmv")
	importWrite(t, replacement, []byte("new source data"))
	if _, err = a.publishOriginalDirectory(ctx, key, file, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(replacement); e != nil || string(data) != "new source data" {
		t.Fatal("completed directory move touched new source", e)
	}
}
