package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalMarketFreshFailureCacheSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/cache-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	type scenario struct {
		Initial              map[string]any `json:"initial"`
		FailedFresh          map[string]any `json:"failed_fresh"`
		OrdinaryAfterFailure map[string]any `json:"ordinary_after_failure"`
		EmptyFresh           map[string]any `json:"empty_fresh"`
		OrdinaryAfterEmpty   map[string]any `json:"ordinary_after_empty"`
		FetchCalls           int32          `json:"fetch_calls"`
	}
	var oracle struct{ Synchronous, Asynchronous scenario }
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]scenario{"synchronous": oracle.Synchronous, "asynchronous": oracle.Asynchronous} {
		t.Run(name, func(t *testing.T) {
			a, err := New(Options{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			var calls, mode atomic.Int32
			var index artifacts.Index
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode.Load() {
				case 1:
					http.Error(w, "synthetic unavailable", http.StatusServiceUnavailable)
				case 2:
					json.NewEncoder(w).Encode(artifacts.RepositoryIndex{SchemaVersion: 1, Plugins: []artifacts.Index{}})
				default:
					json.NewEncoder(w).Encode(index)
				}
			}))
			defer server.Close()
			index = guideMarketIndex("Owned", server.URL+"/worker")
			initial := a.originalMarketSource(context.Background(), server.URL, false)
			if !initial.status.Success || len(initial.discoveries) != len(expected.Initial) {
				t.Fatal("source initial cache result differs", initial)
			}
			mode.Store(1)
			failed := a.originalMarketSource(context.Background(), server.URL, true)
			if failed.status.Success || failed.status.Cached || len(failed.discoveries) != len(expected.FailedFresh) {
				t.Fatal("fresh failure substituted cached data", failed)
			}
			ordinary := a.originalMarketSource(context.Background(), server.URL, false)
			if !ordinary.status.Success || !ordinary.status.Cached || ordinary.status.CheckedAt != initial.status.CheckedAt || len(ordinary.discoveries) != len(expected.OrdinaryAfterFailure) {
				t.Fatal("ordinary call lost the unchanged prior cache", ordinary)
			}
			mode.Store(2)
			empty := a.originalMarketSource(context.Background(), server.URL, true)
			if !empty.status.Success || empty.status.Cached || len(empty.discoveries) != len(expected.EmptyFresh) {
				t.Fatal("empty successful source must replace previous declarations", empty)
			}
			mode.Store(1)
			ordinaryEmpty := a.originalMarketSource(context.Background(), server.URL, false)
			if !ordinaryEmpty.status.Success || !ordinaryEmpty.status.Cached || len(ordinaryEmpty.discoveries) != len(expected.OrdinaryAfterEmpty) || calls.Load() != expected.FetchCalls {
				t.Fatal("empty success was not cached like the source", ordinaryEmpty, calls.Load())
			}
		})
	}
}
