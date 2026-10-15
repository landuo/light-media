package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func directoryFileFixture(t *testing.T, include bool) (*App, DirectoryRule, string) {
	t.Helper()
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	rule := DirectoryRule{ID: "watch", Name: "测试目录", SourcePath: source, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "monitor", Enabled: true, IncludeExisting: include}
	a.config.Directories = []DirectoryRule{rule}
	return a, rule, source
}
func directoryMovieFile(t *testing.T, source string) string {
	t.Helper()
	p := filepath.Join(source, "Test Film (2024).mkv")
	importWrite(t, p, []byte("synthetic monitor video"))
	importWrite(t, strings.TrimSuffix(p, ".mkv")+".nfo", []byte(`<movie><title>Test Film</title><uniqueid type="tmdb" default="true">123</uniqueid></movie>`))
	return p
}
func directoryObservation(t *testing.T, a *App, rule DirectoryRule, path string) DirectoryObservedFile {
	t.Helper()
	var f DirectoryObservedFile
	if e := a.Store.Get(context.Background(), "directory_file", directoryFileID(rule.ID, path), &f); e != nil {
		t.Fatal(e)
	}
	return f
}
func directoryAge(t *testing.T, a *App, rule DirectoryRule, path string) {
	t.Helper()
	f := directoryObservation(t, a, rule, path)
	f.StableSince = time.Now().Add(-2 * time.Minute).UnixNano()
	if e := a.Store.Put(context.Background(), "directory_file", f.ID, f); e != nil {
		t.Fatal(e)
	}
}
func directoryPoll(t *testing.T, a *App, rule DirectoryRule) {
	t.Helper()
	if e := a.pollDirectoryFileRule(context.Background(), rule); e != nil {
		t.Fatal(e)
	}
}

func TestDirectoryFileBaselineAndChangedFileStability(t *testing.T) {
	a, rule, source := directoryFileFixture(t, false)
	p := directoryMovieFile(t, source)
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, p)
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "baseline" || f.PlanID != "" {
		t.Fatalf("existing file was not skipped: %+v", f)
	}
	importWrite(t, p, []byte("changed synthetic bytes, still writing"))
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "observing" || f.PlanID != "" {
		t.Fatalf("changed file bypassed stability: %+v", f)
	}
	var plans int
	_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='import_plan'`).Scan(&plans)
	if plans != 0 {
		t.Fatal("unstable file created a plan")
	}
}

func TestDirectoryFileNewStableVideoImportsOnceAndRetainsSource(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "include-existing"}[include], func(t *testing.T) {
			a, rule, source := directoryFileFixture(t, include)
			if !include {
				directoryPoll(t, a, rule)
			}
			p := directoryMovieFile(t, source)
			directoryPoll(t, a, rule)
			if f := directoryObservation(t, a, rule, p); f.State != "observing" {
				t.Fatalf("first observation: %+v", f)
			}
			directoryAge(t, a, rule, p)
			directoryPoll(t, a, rule)
			f := directoryObservation(t, a, rule, p)
			if f.State != "queued" || f.PlanID == "" {
				t.Fatalf("stable file not queued: %+v", f)
			}
			job, e := a.claim(context.Background())
			if e != nil || job.Kind != "import" {
				t.Fatalf("job=%+v err=%v", job, e)
			}
			if e = a.runImport(context.Background(), job, f.PlanID, nil); e != nil {
				t.Fatal(e)
			}
			directoryPoll(t, a, rule)
			directoryPoll(t, a, rule)
			if got := directoryObservation(t, a, rule, p); got.State != "imported" {
				t.Fatalf("completion not recorded: %+v", got)
			}
			var plan ImportPlan
			if e = a.Store.Get(context.Background(), "import_plan", f.PlanID, &plan); e != nil {
				t.Fatal(e)
			}
			si, e := os.Stat(p)
			if e != nil {
				t.Fatal("source removed", e)
			}
			ti, e := os.Stat(plan.Files[0].Target)
			if e != nil {
				t.Fatal(e)
			}
			if !os.SameFile(si, ti) {
				t.Fatal("hardlink not used")
			}
			var plans, assets int
			_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='import_plan'`).Scan(&plans)
			_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets)
			if plans != 1 || assets != 1 {
				t.Fatalf("duplicate processing plans=%d assets=%d", plans, assets)
			}
		})
	}
}

