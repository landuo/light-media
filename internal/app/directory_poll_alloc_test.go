package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A second directory with the same basename is not the same resource. Only
// the exact source history gates a file. A replacement at the selected source
// restarts stability and recognition; deleting the history alone does not.
func TestDirectoryPollHistoryGateKeepsSourceIdentity(t *testing.T) {
	a, base, _ := importTestApp(t)
	var paths []string
	for _, id := range []string{"first", "second"} {
		source := filepath.Join(base, id)
		paths = append(paths, directoryMovieFile(t, source))
		a.config.Directories = append(a.config.Directories, DirectoryRule{ID: id, SourcePath: source, RootID: "root", MediaType: "movie", MonitorType: "monitor", Enabled: true, IncludeExisting: true})
	}
	a.invalidateSettings()
	first, second := a.config.Directories[0], a.config.Directories[1]
	if _, err := a.Store.DB.Exec(`INSERT INTO transferhistory(src,status,errmsg,src_fileitem,date) VALUES(?,0,'failed','{"plan_id":"old-plan","job_id":"old-job"}',?)`, paths[1], now()); err != nil {
		t.Fatal(err)
	}
	directoryPoll(t, a, first)
	directoryPoll(t, a, second)
	if f := directoryObservation(t, a, first, paths[0]); f.State != "observing" || f.PlanID != "" {
		t.Fatalf("sibling incorrectly gated: %+v", f)
	}
	directoryAge(t, a, second, paths[1])
	directoryPoll(t, a, second)
	if f := directoryObservation(t, a, second, paths[1]); f.State != "error" || f.JobID != "old-job" || f.PlanID != "old-plan" {
		t.Fatalf("lost history gate: %+v", f)
	}
	var id string
	if err := a.Store.DB.QueryRow(`SELECT id FROM transferhistory WHERE src=?`, paths[1]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	removeDirectoryHistoryForTest(t, a, id, false)
	for i := 0; i < 2; i++ {
		directoryAge(t, a, second, paths[1])
		directoryPoll(t, a, second)
	}
	var jobs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='organize_file'`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("deleting an unchanged source's history queued %d jobs: %v", jobs, err)
	}
	replacement := paths[1] + ".new"
	importWrite(t, replacement, []byte("replacement at the same source"))
	if err := os.Rename(replacement, paths[1]); err != nil {
		t.Fatal(err)
	}
	directoryPoll(t, a, second)
	if f := directoryObservation(t, a, second, paths[1]); f.State != "observing" || f.JobID != "" {
		t.Fatalf("replacement skipped stability: %+v", f)
	}
	directoryAge(t, a, second, paths[1])
	directoryPoll(t, a, second)
	f := directoryObservation(t, a, second, paths[1])
	var raw []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM jobs WHERE kind='organize_file' AND id=?`, f.JobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload organizeFilePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Directory == nil || payload.Directory.Rule.ID != second.ID || payload.File.Path != paths[1] || payload.File.InferredMedia == nil || payload.File.InferredMedia.ID != 123 {
		t.Fatalf("incorrect source or identity in retry: %+v", payload)
	}
}

// One operation is a complete warm sweep of four independent rules. Half the
// files have visible successful history and the rest are untouched baselines.
func BenchmarkDirectoryPollWarm844(b *testing.B) {
	a, base, _ := importTestApp(b)
	ctx := b.Context()
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("rule-%d", i)
		source := filepath.Join(base, id)
		if err := os.Mkdir(source, 0700); err != nil {
			b.Fatal(err)
		}
		a.config.Directories = append(a.config.Directories, DirectoryRule{ID: id, SourcePath: source, RootID: "root", MonitorType: "monitor", Enabled: true})
		for j := 0; j < 211; j++ {
			path := filepath.Join(source, fmt.Sprintf("Film %03d (2024).mkv", j))
			if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
				b.Fatal(err)
			}
			if j%2 == 0 {
				if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO transferhistory(src,src_fileitem,date) VALUES(?,json_object('plan_id',?,'job_id',?),?)`, path, fmt.Sprintf("plan-%d-%d", i, j), fmt.Sprintf("job-%d-%d", i, j), now()); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	a.invalidateSettings()
	sweep := func() {
		for _, rule := range a.config.Directories {
			if err := a.pollDirectoryFileRule(ctx, rule); err != nil {
				b.Fatal(err)
			}
		}
	}
	sweep()
	var imported, baseline int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE json_extract(payload,'$.state')='imported'),count(*) FILTER (WHERE json_extract(payload,'$.state')='baseline') FROM directory_file`).Scan(&imported, &baseline); err != nil || imported != 424 || baseline != 420 {
		b.Fatalf("imported=%d baseline=%d: %v", imported, baseline, err)
	}
	b.ReportAllocs()
	for b.Loop() {
		sweep()
	}
}
