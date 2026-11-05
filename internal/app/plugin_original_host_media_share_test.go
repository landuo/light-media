package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
)

func hostMediaSourceObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	value, err := decodeSourceJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return sourceMap(value)
}

func TestOriginalHostMediaShareFieldsPinnedSourceAndActualHTTP(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var oracle struct {
		Queries []struct {
			Meta   json.RawMessage `json:"meta"`
			Type   string          `json:"mtype"`
			Result map[string]any  `json:"result"`
		} `json:"queries"`
		Reports []struct {
			Meta   json.RawMessage `json:"meta"`
			Info   json.RawMessage `json:"info"`
			Result map[string]any  `json:"result"`
		} `json:"reports"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/share-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Queries) != 4 || len(oracle.Reports) != 3 {
		t.Fatal("fixed sharing source oracle", err)
	}
	var query url.Values
	var report map[string]any
	var queryCalls, reportCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recognize/share" {
			t.Error("source share path", r.URL.Path)
		}
		if r.Header.Get("X-MoviePilot-User-Uid") == "" {
			t.Error("configured center missing stable UID")
		}
		if r.Method == http.MethodGet {
			queryCalls++
			query = r.URL.Query()
			fmt.Fprint(w, `{"code":0,"data":{"item":null}}`)
			return
		}
		if r.Method == http.MethodPost {
			reportCalls++
			if json.NewDecoder(r.Body).Decode(&report) != nil {
				t.Error("invalid report JSON")
			}
			fmt.Fprint(w, `{"code":0}`)
			return
		}
		t.Error("unexpected sharing method", r.Method)
	}))
	defer server.Close()
	config := a.Settings()
	center := server.URL
	config.PluginServerURL = &center
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	for _, sample := range oracle.Queries {
		meta := hostMediaSourceObject(t, sample.Meta)
		kwargs, err := originalCatalogRecognitionCallKwargs(meta, map[string]any{"mtype": sample.Type})
		if err != nil {
			t.Fatal(err)
		}
		before := queryCalls
		if _, err := a.originalHostMediaShareFallback(ctx, kwargs, meta); err != nil {
			t.Fatal(err)
		}
		if sample.Result == nil {
			if queryCalls != before {
				t.Fatal("empty keyword must not query")
			}
			continue
		}
		expected := url.Values{}
		for key, value := range sample.Result {
			expected.Set(key, fmt.Sprint(value))
		}
		if queryCalls != before+1 || !reflect.DeepEqual(query, expected) {
			t.Fatal("source shared query fields", expected, query)
		}
	}
	for _, sample := range oracle.Reports {
		meta := hostMediaSourceObject(t, sample.Meta)
		before := reportCalls
		a.originalHostMediaShareReport(ctx, meta, meta, sample.Info)
		if sample.Result == nil {
			if reportCalls != before {
				t.Fatal("unresolved source identity must not report")
			}
			continue
		}
		if reportCalls != before+1 || !reflect.DeepEqual(report, sample.Result) {
			t.Fatal("source report payload", sample.Result, report)
		}
	}
	for _, invalid := range []string{"relative/path", "https://user:secret@invalid.test/path", "https://invalid.test/path?token=x", "https://invalid.test/path#x"} {
		config.MediaRecognizeShareAPI = invalid
		if err := a.saveSettings(ctx, config); err == nil {
			t.Fatal("invalid sharing API accepted", invalid)
		}
	}
}

func TestOriginalHostMediaShareFallbackActualSDKCacheCountAndFailures(t *testing.T) {
	a, _, object := ownedHostServicesFixture(t)
	ctx := context.Background()
	var queries, reports atomic.Int32
	var missingCode atomic.Bool
	share := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recognize/share" {
			t.Error("share endpoint", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			queries.Add(1)
			if r.URL.Query().Get("keyword") != "Owned.Raw.Title" {
				t.Error("raw source query keyword", r.URL.RawQuery)
			}
			if missingCode.Load() {
				fmt.Fprint(w, `{"data":{"item":{"type":"movie","tmdbid":123,"media_source":"themoviedb","media_id":"123"}}}`)
				return
			}
			fmt.Fprint(w, `{"code":0,"data":{"item":{"type":"movie","tmdbid":123,"media_source":"themoviedb","media_id":"123"}}}`)
			return
		}
		reports.Add(1)
		fmt.Fprint(w, `{"code":0}`)
	}))
	defer share.Close()
	settings := a.Settings()
	center := share.URL
	settings.PluginServerURL = &center
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	lookups := []string{}
	originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
		mu.Lock()
		lookups = append(lookups, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/3/movie/123" {
			return 200, `{"id":123,"title":"Owned recognized","original_title":"Owned recognized","release_date":"2025-01-01"}`
		}
		return 404, `{"success":false,"status_code":34}`
	})
	request := plugins.MediaRecognizeRequest{Meta: map[string]any{"original_name": "Owned.Raw.Title", "en_name": "Owned Name", "type": "电影"}}
	result, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, request)
	if err != nil {
		t.Fatal("SDK shared fallback", err)
	}
	var info map[string]any
	if json.Unmarshal(result, &info) != nil || info["title"] != "Owned recognized" || queries.Load() != 1 || reports.Load() != 0 {
		t.Fatal("source fallback did not really identify", string(result), queries.Load(), reports.Load())
	}
	var count int64
	if err := a.Store.Get(ctx, "plugin_host_media_recognize_count", "main", &count); err != nil || count != 1 {
		t.Fatal("actual shared success count", count, err)
	}
	result, err = ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, request)
	if err != nil || queries.Load() != 1 || reports.Load() != 0 {
		t.Fatal("positive cache did not replace negative cache", string(result), err, queries.Load(), reports.Load())
	}
	if json.Unmarshal(result, &info) != nil || info["recognize_cache_hit"] != true {
		t.Fatal("cached recognition marker", string(result))
	}
	// A missing code is not code=0, and cannot increment successful sharing.
	missingCode.Store(true)
	noCache := false
	request.Cache = &noCache
	if raw, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, request); err != nil || string(raw) != "null" {
		t.Fatal("invalid share envelope falsely successful", string(raw), err)
	}
	if err := a.Store.Get(ctx, "plugin_host_media_recognize_count", "main", &count); err != nil || count != 1 {
		t.Fatal("failed share incremented count", count, err)
	}
	settings = a.Settings()
	off := false
	settings.MediaRecognizeShare = &off
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	before := queries.Load()
	if raw, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, request); err != nil || string(raw) != "null" || queries.Load() != before {
		t.Fatal("disabled share still queried", string(raw), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lookups) == 0 {
		t.Fatal("native metadata host was bypassed")
	}
}

func TestOriginalHostMediaShareCustomOriginAndRedirectPrivacy(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var customHits, foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignHits.Add(1); fmt.Fprint(w, `{"code":0}`) }))
	defer foreign.Close()
	custom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customHits.Add(1)
		if r.Header.Get("X-MoviePilot-User-Uid") != "" || r.Header.Get("Authorization") != "" {
			t.Error("identity left configured center")
		}
		if strings.HasSuffix(r.URL.Path, "redirect") {
			http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"item":null}}`)
	}))
	defer custom.Close()
	settings := a.Settings()
	center := "https://center.invalid"
	settings.PluginServerURL = &center
	settings.MediaRecognizeShareAPI = custom.URL + "/owned"
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := a.originalHostMediaShareRequest(ctx, http.MethodGet, url.Values{"keyword": {"synthetic"}}, nil); err != nil {
		t.Fatal(err)
	}
	settings.MediaRecognizeShareAPI = custom.URL + "/redirect"
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := a.originalHostMediaShareRequest(ctx, http.MethodGet, nil, nil); err == nil || customHits.Load() != 2 || foreignHits.Load() != 0 {
		t.Fatal("cross-origin redirect reached third party", err, customHits.Load(), foreignHits.Load())
	}
}
