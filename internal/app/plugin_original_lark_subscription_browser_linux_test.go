package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/web"
)

const originalLarkBrowserEditedKeyword = "owned-browser-restart-keyword"

func TestOriginalLarkProviderSubscriptionBrowserRestartLinuxAMD64(t *testing.T) {
	t.Setenv("RECOGNIZE_SOURCE", "bangumi")
	t.Setenv("SUBSCRIBE_MODE", "rss")
	testOriginalLarkProviderSelection(t, "provider_subscription_browser")
}

func openOriginalLarkProviderBrowserApp(t *testing.T, dir string) (*App, func() error) {
	t.Helper()
	markets := []string{}
	a, err := New(Options{DataDir: dir, Web: web.Handler(), PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	return a, sync.OnceValue(a.Close)
}

func originalLarkProviderBrowserRestart(t *testing.T, first *App, closeFirst func() error, original Subscription) {
	t.Helper()
	oldWorker := originalBaseSDKLoaded(t, first, "LarkMessager")
	var mu sync.RWMutex
	phase := "edit"
	served := original
	handler := first.Handler()
	var writes, reopenedList, reopenedDetail atomic.Int32
	restart := make(chan Subscription, 1)
	done := make(chan struct{}, 1)

	listener, err := net.Listen("tcp4", "127.0.0.1:38123")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__owned_browser_ready", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		workflowJSON(w, map[string]any{"phase": phase, "subscription_id": served.ID, "media_key": mediaKey(served.Media), "title": served.Media.Title, "keyword": originalLarkBrowserEditedKeyword, "revision": served.Revision})
	})
	mux.HandleFunc("POST /__owned_browser_restart", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if phase != "edit" || writes.Load() != 1 {
			http.Error(w, "expected one committed browser edit", http.StatusConflict)
			return
		}
		var edited Subscription
		if err := first.Store.Get(r.Context(), "subscription", original.ID, &edited); err != nil || !larkBrowserEditedSourceMatches(original, edited) {
			http.Error(w, "browser edit changed or lost the verified source subscription", http.StatusConflict)
			return
		}
		phase, handler, served = "restarting", nil, edited
		w.WriteHeader(http.StatusNoContent)
		restart <- edited
	})
	mux.HandleFunc("POST /__owned_browser_done", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		if phase != "reopened" || reopenedList.Load() == 0 || reopenedDetail.Load() == 0 {
			http.Error(w, "expected reopened browser list and provider detail", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		select {
		case done <- struct{}{}:
		default:
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		if handler == nil {
			http.Error(w, "owned fixture restarting", http.StatusServiceUnavailable)
			return
		}
		var observed *atomic.Int32
		switch {
		case phase == "edit" && r.Method == http.MethodPut && r.URL.Path == "/api/subscriptions/"+original.ID:
			if !larkBrowserReadSourceEdit(r, original) {
				http.Error(w, "browser update did not preserve provider identity", http.StatusBadRequest)
				return
			}
			observed = &writes
		case phase == "reopened" && r.Method == http.MethodGet:
			query := r.URL.Query()
			if r.URL.Path == "/api/subscriptions" {
				observed = &reopenedList
			} else if r.URL.Path == "/api/catalog/explore/detail" && query.Get("source") == original.Media.ProviderSource && query.Get("kind") == original.Media.MediaType && query.Get("source_id") == original.Media.ProviderID {
				observed = &reopenedDetail
			}
		}
		if observed == nil {
			handler.ServeHTTP(w, r)
			return
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, r)
		if result.Code == http.StatusOK {
			observed.Add(1)
		}
		for key, values := range result.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(result.Code)
		_, _ = w.Write(result.Body.Bytes())
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	t.Log("owned Lark subscription browser listener ready")

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	var edited Subscription
	select {
	case edited = <-restart:
	case <-ctx.Done():
		t.Fatal("browser did not commit the edit and request the owned restart")
	}
	if err := closeFirst(); err != nil {
		t.Fatal(err)
	}
	if err := first.Store.DB.PingContext(ctx); err == nil {
		t.Fatal("original App database was not closed")
	}
	second, closeSecond := openOriginalLarkProviderBrowserApp(t, first.Dir)
	defer closeSecond()
	second.pluginOriginalLarkTestOrigin = first.pluginOriginalLarkTestOrigin
	second.Start(t.Context())
	if worker := originalBaseSDKLoaded(t, second, "LarkMessager"); worker == oldWorker {
		t.Fatal("reopened App did not initialize a new Lark worker")
	}
	var restored Subscription
	if err := second.Store.Get(ctx, "subscription", original.ID, &restored); err != nil || !larkBrowserEditedSourceMatches(original, restored) || restored.Revision != edited.Revision {
		t.Fatal("reopened App lost the edited subscription's verified source", err)
	}
	origin, err := second.sourceSubscriptionOrigin(ctx, restored)
	if err != nil || origin == nil || origin.Source != "Lark" || origin.UserID != "ou_provider" {
		t.Fatal("reopened App lost the sealed Lark subscription origin", err)
	}
	mu.Lock()
	phase, handler, served = "reopened", second.Handler(), restored
	mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("browser did not verify the reopened App")
	}
	var total int
	if err := second.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE kind='subscription'`).Scan(&total); err != nil || total != 1 {
		t.Fatal("browser edit and reopen did not preserve exactly one subscription", total, err)
	}
	t.Logf("browser saved one keyword edit and reopened the same Lark source subscription %s at revision %d with its original source and episode scope", mediaKey(restored.Media), restored.Revision)
}

func larkBrowserReadSourceEdit(r *http.Request, original Subscription) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return false
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	var fields map[string]json.RawMessage
	var input SubscriptionRequest
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(body, &input) != nil {
		return false
	}
	_, hasTMDBID := fields["tmdb_id"]
	return !hasTMDBID && input.MediaSource == original.Media.ProviderSource && input.MediaID == original.Media.ProviderID &&
		input.MediaType == original.Media.MediaType && input.Keyword == originalLarkBrowserEditedKeyword && input.Revision == original.Revision
}

func larkBrowserEditedSourceMatches(original, edited Subscription) bool {
	return edited.ID == original.ID && edited.Media.ID == 0 && mediaKey(edited.Media) == mediaKey(original.Media) &&
		edited.Keyword == originalLarkBrowserEditedKeyword && edited.Revision == original.Revision+1 && edited.CreatedAt == original.CreatedAt &&
		edited.Season == original.Season && edited.Schedule.Mode == "manual" && edited.SourceContextSHA == original.SourceContextSHA &&
		slices.Equal(edited.ProviderEpisodes, original.ProviderEpisodes)
}
