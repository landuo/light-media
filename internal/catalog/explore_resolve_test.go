package catalog

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestExploreResolveOnlyUniqueTitleYearAndType(t *testing.T) {
	for _, test := range []struct {
		name, body string
		year       int
		want       bool
	}{
		{"exact", `{"page":1,"total_pages":1,"results":[{"id":10,"title":"测试","release_date":"2025-01-01"}]}`, 2025, true},
		{"ambiguous", `{"page":1,"total_pages":1,"results":[{"id":10,"title":"测试","release_date":"2025-01-01"},{"id":11,"title":"测试","release_date":"2025-06-01"}]}`, 2025, false},
		{"wrong_year", `{"page":1,"total_pages":1,"results":[{"id":10,"title":"测试","release_date":"2024-01-01"}]}`, 2025, false},
		{"unknown_year", `{"page":1,"total_pages":1,"results":[{"id":10,"title":"测试","release_date":"2025-01-01"}]}`, 0, false},
		{"substring_is_not_identity", `{"page":1,"total_pages":1,"results":[{"id":10,"title":"测试 第二部","release_date":"2025-01-01"}]}`, 2025, false},
		{"incomplete_candidates", `{"page":1,"total_pages":2,"results":[{"id":10,"title":"测试","release_date":"2025-01-01"}]}`, 2025, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/3/search/movie" || r.URL.Query().Get("query") != "测试" {
					t.Fatal("unexpected resolve request")
				}
				fmt.Fprint(w, test.body)
			})
			resolved, err := client.ResolveExploreMedia(context.Background(), ExploreMedia{Media: Media{Title: "测试", Year: test.year, MediaType: "movie"}, Source: "douban", SourceID: "1292052"})
			if err != nil || (resolved.Media != nil) != test.want {
				t.Fatalf("resolved=%+v err=%v", resolved, err)
			}
			if test.want && resolved.Media.ID != 10 {
				t.Fatal("source ID became TMDB ID")
			}
			if len(resolved.Candidates) == 0 {
				t.Fatal("no choices for ambiguous identity")
			}
		})
	}
}

func TestExploreResolveIMDbUsesExactExternalNamespace(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/find/tt1234567" || r.URL.Query().Get("external_source") != "imdb_id" {
			t.Fatal("incorrect external identity lookup")
		}
		fmt.Fprint(w, `{"movie_results":[{"id":12,"title":"电影同名"}],"tv_results":[{"id":13,"name":"剧集"}]}`)
	})
	resolved, err := client.ResolveExploreMedia(context.Background(), ExploreMedia{Media: Media{Title: "源剧集", MediaType: "tv", IMDbID: "tt1234567"}, Source: "douban", SourceID: "42"})
	if err != nil || resolved.Media == nil || resolved.Media.ID != 13 || resolved.Media.MediaType != "tv" || resolved.MatchMethod != "imdb" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}
