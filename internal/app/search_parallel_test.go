package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/sites"
)

func parallelSearchSite(key, baseURL string) SiteConfig {
	return SiteConfig{Config: sites.Config{ID: key, Kind: "ubits", Name: key, BaseURL: baseURL, Cookie: "synthetic-search-cookie"}, Enabled: true, DailyBudget: 100}
}

// The first configured site deliberately waits until the second site's results
// have reached the actual HTTP reader. A serial implementation cannot pass.
func TestKeywordSearchStreamsFastSiteBeforeSlowSiteCompletes(t *testing.T) {
	a := newTestApp(t)
	slowStarted, releaseSlow := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(releaseSlow) })
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(slowStarted)
		select {
		case <-releaseSlow:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, workflowHTML("Slow.Synthetic.Film.2024.1080p", "", false))
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-slowStarted:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, workflowHTML("Fast.Synthetic.Film.2024.1080p", "", false))
	}))
	defer fast.Close()
	a.config.Sites = []SiteConfig{parallelSearchSite("slow", slow.URL), parallelSearchSite("fast", fast.URL)}
	cookie := setupTestAdmin(t, a)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/search/resources", strings.NewReader(`{"query":"Synthetic Film"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.AddCookie(cookie)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("search did not establish an authenticated stream", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	sawFast, sawComplete, sawQueued := false, false, false
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
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &output); err != nil {
			t.Fatal(err)
		}
		if len(output.Sites) != 2 {
			t.Fatal("stream lost selected site progress", output.Sites)
		}
		if output.Sites[0].Status == "queued" && output.Sites[1].Status == "queued" {
			sawQueued = true
		}
		if len(output.Candidates) == 1 && output.Candidates[0].Resource.SiteID == "fast" {
			if output.Sites[0].Status != "searching" || output.Sites[1].Status != "ok" {
				t.Fatal("finished and in-flight statuses incorrect", output.Sites)
			}
			sawFast = true
			release.Do(func() { close(releaseSlow) })
		}
		if event == "complete" {
			sawComplete = len(output.Candidates) == 2 && output.Sites[0].Status == "ok" && output.Sites[1].Status == "ok"
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal("fast site waited behind slow site or stream was interrupted", err)
	}
	if !sawFast || !sawComplete || !sawQueued {
		t.Fatal("incomplete progressive search", sawQueued, sawFast, sawComplete)
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM site_usage`).Scan(&count); err != nil || count != 2 {
		t.Fatal("parallel search did not preserve one charged request per site", count, err)
	}
}

func TestMediaSearchKeepsPartialResourcesAndCacheHintsAcrossConcurrentSites(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	valid := workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false)
	partial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broken := `<tr><td><a href="details.php?id=102">Broken resource</a></td></tr>`
		io.WriteString(w, strings.TrimSuffix(valid, "</table>")+broken+"</table>")
	}))
	defer partial.Close()
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
	defer failed.Close()
	a.config.Sites = []SiteConfig{parallelSearchSite("partial", partial.URL), parallelSearchSite("failed", failed.URL)}
	var snapshots []SearchOutput
	ctx := context.WithValue(context.Background(), searchProgressKey{}, func(out SearchOutput) { snapshots = append(snapshots, out) })
	out, err := a.search(ctx, SearchRequest{MediaType: m.MediaType, TMDBID: m.ID, Query: "frozen-fixture-title", Page: 2}, false)
	if err != nil || len(out.Candidates) != 1 || out.Sites[0].Status != "partial" || out.Sites[1].Status != "failed" {
		t.Fatalf("partial result was discarded or failure hidden: %+v %v", out, err)
	}
	if len(snapshots) == 0 || snapshots[0].Sites[0].Status != "queued" || len(snapshots[0].Candidates) != 0 {
		t.Fatal("earlier progress snapshots changed after later results")
	}
	var stored struct {
		Query    string `json:"query"`
		MediaKey string `json:"media_key"`
		Page     int    `json:"page"`
	}
	if err := a.Store.Get(context.Background(), "resource", "partial:101", &stored); err != nil {
		t.Fatal("partial listing lost preview revalidation hints", err)
	}
	if stored.Query != "frozen-fixture-title" || stored.MediaKey != mediaKey(m) || stored.Page != 2 {
		t.Fatal("search cache changed the original query/page/identity", stored)
	}
}

func TestCancellingParallelSearchStopsInFlightRequestsAndPreservesCompletedResults(t *testing.T) {
	a := newTestApp(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, workflowHTML("Fast.Synthetic.Film.2024.1080p", "", false))
	}))
	defer fast.Close()
	a.config.Sites = []SiteConfig{parallelSearchSite("pending", server.URL), parallelSearchSite("completed", fast.URL)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, searchProgressKey{}, func(progress SearchOutput) {
		if len(progress.Candidates) == 1 {
			cancel()
		}
	})
	out, err := a.searchResources(ctx, SearchRequest{Query: "synthetic"})
	if !errors.Is(err, context.Canceled) || len(out.Sites) != 2 || out.Sites[0].Status != "cancelled" || out.Sites[1].Status != "ok" || len(out.Candidates) != 1 {
		t.Fatal("cancelled search was represented as success", out, err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach the in-flight HTTP request")
	}
}

func TestConcurrentSearchPreservesPerSiteSerialization(t *testing.T) {
	a := newTestApp(t)
	firstStarted, releaseFirst := make(chan struct{}), make(chan struct{})
	var calls, active, maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		if current > maximum.Load() {
			maximum.Store(current)
		}
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		io.WriteString(w, workflowHTML("Synthetic.Film.2024.1080p", "", false))
	}))
	defer server.Close()
	a.config.Sites = []SiteConfig{parallelSearchSite("same", server.URL)}
	results := make(chan error, 2)
	go func() {
		_, err := a.searchResources(context.Background(), SearchRequest{Query: "first"})
		results <- err
	}()
	<-firstStarted
	go func() {
		_, err := a.searchResources(context.Background(), SearchRequest{Query: "second"})
		results <- err
	}()
	close(releaseFirst)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 || maximum.Load() != 1 {
		t.Fatal("same-site searches bypassed the existing serialization", calls.Load(), maximum.Load())
	}
}

func TestSearchTimeoutStatusDistinguishesIncompleteResults(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	for _, count := range []int{0, 2} {
		status := a.searchStatus(ctx, "slow", count, context.DeadlineExceeded, 0)
		want := "timeout"
		if count > 0 {
			want = "partial"
		}
		if status.Status != want || status.Count != count || !strings.Contains(status.Message, "超时") {
			t.Fatal("timeout concealed missing results", status)
		}
	}
}
