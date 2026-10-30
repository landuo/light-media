package app

import (
	"context"
	"fmt"
	"io"
	"light-media/internal/catalog"
	"light-media/internal/media"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Review regression: native provider Search-backed directory recognition must
// not keep a stale identity after the ordinary authenticated clear command.
func TestIndependentClearCacheDirectoryRecognition(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	name := "Moana.2026.2160p.WEB-DL.mkv"
	file := importScanFile{Path: filepath.Join(t.TempDir(), name), Parsed: media.Parse(name)}
	cached := catalog.Media{ID: 1108427, MediaType: "movie", Title: "Moana", OriginalTitle: "Moana", Year: 2026}
	importCache(t, a, "directory-identify:movie:Moana", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 1, Results: []catalog.Media{cached}})
	before, err := a.directoryFileIdentity(context.Background(), DirectoryRule{MediaType: "mixed"}, file)
	if err != nil || before.TMDBID != cached.ID {
		t.Fatalf("precondition: actual directory identity did not consume seeded TMDB search: id=%d err=%v", before.TMDBID, err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/clear_cache"}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("clear ingress status=%d", response.Code)
	}
	a.wg.Wait()
	after, err := a.directoryFileIdentity(context.Background(), DirectoryRule{MediaType: "mixed"}, file)
	// No TMDB key exists in this owned app. A refreshed provider lookup must fail
	// locally; a successful identical answer proves cache invalidation was missed.
	if err == nil || after.TMDBID != 0 {
		t.Fatalf("stale provider recognition survived /clear_cache: before=%d after=%d err=%v", before.TMDBID, after.TMDBID, err)
	}
}

// The original broadcast queue owns admitted commands independently of the
// HTTP request. Cancel only after the real external worker enters clear().
func TestIndependentClearCacheAcceptedCancellation(t *testing.T) {
	a, cookie, worker := ownedClearFixture(t)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "cache.value"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ownedClearConfigure(t, a, cookie, map[string]any{"enabled": true, "directory": directory, "clear_delay_ms": 250})
	importCache(t, a, "media:movie:120", catalog.Media{ID: 120, MediaType: "movie", Title: "stale"})
	before := ownedClearRead(t, worker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("POST", "/api/v1/message/web", strings.NewReader(`{"text":"/clear_cache"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { a.Handler().ServeHTTP(response, req); close(done) }()
	timeout := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(directory, "clear.entered")); err == nil {
			break
		}
		if time.Now().After(timeout) {
			t.Fatal("real worker did not enter clear before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("accepted operation did not drain")
	}
	a.wg.Wait()
	after := ownedClearRead(t, worker)
	var nativeRows int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='cache' AND id='media:movie:120'`).Scan(&nativeRows); err != nil {
		t.Fatal(err)
	}
	if after.Clears != before.Clears+1 || nativeRows != 0 || len(after.Notices) != len(before.Notices)+1 {
		t.Fatalf("admitted request cancellation lost completion: clearDelta=%d nativeCacheRows=%d noticeDelta=%d", after.Clears-before.Clears, nativeRows, len(after.Notices)-len(before.Notices))
	}
}

// Concurrent authenticated HTTP requests still reach the retained real SDK
// worker exactly once each and do not create completion notification history.
func TestIndependentClearCacheConcurrentHTTP(t *testing.T) {
	a, cookie, worker := ownedClearFixture(t)
	ownedClearConfigure(t, a, cookie, map[string]any{"enabled": true, "result": map[string]any{"__plugin_module_tuple": []any{}}})
	before := ownedClearRead(t, worker)
	importCache(t, a, "media:movie:120", catalog.Media{ID: 120, MediaType: "movie", Title: "stale"})
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	const count = 8
	errors := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("POST", server.URL+"/api/v1/message/web", strings.NewReader(`{"text":"/clear_cache"}`))
			if err != nil {
				errors <- err
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(cookie)
			res, err := server.Client().Do(req)
			if err != nil {
				errors <- err
				return
			}
			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)
			if err != nil || res.StatusCode != 200 || !strings.Contains(string(data), `"accepted":true`) {
				errors <- fmt.Errorf("status=%d read=%v", res.StatusCode, err)
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	a.wg.Wait()
	after := ownedClearRead(t, worker)
	if after.Clears-before.Clears != count || len(after.Notices)-len(before.Notices) != count {
		t.Fatalf("concurrent accepted count differs: clears=%d notices=%d", after.Clears-before.Clears, len(after.Notices)-len(before.Notices))
	}
	var nativeRows, historyRows int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='cache' AND id='media:movie:120'`).Scan(&nativeRows); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&historyRows); err != nil {
		t.Fatal(err)
	}
	if nativeRows != 0 || historyRows != 0 {
		t.Fatalf("empty tuple fallback/history mismatch: cache=%d notifications=%d", nativeRows, historyRows)
	}
}
