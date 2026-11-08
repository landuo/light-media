package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCatalogEpisodeGroupKeepsCanonicalNumbersAndChecksOwnership(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	fixtures := map[string]string{
		"/3/tv/120":                           `{"id":120,"name":"合并季样本","original_name":"Merged season fixture","seasons":[{"season_number":1,"episode_count":25},{"season_number":4,"episode_count":25}],"episode_groups":{"results":[{"id":"merged"},{"id":"invalid-episode"}]}}`,
		"/3/tv/121":                           `{"id":121,"name":"另一部剧集","original_name":"Other series","episode_groups":{"results":[{"id":"another-group"}]}}`,
		"/3/tv/episode_group/merged":          `{"id":"merged","groups":[{"order":4,"name":"篇章","episodes":[{"order":0,"season_number":4,"episode_number":16,"name":"原第16集","still_path":"/safe.jpg"},{"order":1,"season_number":0,"episode_number":2,"still_path":"https://example.invalid/unsafe.jpg"},{"order":2,"season_number":1,"episode_number":25}]},{"order":2,"episodes":[]}]}`,
		"/3/tv/episode_group/invalid-episode": `{"id":"invalid-episode","groups":[{"order":1,"episodes":[{"order":0,"episode_number":2}]}]}`,
	}
	var paths []string
	for path := range fixtures {
		paths = append(paths, path)
	}
	groupCalls := 0
	installMetadataFixture(t, a, "owned-catalog-episode-groups", paths, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/episode_group/") {
			groupCalls++
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fixtures[r.URL.Path]))}, nil
	})
	handler := a.Handler()
	response := callTest(t, handler, http.MethodGet, "/api/catalog/episode-group?id=120&group=merged", "", cookie, "")
	var payload struct{ Data json.RawMessage }
	want := json.RawMessage(`{"groups":[{"order":4,"name":"篇章","episodes":[{"season_number":4,"episode_number":16,"name":"原第16集","overview":"","air_date":"","still_path":"/safe.jpg","runtime":0,"vote_average":0},{"season_number":0,"episode_number":2,"name":"","overview":"","air_date":"","still_path":"","runtime":0,"vote_average":0},{"season_number":1,"episode_number":25,"name":"","overview":"","air_date":"","still_path":"","runtime":0,"vote_average":0}]},{"order":2,"name":"","episodes":[]}]}`)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || !originalBaseJSONEqual(payload.Data, want) || groupCalls != 1 {
		t.Fatalf("group projection %d (%d provider calls): %s", response.Code, groupCalls, response.Body)
	}
	response = callTest(t, handler, http.MethodGet, "/api/catalog/episode-group?id=121&group=merged", "", cookie, "")
	if response.Code != http.StatusNotFound || groupCalls != 1 {
		t.Fatalf("a group cached for another series must stay unavailable: %d (%d provider calls)", response.Code, groupCalls)
	}
	response = callTest(t, handler, http.MethodGet, "/api/catalog/episode-group?id=120&group=invalid-episode", "", cookie, "")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("an episode with no canonical season must fail: %d", response.Code)
	}
}
