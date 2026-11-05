package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalHostMediaNativeCacheActualSDKCloneAndColdRecovery(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	ctx := context.Background()
	settings := a.Settings()
	off := false
	settings.MediaRecognizeShare = &off
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	var searches, details, networks atomic.Int32
	var revision atomic.Int32
	revision.Store(1)
	var failDetail atomic.Bool
	respond := func(r *http.Request) (int, string) {
		if strings.HasPrefix(r.URL.Path, "/3/search/") {
			if r.URL.Query().Get("query") == "Owned Network" {
				networks.Add(1)
				return 0, ""
			}
			searches.Add(1)
			if r.URL.Query().Get("query") == "Owned Missing" {
				return 200, `{"results":[]}`
			}
			return 200, `{"results":[{"id":123,"media_type":"movie","title":"Owned Cache","original_title":"Owned Cache","release_date":"2024-01-01"}]}`
		}
		if r.URL.Path == "/3/movie/123" {
			details.Add(1)
			if failDetail.Load() {
				return 404, `{}`
			}
			return 200, fmt.Sprintf(`{"id":123,"title":"Owned detail v%d","original_title":"Owned Cache","release_date":"2024-01-01"}`, revision.Load())
		}
		t.Error("unexpected native cache endpoint", r.URL.Path)
		return 404, `{}`
	}
	originalHostMediaInstallOwnedTransport(t, a, respond)
	request := plugins.MediaRecognizeRequest{Meta: map[string]any{"en_name": "Owned Cache", "type": "未知"}}
	assertInfo := func(worker *originalPluginInstance, req plugins.MediaRecognizeRequest, title string, hit bool) {
		t.Helper()
		raw, err := ownedHostServicesCall(t, worker, plugins.MediaRecognizeCapability, req)
		if err != nil {
			t.Fatal(err)
		}
		var info map[string]any
		if json.Unmarshal(raw, &info) != nil || info["title"] != title || info["recognize_cache_hit"] != hit {
			t.Fatal("source identity cache must refetch current detail", title, hit, string(raw))
		}
	}
	assertInfo(object, request, "Owned detail v1", false)
	if searches.Load() != 1 || details.Load() != 1 {
		t.Fatal("source miss search+detail", searches.Load(), details.Load())
	}
	revision.Store(2)
	assertInfo(object, request, "Owned detail v1", true)
	if searches.Load() != 1 || details.Load() != 1 {
		t.Fatal("source positive identity reuses provider snapshot", searches.Load(), details.Load())
	}
	noCache := false
	request.Cache = &noCache
	assertInfo(object, request, "Owned detail v2", false)
	if searches.Load() != 2 || details.Load() != 2 {
		t.Fatal("source fresh bypasses both caches and writes back", searches.Load(), details.Load())
	}
	request.Cache = nil
	missing := plugins.MediaRecognizeRequest{Meta: map[string]any{"en_name": "Owned Missing", "type": "未知"}}
	for i := 0; i < 2; i++ {
		raw, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, missing)
		if err != nil || string(raw) != "null" {
			t.Fatal("negative cache", string(raw), err)
		}
	}
	if searches.Load() != 3 {
		t.Fatal("source negative cache repeated search", searches.Load())
	}
	network := plugins.MediaRecognizeRequest{Meta: map[string]any{"en_name": "Owned Network", "type": "未知"}}
	for i := 0; i < 2; i++ {
		raw, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, network)
		if err != nil || string(raw) != "null" {
			t.Fatal("network None", string(raw), err)
		}
	}
	if networks.Load() != 8 {
		t.Fatal("source network None must retry", networks.Load())
	}
	// The ordinary host cache is shared by all plugins, as source TmdbCache is
	// a host singleton. Plugin configuration and Base data remain per instance.
	if response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/OwnedHostServices/clone", `{"suffix":"copy"}`, cookie, "http://example.com"); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	cloneValue, ok := a.pluginRunningInstances.Load("OwnedHostServicescopy")
	if !ok {
		t.Fatal("clone did not load")
	}
	clone := cloneValue.(*originalPluginInstance)
	assertInfo(clone, request, "Owned detail v2", true)
	if searches.Load() != 3 || details.Load() != 2 {
		t.Fatal("host singleton cache not reused by clone", searches.Load(), details.Load())
	}
	beforeSource := object
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	originalHostMediaInstallOwnedTransport(t, b, respond)
	b.Start(ctx)
	loaded, ok := b.pluginRunningInstances.Load("OwnedHostServices")
	if !ok {
		t.Fatal("cold worker did not load")
	}
	cold := loaded.(*originalPluginInstance)
	if cold == beforeSource {
		t.Fatal("cold process reused old worker")
	}
	revision.Store(3)
	assertInfo(cold, request, "Owned detail v3", true)
	if searches.Load() != 3 || details.Load() != 3 {
		t.Fatal("positive identity not restored after App.Close/New/Start", searches.Load(), details.Load())
	}
	if raw, err := ownedHostServicesCall(t, cold, plugins.MediaRecognizeCapability, missing); err != nil || string(raw) != "null" {
		t.Fatal("cold negative lookup", string(raw), err)
	}
	if searches.Load() != 4 {
		t.Fatal("negative entry incorrectly persisted", searches.Load())
	}
	failDetail.Store(true)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	originalHostMediaInstallOwnedTransport(t, c, respond)
	c.Start(ctx)
	loaded, ok = c.pluginRunningInstances.Load("OwnedHostServices")
	if !ok {
		t.Fatal("second cold worker did not load")
	}
	cold = loaded.(*originalPluginInstance)
	if raw, err := ownedHostServicesCall(t, cold, plugins.MediaRecognizeCapability, request); err != nil || string(raw) != "null" {
		t.Fatal("cache hit detail failure", string(raw), err)
	}
	if searches.Load() != 4 || details.Load() != 4 {
		t.Fatal("source cached identity failure must not silently fall back to title", searches.Load(), details.Load())
	}
}
