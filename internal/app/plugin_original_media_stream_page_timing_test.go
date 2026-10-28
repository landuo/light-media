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

func TestOriginalMediaSSEActualSDKPageBeforeNextResponse(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-stream-timing-source-oracle.json")
	var source struct {
		Pages struct {
			First struct {
				Page, Count int
				Released    bool `json:"next_page_released"`
			} `json:"first_append"`
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || source.Pages.First.Page != 0 || source.Pages.First.Count != 1 || source.Pages.First.Released {
		t.Fatal("fixed actual per-page source SSE replay required", err)
	}
	t.Setenv("SEARCH_RESOURCE_PAGES", "2")
	a, cookie, object := ownedModuleFixture(t, true)
	releaseNext := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(releaseNext) })
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			select {
			case <-releaseNext:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, strings.ReplaceAll(workflowHTML("Second.Owned.keyword.2026.1080p", "", false), "id=101", "id=102"))
			return
		}
		if r.URL.Query().Get("page") != "0" {
			t.Error("actual native media page differs")
		}
		_, _ = io.WriteString(w, workflowHTML("First.Owned.keyword.2026.1080p", "", false))
	}))
	defer native.Close()
	installResourceBrowseSite(t, a, native.URL)
	raw, err = os.ReadFile("../../testdata/standard-resource-modules/media-filter-source-oracle.json")
	var model struct {
		Production struct {
			Input json.RawMessage `json:"module_input"`
		} `json:"production_model"`
	}
	if err != nil || json.Unmarshal(raw, &model) != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_execute", "get_search_page_size": "module_page_size"}, "search_results": map[string]json.RawMessage{"": model.Production.Input}, "page_size": 1})
	saveOwnedModule(t, a, cookie, object, string(cfg))
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
	scanner := bufio.NewScanner(response.Body)
	first, complete := false, false
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var output SearchOutput
		_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &output)
		if len(output.Candidates) == 1 && strings.HasPrefix(output.Candidates[0].Resource.Title, "First.") {
			first = true
			release.Do(func() { close(releaseNext) })
		}
		if event == "complete" {
			complete = len(output.Candidates) == 2
		}
	}
	if scanner.Err() != nil || !first || !complete {
		t.Fatal("actual source first page waited for next page response", scanner.Err(), first, complete)
	}
}
