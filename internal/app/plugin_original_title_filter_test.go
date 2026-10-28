package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/priorityrules"
)

type titleFilterSourceOracle struct {
	Commit string
	Cases  []struct {
		Name         string
		Plugin       json.RawMessage `json:"plugin_result"`
		Observations []resourceStreamObservation
		Returned     []map[string]any
		InputAfter   []map[string]any `json:"input_after"`
		InputAlias   bool             `json:"returned_is_input"`
		Error        *string
	}
	Multiple []struct {
		FirstKind    string `json:"first_kind"`
		Observations []struct {
			resourceStreamObservation
			Instance string
		}
		Returned []map[string]any
	} `json:"multi_handlers"`
	Production []struct {
		Name        string
		InputBefore []json.RawMessage `json:"input_before"`
		Returned    []map[string]any
	}
}

func TestOriginalTitleFilterActualHTTPHostSDKAndPrivacy(t *testing.T) {
	reference := readTitleFilterSourceOracle(t)
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	a, cookie, object := ownedModuleFixture(t, true)
	html, err := os.ReadFile("../sites/testdata/ubits-search.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/torrents.php" || r.URL.Query().Get("page") != "1" || r.Header.Get("Cookie") != "owned-site-cookie" {
			t.Error("actual filter host private source request differs")
		}
		_, _ = w.Write(html)
	}))
	defer native.Close()
	installResourceBrowseSite(t, a, native.URL)
	expression := "OwnedAny"
	setRules := func(mutate bool) {
		configuration := &priorityrules.Configuration{SearchGroups: []string{"owned-rule"}}
		if mutate {
			configuration.Groups = []priorityrules.Group{{Name: "owned-rule", RuleString: &expression}}
			configuration.CustomRules = []priorityrules.Definition{{"id": "OwnedAny", "include": ".*"}}
		}
		a.cfgMu.Lock()
		a.config.PriorityRules = configuration
		a.cfgMu.Unlock()
	}
	for _, mode := range []string{"copy_equal", "alias_core_mutation", "mutate_then_fail"} {
		setRules(mode != "mutate_then_fail")
		cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"filter_torrents": "module_filter"}, "filter_mode": mode})
		saveOwnedModule(t, a, cookie, object, string(cfg))
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
		before := requests.Load()
		response := callTest(t, a.Handler(), "POST", "/api/search/resources", `{"query":"Owned keyword","page":2}`, cookie, "")
		var out struct{ Data SearchOutput }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || requests.Load()-before != 1 {
			t.Fatal("real filter host/SDK/native query did not execute", response.Code, requests.Load()-before)
		}
		priorities := map[int]int{}
		for _, row := range out.Data.Candidates {
			priorities[row.Resource.PriorityOrder]++
		}
		// Source13 replay supplies these distinct copy/alias/mutation cases;
		// this real source has2 rows instead of the single owned oracle row.
		want := map[int]int{}
		for _, sample := range reference.Production {
			if sample.Name == mode {
				for _, row := range sample.Returned {
					want[int(row["pri_order"].(float64))]++
				}
			}
		}
		if !reflect.DeepEqual(priorities, want) {
			t.Fatal("real host conflated equal copy/alias or lost valid failure mutation", mode, priorities, want)
		}
		if strings.Contains(response.Body.String(), "owned-site-cookie") || strings.Contains(response.Body.String(), "site_cookie") || strings.Contains(response.Body.String(), "title_torrent_refs") || strings.Contains(response.Body.String(), "originalFilterModel") {
			t.Fatal("private source model or typed references escaped public response")
		}
		var input struct {
			Refs []int `json:"title_torrent_refs"`
		}
		_ = json.Unmarshal(ownedModuleRead(t, object).Inputs["module_filter"], &input)
		if !reflect.DeepEqual(input.Refs, []int{0, 1}) {
			t.Fatal("distinct real source rows were not explicitly represented")
		}
	}
}

