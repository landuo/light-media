package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"light-media/internal/catalog"
)

func TestOriginalMediaQuerySequenceActualSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-query-sequence-source-oracle.json")
	var source struct {
		Commit string
		Cases  []struct {
			Name         string
			Multiple     bool     `json:"multiple_names"`
			Missing      bool     `json:"initial_names_missing"`
			Nonempty     []string `json:"nonempty_keywords"`
			Observations []struct {
				Kind, Keyword string
				Kwargs        map[string]json.RawMessage
			}
			Returned []json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(source.Cases) != 4 {
		t.Fatal("actual process sequence replay required", err)
	}
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	t.Setenv("MAX_SEARCH_NAME_LIMIT", "3")
	t.Setenv("SEARCH_KEYWORD_MIN_INTERVAL", "0") // Clock reservation is separately source-derived; no wall-clock test sleeps.
	for _, sample := range source.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			if sample.Multiple {
				t.Setenv("SEARCH_MULTIPLE_NAME", "true")
			} else {
				t.Setenv("SEARCH_MULTIPLE_NAME", "false")
			}
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, readResourceBrowseOracle(t).Site["url"].(string))
			provider := json.RawMessage(`{"id":900043,"title":"Owned","original_title":"Original","release_date":"2026-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
			full, err := sourceTMDBMediaInfo(provider, "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(full, &fields)
			fields["en_title"] = json.RawMessage(`"English"`)
			fields["names"] = json.RawMessage(`["present"]`)
			recognizedBytes, _ := json.Marshal(fields)
			recognized := json.RawMessage(recognizedBytes)
			if sample.Missing {
				fields["names"] = json.RawMessage(`[]`)
			}
			full, _ = json.Marshal(fields)
			info, err := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "movie"}, full)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]json.RawMessage{}
			for _, keyword := range sample.Nonempty {
				values[keyword], _ = json.Marshal(map[string]any{"1": sample.Returned[:1]})
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"batch_search_torrents": "module_execute", "recognize_media": "module_page_size"}, "search_results": values, "page_size": recognized})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			plan, err := a.prepareOriginalMediaResourceSearch(context.Background(), SearchRequest{originalSearchInfo: &info, MediaType: "movie", TMDBID: 900043}, true)
			if err != nil || plan == nil {
				probe, probeErr := object.callOriginalMethod(context.Background(), "module_page_size", nil)
				_, expectedErr := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "movie"}, recognized)
				_, actualErr := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "movie"}, probe.Result)
				t.Log("owned diagnostic: actual result equals configured recognized JSON", originalBaseJSONEqual(probe.Result, recognized), "SDK", probeErr, "configured projection", expectedErr, "actual projection", actualErr)
				t.Fatal("actual media plan missing", err)
			}
			plan.ctx, plan.configs = context.Background(), []SiteConfig{site}
			plan.run()
			if plan.err != nil || len(plan.results[site.ID].result.Resources) != len(sample.Returned) {
				t.Fatal("source raw-result stop/multiple-name result differs", plan.err)
			}
			frame, err := object.callOriginalMethod(context.Background(), "snapshot", nil)
			var actual struct {
				Keywords []string `json:"search_keywords"`
			}
			if err != nil || json.Unmarshal(frame.Result, &actual) != nil {
				t.Fatal("actual SDK keyword order observation", err)
			}
			want := []string{}
			recognitionCount := 0
			for _, observation := range sample.Observations {
				if observation.Kind == "search" {
					want = append(want, observation.Keyword)
				} else if observation.Kind == "recognize" {
					recognitionCount++
				}
			}
			if !reflect.DeepEqual(actual.Keywords, want) || plan.searched != len(want) {
				t.Fatal("source query order/raw-hit short circuit differs", actual.Keywords, want)
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_page_size"]-before.Calls["module_page_size"] != recognitionCount {
				t.Fatal("source missing-name fill repeated or omitted")
			}
			if after.Calls["get_module"]-before.Calls["get_module"] != 2*len(want)+recognitionCount || after.Calls["get_state"]-before.Calls["get_state"] != 2*len(want)+recognitionCount {
				t.Fatal("source initial recognition/batch frame was queried twice")
			}
			if recognitionCount != 0 {
				var input struct{ Kwargs map[string]json.RawMessage }
				_ = json.Unmarshal(after.Inputs["module_page_size"], &input)
				for _, observation := range sample.Observations {
					if observation.Kind == "recognize" {
						for key, value := range observation.Kwargs {
							if !originalBaseJSONEqual(input.Kwargs[key], value) {
								t.Fatal("source process recognition identity/type caller differs", key)
							}
						}
					}
				}
				if string(input.Kwargs["cache"]) != "true" || string(input.Kwargs["meta"]) != "null" {
					t.Fatal("name fill reused subscription cachefalse/meta request")
				}
			}
		})
	}
}
