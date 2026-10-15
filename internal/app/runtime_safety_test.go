package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestJobTraceSurvivesPersistence(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx := a.traceContext(context.Background(), "wf-trace", "parent-job", "origin-request")
	j, e := a.enqueue(ctx, "asset_check", map[string]string{"id": "own-asset"}, "wf-trace")
	if e != nil {
		t.Fatal(e)
	}
	persisted, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		t.Fatal(e)
	}
	if persisted.ParentJobID != "parent-job" || persisted.RequestID != "origin-request" {
		t.Fatalf("lost persistent trace: %+v", persisted)
	}
}

func TestUnsupportedSchemaDoesNotRecoverRunningJobs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	store, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	_, e = store.DB.Exec(`INSERT INTO migrations VALUES(2,'future'); INSERT INTO jobs(id,workflow_id,kind,state,payload,generation,run_at,created_at,updated_at) VALUES('future-job','wf','import','running','{}',7,0,'now','now')`)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	if reopened, err := OpenStore(dir); err == nil {
		reopened.Close()
		t.Fatal("older program accepted future schema")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var state string
	var gen int
	if e = db.QueryRow(`SELECT state,generation FROM jobs WHERE id='future-job'`).Scan(&state, &gen); e != nil {
		t.Fatal(e)
	}
	if state != "running" || gen != 7 {
		t.Fatalf("unsupported database mutated: %s/%d", state, gen)
	}
}
