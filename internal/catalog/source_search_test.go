package catalog

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestOriginalMessageSearchYearBranchesUseActualHTTP(t *testing.T) {
	calls := []string{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		q := r.URL.Query()
		if q.Get("query") != "测试作品" || q.Get("page") != "1" || q.Has("include_adult") {
			t.Error("original query changed", q)
		}
		switch r.URL.Path {
		case "/3/search/movie":
			if q.Get("year") != "2026" || q.Has("first_air_date_year") {
				t.Error(q)
			}
			fmt.Fprint(w, `{"page":1,"total_pages":1,"total_results":2,"results":[{"id":42,"title":"测试作品 续篇","release_date":"2026-01-01"},{"id":44,"title":"另一个电影","release_date":"2026-12-01"}]}`)
		case "/3/search/tv":
			if q.Get("first_air_date_year") != "2026" || q.Has("year") {
				t.Error(q)
			}
			fmt.Fprint(w, `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":43,"name":"测试作品","first_air_date":"2026-06-01"}]}`)
		default:
			t.Error("unexpected source branch", r.URL.Path)
			w.WriteHeader(400)
		}
	})
	result, e := c.SearchSourceMedia(context.Background(), "测试作品", "未知", "2026")
	if e != nil || len(result) != 2 || result[0].ID != 43 || result[1].ID != 42 || len(calls) != 2 {
		t.Fatal(result, e, calls)
	}
}
func TestOriginalMessageSearchMultiDoesNotApplyTypedContainment(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/search/multi" {
			t.Error(r.URL.Path)
		}
		fmt.Fprint(w, `{"page":1,"total_pages":1,"total_results":2,"results":[{"id":42,"media_type":"movie","title":"别名匹配","release_date":"2026-01-01"},{"id":43,"media_type":"person","name":"演员"}]}`)
	})
	result, e := c.SearchSourceMedia(context.Background(), "测试作品", "未知", "")
	if e != nil || len(result) != 1 || result[0].Title != "别名匹配" {
		t.Fatal(result, e)
	}
}
