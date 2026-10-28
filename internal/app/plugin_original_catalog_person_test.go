package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
)

func TestOriginalCatalogPersonHTTPActualSDKOverrideAndCoreCache(t *testing.T) {
	a, cookie, object := ownedModuleFixture(t, true)
	settings := a.Settings()
	settings.TMDB = TMDBConfig{APIKey: "owned-person-key"}
	a.cfgMu.Lock()
	a.config = settings
	a.cfgMu.Unlock()
	proxy, err := a.proxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	a.catMu.Lock()
	a.catCreds = settings.TMDB
	a.catProxy = proxy
	a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/person/120" {
			t.Error("person native fallback used wrong provider")
		}
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":120,"name":"Owned provider person","biography":"owned biography","also_known_as":[],"birthday":"1980-01-01"}`))}, nil
	}))
	a.catMu.Unlock()
	set := func(result json.RawMessage, handler string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_person_detail": handler, "tmdb_person_detail": "module_fail", "async_search_persons": "module_fail"}, "result": result})
		saveOwnedModule(t, a, cookie, object, string(raw))
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
	}
	request := func(want int) map[string]any {
		t.Helper()
		res := callTest(t, a.Handler(), "GET", "/api/catalog/person?id=120", "", cookie, "")
		if res.Code != want {
			t.Fatal("person host status", res.Code, res.Body.String())
		}
		if want != 200 {
			return nil
		}
		var data struct{ Data map[string]any }
		if json.Unmarshal(res.Body.Bytes(), &data) != nil {
			t.Fatal("person response")
		}
		return data.Data
	}
	set(json.RawMessage(`{"id":"120","name":"Owned plugin person","title":"preserved extra source field","gender":false,"images":{"custom":"owned"}}`), "module_execute")
	before := ownedModuleRead(t, object)
	got := request(200)
	after := ownedModuleRead(t, object)
	if got["name"] != "Owned plugin person" || got["title"] != "preserved extra source field" || got["gender"] != float64(0) || len(got) != 25 || calls.Load() != 0 {
		t.Fatal("source DTO override did not skip native or preserve defaults", got, calls.Load())
	}
	var input struct{ Args, Kwargs json.RawMessage }
	_ = json.Unmarshal(after.Inputs["module_execute"], &input)
	if string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, json.RawMessage(`{"person_id":120}`)) || after.Calls["module_execute"] != before.Calls["module_execute"]+1 || after.Calls["module_fail"] != before.Calls["module_fail"] {
		t.Fatal("fixed async person dispatch input/name/order differ")
	}
	// Source {} is a terminal, valid default-filled MediaPerson, not fallback.
	set(json.RawMessage(`{}`), "module_execute")
	got = request(200)
	if got["id"] != nil || got["type"] != float64(1) || len(got) != 25 || calls.Load() != 0 {
		t.Fatal("empty object incorrectly falls back", got)
	}
	// Source None continues into the existing provider. Its native cache must not
	// hide subsequent module discovery/dispatch at the Chain caller boundary.
	set(json.RawMessage(`null`), "module_execute")
	got = request(200)
	before = ownedModuleRead(t, object)
	again := request(200)
	after = ownedModuleRead(t, object)
	if got["name"] != "Owned provider person" || again["name"] != got["name"] || calls.Load() != 1 || after.Calls["module_execute"] != before.Calls["module_execute"]+1 {
		t.Fatal("provider cache bypassed plugin call or core fetch repeated")
	}
	// Lists still execute core but never replace their accumulated list with the
	// provider's scalar. Fixed response_model=MediaPerson rejects both lists.
	for _, raw := range []string{`[]`, `[{"id":120,"name":"owned"}]`, `false`, `0`, `""`, `{"name":123}`} {
		set(json.RawMessage(raw), "module_execute")
		request(500)
	}
	if calls.Load() != 1 {
		t.Fatal("terminal scalars queried provider or list lost cache")
	}
	// Handler failures are caught by the module engine then core continues.
	set(json.RawMessage(`{"name":"never"}`), "module_fail")
	got = request(200)
	if got["name"] != "Owned provider person" || calls.Load() != 1 {
		t.Fatal("module exception did not continue to core")
	}
}

func TestOriginalCatalogPersonActualSDKFullSourceDispatchOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/person-dispatch-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name                     string
			Plugin, Provider, Output json.RawMessage
			HTTPStatus               int `json:"http_status"`
			Calls                    []struct{ Kind string }
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 10 {
		t.Fatal("actual full person dispatcher oracle")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			settings := a.Settings()
			settings.TMDB = TMDBConfig{APIKey: "owned-person-source-key"}
			a.cfgMu.Lock()
			a.config = settings
			a.cfgMu.Unlock()
			proxy, e := a.proxyConfig()
			if e != nil {
				t.Fatal(e)
			}
			var nativeCalls atomic.Int32
			a.catMu.Lock()
			a.catCreds = settings.TMDB
			a.catProxy = proxy
			a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
				nativeCalls.Add(1)
				if r.URL.Path != "/3/person/120" {
					t.Error("nonNone scalar wrongly reached system signature handling", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(row.Provider))}, nil
			}))
			a.catMu.Unlock()
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_person_detail": "module_execute"}, "result": row.Plugin})
			saveOwnedModule(t, a, cookie, object, string(config))
			result := callTest(t, a.Handler(), "GET", "/api/catalog/person?id=120", "", cookie, "")
			if result.Code != row.HTTPStatus {
				t.Fatal("fixed full person call status differs", row.HTTPStatus, result.Code, result.Body.String())
			}
			wantCore := 0
			for _, call := range row.Calls {
				if call.Kind == "provider" {
					wantCore++
				}
			}
			if int(nativeCalls.Load()) != wantCore {
				t.Fatal("source provider call order/count differs", nativeCalls.Load(), wantCore)
			}
			if row.HTTPStatus == 200 {
				var response struct{ Data json.RawMessage }
				if json.Unmarshal(result.Body.Bytes(), &response) != nil || !originalBaseJSONEqual(response.Data, row.Output) {
					t.Fatal("full source person DTO mismatch", string(response.Data), string(row.Output))
				}
			}
		})
	}
}
