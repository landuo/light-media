package fnos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

var syntheticDetailTitles = []string{"夜王", "生活大爆炸", "诛仙"}

// All values and responses below are invented synthetic protocol fixtures.
func syntheticDetailServer(t *testing.T, searchOverride func(string, []map[string]any) []map[string]any, detailOverride func(string, map[string]any), libraries int) (*httptest.Server, *int) {
	t.Helper()
	count := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v/api/v1")
		body, _ := io.ReadAll(r.Body)
		a, _ := url.ParseQuery(r.Header.Get("authx"))
		payload := string(body)
		if p == "/search/list" {
			payload = "q=" + r.URL.Query().Get("q")
		}
		if Signature(r.URL.Path, payload, a.Get("nonce"), a.Get("timestamp")) != r.Header.Get("authx") {
			t.Error("signature does not match exact read")
		}
		var data any
		switch p {
		case "/sys/version":
			data = map[string]any{"version": "1.0-synthetic", "mediasrvVersion": "1.0-synthetic"}
		case "/login":
			data = map[string]any{"token": "synthetic-private-token"}
		case "/user/info":
			data = map[string]any{"is_admin": 1}
		case "/mdb/list":
			libs := []map[string]any{}
			for i := 0; i < libraries; i++ {
				libs = append(libs, map[string]any{"guid": fmt.Sprintf("%032x", i+16), "name": "Synthetic", "category": "Mix", "dir_list": []string{"/synthetic"}})
			}
			data = libs
		case "/item/list":
			data = map[string]any{"list": []map[string]any{{"type": "TV", "title": "Synthetic"}}}
		case "/search/list":
			title := r.URL.Query().Get("q")
			guid := syntheticDetailGUID(title)
			rows := []map[string]any{{"guid": guid, "type": detailTypes[title], "title": title, "trim_id": "synthetic-provider-id"}}
			if searchOverride != nil {
				rows = searchOverride(title, rows)
			}
			data = rows
		default:
			if !strings.HasPrefix(p, "/item/") {
				t.Errorf("unexpected endpoint %s", p)
				http.Error(w, "unexpected", 400)
				return
			}
			count++
			if r.Method != http.MethodGet || len(body) != 0 || r.Header.Get("Authorization") != "synthetic-private-token" {
				t.Error("detail is not an authenticated bodyless GET")
			}
			guid := strings.TrimPrefix(p, "/item/")
			title := ""
			for _, q := range syntheticDetailTitles {
				if syntheticDetailGUID(q) == guid {
					title = q
				}
			}
			row := map[string]any{"guid": guid, "ancestor_guid": fmt.Sprintf("%032x", 16), "parent_guid": "", "type": detailTypes[title], "title": title, "trim_id": "synthetic-provider-id", "release_date": "2024-01-02", "season_number": 0}
			if detailOverride != nil {
				detailOverride(title, row)
			}
			data = row
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	return s, &count
}

func syntheticDetailGUID(title string) string {
	for i, q := range syntheticDetailTitles {
		if title == q {
			return fmt.Sprintf("%032x", i+1)
		}
	}
	return ""
}

func detailConfig(s *httptest.Server) Config {
	return Config{Host: s.URL, Username: "synthetic-user", Password: "synthetic-password", SampleTitles: syntheticDetailTitles, CaptureSampleDetails: true}
}

func TestOptionalDetailsObservedOnlyForExactScope(t *testing.T) {
	s, count := syntheticDetailServer(t, nil, nil, 1)
	defer s.Close()
	r := Probe(context.Background(), detailConfig(s))
	if r.Status != "passed" || r.Requests != 11 || *count != 3 || len(r.SearchSamples) != 3 {
		t.Fatalf("unexpected baseline: %+v, calls=%d", r, *count)
	}
	for _, search := range r.SearchSamples {
		d := search.Details
		if d == nil || d.Status != "observed" || d.ParentGUID == nil || *d.ParentGUID != "" || d.TrimID == nil || d.Type != detailTypes[search.Query] || d.Title != search.Query || d.Dates["release_date"] != "2024-01-02" {
			t.Fatalf("scope not preserved: %+v", d)
		}
		if d.Year != nil || d.Episode != nil || d.Season == nil || *d.Season != 0 || len(d.MissingFields) != 5 {
			t.Fatalf("missing optional fields inferred or lost: %+v", d)
		}
	}
}

func TestDefaultNeverRequestsDetails(t *testing.T) {
	// The default previously ignored guid, including unknown GUID shapes.
	s, count := syntheticDetailServer(t, func(_ string, rows []map[string]any) []map[string]any {
		rows[0]["guid"] = map[string]string{"unknown": "synthetic-private-token"}
		return rows
	}, nil, 1)
	defer s.Close()
	cfg := detailConfig(s)
	cfg.CaptureSampleDetails = false
	r := Probe(context.Background(), cfg)
	if r.Status != "passed" || r.Requests != 8 || *count != 0 {
		t.Fatalf("default gained new requests: %+v", r)
	}
	for _, search := range r.SearchSamples {
		if search.Details != nil {
			t.Fatal("default emitted details")
		}
	}
}

func TestDetailCoreMismatchAndMissingNeverObserved(t *testing.T) {
	cases := map[string]func(map[string]any){
		"guid-mismatch":    func(d map[string]any) { d["guid"] = strings.Repeat("f", 32) },
		"external-library": func(d map[string]any) { d["ancestor_guid"] = strings.Repeat("e", 32) },
		"invalid-ancestor": func(d map[string]any) { d["ancestor_guid"] = "../other" },
		"non-root-parent":  func(d map[string]any) { d["parent_guid"] = strings.Repeat("a", 32) },
		"null-parent":      func(d map[string]any) { d["parent_guid"] = nil },
		"missing-parent":   func(d map[string]any) { delete(d, "parent_guid") },
		"wrong-type":       func(d map[string]any) { d["type"] = "Episode" },
		"wrong-title":      func(d map[string]any) { d["title"] = "另一个作品" },
		"missing-trim":     func(d map[string]any) { delete(d, "trim_id") },
		"missing-guid":     func(d map[string]any) { delete(d, "guid") },
		"missing-ancestor": func(d map[string]any) { delete(d, "ancestor_guid") },
		"missing-type":     func(d map[string]any) { delete(d, "type") },
		"missing-title":    func(d map[string]any) { delete(d, "title") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := syntheticDetailServer(t, nil, func(_ string, d map[string]any) { mutate(d) }, 1)
			defer s.Close()
			r := Probe(context.Background(), detailConfig(s))
			if r.Status != "unverified" || r.Code != "SAMPLE_DETAILS_UNVERIFIED" {
				t.Fatalf("invalid baseline passed: %+v", r)
			}
			for _, search := range r.SearchSamples {
				if d := search.Details; d == nil || d.Status != "unverified" || d.Code == "" || d.ParentGUID != nil {
					t.Fatalf("invalid parent/scope fabricated: %+v", d)
				}
			}
		})
	}
}

