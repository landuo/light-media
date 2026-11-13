package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/download"
)

// Exercise the actual scanner, downloader protocol, stored native record and
// persistent import preview together. No direct injection into the resolver.
func TestDirectoryMonitorUsesNativeMediaHintBeforeRuleTypeAndRejectsNFOConflict(t *testing.T) {
	for _, conflictingNFO := range []bool{false, true} {
		name := "native_hint_identifies_unknown_filename"
		if conflictingNFO {
			name = "native_hint_conflicts_with_nfo"
		}
		t.Run(name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			identity := importFixture(t, a, "movie")
			path := filepath.Join(source, "unidentified.mkv")
			video := []byte("synthetic completed native download")
			importWrite(t, path, video)
			if conflictingNFO {
				importWrite(t, strings.TrimSuffix(path, ".mkv")+".nfo", []byte(`<movie><title>Another Film</title><uniqueid type="tmdb" default="true">456</uniqueid></movie>`))
			}
			const hash = "dddddddddddddddddddddddddddddddddddddddd"
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("monitor attempted downloader mutation: %s", r.Method)
					http.Error(w, "read only", 405)
					return
				}
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					task := workflowTask(hash, 1)
					task["state"], task["save_path"] = "uploading", source
					task["total_size"], task["completed"] = len(video), len(video)
					workflowJSON(w, []any{task})
				case "/api/v2/torrents/files":
					if r.URL.Query().Get("hash") != hash {
						t.Error("monitor queried a different torrent")
					}
					workflowJSON(w, []any{map[string]any{"index": 0, "name": filepath.Base(path), "size": len(video), "progress": 1}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer qb.Close()
			d := DownloaderConfig{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: qb.URL}, SharedPaths: true}
			a.config.Downloaders = []DownloaderConfig{d}
			for _, kind := range []string{"tv", "movie"} {
				destination := filepath.Join(target, kind)
				if err := os.Mkdir(destination, 0750); err != nil {
					t.Fatal(err)
				}
				if _, err := a.saveManagedDirectory(context.Background(), DirectoryRule{Name: kind, SourcePath: source, LibraryPath: destination, MediaType: kind, Mode: "hardlink", MonitorType: "monitor", Enabled: true, IncludeExisting: true}); err != nil {
					t.Fatal(err)
				}
			}
			// The unknown basename has no type. Without applying the hint before
			// ordered matching it would be captured by the first (TV) watcher.
			tvRule, movieRule := a.Settings().Directories[0], a.Settings().Directories[1]
			if err := a.Store.Put(context.Background(), "native_download", "native-fixture", NativeDownload{ID: "native-fixture", DownloaderID: d.ID, DownloadPath: source, Hash: hash, MediaType: "movie", TMDBID: identity.ID, State: "accepted"}); err != nil {
				t.Fatal(err)
			}
			directoryPoll(t, a, tvRule)
			directoryPoll(t, a, movieRule)
			directoryAge(t, a, movieRule, path)
			directoryPoll(t, a, movieRule)
			observed := directoryRecognize(t, a, movieRule, path)
			if skipped := directoryObservation(t, a, tvRule, path); skipped.PlanID != "" || skipped.State != "baseline" {
				t.Fatalf("TV rule captured a movie hint: %+v", skipped)
			}
			if conflictingNFO {
				if observed.State != "error" || observed.PlanID == "" || !strings.Contains(observed.Reason, "疑问") {
					t.Fatalf("conflicting NFO was not rejected: %+v", observed)
				}
				var count int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='import_plan'`).Scan(&count); err != nil || count != 1 {
					t.Fatal("conflicting hint did not persist exactly one failure", count, err)
				}
				var failed ImportPlan
				if err := a.Store.Get(context.Background(), "import_plan", observed.PlanID, &failed); err != nil || failed.State != "failed" || len(failed.Files) != 1 || failed.Files[0].Status != "blocked" || failed.Files[0].Target != "" {
					t.Fatal("conflicting hint authorized a target", failed, err)
				}
				var state string
				if err := a.Store.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, observed.JobID).Scan(&state); err != nil || state != "failed" {
					t.Fatal("conflict job not failed", state, err)
				}
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM reservations`).Scan(&count); err != nil || count != 0 {
					t.Fatal("conflicting hint reserved slots", count, err)
				}
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&count); err != nil || count != 0 {
					t.Fatal("conflicting hint published assets", count, err)
				}
			} else {
				if observed.State != "queued" || observed.PlanID == "" {
					t.Fatalf("native hint did not produce persistent import plan: %+v", observed)
				}
				var plan ImportPlan
				if err := a.Store.Get(context.Background(), "import_plan", observed.PlanID, &plan); err != nil {
					t.Fatal(err)
				}
				if plan.Media.MediaType != "movie" || plan.Media.ID != identity.ID || plan.RootID != movieRule.RootID || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatalf("hint identity/type/target lost before import: %+v", plan)
				}
			}
			if actual, err := os.ReadFile(path); err != nil || string(actual) != string(video) {
				t.Fatal("monitor changed completed download source", err)
			}
		})
	}
}
