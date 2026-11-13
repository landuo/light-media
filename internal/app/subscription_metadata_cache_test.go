package app

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/catalog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionSeasonLoadingAndSaveReuseNativeResponse(t *testing.T) {
	a := newTestApp(t)
	calls := 0
	installMetadataFixture(t, a, "owned-season-key", []string{"/3/tv/123/season/1"}, func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":9,"season_number":1,"name":"Season","overview":"Description","episodes":[{"id":19,"season_number":1,"episode_number":1,"name":"Episode","overview":"Description","air_date":"2026-01-01"}]}`))}, nil
	})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		view, err := a.originalCatalogSeason(ctx, 123, 1, nil)
		if err != nil || len(view.Episodes) != 1 {
			t.Fatalf("UI season: %+v %v", view, err)
		}
	}
	// Same path used by saveSubscription; still performs native identity checks.
	saved, err := a.nativeSeason(ctx, 123, 1)
	if err != nil || len(saved.Episodes) != 1 || saved.Episodes[0].ID != 19 {
		t.Fatalf("save season: %+v %v", saved, err)
	}
	if calls != 1 {
		t.Fatalf("UI/reopen/save made %d native requests, want 1", calls)
	}
	// Episode groups must never reuse standard-season bytes.
	var group json.RawMessage
	group = json.RawMessage(`{"episodes":[]}`)
	if err := a.Store.Put(ctx, "cache", "source-season:123:1:alternate", map[string]any{"at": time.Now(), "data": group}); err != nil {
		t.Fatal(err)
	}
	raw, err := a.cachedNativeSeasonInformation(ctx, 123, 1, "alternate")
	if err != nil || !strings.Contains(string(raw), `"episodes":[]`) {
		t.Fatalf("group scope: %s %v", raw, err)
	}
}

func TestSubscriptionCreateReusesServerDetailNotBrowserCopy(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	media := catalog.Media{ID: 123, MediaType: "tv", Title: "Trusted detail"}
	info := sourceMediaInformation{Media: media, ProviderInfo: json.RawMessage(`{"id":123}`), MediaInfo: json.RawMessage(`{"tmdb_id":123}`)}
	if err := a.Store.Put(ctx, "cache", "source-detail:tv:123", map[string]any{"at": time.Now(), "data": info}); err != nil {
		t.Fatal(err)
	}
	got, err := a.sourceAsyncRecognizeTMDBMedia(ctx, catalog.Media{ID: 123, MediaType: "tv", Title: "Untrusted browser title"}, nil)
	if err != nil || got.Media.Title != "Trusted detail" {
		t.Fatalf("cached recognition %+v %v", got, err)
	}
}

func TestSeasonPickerColdRequestOmitsUnrelatedAppendices(t *testing.T) {
	a := newTestApp(t)
	calls := 0
	installMetadataFixture(t, a, "owned-season-key", []string{"/3/tv/123"}, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("append_to_response") != "" {
			t.Error("picker fetched unrelated appendices")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":123,"seasons":[{"season_number":1,"episode_count":8,"name":"First"}]}`))}, nil
	})
	for i := 0; i < 2; i++ {
		rows, err := a.originalCatalogSeasons(context.Background(), 123)
		if err != nil || len(rows) != 1 || rows[0]["name"] != "First" {
			t.Fatalf("picker %+v %v", rows, err)
		}
	}
	if calls != 1 {
		t.Fatalf("picker repeated %d requests", calls)
	}
}
