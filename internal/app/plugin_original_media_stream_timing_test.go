package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOriginalMediaSSEActualSDKAndFastSiteBeforeSlow(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-stream-timing-source-oracle.json")
	var source struct {
		Result struct {
			First struct {
				Site     int `json:"site_id"`
				Count    int
				Released bool `json:"slow_released"`
			} `json:"first_append"`
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || source.Result.First.Site != 2 || source.Result.First.Released {
		t.Fatal("actual fixed fast-before-slow SSE oracle required", err)
	}
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	a, cookie, object := ownedModuleFixture(t, true)
	started, releaseSlow := make(chan struct{}), make(chan struct{})
	var startedOnce, release sync.Once
	defer release.Do(func() { close(releaseSlow) })
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		select {
		case <-releaseSlow:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, workflowHTML("Slow.Owned.keyword.2026.1080p", "", false))
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, workflowHTML("Fast.Owned.keyword.2026.1080p", "", false))
	}))
	defer fast.Close()
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{parallelSearchSite("owned-a-slow", slow.URL), parallelSearchSite("owned-b-fast", fast.URL)}
	a.cfgMu.Unlock()
	if err := a.bootstrapNativeSiteIdentities(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../testdata/standard-resource-modules/media-filter-source-oracle.json")
	var model struct {
		Production struct {
			Input json.RawMessage `json:"module_input"`
		} `json:"production_model"`
	}
	if err != nil || json.Unmarshal(raw, &model) != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_page_size"}, "page_size": model.Production.Input})
	saveOwnedModule(t, a, cookie, object, string(config))
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/search", strings.NewReader(`{"media_type":"movie","tmdb_id":900043}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	r.AddCookie(cookie)
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("actual authenticated media SSE not established", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	event := ""
	sawFast, complete := false, false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var output SearchOutput
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &output) != nil {
			t.Fatal("actual media SSE JSON invalid")
		}
		if len(output.Candidates) == source.Result.First.Count && output.Candidates[0].Resource.SiteID == "owned-b-fast" {
			if len(output.Sites) != 2 || output.Sites[0].Status != "searching" || output.Sites[1].Status != "ok" {
				t.Fatal("actual fast/blocked source site states differ")
			}
			sawFast = true
			release.Do(func() { close(releaseSlow) })
		}
		if event == "complete" {
			complete = len(output.Candidates) == 2
		}
	}
	if scanner.Err() != nil || !sawFast || !complete {
		t.Fatal("media SSE held the actual fast site behind blocked slow source", scanner.Err(), sawFast, complete)
	}
}
