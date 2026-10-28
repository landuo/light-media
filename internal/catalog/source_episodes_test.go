package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestSourceEpisodeGroupActualPinnedRenumbering(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-catalog-modules/episodes-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Season        int
			Input, Output json.RawMessage
		} `json:"group_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 3 {
		t.Fatal("source group method oracle")
	}
	for _, row := range oracle.Cases {
		calls := 0
		c := NewWithTransport(Credentials{APIKey: "owned"}, sourceCreditsTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/tv/episode_group/owned-group" {
				t.Fatal("existing provider boundary", r.URL.Path)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"groups":` + string(row.Input) + `}`))}, nil
		}))
		got, e := c.SourceEpisodeRows(context.Background(), 120, row.Season, "owned-group")
		if e != nil || calls != 1 {
			t.Fatal("native group request", e, calls)
		}
		var expected struct{ Episodes json.RawMessage }
		_ = json.Unmarshal(row.Output, &expected)
		if len(expected.Episodes) == 0 {
			expected.Episodes = json.RawMessage(`[]`)
		}
		var a, b any
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal(expected.Episodes, &b)
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if string(aa) != string(bb) {
			t.Fatal("fixed group order/reassignment", string(got), string(expected.Episodes))
		}
	}
}
