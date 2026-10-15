package app

import (
	"context"
	"path/filepath"
	"testing"
)

func TestImportRepeatedConfirmationReturnsOriginalJob(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	p := filepath.Join(source, "Confirmed (2024).mkv")
	importWrite(t, p, []byte("owned-fixture"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: p}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := a.executeImport(context.Background(), plan.ID, []string{plan.Files[0].ID})
	if err != nil || repeated.ID != job.ID {
		t.Fatal("confirmation not idempotent", repeated, err)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='import'`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate execution", count)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	repeated, err = a.executeImport(context.Background(), plan.ID, nil)
	if err != nil || repeated.ID != job.ID {
		t.Fatal("completed confirmation replayed", repeated, err)
	}
}
