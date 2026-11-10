package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFileReferenceBatchRechecksActivePlansAndOwners(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	sourceFile := filepath.Join(source, "queued.mkv")
	targetFile := filepath.Join(target, "queued.mkv")
	plan := ImportPlan{ID: "queued", State: "queued", Files: []PlanFile{{Source: sourceFile, Target: targetFile}}}
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	check, err := a.browserMutationReferenceCheck(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sourceFile, targetFile} {
		if err := check(path); fileCode(err) != "FILE_IN_USE" {
			t.Fatalf("active path allowed: %s %v", path, err)
		}
	}
	plan.State = "complete"
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	// A dispatch obtains a fresh batch; a completed plan must not pin files.
	check, err = a.browserMutationReferenceCheck(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = check(sourceFile); err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: "new-owner", State: "present", Path: targetFile, SourcePath: sourceFile}
	if err = a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	check, err = a.browserMutationReferenceCheck(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sourceFile, targetFile} {
		if err := check(path); fileCode(err) != "FILE_IN_USE" {
			t.Fatalf("new owner ignored: %v", err)
		}
	}
	// Reading one committed batch doesn't wait behind an unrelated writer.
	tx, err := a.Store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	check, err = a.browserMutationReferenceCheck(bounded, true)
	if err != nil {
		t.Fatal(err)
	}
	// The path loop performs no database reads (the writer remains held).
	for i := 0; i < 100; i++ {
		if err := check(filepath.Join(source, "unowned.bin")); err != nil {
			t.Fatal(err)
		}
	}
}
