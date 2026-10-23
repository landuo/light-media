package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func exploreTestClient(t *testing.T, handler func(*http.Request) string) *Explorer {
	t.Helper()
	return NewExplorer(nil, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("unsafe public metadata request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(handler(r))), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	}))
}

func mustExplore(t *testing.T, raw string) ExploreQuery {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParseExploreQuery(values)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestExploreTMDBFilterMappingAndPagination(t *testing.T) {
	for _, kind := range []string{"movies", "tvs"} {
		t.Run(kind, func(t *testing.T) {
			isTV := kind == "tvs"
			path, dateKey, title := "movie", "release_date", "title"
			if isTV {
				path, dateKey, title = "tv", "first_air_date", "name"
			}
			q := mustExplore(t, "source=themoviedb&type="+kind+"&page=2&sort_by="+dateKey+".desc&with_genres=16%7C35&with_original_language=ja&with_keywords=123,456&with_watch_providers=8&vote_average=7.5&vote_count=100&release_date=2024-01-01")
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				v := r.URL.Query()
				if r.URL.Path != "/3/discover/"+path || v.Get("page") != "2" || v.Get("sort_by") != dateKey+".desc" || v.Get("with_genres") != "16|35" || v.Get("with_original_language") != "ja" || v.Get("with_keywords") != "123,456" || v.Get("with_watch_providers") != "8" || v.Get("vote_average.gte") != "7.5" || v.Get("vote_count.gte") != "100" || v.Get(dateKey+".gte") != "2024-01-01" || v.Get("release_date") != "" {
					t.Errorf("filters mapped incorrectly: %s", v.Encode())
				}
				fmt.Fprintf(w, `{"page":2,"total_pages":3,"total_results":44,"results":[{"id":101,%q:"测试","%s":"2024-01-02"}]}`, title, dateKey)
			})
			p, err := client.Explore(context.Background(), q)
			if err != nil || len(p.Results) != 1 || !p.HasMore || p.TotalPages != 3 || p.Results[0].ID != 101 || p.Results[0].SourceID != "101" {
				t.Fatalf("page=%+v err=%v", p, err)
			}
		})
	}
}

func TestExploreRejectsInvalidFiltersBeforeRequest(t *testing.T) {
	for _, query := range []string{
		"source=unknown", "source=themoviedb&page=0", "source=douban&page=501", "source=themoviedb&vote_average=NaN", "source=themoviedb&vote_count=1.5", "source=themoviedb&vote_average=11", "source=themoviedb&type=tvs&sort_by=release_date.desc", "source=themoviedb&with_genres=1%26api_key=oops", "source=themoviedb&release_date=2024-02-30", "source=douban&sort=X", "source=douban&count=-1", "source=douban&tags=x%0Ay", "source=bangumi&type=1", "source=bangumi&cat=4", "source=bangumi&year=abc", "source=bangumi&sort=score", "source=douban&page=1&page=2",
	} {
		v, _ := url.ParseQuery(query)
		if _, err := ParseExploreQuery(v); err == nil {
			t.Errorf("accepted %s", query)
		}
	}
}

func TestExploreDoubanRawCountDeterminesPaginationAndPreservesIdentity(t *testing.T) {
	client := exploreTestClient(t, func(r *http.Request) string {
		q := r.URL.Query()
		if r.URL.Host != "frodo.douban.com" || r.URL.Path != "/api/v2/movie/recommend" || q.Get("start") != "2" || q.Get("count") != "2" || q.Get("sort") != "S" || q.Get("tags") != "喜剧,日本,2025" || q.Get("_sig") == "" || q.Get("_ts") == "" || q.Get("os_rom") != "android" {
			t.Fatal("incorrect Douban request")
		}
		return `{"start":2,"items":[{"id":"1292052","type":"movie","title":"测试电影","year":"2025","pic":{"large":"https://img3.doubanio.com/view/photo/l/public/p1.jpg"},"rating":{"value":8.2,"count":99}},{"id":"1292053","type":"movie","title":"无封面","pic":{"normal":"https://img3.doubanio.com/movie_large.jpg"}}]}`
	})
	p, err := client.Explore(context.Background(), mustExplore(t, "source=douban&type=movies&page=2&count=2&sort=S&tags="+url.QueryEscape("喜剧,日本,2025")))
	if err != nil || len(p.Results) != 1 || !p.HasMore || p.TotalPages != 0 {
		t.Fatalf("pagination=%+v err=%v", p, err)
	}
	m := p.Results[0]
	if m.ID != 0 || m.Source != "douban" || m.SourceID != "1292052" || m.Year != 2025 || m.MediaType != "movie" {
		t.Fatalf("incorrect provider identity: %+v", m)
	}
}

func TestExploreBangumiCategoryZeroAndLastPage(t *testing.T) {
	client := exploreTestClient(t, func(r *http.Request) string {
		q := r.URL.Query()
		if r.URL.Host != "api.bgm.tv" || r.URL.Path != "/v0/subjects" || q.Get("type") != "2" || q.Get("cat") != "0" || q.Get("year") != "2025" || q.Get("sort") != "date" || q.Get("offset") != "2" || q.Get("limit") != "2" {
			t.Fatal("incorrect Bangumi request")
		}
		return `{"total":3,"offset":2,"limit":2,"data":[{"id":326,"type":2,"name":"Original","name_cn":"中文名","date":"2025-01-03","platform":"剧场版","images":{"large":"https://lain.bgm.tv/pic/cover/l/a.jpg"},"rating":{"score":9.1,"total":999}}]}`
	})
	p, err := client.Explore(context.Background(), mustExplore(t, "source=bangumi&type=2&cat=0&sort=date&year=2025&page=2&count=2"))
	if err != nil || p.HasMore || p.TotalPages != 2 || p.TotalResults != 3 || len(p.Results) != 1 {
		t.Fatalf("page=%+v err=%v", p, err)
	}
	m := p.Results[0]
	if m.ID != 0 || m.SourceID != "326" || m.MediaType != "movie" || m.OriginalTitle != "Original" {
		t.Fatalf("wrong identity %+v", m)
	}
}

func TestExploreBangumiClearedSortIsDistinctFromMissingSort(t *testing.T) {
	missing := mustExplore(t, "source=bangumi")
	cleared := mustExplore(t, "source=bangumi&sort=")
	if missing.Params.Get("sort") != "rank" || !cleared.Params.Has("sort") || cleared.Params.Get("sort") != "" || missing.CacheKey() == cleared.CacheKey() {
		t.Fatal("explicit cleared sorting was replaced with the missing-parameter default")
	}
}

func TestExploreProviderErrorsAndDetailIDMismatch(t *testing.T) {
	for _, body := range []string{`{"code":403,"msg":"denied"}`, `{"items":null}`, `{"items":[{"id":"4","type":"movie","title":"X"}],"start":31}`, `<html>challenge</html>`} {
		client := exploreTestClient(t, func(*http.Request) string { return body })
		if _, err := client.Explore(context.Background(), mustExplore(t, "source=douban")); err == nil {
			t.Fatalf("false success: %s", body)
		}
	}
	client := exploreTestClient(t, func(r *http.Request) string {
		if r.URL.Path != "/api/v2/tv/123" {
			t.Fatal("wrong source detail path")
		}
		return `{"id":"124","type":"tv","title":"X"}`
	})
	if _, err := client.Detail(context.Background(), "douban", "tv", "123"); err == nil {
		t.Fatal("mismatched source detail ID accepted")
	}
}

func TestExploreImageOriginsCannotBecomeArbitraryFetches(t *testing.T) {
	for _, raw := range []string{"https://img3.doubanio.com/view/p1.jpg", "https://img1.doubanio.com/view/p2.webp"} {
		if ExploreImageURL("douban", raw) == "" {
			t.Fatal("real CDN rejected")
		}
	}
	for _, raw := range []string{"http://img3.doubanio.com/p.jpg", "https://img3.doubanio.com.evil.invalid/p.jpg", "https://img3.doubanio.com:444/p.jpg", "https://user@img3.doubanio.com/p.jpg", "https://127.0.0.1/p.jpg", "https://lain.bgm.tv/p.jpg"} {
		if ExploreImageURL("douban", raw) != "" {
			t.Errorf("unsafe image accepted: %s", raw)
		}
	}
}

// Opt-in read-only requests to public providers. Ordinary tests use protocol
// fixtures above and never claim live service acceptance from an HTTP 200.
func TestExploreLivePublicProviders(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_EXPLORE_LIVE") != "1" {
		t.Skip("live provider probe is opt-in")
	}
	client := NewExplorer(nil, http.DefaultTransport)
	defer client.CloseIdleConnections()
	for _, source := range []string{"douban", "bangumi"} {
		t.Run(source, func(t *testing.T) {
			filters := "&sort=S&tags=" + url.QueryEscape("动画,2025")
			if source == "bangumi" {
				filters = "&sort=rank&cat=1&year=2025"
			}
			q := mustExplore(t, "source="+source+"&count=2"+filters)
			p, err := client.Explore(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Results) == 0 {
				t.Fatal("provider returned no usable real records")
			}
			m, err := client.Detail(context.Background(), source, p.Results[0].MediaType, p.Results[0].SourceID)
			if err != nil {
				t.Fatal(err)
			}
			if m.ID != 0 || m.Title == "" || m.SourceID != p.Results[0].SourceID {
				t.Fatal("live source identity invalid")
			}
			second, err := client.Explore(context.Background(), mustExplore(t, "source="+source+"&count=2&page=2"+filters))
			if err != nil || len(second.Results) == 0 || second.Page != 2 {
				t.Fatalf("second page missing: %+v err=%v", second, err)
			}
			seen := map[string]bool{}
			for _, item := range p.Results {
				seen[item.SourceID] = true
			}
			for _, item := range second.Results {
				if seen[item.SourceID] {
					t.Fatal("pagination repeated first-page identities")
				}
				if item.Year != 2025 {
					t.Fatalf("year filter not applied: %+v", item)
				}
			}
			t.Logf("source=%s records=%d/%d pages=1/2 has_more=%v detail_id=%s title=%s", source, len(p.Results), len(second.Results), p.HasMore, m.SourceID, m.Title)
		})
	}
}