func TestInvalidOrAmbiguousCandidatesNeverGetDetails(t *testing.T) {
	cases := map[string]func([]map[string]any) []map[string]any{
		"duplicate":     func(rows []map[string]any) []map[string]any { return append(rows, rows[0]) },
		"wrong-type":    func(rows []map[string]any) []map[string]any { rows[0]["type"] = "Episode"; return rows },
		"partial-title": func(rows []map[string]any) []map[string]any { rows[0]["title"] = "夜王 2"; return rows },
		"truncated": func(rows []map[string]any) []map[string]any {
			return []map[string]any{rows[0], rows[0], rows[0], rows[0]}
		},
		"path-guid":    func(rows []map[string]any) []map[string]any { rows[0]["guid"] = "../sys/version"; return rows },
		"missing-guid": func(rows []map[string]any) []map[string]any { delete(rows[0], "guid"); return rows },
		"empty":        func(rows []map[string]any) []map[string]any { return []map[string]any{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, count := syntheticDetailServer(t, func(_ string, rows []map[string]any) []map[string]any { return mutate(rows) }, nil, 1)
			defer s.Close()
			r := Probe(context.Background(), detailConfig(s))
			if r.Status != "unverified" || *count != 0 || r.Requests != 8 {
				t.Fatalf("ambiguous/invalid candidate fetched: %+v", r)
			}
		})
	}
}

func TestOptionalFieldShapeAndRedaction(t *testing.T) {
	s, _ := syntheticDetailServer(t, nil, func(_ string, d map[string]any) {
		d["trim_id"] = "synthetic-user synthetic-password synthetic-private-token https://example.invalid/private?token=secret"
		d["year"] = "2024"
		d["episode_number"] = 0
		d["unknown_secret"] = "must-not-emit-this-field"
	}, 1)
	r := Probe(context.Background(), detailConfig(s))
	s.Close()
	if r.Status != "passed" || r.SearchSamples[0].Details.Year == nil || *r.SearchSamples[0].Details.Year != 2024 {
		t.Fatalf("explicit year not captured: %+v", r)
	}
	b, _ := json.Marshal(r)
	for _, bad := range []string{"synthetic-user", "synthetic-password", "synthetic-private-token", "https://", "must-not-emit-this-field"} {
		if strings.Contains(string(b), bad) {
			t.Fatal("detail field leaked known secret, URL or unknown data")
		}
	}
	for _, tc := range []struct {
		key   string
		value any
	}{{"release_date", "2024-02-30"}, {"release_date", 2024}, {"year", "secret"}, {"year", 0}, {"season_number", -1}, {"episode_number", "unknown"}} {
		t.Run(tc.key+fmt.Sprint(tc.value), func(t *testing.T) {
			s, _ := syntheticDetailServer(t, nil, func(_ string, d map[string]any) { d[tc.key] = tc.value }, 1)
			defer s.Close()
			r := Probe(context.Background(), detailConfig(s))
			if r.Status != "unverified" || r.SearchSamples[0].Details.Code != "INVALID_OPTIONAL_FIELD" {
				t.Fatalf("malformed optional data passed: %+v", r)
			}
		})
	}
}

func TestDetailsConfigAndMaximumRequestBudget(t *testing.T) {
	for _, titles := range [][]string{nil, {"夜王"}, {"夜王", "诛仙", "诛仙"}, {"夜王", "诛仙", "other"}} {
		r := Probe(context.Background(), Config{Host: "https://example.invalid", SampleTitles: titles, CaptureSampleDetails: true})
		if r.Code != "FIXED_DETAIL_SAMPLES_REQUIRED" || r.Requests != 0 {
			t.Fatalf("invalid fixed scope attempted network: %+v", r)
		}
	}
	s, count := syntheticDetailServer(t, nil, nil, 9)
	defer s.Close()
	cfg := detailConfig(s)
	cfg.SampleTitles = []string{"诛仙", "夜王", "生活大爆炸"}
	r := Probe(context.Background(), cfg)
	if r.Status != "passed" || r.Requests != 18 || *count != 3 || len(r.Libraries) != 8 || !r.Truncated {
		t.Fatalf("scope/request bounds differ: %+v", r)
	}
}
