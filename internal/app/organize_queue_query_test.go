package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestOrganizeQueueSeeksActiveGroupsAndKeepsUnfinishedBatch(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, row := range []struct{ id, kind, state, group string }{
		{"active", "import", "running", "live"},
		{"failed-peer", "import", "failed", "live"},
		{"completed-peer", "import", "succeeded", "live"},
		{"cancelled-peer", "import", "cancelled", "live"},
		{"old", "import", "succeeded", "ended"},
		{"prepare", "organize_prepare", "queued", ""},
		{"listed", "organize_prepare", "running", ""},
		{"unrelated", "site_refresh", "running", ""},
	} {
		if _, err := a.Store.DB.Exec(`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,'',?,?,?,0,?,?)`, row.id, row.kind, row.state, []byte(`{}`), now(), now()); err != nil {
			t.Fatal(err)
		}
		if row.group != "" {
			member := organizeQueueMember{Job: row.id, Group: row.group, File: row.id, Source: "/fixture/" + row.id, Meta: "meta", Media: "media"}
			if err := a.Store.Put(ctx, "organize_queue_member", row.id, member); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.Store.Put(ctx, "organize_file_listing", "listed", struct{}{}); err != nil {
		t.Fatal(err)
	}
	assertIDs := func(want []string) {
		t.Helper()
		jobs, err := a.queryJobs(ctx, organizeQueueJobsSQL)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, j := range jobs {
			got = append(got, j.ID)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("queue=%v want=%v", got, want)
		}
	}
	assertSubmission := func(wantGroup, wantJob string) {
		t.Helper()
		tx, err := a.Store.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, input := range [][2]string{{"meta", ""}, {"", "media"}} {
			var group string
			err := tx.QueryRowContext(ctx, matchingOrganizeGroups, input[0], input[1]).Scan(&group)
			if group != wantGroup || err != nil && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("submission group=%q want=%q err=%v", group, wantGroup, err)
			}
		}
		job, err := activeOrganizeSourceTx(ctx, tx, "/fixture/completed-peer")
		if err != nil || job != wantJob {
			t.Fatalf("active duplicate job=%q want=%q err=%v", job, wantJob, err)
		}
	}
	assertIDs([]string{"active", "failed-peer", "completed-peer", "prepare"})
	assertSubmission("live", "completed-peer")
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id='active'`); err != nil {
		t.Fatal(err)
	}
	assertIDs([]string{"prepare"})
	assertSubmission("", "")
	rows, err := a.Store.reader().QueryContext(ctx, "EXPLAIN QUERY PLAN "+liveOrganizeGroups)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "SEARCH j USING INDEX jobs_due (state=?)") || !strings.Contains(plan, "SEARCH m USING INDEX organize_queue_job (<expr>=?)") {
		t.Fatalf("queue scans archived membership:\n%s", plan)
	}
}
