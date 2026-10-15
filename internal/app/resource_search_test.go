package app

import (
	"context"
	"errors"
	"io"
	"light-media/internal/catalog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestKeywordResourceSearchDoesNotInventIdentityOrFetchPrivateMaterial(t *testing.T) {
	a := newTestApp(t)
	var requests atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/torrents.php" || r.URL.Query().Get("search") != "共用关键词" || r.URL.Query().Get("page") != "1" {
			t.Error("search escaped frozen query/page", r.URL.Path)
		}
		io.WriteString(w, workflowHTML("Unrelated.Film.2024.1080p.WEB-DL.x264-TEST", "", false))
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
	result, err := a.searchResources(context.Background(), SearchRequest{Query: "共用关键词", Page: 2})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("keyword search failed %+v %v", result, err)
	}
	if !result.Candidates[0].NeedsConfirmation || result.Candidates[0].Reasons[0] != "media_identity_required" || requests.Load() != 1 {
		t.Fatal("search asserted identity or fetched metainfo", result)
	}
	var intents, blobs int
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='intent'`).Scan(&intents)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='material_blob'`).Scan(&blobs)
	if intents != 0 || blobs != 0 {
		t.Fatal("search created a download", intents, blobs)
	}
	if _, err = a.previewDownload(context.Background(), PreviewRequest{SearchRequest: SearchRequest{Query: "共用关键词"}, SiteID: "fixture-site", ResourceID: "101"}, false); err == nil {
		t.Fatal("material preview bypassed required identity")
	}
}

func TestKeywordResourceNumberingSuggestionsDoNotInventSeasonForAbsoluteEpisodes(t *testing.T) {
	for _, tc := range []struct {
		title    string
		season   int
		episodes []int
	}{
		{"Synthetic Show S02E05 1080p", 2, []int{5}},
		{"Synthetic.Show.S03E04-E06.WEB-DL.mkv", 3, []int{4, 5, 6}},
		{"Synthetic Show EP260 1080p", 0, []int{}},
		{"Synthetic Show S02 1080p", 0, []int{}},
		{"Synthetic Show S02E05E05 1080p", 0, []int{}},
	} {
		season, episodes := resourceNumberingSuggestion(tc.title)
		if season != tc.season || !reflect.DeepEqual(episodes, tc.episodes) {
			t.Fatalf("%s produced season=%d episodes=%v", tc.title, season, episodes)
		}
	}
}

func TestSingleEpisodePreviewNeverExpandsAnUnspecifiedTargetIntoTheWholeSeason(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-01"}, {ID: 2, SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2024-01-02"}, {ID: 3, SeasonNumber: 1, EpisodeNumber: 3, AirDate: "2024-01-03"}}})
	torrent, _ := workflowTorrent("测试剧集 S01E02 1080p.mkv")
	var calls atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
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
	workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
	request := PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID, Season: 1, Query: "测试剧集"}, SiteID: "fixture-site", ResourceID: "101"}
	if _, err := a.previewDownload(context.Background(), request, false); err == nil {
		t.Fatal("empty episode selection silently became full season")
	} else {
		var api *APIError
		if !errors.As(err, &api) || api.Code != "REVIEW_EPISODES" {
			t.Fatal("wrong review error", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unspecified target fetched private material before asking for target")
	}
	request.Episodes = []int{2}
	preview, err := a.previewDownload(context.Background(), request, false)
	if err != nil {
		t.Fatal("single-episode click could not preview", err)
	}
	if !reflect.DeepEqual(preview.Episodes, []int{2}) || !reflect.DeepEqual(preview.Request.Episodes, []int{2}) {
		t.Fatal("single-episode target was widened", preview.Episodes, preview.Request.Episodes)
	}
	if calls.Load() != 2 {
		t.Fatal("expected actual listing and metainfo verification", calls.Load())
	}
}
