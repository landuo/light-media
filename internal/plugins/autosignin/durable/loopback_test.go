package durable

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	source "light-media/internal/plugins/autosignin"
)

func TestRealHTTPReceiptSurvivesStatisticsFailureAndFullCoreResume(t *testing.T) {
	store, binding, _ := fixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/attendance.php" || r.Header.Get("Cookie") != "sid=synthetic-durable" {
			t.Error("owned source request differs")
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		io.WriteString(w, confirmed)
	}))
	defer server.Close()
	c := controller(store, binding, nil)
	c.Dependencies.Factory = func(context.Context, source.Object) (BoundSite, error) {
		transport, err := source.BindSiteHTTP(source.SiteHTTPBinding{URL: server.URL, Cookie: "sid=synthetic-durable"})
		return BoundSite{Site: source.Object{"id": 1, "name": "Synthetic", "url": server.URL + "/", "cookie": "sid=synthetic-durable"}, Hooks: source.SiteVisitHooks{HTTP: transport}, Revision: "fixed-owned-http"}, err
	}
	boot(t, c)
	failed := &failEffectStore{Journal: store, Kind: "stat"}
	c.Journal = failed
	if _, err := c.Execute(context.Background(), "http", Command{Action: "service"}); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	state, _ := store.State(context.Background(), binding)
	if len(state.History) != 0 || len(state.Stats) != 0 || calls.Load() != 1 {
		t.Fatal("post-request stat failure not interrupted", calls.Load(), state)
	}
	if _, err := c.Execute(context.Background(), "http", Command{Action: "service"}); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State(context.Background(), binding)
	if len(state.History) != 2 || len(state.Stats) != 1 || calls.Load() != 1 {
		t.Fatal("recovery repeated physical request or lost effects", calls.Load(), state)
	}
}
func TestActualTruncatedHTTPReplyIsUncertainInsteadOfFallback(t *testing.T) {
	store, binding, _ := fixture(t)
	var commits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		commits.Add(1)
		w.Header().Set("Content-Length", "9999")
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		io.WriteString(w, confirmed)
	}))
	defer server.Close()
	c := controller(store, binding, nil)
	c.Dependencies.Factory = func(context.Context, source.Object) (BoundSite, error) {
		transport, err := source.BindSiteHTTP(source.SiteHTTPBinding{URL: server.URL, Cookie: "sid=synthetic-durable"})
		return BoundSite{Site: source.Object{"id": 1, "name": "Synthetic", "url": server.URL + "/", "cookie": "sid=synthetic-durable"}, Hooks: source.SiteVisitHooks{HTTP: transport}, Revision: "fixed-owned-truncated-http"}, err
	}
	boot(t, c)
	if _, err := c.Execute(context.Background(), "truncated", Command{Action: "service"}); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	if _, err := c.Execute(context.Background(), "truncated", Command{Action: "service"}); !errors.Is(err, ErrUncertain) || commits.Load() != 1 {
		t.Fatal("truncated reply caused blind repeat/fallback", err, commits.Load())
	}
}
func TestDomainAPIFencesKeyBeforeLookupAndUsesSameVisitJournal(t *testing.T) {
	store, binding, _ := fixture(t)
	target := &fixedHTTP{Response: confirmed}
	c := controller(store, binding, target)
	boot(t, c)
	lookups := 0
	c.Dependencies.Lookup = func(context.Context, string) (source.Object, error) {
		lookups++
		return source.Object{"id": 1, "name": "Synthetic"}, nil
	}
	result, err := c.Execute(context.Background(), "bad-api", Command{Action: "api", Domain: "https://synthetic.invalid", APIKeyAccepted: false})
	if err != nil || result["success"] != false || lookups != 0 || target.Calls.Load() != 0 {
		t.Fatal("API key gate bypassed", result, err)
	}
	result, err = c.Execute(context.Background(), "good-api", Command{Action: "api", Domain: "https://synthetic.invalid", APIKeyAccepted: true})
	if err != nil || result["success"] != true || lookups != 1 || target.Calls.Load() != 1 {
		t.Fatal("bound domain API failed", result, err)
	}
	again, err := c.Execute(context.Background(), "good-api", Command{Action: "api", Domain: "https://synthetic.invalid", APIKeyAccepted: true})
	if err != nil || again["success"] != true || lookups != 1 || target.Calls.Load() != 1 {
		t.Fatal("API reply loss triggered new lookup/visit", again, err)
	}
}
