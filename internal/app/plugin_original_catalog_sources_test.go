package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalCatalogSourcesFixedEndpointModelOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/source-events-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Kind, Name, State string
			EventType         string `json:"event_type"`
			Deliver           bool
			Mutation, Output  json.RawMessage
			EventInput        json.RawMessage `json:"event_input"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 14 {
		t.Fatal("fixed source endpoint/model oracle")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Kind+"/"+row.Name, func(t *testing.T) {
			a := newTestApp(t)
			count := 0
			if row.Deliver {
				a.registerOriginalChainHandler(row.EventType, originalMutableEventHandler{ID: "owned-source", Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
					count++
					if !originalBaseJSONEqual(event.Data, row.EventInput) {
						t.Fatal("source initial event context differs", string(event.Data), string(row.EventInput))
					}
					if string(row.Mutation) != "null" {
						data, _ := json.Marshal(map[string]json.RawMessage{"extra_sources": row.Mutation})
						event.Data = data
					}
					return nil
				}})
			}
			got, err := a.originalCatalogSources(context.Background(), row.Kind)
			if row.State == "response-invalid" {
				if err == nil {
					t.Fatal("source response model rejects this mutation")
				}
				return
			}
			if err != nil {
				t.Fatal("source accepts source list", err)
			}
			out, _ := json.Marshal(got)
			if !originalBaseJSONEqual(out, row.Output) {
				t.Fatal("source output/default fields differ", string(out), string(row.Output))
			}
			if row.Deliver && count != 1 {
				t.Fatal("source event invoked more than once")
			}
		})
	}
}
func TestOriginalCatalogSourcesHTTPAuthAliasAndPreferenceNoImplicitEvent(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	count := 0
	source := json.RawMessage(`{"extra_sources":[{"name":"Owned source","api_path":"plugin/Owned/feed?list=normal","type":"电影"}]}`)
	a.registerOriginalChainHandler("recommend.source", originalMutableEventHandler{ID: "owned-recommend", Invoke: func(_ context.Context, event *plugins.MutableEvent) error { count++; event.Data = source; return nil }})
	for _, path := range []string{"/api/v1/recommend/source", "/api/v2/recommend/source", "/api/recommend/source"} {
		res := callTest(t, a.Handler(), http.MethodGet, path, "", nil, "")
		if res.Code != 401 {
			t.Fatal("source alias bypassed session auth")
		}
		res = callTest(t, a.Handler(), http.MethodGet, path, "", cookie, "")
		var rows []map[string]any
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0]["name"] != "Owned source" {
			t.Fatal("source alias changed raw wire", res.Code, res.Body.String())
		}
	}
	native := callTest(t, a.Handler(), http.MethodGet, "/api/catalog/recommend/sources", "", cookie, "")
	var v struct {
		Data struct {
			Sources []any
			Extra   []any `json:"extra_sources"`
		}
	}
	if native.Code != 200 || json.Unmarshal(native.Body.Bytes(), &v) != nil || len(v.Data.Sources) != 15 || len(v.Data.Extra) != 1 || count != 4 {
		t.Fatal("native source extras/event count", count, native.Body.String())
	}
	saved := callTest(t, a.Handler(), "PUT", "/api/catalog/recommend/preferences", `{"enabled":{"plugin/Owned/feed?list=normal":false,"tmdb_movies":false}}`, cookie, "")
	if saved.Code != 200 {
		t.Fatal("extra path preference failed", saved.Code, saved.Body.String())
	}
	loaded := callTest(t, a.Handler(), "GET", "/api/catalog/recommend/preferences", "", cookie, "")
	if loaded.Code != 200 || !json.Valid(loaded.Body.Bytes()) || count != 4 {
		t.Fatal("preference save/read broadcast source event or lost keys", count)
	}
	var config struct{ Data RecommendPreferences }
	_ = json.Unmarshal(loaded.Body.Bytes(), &config)
	if config.Data.Enabled["plugin/Owned/feed?list=normal"] || config.Data.Enabled["tmdb_movies"] {
		t.Fatal("extra path/builtin preference not retained")
	}
}
