package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRecommendSourcesOriginalOrderAndIndependentCopy(t *testing.T) {
	sources := RecommendSources()
	want := []string{"tmdb_trending", "douban_showing", "bangumi_calendar", "anilist_trending", "anilist_popular_this_season", "tmdb_movies", "tmdb_tvs", "douban_movie_hot", "douban_tv_hot", "douban_tv_animation", "douban_movies", "douban_tvs", "douban_movie_top250", "douban_tv_weekly_chinese", "douban_tv_weekly_global"}
	got := []string{}
	for _, s := range sources {
		got = append(got, s.ID)
		if s.Title == "" || !strings.Contains("|movie|tv|anime|rankings|", "|"+s.Category+"|") {
			t.Fatalf("missing display metadata: %+v", s)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source order: %v", got)
	}
	sources[0].ID = "changed"
	if RecommendSources()[0].ID != want[0] {
		t.Fatal("caller modified source whitelist")
	}
}

func TestRecommendRejectsUnknownSourcesAndInvalidPagesBeforeRequest(t *testing.T) {
	e := exploreTestClient(t, func(*http.Request) string {
		t.Fatal("invalid input made an upstream request")
		return ""
	})
	for _, test := range []struct {
		id   string
		page int
	}{{"", 1}, {"unknown", 1}, {"douban_showing/../movie", 1}, {"https://example.com", 1}, {"douban_showing", 0}, {"bangumi_calendar", -1}, {"anilist_trending", 501}, {"tmdb_movies", 501}} {
		_, err := e.Recommend(context.Background(), test.id, test.page)
		var typed *ExploreError
		if !errors.As(err, &typed) || typed.Code != "invalid_input" {
			t.Fatalf("accepted %+v: %v", test, err)
		}
	}
	if _, err := e.Recommend(nil, "douban_showing", 1); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestRecommendTMDBOriginalRoutesLanguageUnionAndIdentity(t *testing.T) {
	for _, test := range []struct{ id, path, kind, title, language string }{
		{"tmdb_trending", "/3/trending/all/week", "movie", "title", ""},
		{"tmdb_movies", "/3/discover/movie", "movie", "title", ""},
		{"tmdb_tvs", "/3/discover/tv", "tv", "name", "zh|en|ja|ko"},
	} {
		t.Run(test.id, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != test.path || q.Get("page") != "2" || q.Get("with_original_language") != test.language {
					t.Fatalf("wrong recommendation request: %s", r.URL)
				}
				if strings.HasPrefix(test.path, "/3/discover/") && (q.Get("sort_by") != "popularity.desc" || q.Get("vote_count.gte") != "0" || q.Get("vote_average.gte") != "0") {
					t.Fatal("wrong original discover defaults")
				}
				fmt.Fprintf(w, `{"page":2,"total_pages":3,"total_results":45,"results":[{"id":101,"media_type":%q,%q:"测试"}]}`, test.kind, test.title)
			})
			p, err := NewExplorer(client, nil).Recommend(context.Background(), test.id, 2)
			if err != nil || p.Page != 2 || len(p.Results) != 1 || !p.HasMore {
				t.Fatalf("result %+v: %v", p, err)
			}
			m := p.Results[0]
			if m.Source != "themoviedb" || m.ID != 101 || m.SourceID != "101" || m.MediaType != test.kind {
				t.Fatalf("incorrect TMDB identity: %+v", m)
			}
		})
	}
}

func TestRecommendDoubanNativeCollectionRoutingAndPagination(t *testing.T) {
	for _, test := range []struct{ id, collection, kind string }{
		{"douban_showing", "movie_showing", "movie"},
		{"douban_movie_hot", "movie_hot_gaia", "movie"},
		{"douban_tv_hot", "tv_hot", "tv"},
		{"douban_tv_animation", "tv_animation", "tv"},
		{"douban_movie_top250", "movie_top250", "movie"},
		{"douban_tv_weekly_chinese", "tv_chinese_best_weekly", "tv"},
		{"douban_tv_weekly_global", "tv_global_best_weekly", "tv"},
	} {
		t.Run(test.id, func(t *testing.T) {
			e := exploreTestClient(t, func(r *http.Request) string {
				q := r.URL.Query()
				if r.URL.Host != "frodo.douban.com" || r.URL.Path != "/api/v2/subject_collection/"+test.collection+"/items" || q.Get("start") != "30" || q.Get("count") != "30" || q.Get("_sig") == "" {
					t.Fatalf("wrong native collection request: %s", r.URL.Path)
				}
				return fmt.Sprintf(`{"start":30,"total":31,"subject_collection_items":[{"id":"101","type":%q,"title":"原生榜单","year":"2026","cover":{"large":{"url":"https://img3.doubanio.com/view/photo/l/public/p1.jpg"}}}]}`, test.kind)
			})
			p, err := e.Recommend(context.Background(), test.id, 2)
			if err != nil || p.Page != 2 || p.HasMore || p.TotalPages != 2 || p.TotalResults != 31 || len(p.Results) != 1 {
				t.Fatalf("collection page %+v: %v", p, err)
			}
			m := p.Results[0]
			if m.Source != "douban" || m.ID != 0 || m.SourceID != "101" || m.MediaType != test.kind || m.PosterPath == "" {
				t.Fatalf("native ID or collection poster lost: %+v", m)
			}
		})
	}
}

