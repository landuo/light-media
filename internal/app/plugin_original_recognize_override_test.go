package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

// Expected routing/call order comes from actual fixed ChainBase methods,
// including the easily missed false/empty-dict terminals and list core call.
func TestOriginalModuleRecognizeActualSDKSourceOracle(t *testing.T) {
	testOriginalModuleRecognizeActualSDKSourceOracle(t, false)
}

func TestOriginalModuleAsyncSubscriptionCallerActualSDKSourcePrefix(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/async-recognize-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Caller struct {
			Method string          `json:"recognize_method"`
			Kwargs json.RawMessage `json:"kwargs"`
		} `json:"caller_prefix"`
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Caller.Method != "async_recognize_media" {
		t.Fatal("fixed actual async_add prefix missing")
	}
	a, cookie, object := ownedModuleFixture(t, true)
	provider := json.RawMessage(`{"id":900043,"name":"Owned async full model","original_name":"Owned async original","first_air_date":"2026-01-01","overview":"owned async provider","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"seasons":[{"season_number":2,"episode_count":8}]}`)
	full, err := sourceTMDBMediaInfo(provider, "tv", "https://image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_execute", "recognize_media": "module_fail", "tmdb_info": "module_fail"}, "result": full})
	saveOwnedModule(t, a, cookie, object, string(config))
	object = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, object)
	source, err := a.buildUISubscriptionAdd(context.Background(), SubscriptionRequest{MediaType: "tv", TMDBID: 900043, SourceName: json.RawMessage(`"Owned async source"`), SourceYear: json.RawMessage(`"2026"`), Season: 2, TotalEpisodes: 8, State: "paused"}, "owned-async-user")
	if err != nil || source.Request.Media.Title != "Owned async full model" {
		t.Fatal("actual asynchronous subscription factory lost plugin media", err)
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 || after.Calls["module_fail"] != before.Calls["module_fail"] {
		t.Fatal("asynchronous caller used synchronous/wrong module name")
	}
	var actual struct{ Args, Kwargs json.RawMessage }
	if json.Unmarshal(after.Inputs["module_execute"], &actual) != nil || string(actual.Args) != "[]" {
		t.Fatal("actual asynchronous SDK input missing")
	}
	wantValue, _ := decodeSourceJSON(ref.Caller.Kwargs)
	gotValue, _ := decodeSourceJSON(actual.Kwargs)
	want, got := sourceMap(wantValue), sourceMap(gotValue)
	wantMeta, gotMeta := sourceMap(want["meta"]), sourceMap(got["meta"])
	if len(gotMeta) <= len(wantMeta) {
		t.Fatal("complete reused MetaInfo became a minimal caller fragment")
	}
	for key, value := range wantMeta {
		if !reflect.DeepEqual(gotMeta[key], value) {
			t.Fatal("fixed async_add did not set metadata before recognition", key)
		}
	}
	delete(want, "meta")
	delete(got, "meta")
	if !reflect.DeepEqual(got, want) {
		t.Fatal("fixed async_add resolved source/mediaid/cache kwargs differ")
	}
	meta, err := releasemeta.DecodeMetaObjectState(source.Origin.Meta)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := meta.ToDict()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(fields)
	persistedMeta, _ := decodeSourceJSON(encoded)
	if !reflect.DeepEqual(sourceMap(persistedMeta), gotMeta) {
		t.Fatal("recognition and registration used different metadata snapshots")
	}
}

func TestOriginalModuleAsyncRecognizeActualSDKSourceOracle(t *testing.T) {
	testOriginalModuleRecognizeActualSDKSourceOracle(t, true)
}

func TestOriginalModuleSyncCompletionCallerActualSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/recognize-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Caller struct {
			Record, Kwargs json.RawMessage
			Method         string
		} `json:"sync_subscription_caller"`
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Caller.Method != "recognize_media" {
		t.Fatal("fixed synchronous subscription caller replay missing")
	}
	a, cookie, object := ownedModuleFixture(t, true)
	provider := json.RawMessage(`{"id":900043,"name":"Owned sync full model","original_name":"Owned sync original","first_air_date":"2026-01-01","overview":"owned sync provider","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"seasons":[{"season_number":2,"episode_count":8}]}`)
	full, err := sourceTMDBMediaInfo(provider, "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_execute", "async_recognize_media": "module_fail"}, "result": full})
	saveOwnedModule(t, a, cookie, object, string(config))
	object = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, object)
	saved := Subscription{ID: "owned-sync-completion", Media: catalog.Media{ID: 900043, MediaType: "tv"}, Season: 2}
	source, err := a.buildSubscriptionCompletionBusiness(context.Background(), saved, ref.Caller.Record)
	if err != nil {
		t.Fatal("actual completion caller could not consume plugin media", err)
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 || after.Calls["module_fail"] != before.Calls["module_fail"] {
		t.Fatal("completion did not keep the synchronous extension point")
	}
	var actual struct{ Args, Kwargs json.RawMessage }
	if json.Unmarshal(after.Inputs["module_execute"], &actual) != nil || string(actual.Args) != "[]" {
		t.Fatal("actual completion SDK input missing")
	}
	wantValue, _ := decodeSourceJSON(ref.Caller.Kwargs)
	gotValue, _ := decodeSourceJSON(actual.Kwargs)
	want, got := sourceMap(wantValue), sourceMap(gotValue)
	wantMeta, gotMeta := sourceMap(want["meta"]), sourceMap(got["meta"])
	for key, value := range wantMeta {
		if !reflect.DeepEqual(gotMeta[key], value) {
			t.Fatal("source build_subscribe_meta None/IDs/type/season assignment differs", key)
		}
	}
	delete(want, "meta")
	delete(got, "meta")
	if !reflect.DeepEqual(got, want) {
		t.Fatal("source synchronous subscription kwargs/cache/group differ")
	}
	meta, err := releasemeta.DecodeMetaObjectState(source.Meta)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := meta.ToDict()
	encoded, _ := json.Marshal(fields)
	actualMeta, _ := decodeSourceJSON(encoded)
	if err != nil || !reflect.DeepEqual(sourceMap(actualMeta), gotMeta) {
		t.Fatal("completion notification did not reuse recognized metadata")
	}
}