func TestDirectoryFileRuleChangeBlocksQueuedPublication(t *testing.T) {
	for _, change := range []string{"disabled", "target", "source"} {
		t.Run(change, func(t *testing.T) {
			a, rule, source := directoryFileFixture(t, true)
			p := directoryMovieFile(t, source)
			directoryPoll(t, a, rule)
			directoryAge(t, a, rule, p)
			directoryPoll(t, a, rule)
			f := directoryObservation(t, a, rule, p)
			switch change {
			case "disabled":
				a.config.Directories[0].Enabled = false
			case "target":
				a.config.Directories[0].RootID = "elsewhere"
			case "source":
				a.config.Directories[0].SourcePath = filepath.Dir(source)
			}
			job, e := a.claim(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if e = a.runImport(context.Background(), job, f.PlanID, nil); e == nil {
				t.Fatal("changed rule published queued import")
			}
			var count int
			_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&count)
			if count != 0 {
				t.Fatal("changed rule created an asset")
			}
		})
	}
}

func TestDirectoryFileAmbiguousIdentityNeedsReviewAndNFOCanResolve(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	p := filepath.Join(source, "Test Film (2024).mkv")
	importWrite(t, p, []byte("synthetic"))
	importCache(t, a, "directory-identify:movie:Test Film", catalog.Page{TotalPages: 1, Results: []catalog.Media{{ID: 123, MediaType: "movie", Title: "Test Film", Year: 2024}, {ID: 456, MediaType: "movie", Title: "Test Film", Year: 2024}}})
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, p)
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "needs_review" || f.PlanID != "" {
		t.Fatalf("ambiguous identity imported: %+v", f)
	}
	importWrite(t, strings.TrimSuffix(p, ".mkv")+".nfo", []byte(`<movie><title>Test Film</title><uniqueid type="tmdb" default="true">123</uniqueid></movie>`))
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "observing" {
		t.Fatalf("corrected NFO not reconsidered: %+v", f)
	}
	directoryAge(t, a, rule, p)
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "queued" {
		t.Fatalf("corrected NFO not imported: %+v", f)
	}
}

func TestDirectoryFileIncompleteDownloaderNeverImports(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	p := directoryMovieFile(t, source)
	state := "downloading"
	progress := .5
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			task := workflowTask(strings.Repeat("a", 40), progress)
			task["state"] = state
			task["save_path"] = "/downloads"
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/files":
			workflowJSON(w, []any{map[string]any{"index": 0, "name": filepath.Base(p), "size": 22, "progress": progress}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: qb.URL}, DownloadPath: "/downloads", Mappings: []PathMapping{{From: "/downloads", To: source}}}
	a.config.Downloaders = []DownloaderConfig{d}
	rule.DownloaderID = d.ID
	rule.DownloadPath = "/downloads"
	a.config.Directories[0] = rule
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, p)
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "downloading" || f.PlanID != "" {
		t.Fatalf("partial task imported: %+v", f)
	}
	state = "checkingUP"
	progress = 1
	directoryAge(t, a, rule, p)
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "downloading" || f.PlanID != "" {
		t.Fatalf("checking task imported: %+v", f)
	}
	// An independent filesystem rule still checks configured downloaders whose
	// mapped paths overlap, including without an explicit downloader binding.
	rule.DownloaderID = ""
	rule.DownloadPath = ""
	a.config.Directories[0] = rule
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, p)
	directoryPoll(t, a, rule)
	if f := directoryObservation(t, a, rule, p); f.State != "downloading" || f.PlanID != "" {
		t.Fatalf("standalone watcher ignored checking task: %+v", f)
	}
	qb.Close()
	if e := a.pollDirectoryFileRule(context.Background(), rule); e == nil {
		t.Fatal("unavailable downloader treated as safe")
	}
	var status managedConnection
	if e := a.Store.Get(context.Background(), "directory_status", rule.ID, &status); e != nil || status.Status != "error" {
		t.Fatalf("failure not visible: %+v %v", status, e)
	}
}
