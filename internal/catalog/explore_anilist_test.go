package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func resetAniListTestCache(t *testing.T) {
	t.Helper()
	reset := func() {
		aniListCache.Lock()
		aniListCache.Translations = nil
		aniListCache.Expires = time.Time{}
		aniListCache.ProxyDisabledTill = time.Time{}
		aniListCache.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func aniListTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestAniListExploreQueryOriginalFilters(t *testing.T) {
	q, err := ParseExploreQuery(url.Values{"source": {"anilist"}})
	if err != nil || q.Kind != "anime" || q.Count != 20 || q.Page != 1 || q.Params.Get("sort") != "POPULARITY_DESC" {
		t.Fatalf("defaults: %+v, %v", q, err)
	}
	q, err = ParseExploreQuery(url.Values{"source": {"anilist"}, "page": {"3"}, "count": {"50"}, "search": {"  测试 & name  "}, "genre": {"Slice of Life"}, "format": {"MOVIE"}, "season": {"FALL"}, "season_year": {"2026"}, "status": {"RELEASING"}, "country": {"JP"}, "sort": {"SCORE_DESC"}})
	if err != nil || q.Count != 50 || q.Params.Get("seasonYear") != "2026" || q.Params.Get("search") != "测试 & name" || q.Params.Get("format") != "MOVIE" {
		t.Fatalf("filters: %+v, %v", q, err)
	}
	for key, value := range map[string]string{"count": "51", "page": "0", "sort": "POPULARITY_DESC TRENDING_DESC", "format": "MANGA", "season": "autumn", "season_year": "tomorrow", "status": "INVALID", "country": "JP&country=CN"} {
		_, err = ParseExploreQuery(url.Values{"source": {"anilist"}, key: {value}})
		if err == nil {
			t.Errorf("accepted invalid %s=%q", key, value)
		}
	}
}

func TestAniListExploreFallbackTranslationPaginationAndIdentity(t *testing.T) {
	resetAniListTestCache(t)
	requests := map[string]int{}
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests[req.URL.Host]++
		if req.URL.Scheme != "https" || req.Header.Get("Authorization") != "" || req.URL.RawQuery != "" {
			t.Fatal("unexpected origin or credentials")
		}
		switch req.URL.Host {
		case "trace.moe":
			if req.Method != http.MethodPost || req.URL.Path != "/anilist/" {
				t.Fatal("wrong proxy endpoint")
			}
			return aniListTestResponse(404, `{}`), nil
		case "raw.githubusercontent.com":
			if req.Method != http.MethodGet || req.URL.Path != "/soruly/anilist-chinese/master/anilist-chinese.json" {
				t.Fatal("wrong translations endpoint")
			}
			return aniListTestResponse(200, `[{"id":151,"title":"测试动画","synonyms":["中文别名","Original"]}]`), nil
		case "graphql.anilist.co":
			var body struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"page": float64(2), "count": float64(20), "search": "名称 & x=1", "genre": "Drama", "format": "MOVIE", "season": "FALL", "seasonYear": float64(2026), "status": "FINISHED", "country": "JP", "sort": []any{"SCORE_DESC"}}
			if req.Method != http.MethodPost || req.URL.Path != "/" || !reflect.DeepEqual(body.Variables, want) || !strings.Contains(body.Query, "type: ANIME") || !strings.Contains(body.Query, "isAdult: false") {
				t.Fatalf("incorrect GraphQL request: %+v", body)
			}
			return aniListTestResponse(200, `{"data":{"Page":{"pageInfo":{"currentPage":2,"perPage":20,"lastPage":4,"total":65,"hasNextPage":true},"media":[{"id":151,"title":{"native":"Original","romaji":"Romanized","english":"English"},"format":"MOVIE","status":"FINISHED","description":"<b>Hello</b><br />World &amp; all","startDate":{"year":2026,"month":10},"averageScore":86,"popularity":1234,"duration":110,"countryOfOrigin":"JP","coverImage":{"extraLarge":"https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/b151.jpg"},"bannerImage":"https://bad.example/banner.jpg","genres":["Drama"],"synonyms":["Original"]},{"id":151,"title":{"native":"duplicate"}},{"id":152,"title":{"native":"filtered adult"},"isAdult":true}]}}}`), nil
		default:
			t.Fatalf("unexpected host %s", req.URL.Host)
			return nil, nil
		}
	})
	q, err := ParseExploreQuery(url.Values{"source": {"anilist"}, "page": {"2"}, "search": {"名称 & x=1"}, "genre": {"Drama"}, "format": {"MOVIE"}, "season": {"FALL"}, "season_year": {"2026"}, "status": {"FINISHED"}, "country": {"JP"}, "sort": {"SCORE_DESC"}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		page, err := NewExplorer(nil, transport).Explore(context.Background(), q)
		if err != nil || len(page.Results) != 1 || page.Page != 2 || page.TotalPages != 4 || page.TotalResults != 65 || !page.HasMore {
			t.Fatalf("page %+v: %v", page, err)
		}
		media := page.Results[0]
		if media.ID != 0 || media.Source != "anilist" || media.SourceID != "151" || media.Title != "测试动画" || media.OriginalTitle != "Original" || media.MediaType != "movie" || media.ReleaseDate != "2026-10" || media.Year != 2026 || media.VoteAverage != 8.6 || media.Runtime != 110 || media.OriginalLanguage != "ja" || media.Overview != "Hello\nWorld & all" || media.PosterPath == "" || media.BackdropPath != "" || len(media.Genres) != 1 || media.Genres[0].ID != 0 {
			t.Fatalf("normalized media %+v", media)
		}
		if !reflect.DeepEqual(media.Aliases, []string{"English", "Romanized", "Original", "中文别名"}) {
			t.Fatalf("aliases: %+v", media.Aliases)
		}
	}
	if !reflect.DeepEqual(requests, map[string]int{"trace.moe": 1, "graphql.anilist.co": 2, "raw.githubusercontent.com": 1}) {
		t.Fatalf("cross-instance cache did not reuse public data: %+v", requests)
	}
}

