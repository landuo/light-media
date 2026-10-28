package app

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/catalog"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestOriginalCatalogEpisodesActualSourceSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/episodes-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name                   string
			Plugin, Native, Output json.RawMessage
			Status                 int
			ErrorCount             int `json:"error_count"`
			Calls                  []struct {
				Kind   string
				Kwargs json.RawMessage
			}
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 {
		t.Fatal("actual fixed episode caller/engine/system/DTO")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_episodes": "module_execute"}, "result": row.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			a.systemMessages.get("system")
			core := 0
			got, e := a.originalCatalogEpisodes(context.Background(), 120, 0, nil, func() (json.RawMessage, error) { core++; return row.Native, nil })
			if (e == nil) != (row.Status == 200) {
				t.Fatal("fixed endpoint schema/terminal result", e, row.Status)
			}
			wantCore := 0
			for _, call := range row.Calls {
				if call.Kind == "provider" {
					wantCore++
				}
			}
			if core != wantCore {
				t.Fatal("None/list/tuple/false native call order", core, wantCore)
			}
			a.wg.Wait()
			messages := 0
			for {
				message := a.systemMessages.get("system")
				if len(message) == 0 {
					break
				}
				if !json.Valid(message) {
					t.Fatal("invalid actual source-style error queue")
				}
				messages++
			}
			if messages != row.ErrorCount {
				t.Fatal("source native failure notification side effect", messages, row.ErrorCount)
			}
			if e == nil {
				raw, _ := json.Marshal(got)
				if !originalBaseJSONEqual(raw, row.Output) {
					t.Fatal("source episode list/default/coercion", string(raw), string(row.Output))
				}
			}
			snapshot := ownedModuleRead(t, originalBaseSDKLoaded(t, a, ownedModuleID))
			var input struct{ Kwargs json.RawMessage }
			if json.Unmarshal(snapshot.Inputs["module_execute"], &input) != nil || !originalBaseJSONEqual(input.Kwargs, json.RawMessage(`{"tmdbid":120,"season":0,"episode_group":null}`)) {
				t.Fatal("actual SDK episode source kwargs", string(input.Kwargs))
			}
		})
	}
}

func TestOriginalCatalogEpisodesActualHTTPAndRawAlias(t *testing.T) {
	a, cookie, object := ownedModuleFixture(t, true)
	settings := a.Settings()
	settings.TMDB = TMDBConfig{APIKey: "owned-episodes-key"}
	a.cfgMu.Lock()
	a.config = settings
	a.cfgMu.Unlock()
	proxy, _ := a.proxyConfig()
	providerCalls := 0
	providerStatus := 200
	a.catMu.Lock()
	a.catCreds = settings.TMDB
	a.catProxy = proxy
	a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
		providerCalls++
		if r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/tv/120/season/0" {
			t.Fatal("native episode origin/path", r.URL.Path)
		}
		return &http.Response{StatusCode: providerStatus, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":1200,"season_number":0,"name":"Owned native season","episodes":[{"episode_number":1,"season_number":0,"name":"Owned native episode"}]}`))}, nil
	}))
	a.catMu.Unlock()
	set := func(result json.RawMessage) {
		t.Helper()
		cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_episodes": "module_execute"}, "result": result})
		saveOwnedModule(t, a, cookie, object, string(cfg))
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
	}
	set(json.RawMessage(`[{"episode_number":"2","season_number":0,"name":"Owned plugin episode","guest_stars":[3]}]`))
	response := callTest(t, a.Handler(), "GET", "/api/catalog/season?id=120&season=0", "", cookie, "")
	var native struct{ Data catalog.Season }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &native) != nil || len(native.Data.Episodes) != 2 || native.Data.Episodes[0].EpisodeNumber != 2 || native.Data.Episodes[1].EpisodeNumber != 1 || native.Data.ID != 1200 || providerCalls != 1 {
		t.Fatal("actual native host did not consume source order/DTO", response.Code, response.Body.String(), providerCalls)
	}
	var rawEpisode map[string]json.RawMessage
	if json.Unmarshal(native.Data.Episodes[0].OriginalEpisode, &rawEpisode) != nil || string(rawEpisode["guest_stars"]) != `[3]` || len(rawEpisode) != 11 {
		t.Fatal("native compact view lost original source episode")
	}
	response = callTest(t, a.Handler(), "GET", "/api/v1/tmdb/120/0", "", cookie, "")
	var rows []map[string]json.RawMessage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &rows) != nil || len(rows) != 2 || rows[0]["id"] != nil || len(rows[0]) != 11 {
		t.Fatal("source raw episodes route/shape", response.Code, response.Body.String())
	}
	set(json.RawMessage(`false`))
	before := providerCalls
	response = callTest(t, a.Handler(), "GET", "/api/catalog/season?id=120&season=0", "", cookie, "")
	if response.Code != 500 || providerCalls != before {
		t.Fatal("source false episode result must reject without native", response.Code, providerCalls, before)
	}
	set(json.RawMessage(`null`))
	providerStatus = 503
	a.systemMessages.get("system")
	response = callTest(t, a.Handler(), "GET", "/api/v1/tmdb/120/0", "", cookie, "")
	a.wg.Wait()
	if response.Code != 200 || strings.TrimSpace(response.Body.String()) != "[]" || len(a.systemMessages.get("system")) != 0 {
		t.Fatal("source provider catches network failure before module SystemError", response.Code, response.Body.String())
	}
	providerStatus = 200
	response = callTest(t, a.Handler(), "GET", "/api/v1/tmdb/120/0", "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &rows) != nil || len(rows) != 1 {
		t.Fatal("provider recovery did not actually run", response.Code)
	}
}

