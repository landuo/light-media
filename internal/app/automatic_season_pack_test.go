package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
)

func automaticPackTorrent(names []string) []byte {
	files := []any{}
	for _, name := range names {
		files = append(files, map[string]any{"length": 1024, "path": []any{name}})
	}
	return workflowBencode(map[string]any{"info": map[string]any{"name": "测试剧集 S01", "piece length": 16384, "pieces": bytes.Repeat([]byte{'p'}, 20), "files": files}})
}
func automaticPackApp(t *testing.T, files []string, future bool) (*App, Subscription, *atomic.Int32) {
	t.Helper()
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
	installMetadataFixture(t, a, "synthetic-automatic-pack", []string{"/3/tv/123"}, func(*http.Request) (*http.Response, error) {
		const body = `{"id":123,"name":"测试剧集","original_name":"Test Film","first_air_date":"2024-01-01","original_language":"zh","origin_country":["CN"],"overview":"测试简介","number_of_seasons":1,"number_of_episodes":3,"seasons":[{"season_number":1,"episode_count":3}],"genres":[{"id":18,"name":"剧情"}],"credits":{"cast":[],"crew":[]},"external_ids":{}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	episodes := []catalog.Episode{}
	for ep := 1; ep <= 3; ep++ {
		date := "2024-01-01"
		if future && ep == 3 {
			date = "2099-01-01"
		}
		episodes = append(episodes, catalog.Episode{ID: int64(ep), SeasonNumber: 1, EpisodeNumber: ep, AirDate: date})
	}
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: episodes})
	torrent := automaticPackTorrent(files)
	material, err := metainfo.ParseMaterial(torrent)
	if err != nil {
		t.Fatal(err)
	}
	adds := &atomic.Int32{}
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if adds.Load() > 0 {
				workflowJSON(w, []any{workflowTask(material.Hash, 0)})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			adds.Add(1)
			io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML("测试剧集 S01 1080p", "", false))
		case "/download.php":
			w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	workflowConfigure(t, a, site.URL, qb.URL)
	s, err := a.saveSubscription(context.Background(), "", SubscriptionRequest{MediaType: m.MediaType, TMDBID: m.ID, Season: 1, TargetScope: "season", StartEpisode: 1, DownloaderID: "fixture-qb", RootID: "root", State: "active"})
	if err != nil {
		t.Fatal(err)
	}
	return a, s, adds
}
func runAutomaticPackCheck(t *testing.T, a *App, s Subscription) {
	t.Helper()
	job := importClaim(t, a, "subscription", s.ID)
	if err := a.checkSubscription(context.Background(), job, s.ID, s.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := a.finishJob(context.Background(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
}
func TestAutomaticSeasonPackCreatesOneIntentForOnlyItsProvenMissingEpisodes(t *testing.T) {
	a, s, adds := automaticPackApp(t, []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"}, false)
	runAutomaticPackCheck(t, a, s)
	rows, err := a.Store.List(context.Background(), "intent", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatal("expected exactly one pack intent", len(rows), err)
	}
	var in Intent
	if err = json.Unmarshal(rows[0], &in); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.Episodes, []int{1, 2, 3}) || !in.Request.VerifiedSeasonPack || !in.Auto {
		t.Fatalf("pack not frozen %+v", in)
	}
	var slots int
	a.Store.DB.QueryRow(`SELECT count(*) FROM reservations WHERE intent_id=?`, in.ID).Scan(&slots)
	if slots != 3 || adds.Load() != 0 {
		t.Fatal("incorrect reservations or unconfirmed daemon write", slots, adds.Load())
	}
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='download' AND json_extract(payload,'$.id')=?`, in.ID))
	if err != nil {
		t.Fatal(err)
	}
	a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID)
	job.State = "running"
	job.Generation++
	if err = a.runDownload(context.Background(), job, in.ID); err != nil {
		t.Fatal("verified pack failed final submission", err)
	}
	if adds.Load() != 1 {
		t.Fatal("pack was not submitted exactly once", adds.Load())
	}
	runAutomaticPackCheck(t, a, s)
	again, _ := a.Store.List(context.Background(), "intent", 10, 0)
	if len(again) != 1 {
		t.Fatal("reserved pack was created twice", len(again))
	}
}
func TestAutomaticSeasonPackUnsafeFilesRequireManualReviewWithoutIntent(t *testing.T) {
	cases := []struct {
		name     string
		files    []string
		start    int
		existing bool
		future   bool
	}{
		{"before-start", []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv"}, 2, false, false},
		{"already-in-library", []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv"}, 1, true, false},
		{"cross-season", []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S02E02 1080p.mkv"}, 1, false, false},
		{"future", []string{"测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"}, 1, false, true},
		{"unknown-episode", []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E04 1080p.mkv"}, 1, false, false},
		{"ambiguous-file", []string{"测试剧集 S01E01 1080p.mkv", "unexpected.mkv"}, 1, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, s, adds := automaticPackApp(t, tc.files, tc.future)
			s.StartEpisode = tc.start
			a.Store.Put(context.Background(), "subscription", s.ID, s)
			if tc.existing {
				a.Store.DB.Exec(`INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(s.Media), 1, 1, "existing-fixture")
			}
			runAutomaticPackCheck(t, a, s)
			rows, _ := a.Store.List(context.Background(), "intent", 10, 0)
			if len(rows) != 0 || adds.Load() != 0 {
				t.Fatal("unsafe pack became a download", len(rows), adds.Load())
			}
			var current Subscription
			a.Store.Get(context.Background(), "subscription", s.ID, &current)
			if !strings.Contains(current.LastReason, "人工核对") {
				t.Fatal("manual review was hidden", current.LastReason)
			}
		})
	}
}

func TestAutomaticSeasonPackFreezesOnlyProvenSubsetAndRechecksAirDatesBeforeAdd(t *testing.T) {
	for _, subset := range []bool{false, true} {
		t.Run(fmt.Sprint("subset=", subset), func(t *testing.T) {
			files := []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"}
			expected := []int{1, 2, 3}
			if subset {
				files = files[1:]
				expected = []int{2, 3}
			}
			a, s, adds := automaticPackApp(t, files, false)
			runAutomaticPackCheck(t, a, s)
			rows, _ := a.Store.List(context.Background(), "intent", 10, 0)
			if len(rows) != 1 {
				t.Fatal("subset did not yield exactly one intent", len(rows))
			}
			var in Intent
			json.Unmarshal(rows[0], &in)
			if !reflect.DeepEqual(in.Episodes, expected) {
				t.Fatal("unproven missing episodes were included", in.Episodes, expected)
			}
			updated := catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-01"}, {ID: 2, SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2024-01-01"}, {ID: 3, SeasonNumber: 1, EpisodeNumber: 3, AirDate: "2099-01-01"}}}
			importCache(t, a, "season:123:1", updated)
			job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='download' AND json_extract(payload,'$.id')=?`, in.ID))
			if err != nil {
				t.Fatal(err)
			}
			a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID)
			job.State = "running"
			job.Generation++
			if err = a.runDownload(context.Background(), job, in.ID); err == nil {
				t.Fatal("changed future air date was ignored")
			}
			if adds.Load() != 0 {
				t.Fatal("pack submitted after metadata changed")
			}
		})
	}
}

func TestQueuedAutomaticSingleEpisodeRechecksCurrentAirDateInsteadOfFrozenDateSnapshot(t *testing.T) {
	a, s, adds := automaticPackApp(t, []string{"测试剧集 S01E02 1080p.mkv"}, false)
	torrent, _ := workflowTorrent("测试剧集 S01E02 1080p.mkv")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML("测试剧集 S01E02 1080p", "", false))
		case "/download.php":
			w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	a.config.Sites[0].BaseURL = site.URL
	request := PreviewRequest{SearchRequest: SearchRequest{MediaType: "tv", TMDBID: 123, Season: 1, Episodes: []int{2}, EpisodeDates: map[int]string{2: "2024-01-01"}}, SiteID: "fixture-site", ResourceID: "101", SubscriptionID: s.ID, Revision: s.Revision}
	preview, err := a.previewDownload(context.Background(), request, true)
	if err != nil {
		t.Fatal(err)
	}
	in, err := a.confirmDownload(context.Background(), preview.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-01"}, {ID: 2, SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2099-01-01"}, {ID: 3, SeasonNumber: 1, EpisodeNumber: 3, AirDate: "2024-01-03"}}})
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='download' AND json_extract(payload,'$.id')=?`, in.ID))
	if err != nil {
		t.Fatal(err)
	}
	a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID)
	job.State = "running"
	job.Generation++
	if err = a.runDownload(context.Background(), job, in.ID); err == nil {
		t.Fatal("single episode used an obsolete aired-date snapshot")
	}
	if adds.Load() != 0 {
		t.Fatal("postponed automatic episode was sent to downloader")
	}
}

func TestManualPreviewCannotAssertAutomaticPackValidation(t *testing.T) {
	a, _, _ := automaticPackApp(t, []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"}, false)
	preview, err := a.previewDownload(context.Background(), PreviewRequest{SearchRequest: SearchRequest{MediaType: "tv", TMDBID: 123, Season: 1, Episodes: []int{1, 2, 3}, VerifiedSeasonPack: true}, SiteID: "fixture-site", ResourceID: "101"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Request.VerifiedSeasonPack || !preview.NeedsConfirmation {
		t.Fatal("client-supplied flag bypassed manual confirmation")
	}
	if _, err = a.confirmDownload(context.Background(), preview.ID, false); err == nil {
		t.Fatal("manual pack confirmed without explicit review")
	}
}
