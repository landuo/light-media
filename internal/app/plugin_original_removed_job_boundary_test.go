package app

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

type removedPluginJobTransport func(*http.Request) (*http.Response, error)

func (f removedPluginJobTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Historical persisted core jobs must be explicitly rejected, not routed into
// an unadapted native plugin or silently treated as a successful ordinary job.
func TestOriginalRemovedLarkCoreJobExplicitlyUnavailable(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var requests atomic.Int32
	prior := http.DefaultTransport
	http.DefaultTransport = removedPluginJobTransport(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected outbound IO from removed plugin job")
	})
	t.Cleanup(func() { http.DefaultTransport = prior })
	queued, err := a.enqueue(ctx, "lark_message", map[string]string{"id": "owned-historical-inbox"}, "owned-removed-plugin")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := a.claim(ctx)
	if err != nil || claimed.ID != queued.ID || claimed.State != "running" {
		t.Fatal("real core claim", claimed, err)
	}
	err = a.safeRun(ctx, claimed)
	if pluginErrorCodeInternal(err) != "PLUGIN_ADAPTER_UNAVAILABLE" {
		t.Fatalf("removed adapter silently succeeded or entered old native path: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("removed adapter attempted %d requests", requests.Load())
	}
	var runs, jobs int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("legacy plugin execution created", runs, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("removed adapter created child tasks", jobs, err)
	}
	if _, loaded := a.pluginRunningInstances.Load("LarkMessager"); loaded {
		t.Fatal("removed adapter secretly loaded")
	}
}
