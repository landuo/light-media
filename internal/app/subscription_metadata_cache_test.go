package app

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/catalog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionCreateReusesServerDetailNotBrowserCopy(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	raw := json.RawMessage(`{"id":123,"name":"Trusted detail","original_name":"Original","first_air_date":"2026-01-01","original_language":"zh","origin_country":["CN"],"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"alternative_titles":{"titles":[],"results":[]},"translations":{"translations":[]},"seasons":[],"poster_path":"/owned.jpg"}`)
	info := sourceMediaInformation{ProviderInfo: raw, MediaInfo: json.RawMessage(`{"title":"stale","category":"stale"}`)}
	a.config.TMDBImageDomain = "https://images.example.invalid"

	if err := a.Store.Put(ctx, "cache", "source-detail:tv:123", map[string]any{"at": time.Now(), "data": info}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte("movie: {}\ntv:\n  Updated: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	got, err := a.sourceAsyncRecognizeTMDBMedia(ctx, catalog.Media{ID: 123, MediaType: "tv", Title: "Untrusted browser title"}, nil)
	if err != nil || got.Media.Title != "Trusted detail" || !strings.Contains(string(got.MediaInfo), "images.example.invalid") || strings.Contains(string(got.MediaInfo), "stale") || !strings.Contains(string(got.MediaInfo), "Updated") {
		t.Fatalf("cached recognition %+v %v", got, err)
	}
	// A newly required field not present in the old provider bytes forces a fresh
	// provider read rather than certifying absence from an old projection.
	calls := 0
	installMetadataFixture(t, a, "owned-extra-field", []string{"/3/tv/123"}, func(r *http.Request) (*http.Response, error) {
		calls++
		fresh := strings.TrimSuffix(string(raw), "}") + `,"status":"Returning Series"}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fresh))}, nil
	})
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte("movie: {}\ntv:\n  Running:\n    status: 'Returning Series'\n  Other: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	got, err = a.cachedSubscriptionSourceInformation(ctx, catalog.Media{ID: 123, MediaType: "tv"})
	if err != nil || calls != 1 || !strings.Contains(string(got.MediaInfo), "Running") {
		t.Fatalf("live policy extra field: calls=%d info=%s err=%v", calls, got.MediaInfo, err)
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

func TestReviewInvalidRawSeasonPoisonsRetry(t *testing.T) {
	a := newTestApp(t)
	calls := 0
	installMetadataFixture(t, a, "fixture", []string{"/3/tv/123/season/1"}, func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"id":9,"season_number":1,"name":"Season","overview":"Description","episodes":[]}`
		if calls == 1 {
			body = `{"id":9,"season_number":2,"name":"Season","overview":"Description","episodes":[]}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	_, first := a.nativeSeason(context.Background(), 123, 1)
	_, second := a.nativeSeason(context.Background(), 123, 1)
	t.Logf("first=%v second=%v provider_calls=%d", first, second, calls)
	if first == nil || second != nil || calls != 2 {
		t.Fatal("invalid response prevented a fresh retry")
	}
}
func TestReviewStaleRawSeasonRenewedAsFreshProjection(t *testing.T) {
	a := newTestApp(t)
	calls := 0
	installMetadataFixture(t, a, "fixture", []string{"/3/tv/123/season/1"}, func(r *http.Request) (*http.Response, error) { calls++; return nil, context.DeadlineExceeded })
	old := time.Now().Add(-25 * time.Hour)
	raw := json.RawMessage(`{"id":9,"season_number":1,"name":"Season","overview":"Description","episodes":[]}`)
	if err := a.Store.Put(t.Context(), "cache", "source-season:123:1:", map[string]any{"at": old, "data": raw}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := a.nativeSeason(t.Context(), 123, 1); err == nil {
			t.Fatal("unvalidated stale raw was accepted")
		}
	}
	var saved struct {
		At time.Time `json:"at"`
	}
	if err := a.Store.Get(t.Context(), "cache", "season:123:1", &saved); err == nil {
		t.Fatal("stale raw renewed a validated cache entry")
	}
	if calls != 2 {
		t.Fatalf("provider attempts %d", calls)
	}
}
