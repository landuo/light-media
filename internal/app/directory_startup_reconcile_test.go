package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The pass works from stored observations, so a record's key always matches the
// one the scanner reads. Only stranded states inside the window are released.
func TestStartupReconcileReleasesStrandedObservations(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	source := t.TempDir()
	rule := DirectoryRule{ID: "directory_probe", Enabled: true, MonitorType: "monitor", SourcePath: source}

	makeFile := func(name string, age time.Duration) string {
		p := filepath.Join(source, name)
		if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	put := func(path, state, reason string) {
		key := directoryFileID(rule.ID, path)
		if err := a.Store.Put(ctx, "directory_file", key, DirectoryObservedFile{
			ID: key, DirectoryID: rule.ID, Path: path, State: state, Reason: reason, LastSeen: now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	tracked := makeFile("tracked.mkv", 20*time.Minute)
	put(tracked, "tracking", "本应用下载任务，等待确认下载完成")
	baselined := makeFile("baseline.mkv", 30*time.Minute)
	put(baselined, "baseline", "启用监听时已存在，已跳过")
	identityErr := makeFile("identity.mkv", 40*time.Minute)
	put(identityErr, "error", "影视身份不唯一或不完整，请手动指定")
	oldStranded := makeFile("old.mkv", 96*time.Hour)
	put(oldStranded, "baseline", "启用监听时已存在，已跳过")
	fresh := makeFile("fresh.mkv", 30*time.Second)
	put(fresh, "baseline", "启用监听时已存在，已跳过")
	imported := makeFile("imported.mkv", 20*time.Minute)
	put(imported, "imported", "已入库")

	window := time.Now().Add(-directoryStartupReconcileWindow)
	for _, tc := range []struct {
		path string
		want bool
	}{
		{tracked, true},
		{baselined, true},
		{identityErr, true},
		{oldStranded, false},
		{fresh, false},
		{imported, false},
	} {
		key := directoryFileID(rule.ID, tc.path)
		var f DirectoryObservedFile
		if err := a.Store.Get(ctx, "directory_file", key, &f); err != nil {
			t.Fatal(err)
		}
		got, why := a.releaseStrandedObservation(ctx, rule, f, window)
		if got != tc.want {
			t.Fatalf("%s: released=%v want %v (%s)", filepath.Base(tc.path), got, tc.want, why)
		}
		if got {
			var after DirectoryObservedFile
			if err := a.Store.Get(ctx, "directory_file", key, &after); err != nil {
				t.Fatal(err)
			}
			if after.State != "observing" {
				t.Fatalf("%s released to %q", filepath.Base(tc.path), after.State)
			}
		}
	}
}

// A plan or job that can still progress keeps the file parked.
func TestStartupReconcileRespectsLivePlanAndJob(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	source := t.TempDir()
	rule := DirectoryRule{ID: "directory_probe2", Enabled: true, MonitorType: "monitor", SourcePath: source}
	path := filepath.Join(source, "live.mkv")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "import_plan", "plan_live", ImportPlan{ID: "plan_live", State: "running"}); err != nil {
		t.Fatal(err)
	}
	// A live plan is only live while its job is: the release path reads the job.
	if _, err := a.Store.DB.ExecContext(ctx,
		`INSERT INTO jobs(id,kind,state,workflow_id,payload,run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"job_live", "import", "running", "wf_probe", `{"id":"plan_live"}`, 0, now(), now()); err != nil {
		t.Fatal(err)
	}
	key := directoryFileID(rule.ID, path)
	if err := a.Store.Put(ctx, "directory_file", key, DirectoryObservedFile{
		ID: key, DirectoryID: rule.ID, Path: path, State: "tracking", PlanID: "plan_live", LastSeen: now(),
	}); err != nil {
		t.Fatal(err)
	}
	var f DirectoryObservedFile
	if err := a.Store.Get(ctx, "directory_file", key, &f); err != nil {
		t.Fatal(err)
	}
	if ok, why := a.releaseStrandedObservation(ctx, rule, f, time.Now().Add(-time.Hour)); ok {
		t.Fatalf("a running plan must keep the file parked (%s)", why)
	}

	// A failed plan and a failed job no longer block.
	if err := a.Store.Put(ctx, "import_plan", "plan_dead", ImportPlan{ID: "plan_dead", State: "failed"}); err != nil {
		t.Fatal(err)
	}
	f.PlanID, f.JobID = "plan_dead", "job_missing"
	if ok, why := a.releaseStrandedObservation(ctx, rule, f, time.Now().Add(-time.Hour)); !ok {
		t.Fatalf("a failed plan must be cleared: %s", why)
	}
}
