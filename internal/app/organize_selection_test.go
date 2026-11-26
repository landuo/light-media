package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"light-media/internal/catalog"
)

// Exercise each public entry without a preview. Individual files remain
// addressable while queued and keep the same ID through actual publication.
func TestInteractiveDirectoryAndHistoryFileJobsPublish(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprintf("history=%t", history), func(t *testing.T) {
			a, source, target := importTestApp(t)
			ctx := t.Context()
			identity := importFixture(t, a, "tv")
			originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
				if r.URL.Path != "/3/tv/123" {
					t.Errorf("unexpected request %s", r.URL.Path)
					return 404, `{}`
				}
				return 200, `{"id":123,"name":"测试剧集","original_name":"Original Show","first_air_date":"2024-01-01","genres":[],"credits":{},"external_ids":{}}`
			})
			season := catalog.Season{SeriesID: 123, SeasonNumber: 2}
			for _, ep := range []int{1, 2} {
				season.Episodes = append(season.Episodes, catalog.Episode{ID: int64(1230 + ep), SeasonNumber: 2, EpisodeNumber: ep})
				path := filepath.Join(source, fmt.Sprintf("Original.Show.S02E%02d.mkv", ep))
				importWrite(t, path, []byte("synthetic fixture"))
				if history {
					if _, err := a.recordManualOrganizeFailure(ctx, path, id("wf_"), bad("REVIEW_IMPORT", "fixture")); err != nil {
						t.Fatal(err)
					}
				}
			}
			importCache(t, a, "season:123:2", season)
			form := manualOrganizeRequest{Interactive: true, Background: !history, Paths: []string{source}, TargetPath: target, Mode: "hardlink", MediaType: "tv", TMDBID: identity.ID, Scraping: importFlag(false)}
			if history {
				form.Paths = nil
				for _, h := range historyParityReadPage(t, a, "").Items {
					form.HistoryIDs = append(form.HistoryIDs, h.ID)
				}
				form.HistoryIDs = append(form.HistoryIDs, "deleted-history")
			}
			out, err := a.queueSelectedManualFiles(ctx, form)
			if err != nil || out.PreparationJobID != "" || out.Submitted != 2 || (out.Failed == 1) != history {
				t.Fatal(out, err)
			}
			ids := map[string]string{}
			for _, i := range out.Items {
				if i.Status == "queued" {
					if i.JobID == "" || ids[i.JobID] != "" {
						t.Fatal(i)
					}
					ids[i.JobID] = i.Source
				}
			}
			if len(ids) != 2 {
				t.Fatal(out)
			}
			readQueued := func() {
				t.Helper()
				for jid := range ids {
					j, err := a.pendingOrganizeJob(ctx, jid)
					if err != nil || j.Kind != "organize_file" || j.State != "queued" || j.ParentJobID != "" {
						t.Fatal(j, err)
					}
				}
			}
			readQueued()
			if _, err = a.deliverOrganizeInbox(ctx); err != nil {
				t.Fatal(err)
			}
			repeat, err := a.queueSelectedManualFiles(ctx, form)
			if err != nil || repeat.Submitted != 2 {
				t.Fatal(repeat, err)
			}
			for _, i := range repeat.Items {
				if i.JobID != "" && ids[i.JobID] != i.Source {
					t.Fatalf("existing ID not reused: %+v", i)
				}
			}
			for _, kind := range []string{"organize_file", "import"} {
				for jid := range ids {
					if _, err := a.Store.DB.Exec("UPDATE jobs SET state='running',generation=generation+1 WHERE id=? AND kind=? AND state='queued'", jid, kind); err != nil {
						t.Fatal(err)
					}
					job, err := scanJob(a.Store.DB.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE id=?", jid))
					if err != nil || job.Kind != kind || job.State != "running" {
						t.Fatal(job, err)
					}
					a.runClaimedJob(ctx, job)
				}
			}
			for jid, path := range ids {
				var state, dest string
				if err := a.Store.DB.QueryRow("SELECT state FROM jobs WHERE id=?", jid).Scan(&state); err != nil || state != "succeeded" {
					t.Fatal(jid, state, err)
				}
				if err := a.Store.DB.QueryRow("SELECT dest FROM transferhistory WHERE src=? AND status=1", path).Scan(&dest); err != nil {
					t.Fatal(err)
				}
				s, e := os.Stat(path)
				if e != nil {
					t.Fatal(e)
				}
				d, e := os.Stat(dest)
				if e != nil || !os.SameFile(s, d) {
					t.Fatal(path, dest, e)
				}
			}
		})
	}
}