func TestOriginalTitleFilterCloneMutationActualSDKSource(t *testing.T) {
	source, browse := readTitleFilterSourceOracle(t), readResourceBrowseOracle(t)
	for _, sample := range source.Multiple {
		t.Run(sample.FirstKind, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, browse.Site["url"].(string))
			configure := func(id, mode string) {
				t.Helper()
				body, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"filter_torrents": "module_filter"}, "filter_mode": mode})
				r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(body), cookie, "")
				assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			}
			configure(ownedModuleID, sample.FirstKind)
			r := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedModuleID+"/clone", `{"suffix":"Filter"}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			configure(ownedModuleID+"filter", "mutate_then_fail")
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			clone := originalBaseSDKLoaded(t, a, ownedModuleID+"filter")
			before, beforeClone := ownedModuleRead(t, object), ownedModuleRead(t, clone)
			var kwargs struct {
				TorrentList []json.RawMessage `json:"torrent_list"`
			}
			_ = json.Unmarshal(sample.Observations[0].Kwargs, &kwargs)
			resource, err := a.originalResourceFromSourceTorrent(context.Background(), site, kwargs.TorrentList[0])
			if err != nil {
				t.Fatal(err)
			}
			settings := a.Settings()
			settings.PriorityRules = &priorityrules.Configuration{SearchGroups: []string{"owned-rule"}}
			filter, err := a.preparePriorityFilter(context.Background(), SearchRequest{}, settings)
			if err != nil {
				t.Fatal(err)
			}
			out, err := filter.apply(context.Background(), a, SearchOutput{Candidates: []Candidate{{Resource: resource}}})
			if err != nil || len(out.Candidates) != len(sample.Returned) {
				t.Fatal("source/clone mutation failure lost prior result", err)
			}
			for i, row := range sample.Returned {
				if out.Candidates[i].Resource.Title != row["title"] || float64(out.Candidates[i].Resource.PriorityOrder) != row["pri_order"] {
					t.Fatal("equal copy incorrectly inferred as input alias or clone mutation rolled back", i)
				}
			}
			after, afterClone := ownedModuleRead(t, object), ownedModuleRead(t, clone)
			if before.PID == beforeClone.PID || after.Calls["module_filter"]-before.Calls["module_filter"] != 1 || afterClone.Calls["module_filter"]-beforeClone.Calls["module_filter"] != 1 {
				t.Fatal("source/clone distinct actual SDK sequence not invoked")
			}
		})
	}
}

// Per-call list/model identity proof uses the same explicitly owned identity
// core boundary as the fixed replay. Actual native rule execution and HTTP are
// tested separately; this test is not a replacement for that complete host.
func TestOriginalTitleFilterStateIdentityActualSDKSource(t *testing.T) {
	source := readTitleFilterSourceOracle(t)
	for _, sample := range source.Cases {
		if sample.Name == "no_groups" || sample.Error != nil {
			continue
		}
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"filter_torrents": "module_filter"}, "filter_mode": sample.Name, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			var input struct {
				Groups []string          `json:"rule_groups"`
				Rows   []json.RawMessage `json:"torrent_list"`
			}
			_ = json.Unmarshal(sample.Observations[0].Kwargs, &input)
			state, err := newOriginalTitleFilterState(input.Groups, input.Rows)
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.invokeOriginalPluginModuleGroupsWithTitleFilter(context.Background(), "filter_torrents", nil, json.RawMessage(`[]`), sample.Observations[0].Kwargs, nil, nil, state)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range sample.Observations {
				if row.Kind != "core" {
					continue
				}
				if sample.Name == "alias_core_mutation" || sample.Name == "copy_equal" || sample.Name == "copy_container" {
					for _, model := range state.input.rows {
						var fields map[string]json.RawMessage
						_ = json.Unmarshal(model.value, &fields)
						fields["pri_order"] = json.RawMessage(`100`)
						model.value, _ = json.Marshal(fields)
					}
				}
				state.merge(originalTitleFilterValue{list: state.input})
			}
			actualInput, err := state.input.sourceJSON()
			wantInput, _ := json.Marshal(sample.InputAfter)
			if err != nil || !originalBaseJSONEqual(actualInput, wantInput) || (state.result.list == state.input) != sample.InputAlias {
				t.Fatal("source input list mutation or explicit object identity differs", err)
			}
			if state.result.list != nil {
				result, err := state.resultJSON()
				want, _ := json.Marshal(sample.Returned)
				if err != nil || !originalBaseJSONEqual(result, want) {
					t.Fatal("source full model identity aggregation differs", err)
				}
			}
		})
	}
}

func readTitleFilterSourceOracle(t *testing.T) titleFilterSourceOracle {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/filter-source-oracle.json")
	var source titleFilterSourceOracle
	if err != nil || json.Unmarshal(raw, &source) != nil || source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(source.Cases) != 13 {
		t.Fatal("actual fixed title-filter source replay missing", err)
	}
	return source
}

func TestOriginalTitleFilterActualSDKSourceOracle(t *testing.T) {
	source, browse := readTitleFilterSourceOracle(t), readResourceBrowseOracle(t)
	var initial json.RawMessage
	for _, sample := range source.Cases {
		if sample.Name == "none" {
			var kwargs struct {
				TorrentList []json.RawMessage `json:"torrent_list"`
			}
			_ = json.Unmarshal(sample.Observations[0].Kwargs, &kwargs)
			initial = kwargs.TorrentList[0]
		}
	}
	for _, sample := range source.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, browse.Site["url"].(string))
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"filter_torrents": "module_filter"}, "filter_mode": sample.Name, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			resource, err := a.originalResourceFromSourceTorrent(context.Background(), site, initial)
			if err != nil {
				t.Fatal(err)
			}
			configuration := &priorityrules.Configuration{SearchGroups: []string{"owned-rule"}}
			if sample.Name == "no_groups" {
				configuration.SearchGroups = []string{}
			}
			if sample.Name == "alias_core_mutation" || sample.Name == "copy_equal" || sample.Name == "copy_container" {
				expression := "OwnedAny"
				configuration.Groups = []priorityrules.Group{{Name: "owned-rule", RuleString: &expression}}
				configuration.CustomRules = []priorityrules.Definition{{"id": "OwnedAny", "include": ".*"}}
			}
			settings := a.Settings()
			settings.PriorityRules = configuration
			filter, err := a.preparePriorityFilter(context.Background(), SearchRequest{}, settings)
			if err != nil {
				t.Fatal(err)
			}
			before := ownedModuleRead(t, object)
			output, err := filter.apply(context.Background(), a, SearchOutput{Candidates: []Candidate{{Resource: resource}}, Sites: []SiteStatus{}})
			if (err != nil) != (sample.Error != nil) {
				t.Fatal("source title-filter error boundary differs", err)
			}
			if err == nil {
				got, want := [][2]any{}, [][2]any{}
				for _, row := range output.Candidates {
					got = append(got, [2]any{row.Resource.Title, float64(row.Resource.PriorityOrder)})
				}
				for _, row := range sample.Returned {
					want = append(want, [2]any{row["title"], row["pri_order"]})
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("source alias/copy/core mutation/list accumulation differs", got, want)
				}
			}
			after := ownedModuleRead(t, object)
			wantCalls := 1
			if sample.Name == "no_groups" {
				wantCalls = 0
			}
			if after.Calls["module_filter"]-before.Calls["module_filter"] != wantCalls || after.Calls["get_state"]-before.Calls["get_state"] != wantCalls || after.Calls["get_module"]-before.Calls["get_module"] != wantCalls {
				t.Fatal("source no-group skip / actual filter discovery frame differs")
			}
			if wantCalls > 0 {
				var input struct {
					Args, Kwargs json.RawMessage
					Refs         []int `json:"title_torrent_refs"`
				}
				if json.Unmarshal(after.Inputs["module_filter"], &input) != nil || string(input.Args) != "[]" || len(input.Refs) != 1 || input.Refs[0] != 0 || !originalBaseJSONEqual(input.Kwargs, sample.Observations[0].Kwargs) {
					t.Fatal("actual source three kwargs changed or explicit typed identity metadata missing")
				}
			}
		})
	}
}
