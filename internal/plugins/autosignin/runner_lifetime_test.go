package autosignin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// This is source-consumer/SDK evidence, not an App owner/Manager readiness
// claim. All HTTP requests use this test's loopback server and synthetic cookie.
// Three serial source visits each stay below the original 60s HTTP limit but
// together exceed the former global 2m deadline.
func TestAutoSignInSDKSourceLifetimeOverTwoMinutesAndCancellation(t *testing.T) {
	t.Run("source-three-serial-visits-over-two-minutes", func(t *testing.T) {
		runSourceLifetime(t, 41*time.Second, false)
	})
	t.Run("source-network-parent-cancellation", func(t *testing.T) {
		runSourceLifetime(t, 41*time.Second, true)
	})
}

func runSourceLifetime(t *testing.T, delay time.Duration, cancelOnRequest bool) {
	t.Helper()
	var requests atomic.Int32
	entered := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "sid=owned-lifetime-fixture" {
			t.Error("host binding lost synthetic cookie")
		}
		once.Do(func() { close(entered) })
		select {
		case <-r.Context().Done():
			return
		case <-time.After(delay):
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		io.WriteString(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer server.Close()
	sites := []Object{}
	for i := 1; i <= 3; i++ {
		sites = append(sites, Object{"id": i, "name": "Owned synthetic site", "url": server.URL + "/"})
	}
	e := New(Inventory{Configured: sites, Indexers: sites}, func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
	if err := e.Init(Object{"enabled": true, "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1, "notify": false}); err != nil {
		t.Fatal(err)
	}
	hooks := EntryHooks{HistoryKeys: func() []string {
		keys := []string{}
		for key := range e.Data {
			keys = append(keys, key)
		}
		return keys
	}, Run: RunHooks{Visit: func(ctx context.Context, metadata Object, op string, loaded bool) (Result, error) {
		binding, err := BindSiteHTTP(SiteHTTPBinding{URL: server.URL + "/", Cookie: "sid=owned-lifetime-fixture", UserAgent: "owned-fixture"})
		if err != nil {
			return Result{}, err
		}
		visitor := SiteVisitor{Hooks: SiteVisitHooks{HTTP: binding, Stats: func(context.Context, string, bool, int) error { return nil }}}
		private := clone(metadata).(Object)
		private["cookie"] = "sid=owned-lifetime-fixture"
		return visitor.Visit(ctx, private, op, loaded)
	}}}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if cancelOnRequest {
		go func() {
			select {
			case <-entered:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	invocation, _ := json.Marshal(plugins.Invocation{Action: "service", Config: json.RawMessage(`{}`)})
	start := time.Now()
	out, err := (plugins.Runner{Timeout: 2 * time.Minute, ExcludeHostTime: true}).Run(ctx, plugins.Command{Path: path, Args: []string{"-test.run=^TestAutoSignInSDKProcess$", "--", "autosignin-sdk-fixture"}}, Manifest(), "AutoSignIn", "invoke", invocation, func(hostCtx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
		if identity != (plugins.Identity{PluginID: "AutoSignIn", InstanceID: "AutoSignIn", Version: Version}) || method != RunCapability || string(raw) != `{}` {
			t.Error("source SDK grant changed")
			return nil, ErrAdapterNotReady
		}
		if err := e.SignIn(hostCtx, nil, hooks); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"done":true}`), nil
	})
	if cancelOnRequest {
		if err == nil || ctx.Err() == nil || time.Since(start) > 10*time.Second || requests.Load() < 1 || len(e.Data) != 0 {
			t.Fatal("actual source cancellation did not stop network/history", err, requests.Load(), len(e.Data), time.Since(start))
		}
		return
	}
	if err != nil || time.Since(start) < 2*time.Minute || requests.Load() != 3 || string(out.Result) != `{"done":true}` || out.HostCalls != 1 {
		t.Fatal("long original source consumer did not complete", err, requests.Load(), time.Since(start), string(out.Result))
	}
	daily := asObject(e.Data["签到-2026-01-02"])
	if len(asObject(daily["results"])) != 3 {
		t.Fatal("source history lost completed sites")
	}
	t.Logf("actual source Engine/Visitor + child SDK: elapsed=%s requests=%d host_calls=%d", time.Since(start), requests.Load(), out.HostCalls)
}
