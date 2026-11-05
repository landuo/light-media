package app

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalHostMediaNativeNameMatchActualSDKFixedSource(t *testing.T) {
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case         string                     `json:"case"`
			Meta         map[string]any             `json:"meta"`
			Responses    map[string]json.RawMessage `json:"responses"`
			Details      map[string]map[string]any  `json:"details"`
			Groups       []map[string]any           `json:"groups"`
			Trace        []map[string]any           `json:"trace"`
			SelectedID   *int64                     `json:"selected_id"`
			SelectedType *string                    `json:"selected_type"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/recognition-match-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 16 {
		t.Fatal("fixed source matcher oracle", err)
	}
	a, _, object := ownedHostServicesFixture(t)
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			trace := []map[string]any{}
			originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
				if r.URL.Query().Get("language") != "zh" {
					t.Error("pinned TMDB_LOCALE", r.URL.RawQuery)
				}
				if strings.HasPrefix(r.URL.Path, "/3/search/") {
					kind, name, year := strings.TrimPrefix(r.URL.Path, "/3/search/"), r.URL.Query().Get("query"), r.URL.Query().Get("year")
					if kind == "tv" {
						year = r.URL.Query().Get("first_air_date_year")
					}
					var yearValue any
					if year != "" {
						yearValue = year
					}
					trace = append(trace, map[string]any{"operation": "search", "kind": kind, "name": name, "year": yearValue})
					body, present := sample.Responses[kind+":"+name+":"+year]
					if present && string(body) == "null" {
						return 503, `{"success":false,"status_message":"owned provider error"}`
					}
					if !present {
						body = json.RawMessage(`[]`)
					}
					return 200, `{"results":` + string(body) + `}`
				}
				if r.URL.Path == "/3/tv/episode_group/owned-group" {
					trace = append(trace, map[string]any{"operation": "groups", "group": "owned-group"})
					body, _ := json.Marshal(map[string]any{"groups": sample.Groups})
					return 200, string(body)
				}
				parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/3/"), "/")
				if len(parts) != 2 {
					t.Error("unknown source recognition path", r.URL.Path)
					return 404, `{}`
				}
				kind, id := parts[0], parts[1]
				number, _ := strconv.ParseInt(id, 10, 64)
				trace = append(trace, map[string]any{"operation": "detail", "kind": kind, "id": number})
				info, present := sample.Details[kind+":"+id]
				if !present {
					return 404, `{}`
				}
				wire := map[string]any{}
				for key, value := range info {
					wire[key] = value
				}
				wire["media_type"] = kind
				if names, ok := wire["names"].([]any); ok {
					titles := []any{}
					for _, name := range names {
						titles = append(titles, map[string]any{"title": name})
					}
					key := "titles"
					if kind == "tv" {
						key = "results"
					}
					wire["alternative_titles"] = map[string]any{key: titles}
				}
				body, _ := json.Marshal(wire)
				return 200, string(body)
			})
			noCache := false
			request := plugins.MediaRecognizeRequest{Meta: sample.Meta, Cache: &noCache}
			if len(sample.Groups) > 0 {
				request.EpisodeGroup = "owned-group"
			}
			observed, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, request)
			if err != nil {
				t.Fatal("actual SDK native matcher", err)
			}
			if sample.SelectedID == nil {
				if string(observed) != "null" {
					t.Fatal("source no match", string(observed))
				}
			} else {
				var info struct {
					ID   int64  `json:"tmdb_id"`
					Type string `json:"type"`
				}
				if json.Unmarshal(observed, &info) != nil || info.ID != *sample.SelectedID || info.Type != *sample.SelectedType {
					t.Fatal("source selected identity", sample.SelectedID, sample.SelectedType, string(observed))
				}
			}
			actualTrace, _ := json.Marshal(trace)
			expectedTrace, _ := json.Marshal(sample.Trace)
			if !originalBaseJSONEqual(actualTrace, expectedTrace) {
				t.Fatal("fixed source search/type/year/alias/detail order", string(expectedTrace), string(actualTrace))
			}
		})
	}
}

func TestOriginalHostMediaNativeExplicitUnknownTypeActualSDK(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Case     string   `json:"case"`
			TV       bool     `json:"tv"`
			Movie    bool     `json:"movie"`
			Trace    []string `json:"trace"`
			Selected *string  `json:"selected_type"`
		} `json:"unknown_cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/recognition-match-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("source UNKNOWN direct get_info oracle", err)
	}
	a, _, object := ownedHostServicesFixture(t)
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			trace := []string{}
			originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/3/tv/123":
					trace = append(trace, "tv")
					if !sample.TV {
						return 404, `{"success":false,"status_message":"owned miss"}`
					}
					return 200, `{"id":123,"name":"Owned TV","original_name":"Owned TV","first_air_date":"2024-01-01"}`
				case "/3/movie/123":
					trace = append(trace, "movie")
					if !sample.Movie {
						return 404, `{"success":false,"status_message":"owned miss"}`
					}
					return 200, `{"id":123,"title":"Owned Movie","original_title":"Owned Movie","release_date":"2024-01-01"}`
				default:
					t.Error("unexpected source UNKNOWN endpoint", r.URL.Path)
					return 404, `{}`
				}
			})
			fresh := false
			raw, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, plugins.MediaRecognizeRequest{Source: "themoviedb", TMDBID: 123, Type: "未知", Meta: map[string]any{"cn_name": "Owned TV", "type": "电视剧", "year": "2024"}, Cache: &fresh})
			if err != nil {
				t.Fatal(err)
			}
			if sample.Selected == nil {
				if string(raw) != "null" {
					t.Fatal("explicit UNKNOWN must not meta-disambiguate", string(raw))
				}
			} else {
				var info map[string]any
				if json.Unmarshal(raw, &info) != nil || info["type"] != *sample.Selected {
					t.Fatal("source explicit UNKNOWN sole identity", sample.Selected, string(raw))
				}
			}
			if !reflect.DeepEqual(trace, sample.Trace) {
				t.Fatal("source UNKNOWN TV then movie", sample.Trace, trace)
			}
		})
	}
}
