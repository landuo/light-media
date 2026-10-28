package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestOriginalMediaFilterActualSDKSourceIdentityAndMutation(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-filter-source-oracle.json")
	var reference struct {
		Commit string
		Cases  []struct {
			Sites        int
			Mode         string
			Returned     []map[string]any
			Refs         []*int                           `json:"input_model_refs"`
			Media        struct{ Title, Category string } `json:"media_after"`
			Observations []struct {
				Kwargs struct {
					Rows []json.RawMessage `json:"torrent_list"`
				}
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &reference) != nil || reference.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(reference.Cases) != 10 {
		t.Fatal("actual fixed media/site filter prefix replay required", err)
	}
	for _, sample := range reference.Cases {
		t.Run(sample.Mode+string(rune('0'+sample.Sites)), func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, readResourceBrowseOracle(t).Site["url"].(string))
			if sample.Sites == 2 {
				second := site
				second.ID = "owned-second-site"
				second.Name = "Owned second site"
				second.BaseURL = "http://127.0.0.1:43101"
				a.cfgMu.Lock()
				a.config.Sites = append(a.config.Sites, second)
				a.cfgMu.Unlock()
				if err := a.bootstrapNativeSiteIdentities(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			provider := json.RawMessage(`{"id":900043,"title":"Owned media","original_title":"Owned media","release_date":"2026-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
			full, err := sourceTMDBMediaInfo(provider, "movie", "image.tmdb.org", "Owned original category")
			if err != nil {
				t.Fatal(err)
			}
			info, err := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "movie"}, full)
			if err != nil {
				t.Fatal(err)
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"filter_torrents": "module_filter"}, "filter_mode": sample.Mode, "result": false})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			candidates := []Candidate{}
			for _, observation := range sample.Observations {
				for _, row := range observation.Kwargs.Rows {
					var identity struct{ Site int }
					_ = json.Unmarshal(row, &identity)
					if identity.Site == 2 {
						// Only the owned second site's URL changes to its distinct
						// authenticated native origin; no filter/model field is faked.
						row = json.RawMessage(strings.ReplaceAll(string(row), site.BaseURL, "http://127.0.0.1:43101"))
					}
					resource, err := a.originalResourceFromSourceTorrent(context.Background(), site, row)
					if err != nil {
						t.Fatal(err)
					}
					candidates = append(candidates, Candidate{Resource: resource})
				}
			}
			filter, err := a.preparePriorityFilter(context.Background(), SearchRequest{Policy: Policy{FilterGroups: []string{"owned-rule"}}, priorityMedia: &info.Media, originalSearchInfo: &info}, a.Settings())
			if err != nil {
				t.Fatal(err)
			}
			out, err := filter.apply(context.Background(), a, SearchOutput{Candidates: candidates})
			if err != nil || len(out.Candidates) != len(sample.Returned) {
				t.Fatal("actual media site filter shape differs", err, len(out.Candidates), len(sample.Returned))
			}
			for i, row := range out.Candidates {
				want := sample.Returned[i]
				if row.Resource.Title != want["title"] || float64(row.Resource.PriorityOrder) != want["pri_order"] {
					t.Fatal("source media mutation/list order differs", i)
				}
				ref := row.originalFilterModel.ref
				if sample.Refs[i] == nil && ref < sample.Sites || sample.Refs[i] != nil && ref != *sample.Refs[i] {
					t.Fatal("equal copy incorrectly treated as input model", i, ref)
				}
			}
			value, _ := decodeSourceJSON(info.MediaInfo)
			fields := sourceMap(value)
			if fields["title"] != sample.Media.Title || fields["category"] != sample.Media.Category {
				t.Fatal("valid MediaInfo mutation before failure was lost")
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_filter"]-before.Calls["module_filter"] != sample.Sites || after.Calls["get_module"]-before.Calls["get_module"] != sample.Sites {
				t.Fatal("source per-site module frames differ")
			}
			var input struct {
				Kwargs struct {
					Media  json.RawMessage `json:"mediainfo"`
					Groups []string        `json:"rule_groups"`
				}
			}
			_ = json.Unmarshal(after.Inputs["module_filter"], &input)
			if string(input.Kwargs.Media) == "null" || !reflect.DeepEqual(input.Kwargs.Groups, []string{"owned-rule"}) {
				t.Fatal("actual complete media caller became title/None")
			}
			var observed, original map[string]json.RawMessage
			_ = json.Unmarshal(input.Kwargs.Media, &observed)
			_ = json.Unmarshal(full, &original)
			if !originalBaseJSONEqual(observed["tmdb_info"], original["tmdb_info"]) {
				t.Fatal("live module MediaInfo was replaced by public serializer hiding provider fields")
			}
		})
	}
}

func TestOriginalMediaDeduplicateFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-filter-source-oracle.json")
	var source struct {
		Cases []struct {
			Name  string
			Input []struct {
				Tag     string
				Torrent json.RawMessage
			}
			Tags []string `json:"returned_tags"`
		} `json:"dedup_cases"`
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Cases) != 3 {
		t.Fatal("fixed media dict dedup replay missing", err)
	}
	for _, sample := range source.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, _, _ := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, readResourceBrowseOracle(t).Site["url"].(string))
			out := SearchOutput{Candidates: []Candidate{}}
			for _, item := range sample.Input {
				resource, err := a.originalResourceFromSourceTorrent(context.Background(), site, item.Torrent)
				if err != nil {
					t.Fatal(err)
				}
				out.Candidates = append(out.Candidates, Candidate{Resource: resource, Reasons: []string{item.Tag}})
			}
			out, err = originalMediaResourceDeduplicate(out, []SiteConfig{site})
			if err != nil {
				t.Fatal(err)
			}
			actual := []string{}
			for _, row := range out.Candidates {
				actual = append(actual, row.Reasons[0])
			}
			if !reflect.DeepEqual(actual, sample.Tags) {
				t.Fatal("source last-value/first-position/None string collision differs", actual, sample.Tags)
			}
		})
	}
}

func TestOriginalMediaLiveAttributesFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-filter-source-oracle.json")
	// Source keys are deliberately different from public DTO field names.
	var root map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &root) != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"production_model", "production_nil_source"} {
		var sample struct {
			Input  json.RawMessage `json:"module_input"`
			Public json.RawMessage
		}
		if json.Unmarshal(root[name], &sample) != nil {
			t.Fatal("actual source live/private sample missing")
		}
		normalized, err := originalFilterSourceMediaJSON(sample.Input)
		if err != nil {
			t.Fatal(err)
		}
		var actual, before, public map[string]json.RawMessage
		_ = json.Unmarshal(normalized, &actual)
		_ = json.Unmarshal(sample.Input, &before)
		_ = json.Unmarshal(sample.Public, &public)
		for _, field := range []string{"source", "media_id", "tmdb_info", "douban_info", "bangumi_info", "anilist_info"} {
			if !originalBaseJSONEqual(actual[field], before[field]) {
				t.Fatal("live source attribute changed by public serialization", name, field)
			}
		}
		if _, exists := actual["mediaid_prefix"]; exists {
			t.Fatal("public-only prefix was injected into live model")
		}
		for _, field := range []string{"detail_link", "title_year"} {
			if !originalBaseJSONEqual(actual[field], public[field]) {
				t.Fatal("readonly source property not recomputed", field)
			}
		}
		a := newTestApp(t)
		information, err := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "movie"}, sample.Input)
		if err != nil || information.Media.ID != 900043 {
			t.Fatal("actual None source with matching explicit TMDB identity was rejected", err)
		}
		var retained map[string]json.RawMessage
		_ = json.Unmarshal(information.MediaInfo, &retained)
		if !originalBaseJSONEqual(retained["source"], before["source"]) {
			t.Fatal("native compact projection rewrote source live attribute")
		}
	}
}

func TestOriginalMediaClearAndCallerObjectActualSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-filter-source-oracle.json")
	var source struct {
		Model struct {
			Input json.RawMessage `json:"module_input"`
		} `json:"production_model"`
		Cleared      json.RawMessage `json:"production_clear"`
		MutatedClear json.RawMessage `json:"production_mutated_clear"`
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Cleared) == 0 {
		t.Fatal("actual source clear/caller sample missing", err)
	}
	cleared, err := originalClearMediaSearchInformation(source.Model.Input)
	if err != nil || !originalBaseJSONEqual(cleared, source.Cleared) {
		t.Fatal("source exact clear retains IDs/title/category and replaces provider/list values", err)
	}
	a, cookie, object := ownedModuleFixture(t, true)
	site := installResourceBrowseSite(t, a, readResourceBrowseOracle(t).Site["url"].(string))
	info, err := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "movie"}, source.Model.Input)
	if err != nil {
		t.Fatal(err)
	}
	var row json.RawMessage
	for _, sample := range readResourceBrowseOracle(t).Cases {
		if sample.Name == "plugin_list" {
			row = sample.Plugin
		}
	}
	result, _ := json.Marshal(map[string]json.RawMessage{"1": row})
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"batch_search_torrents": "module_execute", "filter_torrents": "module_filter"}, "result": json.RawMessage(result), "filter_mode": "media_mutate_then_fail"})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	out, err := a.search(context.Background(), SearchRequest{MediaType: "movie", TMDBID: 900043, SiteIDs: []string{site.ID}, originalSearchInfo: &info, Policy: Policy{FilterGroups: []string{"owned-rule"}}}, true)
	if err != nil || len(out.Candidates) != 1 || !originalBaseJSONEqual(info.MediaInfo, source.MutatedClear) {
		t.Fatal("caller input object lost valid mutation or exact search clear", err, len(out.Candidates))
	}
}