func TestRecommendDoubanLatestUsesNativeReleaseSort(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		e := exploreTestClient(t, func(r *http.Request) string {
			q := r.URL.Query()
			if r.URL.Host != "frodo.douban.com" || r.URL.Path != "/api/v2/"+kind+"/recommend" || q.Get("sort") != "R" || q.Get("start") != "30" || q.Get("count") != "30" || q.Get("tags") != "" {
				t.Fatal("latest list does not match original native discover request")
			}
			return `{"start":30,"total":30,"items":[]}`
		})
		p, err := e.Recommend(context.Background(), "douban_"+kind+"s", 2)
		if err != nil || p.HasMore || len(p.Results) != 0 {
			t.Fatalf("empty final page %+v: %v", p, err)
		}
	}
}

func TestRecommendBangumiCalendarFlattensPagesAndKeepsNativeIdentity(t *testing.T) {
	items := []string{}
	for i := 1; i <= 31; i++ {
		items = append(items, fmt.Sprintf(`{"id":%d,"type":2,"name":"Native","name_cn":"每日放送","air_date":"2026-10-01","images":{"large":"http://lain.bgm.tv/pic/cover/l/a.jpg"}}`, i))
	}
	e := exploreTestClient(t, func(r *http.Request) string {
		if r.URL.Host != "api.bgm.tv" || r.URL.Path != "/calendar" || r.URL.Query().Get("_ts") == "" {
			t.Fatal("calendar was replaced with Bangumi discovery")
		}
		return `[{"weekday":{"id":1},"items":[` + strings.Join(items[:20], ",") + `]},{"weekday":{"id":2},"items":[` + strings.Join(items[20:], ",") + `]}]`
	})
	for _, test := range []struct {
		page, count int
		more        bool
	}{{1, 30, true}, {2, 1, false}, {3, 0, false}} {
		p, err := e.Recommend(context.Background(), "bangumi_calendar", test.page)
		if err != nil || len(p.Results) != test.count || p.HasMore != test.more || p.TotalResults != 31 || p.TotalPages != 2 {
			t.Fatalf("calendar page %+v: %v", p, err)
		}
		for i, m := range p.Results {
			if m.ID != 0 || m.Source != "bangumi" || m.SourceID != fmt.Sprint((test.page-1)*30+i+1) || m.ReleaseDate != "2026-10-01" || m.PosterPath != "https://lain.bgm.tv/pic/cover/l/a.jpg" {
				t.Fatalf("invalid native calendar media: %+v", m)
			}
		}
	}
}