func TestAniListDetailSourceIdentityAndBusinessErrors(t *testing.T) {
	for _, test := range []struct {
		name, body, code string
	}{
		{"detail", `{"data":{"Media":{"id":1,"title":{"native":"日本語","romaji":"Romaji"},"format":"ONA","episodes":12,"seasonYear":2026,"startDate":{},"characters":{"edges":[{"node":{"name":{"native":"角色"}},"voiceActors":[{"name":{"native":"声优","full":"Voice Actor"},"image":{"large":"https://s4.anilist.co/file/actor.jpg"}}]}]}}}}`, ""},
		{"wrong identity", `{"data":{"Media":{"id":2,"title":{"native":"Wrong"}}}}`, "invalid_response"},
		{"absent", `{"data":{"Media":null}}`, "not_found"},
		{"adult", `{"data":{"Media":{"id":1,"title":{"native":"Adult"},"isAdult":true}}}`, "not_found"},
		{"graphql rate limit", `{"errors":[{"status":429,"message":"private upstream message"}],"data":null}`, "rate_limited"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetAniListTestCache(t)
			aniListCache.Lock()
			aniListCache.Expires = time.Now().Add(time.Hour)
			aniListCache.ProxyDisabledTill = time.Now().Add(time.Hour)
			aniListCache.Unlock()
			explorer := NewExplorer(nil, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "graphql.anilist.co" {
					t.Fatalf("unexpected host %s", req.URL.Host)
				}
				return aniListTestResponse(200, test.body), nil
			}))
			media, err := explorer.Detail(context.Background(), "anilist", "tv", "1")
			if test.code != "" {
				var providerErr *ExploreError
				if !errors.As(err, &providerErr) || providerErr.Source != "anilist" || providerErr.Code != test.code || strings.Contains(err.Error(), "private upstream") {
					t.Fatalf("error %v, expected %s", err, test.code)
				}
				return
			}
			if err != nil || media.ID != 0 || media.SourceID != "1" || media.MediaType != "tv" || media.Year != 2026 || media.NumberOfEpisodes != 12 || media.NumberOfSeasons != 1 || len(media.Cast) != 1 || media.Cast[0].ID != 0 || media.Cast[0].Name != "声优" || media.Cast[0].Character != "角色" {
				t.Fatalf("detail %+v: %v", media, err)
			}
		})
	}
}

func TestAniListTranslationUnavailablePreservesNativeTitle(t *testing.T) {
	resetAniListTestCache(t)
	explorer := NewExplorer(nil, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "raw.githubusercontent.com" {
			return aniListTestResponse(503, `{}`), nil
		}
		return aniListTestResponse(200, `{"data":{"Media":{"id":1,"title":{"native":"原始标题"},"format":"TV"}}}`), nil
	}))
	media, err := explorer.Detail(context.Background(), "anilist", "tv", "1")
	if err != nil || media.Title != "原始标题" {
		t.Fatalf("translation failure discarded valid detail: %+v, %v", media, err)
	}
}

func TestAniListFuzzyDates(t *testing.T) {
	for _, test := range []struct {
		year, month, day int
		want             string
	}{{2026, 0, 0, "2026"}, {2026, 10, 0, "2026-10"}, {2026, 10, 23, "2026-10-23"}, {2026, 2, 30, ""}, {0, 0, 0, ""}} {
		if got := aniListDate(test.year, test.month, test.day); got != test.want {
			t.Errorf("date %d/%d/%d = %q, want %q", test.year, test.month, test.day, got, test.want)
		}
	}
}
