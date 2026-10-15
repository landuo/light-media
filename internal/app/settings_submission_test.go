package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The second daemon existence read is inside Client.Add, after the final
// application snapshot check. Holding it reproduces the dangerous interval in
// which a settings save used to return success before the old Add was sent.
func TestSettingsCannotPublishDestinationBetweenFinalCheckAndAdd(t *testing.T) {
	for _, route := range []string{"managed_downloader", "settings_put"} {
		t.Run(route, func(t *testing.T) {
			a, _, _ := importTestApp(t)
			m := importFixture(t, a, "movie")
			torrent, hash := workflowTorrent("测试影片.2024.1080p.mkv")
			insideAdd, releaseAdd := make(chan struct{}), make(chan struct{})
			var releaseOnce, interceptOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseAdd) }) }
			defer release()
			var submitted atomic.Bool
			var adds atomic.Int32
			var savepath string
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if r.URL.Query().Get("hashes") != "" {
						interceptOnce.Do(func() { close(insideAdd); <-releaseAdd })
					}
					if submitted.Load() {
						workflowJSON(w, []any{workflowTask(hash, 0)})
					} else {
						workflowJSON(w, []any{})
					}
				case "/api/v2/torrents/add":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
					}
					savepath = r.FormValue("savepath")
					adds.Add(1)
					submitted.Store(true)
					_, _ = io.WriteString(w, "Ok.")
				default:
					http.NotFound(w, r)
				}
			}))
			defer func() { release(); qb.Close() }()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/torrents.php" {
					_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
				} else if r.URL.Path == "/download.php" {
					_, _ = w.Write(torrent)
				} else {
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			a.config.Downloaders[0].Name = "fixture"
			ctx := context.Background()
			preview, err := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101"}, false)
			if err != nil {
				t.Fatal(err)
			}
			intent, err := a.confirmDownload(ctx, preview.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			job, err := a.claim(ctx)
			if err != nil {
				t.Fatal(err)
			}
			cookie := setupTestAdmin(t, a)
			runDone := make(chan error, 1)
			go func() { runDone <- a.runDownload(ctx, job, intent.ID) }()
			select {
			case <-insideAdd:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not reach final Add existence check")
			}
			settings := a.Settings()
			settings.Downloaders[0].DownloadPath = "/new-downloads"
			mutationDone := make(chan error, 1)
			go func() {
				if route == "managed_downloader" {
					_, err := a.saveManagedDownloader(ctx, ManagedDownloaderRequest{DownloaderConfig: settings.Downloaders[0]})
					mutationDone <- err
					return
				}
				raw, err := json.Marshal(settings)
				if err != nil {
					mutationDone <- err
					return
				}
				w := callTest(t, a.Handler(), http.MethodPut, "/api/settings", string(raw), cookie, "")
				if w.Code != http.StatusOK {
					mutationDone <- fmt.Errorf("settings status %d", w.Code)
					return
				}
				mutationDone <- nil
			}()
			select {
			case err := <-mutationDone:
				release()
				<-runDone
				t.Fatalf("settings committed while old-destination Add was pending: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			if a.Settings().Downloaders[0].DownloadPath != "/downloads" {
				t.Fatal("new destination became visible before old Add ended")
			}
			release()
			select {
			case err := <-runDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("download did not complete")
			}
			select {
			case err := <-mutationDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("settings did not resume after Add")
			}
			if adds.Load() != 1 || savepath != "/downloads" || a.Settings().Downloaders[0].DownloadPath != "/new-downloads" {
				t.Fatalf("wrong destination ordering adds=%d path=%q", adds.Load(), savepath)
			}
			if err := a.guardDownloaderSnapshot(ctx, intent); err == nil {
				t.Fatal("subsequent use of old confirmed snapshot was not rejected")
			}
		})
	}
}