func testOriginalModuleRecognizeActualSDKSourceOracle(t *testing.T, async bool) {
	name, method := "recognize-source-oracle.json", "recognize_media"
	if async {
		name, method = "async-recognize-source-oracle.json", "async_recognize_media"
	}
	raw, err := os.ReadFile("../../testdata/standard-module-host/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Commit string
		Cases  []struct {
			Name         string
			Plugin       json.RawMessage `json:"plugin_result"`
			Returned     json.RawMessage
			Error        *string
			Observations []struct {
				Kind, Method string
				Args, Kwargs json.RawMessage
			}
		}
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(ref.Cases) != 10 {
		t.Fatal("fixed recognition oracle unavailable")
	}
	for _, sample := range ref.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			settings := a.Settings()
			settings.TMDB = TMDBConfig{APIKey: "owned-recognition-key"}
			a.cfgMu.Lock()
			a.config = settings
			a.cfgMu.Unlock()
			proxy, err := a.proxyConfig()
			if err != nil {
				t.Fatal(err)
			}
			calls := new(atomic.Int32)
			body := `{"id":900043,"title":"owned core recognition","original_title":"owned original","release_date":"2026-01-01","overview":"owned core overview"}`
			a.catMu.Lock()
			a.catCreds = settings.TMDB
			a.catProxy = proxy
			a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				if r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/movie/900043" {
					t.Error("native fallback left fixed TMDB origin")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			a.catMu.Unlock()
			handler := "module_execute"
			if sample.Name == "failure" {
				handler = "module_fail"
			}
			result := sample.Plugin
			if sample.Name == "all_none_tuple" {
				result = json.RawMessage(`{"__plugin_module_tuple":[null,null]}`)
			}
			modules := map[string]string{"tmdb_info": "module_fail", "recognize_media": "module_fail", "async_recognize_media": "module_fail"}
			modules[method] = handler
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": modules, "result": result})
			saveOwnedModule(t, a, cookie, object, string(config))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			var out sourceMediaInformation
			if async {
				out, err = a.sourceAsyncRecognizeTMDBMedia(context.Background(), catalog.Media{ID: 900043, MediaType: "movie"}, nil)
			} else {
				out, err = a.sourceRecognizeTMDBMedia(context.Background(), catalog.Media{ID: 900043, MediaType: "movie"})
			}
			var expected map[string]any
			_ = json.Unmarshal(sample.Returned, &expected)
			if expected == nil {
				if err == nil {
					t.Fatal("source empty/invalid recognition became successful media")
				}
			} else {
				if err != nil || out.Media.Title != expected["title"] || out.Media.ID != 900043 {
					t.Fatal("actual plugin/core result differs from fixed source", err, out.Media.Title, expected["title"])
				}
				var full map[string]any
				if json.Unmarshal(out.MediaInfo, &full) != nil || full["title"] != expected["title"] {
					t.Fatal("source MediaInfo was not retained")
				}
				if sample.Name == "model_without_provider" && string(out.ProviderInfo) != "null" {
					t.Fatal("plugin model invented an upstream provider response")
				}
			}
			expectedCore := 0
			for _, observation := range sample.Observations {
				if observation.Kind == "core" {
					expectedCore++
				}
			}
			if int(calls.Load()) != expectedCore {
				t.Fatal("source null/list/scalar core-call order differs", calls.Load(), expectedCore)
			}
			after := ownedModuleRead(t, object)
			if after.Calls[handler]-before.Calls[handler] != 1 {
				t.Fatal("no actual SDK override invocation")
			}
			var actual struct{ Args, Kwargs any }
			if json.Unmarshal(after.Inputs[handler], &actual) != nil {
				t.Fatal("actual worker input missing")
			}
			var expectedArgs, expectedKwargs any
			json.Unmarshal(sample.Observations[0].Args, &expectedArgs)
			json.Unmarshal(sample.Observations[0].Kwargs, &expectedKwargs)
			if !reflect.DeepEqual(actual.Args, expectedArgs) || !reflect.DeepEqual(actual.Kwargs, expectedKwargs) {
				t.Fatal("source normalized explicit ID arguments differ", actual)
			}
		})
	}
}