func TestRecommendAniListNativeFeedAndSeasonBoundaries(t *testing.T) {
	for month := time.January; month <= time.December; month++ {
		q := recommendAniListQuery("anilist_popular_this_season", 2, time.Date(2026, month, 1, 0, 0, 0, 0, time.UTC))
		if q.Params.Get("season") != []string{"WINTER", "SPRING", "SUMMER", "FALL"}[(month-1)/3] || q.Params.Get("seasonYear") != "2026" || q.Params.Get("sort") != "POPULARITY_DESC" || q.Count != 20 || q.Page != 2 {
			t.Fatalf("incorrect season mapping %+v", q)
		}
	}
	for _, id := range []string{"anilist_trending", "anilist_popular_this_season"} {
		t.Run(id, func(t *testing.T) {
			resetAniListTestCache(t)
			aniListCache.Lock()
			aniListCache.Expires = time.Now().Add(time.Hour)
			aniListCache.ProxyDisabledTill = time.Now().Add(time.Hour)
			aniListCache.Unlock()
			e := exploreTestClient(t, func(r *http.Request) string {
				if r.URL.Host != "graphql.anilist.co" || r.Method != http.MethodPost {
					t.Fatal("AniList feed changed provider")
				}
				var body struct {
					Variables map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				sort := "TRENDING_DESC"
				if id == "anilist_popular_this_season" {
					sort = "POPULARITY_DESC"
					if body.Variables["seasonYear"] == nil || body.Variables["season"] == nil {
						t.Fatal("seasonal feed omitted season/year")
					}
				} else if body.Variables["seasonYear"] != nil || body.Variables["season"] != nil {
					t.Fatal("trending feed restricted to one season")
				}
				if body.Variables["page"] != float64(2) || body.Variables["count"] != float64(20) || !reflect.DeepEqual(body.Variables["sort"], []any{sort}) {
					t.Fatalf("wrong AniList pagination/sort: %+v", body.Variables)
				}
				return `{"data":{"Page":{"pageInfo":{"currentPage":2,"perPage":20,"lastPage":2,"total":21,"hasNextPage":false},"media":[{"id":101,"title":{"native":"Native"},"format":"TV"}]}}}`
			})
			p, err := e.Recommend(context.Background(), id, 2)
			if err != nil || p.HasMore || len(p.Results) != 1 || p.Results[0].ID != 0 || p.Results[0].SourceID != "101" || p.Results[0].Source != "anilist" {
				t.Fatalf("AniList result %+v: %v", p, err)
			}
		})
	}
}

func TestRecommendAniListSeasonUsesShanghaiDay(t *testing.T) {
	for _, test := range []struct{ utc, season, year string }{
		{"2026-03-31T15:59:59Z", "WINTER", "2026"},
		{"2026-03-31T16:00:00Z", "SPRING", "2026"},
		{"2026-06-30T16:00:00Z", "SUMMER", "2026"},
		{"2026-09-30T16:00:00Z", "FALL", "2026"},
		{"2026-12-31T15:59:59Z", "FALL", "2026"},
		{"2026-12-31T16:00:00Z", "WINTER", "2027"},
	} {
		now, err := time.Parse(time.RFC3339, test.utc)
		if err != nil {
			t.Fatal(err)
		}
		q := recommendAniListQuery("anilist_popular_this_season", 1, now)
		if q.Params.Get("season") != test.season || q.Params.Get("seasonYear") != test.year {
			t.Fatalf("UTC %s has wrong Shanghai season: %+v", test.utc, q.Params)
		}
	}
}

func TestRecommendNativeBusinessErrorsNeverBecomeEmptySuccess(t *testing.T) {
	for _, test := range []struct{ id, body string }{
		{"douban_showing", `{"code":403,"msg":"private provider message"}`},
		{"douban_showing", `{"subject_collection_items":null}`},
		{"douban_showing", `{"subject_collection_items":[],"start":30}`},
		{"douban_showing", `{"subject_collection_items":[],"total":-1}`},
		{"douban_showing", `{"subject_collection_items":[{"id":"101","type":"tv","title":"wrong type"}]}`},
		{"douban_showing", `{"subject_collection_items":[` + strings.Repeat(`{"id":"101","title":"x"},`, 30) + `{"id":"102","title":"x"}]}`},
		{"bangumi_calendar", `{"error":"private provider message"}`},
		{"bangumi_calendar", `null`},
		{"bangumi_calendar", `[{"weekday":{"id":1},"items":[{"id":0,"type":2,"name":"bad"}]}]`},
		{"bangumi_calendar", `[{"weekday":{"id":1}}]`},
	} {
		e := exploreTestClient(t, func(*http.Request) string { return test.body })
		_, err := e.Recommend(context.Background(), test.id, 1)
		var typed *ExploreError
		if !errors.As(err, &typed) || typed.Code != "invalid_response" || strings.Contains(err.Error(), "private") {
			t.Fatalf("false success or unsafe error for %s: %v", test.id, err)
		}
	}
	e := NewExplorer(nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("private provider body")), Header: http.Header{}}, nil
	}))
	_, err := e.Recommend(context.Background(), "douban_showing", 1)
	var typed *ExploreError
	if !errors.As(err, &typed) || typed.Code != "rate_limited" || typed.Source != "douban" {
		t.Fatalf("native provider error lost: %v", err)
	}
}

func TestRecommendNativePaginationUsesRawCountAndStopsAtBound(t *testing.T) {
	items := strings.Repeat(`{"id":"101","title":"No poster"},`, 29) + `{"id":"102","title":"Last"}`
	e := exploreTestClient(t, func(*http.Request) string {
		return `{"subject_collection_items":[` + items + `]}`
	})
	for _, page := range []int{1, 500} {
		p, err := e.Recommend(context.Background(), "douban_showing", page)
		if err != nil || len(p.Results) != 30 || p.HasMore != (page == 1) || p.TotalPages != 0 {
			t.Fatalf("unknown-total pagination %+v: %v", p, err)
		}
	}
}