func TestOriginalCatalogEpisodeDTOActualPydantic(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/episodes-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Input, Output json.RawMessage
			Status        int
		} `json:"dto_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 10 {
		t.Fatal("actual Pydantic episode DTO")
	}
	for i, row := range oracle.Cases {
		got, e := originalCatalogEpisodeProjection(row.Input)
		if (e == nil) != (row.Status == 200) {
			t.Fatal("actual DTO validation", i, e, row.Status)
		}
		if e == nil {
			raw, _ := json.Marshal(got)
			if !originalBaseJSONEqual(raw, row.Output) {
				t.Fatal("actual DTO defaults/coercion", i, string(raw), string(row.Output))
			}
		}
	}
}

func TestOriginalInternalSeasonActualSyncModuleAndCacheBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/internal-episodes-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name       string
			Plugin     json.RawMessage
			Native     json.RawMessage
			Projection json.RawMessage `json:"typed_projection"`
			Valid      bool            `json:"typed_sequence_valid"`
			ErrorCount int             `json:"error_count"`
			Calls      []struct{ Kind string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("fixed sync episode oracle", err)
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"tmdb_episodes": "module_execute", "async_tmdb_episodes": "module_execute"}, "result": row.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			providerCalls := 0
			settings := a.Settings()
			settings.TMDB = TMDBConfig{APIKey: "owned-sync-episode-key"}
			a.cfgMu.Lock()
			a.config = settings
			a.cfgMu.Unlock()
			proxy, _ := a.proxyConfig()
			a.catMu.Lock()
			a.catCreds, a.catProxy = settings.TMDB, proxy
			a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
				providerCalls++
				if r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/tv/120/season/0" {
					t.Error("wrong internal provider origin", r.URL.String())
				}
				body := `{"id":1200,"season_number":0,"name":"Owned native season","episodes":` + string(row.Native) + `}`
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			a.catMu.Unlock()
			for repeat := 0; repeat < 2; repeat++ {
				result, err := a.season(context.Background(), 120, 0)
				if (err == nil) != row.Valid {
					t.Fatal("fixed source sequence boundary", err, row.Valid)
				}
				if err == nil {
					rows := []json.RawMessage{}
					for _, episode := range result.Episodes {
						rows = append(rows, episode.OriginalEpisode)
					}
					got, _ := json.Marshal(rows)
					if !originalBaseJSONEqual(got, row.Projection) {
						t.Fatal("sync native consumer lost source sequence/DTO", string(got), string(row.Projection))
					}
				}
			}
			wantProvider := 0
			for _, call := range row.Calls {
				if call.Kind == "provider" {
					wantProvider = 2
				}
			}
			if providerCalls != wantProvider {
				t.Fatal("plugin must stay outside existing native cache", providerCalls, wantProvider)
			}
			snapshot := ownedModuleRead(t, originalBaseSDKLoaded(t, a, ownedModuleID))
			var input struct{ Kwargs json.RawMessage }
			if snapshot.Calls["module_execute"] != 2 || json.Unmarshal(snapshot.Inputs["module_execute"], &input) != nil || !originalBaseJSONEqual(input.Kwargs, json.RawMessage(`{"tmdbid":120,"season":0,"episode_group":null}`)) {
				t.Fatal("sync SDK input/call count", snapshot.Calls, string(input.Kwargs))
			}
		})
	}
}
