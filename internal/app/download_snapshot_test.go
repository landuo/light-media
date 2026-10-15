package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
)

func queuedSnapshotFixture(t *testing.T) (*App, Intent, Job, *atomic.Int32) {
	t.Helper()
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, _ := workflowTorrent("测试影片.2024.1080p.mkv")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
		} else if r.URL.Path == "/download.php" {
			w.Write(torrent)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	var adds atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			workflowJSON(w, []any{})
		case "/api/v2/torrents/add":
			adds.Add(1)
			io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	workflowConfigure(t, a, site.URL, qb.URL)
	preview, err := a.previewDownload(context.Background(), PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101"}, false)
	if err != nil {
		t.Fatal(err)
	}
	in, err := a.confirmDownload(context.Background(), preview.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.DownloaderSnapshot == "" || in.DownloaderSnapshot != preview.DownloaderSnapshot {
		t.Fatal("destination snapshot was not frozen")
	}
	job, err := a.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return a, in, job, &adds
}
func TestQueuedDownloadDoesNotResubmitNewlyRegisteredLibraryAsset(t *testing.T) {
	a, in, job, adds := queuedSnapshotFixture(t)
	path := filepath.Join(a.config.Roots[0].Path, "already-have-this-film.mkv")
	importWrite(t, path, []byte("existing complete video"))
	libraryReviewScan(t, a)
	candidate := libraryOnlyCandidate(t, a)
	if _, err := a.registerLibraryAsset(context.Background(), LibraryRegisterRequest{CandidateID: candidate.ID, MediaType: in.Media.MediaType, TMDBID: in.Media.ID, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	err := a.runDownload(context.Background(), job, in.ID)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "REVIEW_ALREADY_IN_LIBRARY" || adds.Load() != 0 {
		t.Fatalf("queued registered target Add count=%d err=%v", adds.Load(), err)
	}
}
func TestQueuedDownloadRejectsChangedDestinationButAllowsCredentialRotation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*DownloaderConfig)
	}{{"endpoint", func(d *DownloaderConfig) { d.BaseURL = "http://127.0.0.1:1" }}, {"download directory", func(d *DownloaderConfig) { d.DownloadPath = "/other-downloads" }}, {"mapping", func(d *DownloaderConfig) { d.Mappings = []PathMapping{{From: "/downloads", To: "/different"}} }}} {
		t.Run(tc.name, func(t *testing.T) {
			a, in, job, adds := queuedSnapshotFixture(t)
			tc.change(&a.config.Downloaders[0])
			err := a.runDownload(context.Background(), job, in.ID)
			var api *APIError
			if !errors.As(err, &api) || api.Code != "REVIEW_DOWNLOADER_CHANGED" || adds.Load() != 0 {
				t.Fatalf("changed destination reached Add: %v count=%d", err, adds.Load())
			}
		})
	}
	a, in, _, _ := queuedSnapshotFixture(t)
	a.config.Downloaders[0].Username = "rotated-user"
	a.config.Downloaders[0].Password = "rotated-owned-fixture-secret"
	if err := a.guardDownloaderSnapshot(context.Background(), in); err != nil {
		t.Fatal("credential rotation invalidated destination", err)
	}
	old := a.config.Downloaders[0]
	a.config.Downloaders[0].Mappings = []PathMapping{{From: "/downloads", To: "/new-source"}}
	client, err := download.New(old.Config)
	if err != nil {
		t.Fatal(err)
	}
	err = a.planCompletedDownload(context.Background(), client, old, &in, download.Task{Hash: in.Hash, Progress: 1})
	var api *APIError
	if !errors.As(err, &api) || api.Code != "REVIEW_DOWNLOADER_CHANGED" {
		t.Fatalf("completed task used changed mapping: %v", err)
	}
}
