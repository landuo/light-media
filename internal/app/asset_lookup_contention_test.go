package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAssetAtPathReadsCommittedOwnerWhileWriterBusy(t *testing.T) {
	a, _, target := importTestApp(t)
	path := filepath.Join(target, "owned.mkv")
	for _, state := range []string{"removed", "superseded", "present"} {
		if err := a.Store.Put(context.Background(), "asset", state, Asset{ID: state, Path: path, State: state}); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := a.Store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := txPut(context.Background(), tx, "asset", "present", Asset{ID: "present", Path: path, State: "removed"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var owner Asset
	if err := a.assetAtPath(ctx, path, &owner); err != nil || owner.ID != "present" || owner.State != "present" {
		t.Fatalf("path lookup waited for writer or exposed uncommitted/removed owner: %+v, %v", owner, err)
	}
}
