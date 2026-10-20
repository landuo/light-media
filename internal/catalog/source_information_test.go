package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type sourceInfoTransport func(*http.Request) (*http.Response, error)

func (f sourceInfoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSourceInformationUsesOriginalAppendListAndFullResponse(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			raw := `{"id":42,"original_language":"zh","origin_country":["CN"],"genres":[{"id":16,"name":"动画"}],"credits":{"cast":[],"crew":[]},"external_ids":{"imdb_id":"tt1234567"},"alternative_titles":{"titles":[],"results":[]},"translations":{"translations":[]},"title":"电影","original_title":"Original","release_date":"2026-01-01","name":"剧集","original_name":"Series","first_air_date":"2026-01-01","seasons":[{"season_number":0,"episode_count":1}],"episode_groups":{"results":[{"id":"source-group"}]},"source_only_field":{"beyond":"compact DTO"}}`
			c := NewWithTransport(Credentials{APIKey: "owned"}, sourceInfoTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				want := "images,credits,alternative_titles,translations,release_dates,external_ids"
				if kind == "tv" {
					want = "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
				}
				if r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/"+kind+"/42" || r.URL.Query().Get("api_key") != "owned" || r.URL.Query().Get("append_to_response") != want || r.URL.Query().Get("language") != "zh-CN" {
					t.Fatal("source metadata was read through UI detail or an unrelated authority", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw)), Request: r}, nil
			}))
			got, media, e := c.SourceInformation(context.Background(), kind, 42, nil)
			if e != nil || calls != 1 || media.ID != 42 || media.MediaType != kind {
				t.Fatal(media, e, calls)
			}
			var full map[string]any
			if json.Unmarshal(got, &full) != nil || full["source_only_field"] == nil || full["episode_groups"] == nil {
				t.Fatal("full source response was replaced by compact catalog DTO")
			}
		})
	}
}
