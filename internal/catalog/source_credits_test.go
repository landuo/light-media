package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

type sourceCreditsTransport func(*http.Request) (*http.Response, error)

func (f sourceCreditsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSourcePersonCreditsActualFixedDateCastPaging(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-catalog-modules/credits-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Page                int
			Movies, Tvs, Output []map[string]json.RawMessage
		} `json:"provider_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("fixed credits native API algorithm")
	}
	for _, row := range oracle.Cases {
		t.Run(strconv.Itoa(row.Page), func(t *testing.T) {
			calls := []string{}
			client := newClient(Credentials{APIKey: strings.Repeat("a", 32)}, sourceCreditsTransport(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				rows := row.Movies
				if strings.HasSuffix(r.URL.Path, "tv_credits") {
					rows = row.Tvs
				}
				body, _ := json.Marshal(map[string]any{"cast": rows, "crew": []any{map[string]any{"id": 999}}})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
			}), 0)
			got, e := client.SourcePersonCredits(context.Background(), 120, row.Page)
			if e != nil || len(got.Results) != len(row.Output) {
				t.Fatal("actual source native page count", e, len(got.Results), len(row.Output))
			}
			for i, want := range row.Output {
				var title string
				if json.Unmarshal(want["title"], &title) != nil {
					_ = json.Unmarshal(want["name"], &title)
				}
				if got.Results[i].Title != title {
					t.Fatal("source cast order/no crew/no dedupe/24 slice", i, got.Results[i].Title, title)
				}
			}
			if len(calls) != 2 || !strings.HasSuffix(calls[0], "/movie_credits") || !strings.HasSuffix(calls[1], "/tv_credits") {
				t.Fatal("source native provider sequence", calls)
			}
		})
	}
}
