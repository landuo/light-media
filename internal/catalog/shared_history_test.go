package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSharedHistoryDetailPreservesTVDBWithoutAnotherRequest(t *testing.T) {
	for _, input := range []any{nil, int64(0), int64(456)} {
		t.Run(stringJSON(input), func(t *testing.T) {
			body := categoryDetailFixture("tv")
			body["external_ids"] = map[string]any{"tvdb_id": input}
			hits := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.URL.Query().Get("append_to_response") != "credits,recommendations,external_ids,alternative_titles" {
					t.Fatal("changed native request")
				}
				_ = json.NewEncoder(w).Encode(body)
			})
			got, err := client.Detail(context.Background(), "tv", 123)
			if err != nil {
				t.Fatal(err)
			}
			if hits != 1 {
				t.Fatal("extra request", hits)
			}
			if input == nil {
				if got.TVDBID != nil {
					t.Fatal("fabricated null TVDB ID")
				}
			} else if got.TVDBID == nil || *got.TVDBID != input.(int64) {
				t.Fatal("TVDB ID lost", got.TVDBID)
			}
			raw, _ := json.Marshal(got)
			var cold Media
			if err = json.Unmarshal(raw, &cold); err != nil {
				t.Fatal(err)
			}
			if stringJSON(cold.TVDBID) != stringJSON(got.TVDBID) {
				t.Fatal("cache round trip lost ID")
			}
		})
	}
}
func stringJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