func TestInteractiveFileBatchAtomicRetryAndRestart(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := t.Context()
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range []string{"Show.S01E01.mkv", "Show.S01E02.mkv"} {
		path := filepath.Join(source, file)
		paths = append(paths, path)
		importWrite(t, path, []byte("fixture"))
	}
	form := manualOrganizeRequest{Interactive: true, Background: true, Paths: paths, TargetPath: target, Mode: "hardlink"}
	if _, err := a.organizeInbox.db.Exec("CREATE TRIGGER reject_fixture BEFORE INSERT ON request WHEN NEW.subject LIKE '%02.mkv' BEGIN SELECT RAISE(ABORT,'fixture rejection'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.queueSelectedManualFiles(ctx, form); err == nil {
		t.Fatal("accepted partial transaction")
	}
	var rows int
	if err := a.organizeInbox.db.QueryRow("SELECT (SELECT count(*) FROM request)+(SELECT count(*) FROM batch_member)+(SELECT count(*) FROM file_batch)").Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
	if _, err := a.organizeInbox.db.Exec("DROP TRIGGER reject_fixture"); err != nil {
		t.Fatal(err)
	}
	var results [3]manualOrganizeOutput
	var errs [3]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = a.queueSelectedManualFiles(ctx, form) }()
	}
	wg.Wait()
	for i, out := range results {
		if errs[i] != nil || out.Submitted != 2 || out.Items[0].JobID != results[0].Items[0].JobID || out.Items[1].JobID != results[0].Items[1].JobID {
			t.Fatal(results, errs)
		}
	}
	var group string
	var listing []byte
	if err := a.organizeInbox.db.QueryRow("SELECT id,listings FROM file_batch").Scan(&group, &listing); err != nil {
		t.Fatal(err)
	}
	var jobs []Job
	for _, item := range results[0].Items {
		j, err := a.pendingOrganizeJob(ctx, item.JobID)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, j := range jobs {
		r, err := a.pendingOrganizeJob(ctx, j.ID)
		if err != nil || r.ID != j.ID {
			t.Fatal(r, err)
		}
	}
	if _, err := a.deliverOrganizeInbox(ctx); err != nil {
		t.Fatal(err)
	}
	// A crash after the main commit can leave every receipt present.
	if _, err := a.organizeInbox.db.Exec("INSERT INTO file_batch VALUES(?,?)", group, listing); err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		var item organizeFilePayload
		if err := json.Unmarshal(j.Payload, &item); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(j)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.organizeInbox.db.Exec("INSERT INTO request VALUES(?,?,?,?,?,'queued')", j.ID, j.Kind, item.File.Path, item.ID, body); err != nil {
			t.Fatal(err)
		}
		if _, err = a.organizeInbox.db.Exec("INSERT INTO batch_member VALUES(?,?)", j.ID, group); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.deliverOrganizeInbox(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind IN ('organize_file','organize_prepare')").Scan(&rows); err != nil || rows != 2 {
		t.Fatal(rows, err)
	}
	if err := a.organizeInbox.db.QueryRow("SELECT (SELECT count(*) FROM request)+(SELECT count(*) FROM batch_member)+(SELECT count(*) FROM file_batch)").Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
}
