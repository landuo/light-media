package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkParallelActualAppHTTPAndBaseScope(t *testing.T) {
	binary := os.Getenv("OWNED_HOST_SITE_HTTP_PARALLEL_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_HOST_SITE_HTTP_PARALLEL_FIXTURE_BINARY requires separately compiled parallel worker")
	}
	t.Setenv("OWNED_HOST_SITE_HTTP_FIXTURE_BINARY", binary)
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "configuration-revoked"}[revoke], func(t *testing.T) {
			a, _, p := ownedSiteHTTP(t)
			entered := make(chan struct{}, 40)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var peak, active, posts atomic.Int64
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				n := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				entered <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Write([]byte("owned-parallel"))
			}))
			defer peer.Close()
			ownedSiteHTTPConfig(t, a, peer.URL, "owned-private=scope", "OwnedParallel/1")
			type answer struct {
				raw plugins.RunResult
				err error
			}
			done := make(chan answer, 1)
			go func() {
				out, e := p.callOriginalMethod(context.Background(), "request", json.RawMessage(`{"parallel_count":40,"path":"/owned"}`))
				done <- answer{out, e}
			}()
			for i := 0; i < 40; i++ {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("actual App serialized peer", i)
				}
			}
			if posts.Load() != 40 || peak.Load() != 40 {
				t.Fatal("not physical overlap", posts.Load(), peak.Load())
			}
			if revoke {
				settings := a.Settings()
				settings.Sites[0].UserAgent = "OwnedParallel/2"
				if e := a.saveSettings(context.Background(), settings); e != nil {
					t.Fatal(e)
				}
			}
			once.Do(func() { close(release) })
			var result answer
			select {
			case result = <-done:
			case <-time.After(6 * time.Second):
				t.Fatal("App owner not joined")
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			var body struct {
				Results  []plugins.SiteHTTPResultV1
				Errors   []string
				Budget   plugins.HostCallBudgetSnapshot
				BudgetOK bool `json:"budget_ok"`
			}
			if e := json.Unmarshal(result.raw.Result, &body); e != nil {
				t.Fatal(e)
			}
			if len(body.Results) != 40 || len(body.Errors) != 44 || !body.BudgetOK || body.Budget.HostCallsCharged != 44 || result.raw.HostCalls != 44 {
				t.Fatalf("real phase charges=%d %+v", result.raw.HostCalls, body.Budget)
			}
			for i, r := range body.Results {
				if body.Errors[i] != "" {
					t.Fatal("scope should be typed response", i, body.Errors[i])
				}
				if revoke {
					if !r.ScopeChanged || r.Response == nil || string(r.Response.Content) != "owned-parallel" || r.RequestState != "completed" {
						t.Fatal("scope denial swallowed or selectedPOST replayed", i, r)
					}
				} else if r.Response == nil || string(r.Response.Content) != "owned-parallel" {
					t.Fatal("bad native result", i, r)
				}
			}
			for _, e := range body.Errors[40:] {
				if e != "" {
					t.Fatal("concurrent Base failed", e)
				}
			}
			// These are actual private SQLite rows, not success inferred from HTTP200.
			for i := 0; i < 2; i++ {
				key := []string{"parallel-0", "parallel-1", "parallel-2", "parallel-3"}[i]
				_, e := a.pluginStore.Data(context.Background(), p.installation.InstanceID, key)
				if e != nil {
					t.Fatal("Base persisted read", key, e)
				}
			}
			t.Logf("actual independent installed worker/App/SQLite + ownedHTTP: peak40 selectedPOST40,44RPC; revoke=%v", revoke)
		})
	}
}

// These independent guard callbacks deterministically reject once inside the
// native request path then become valid again, modeling a transient revocation.
// They are deliberately separate from the green parallel acceptance tests.
func TestWorkAuditTransientSiteRequestGuardMustNotBecomeVendorNone(t *testing.T) {
	a, _, p := ownedSiteHTTP(t)
	var hits atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("must not enter")) }))
	defer peer.Close()
	ownedSiteHTTPConfig(t, a, peer.URL, "", "")
	var checks atomic.Int64
	scope := originalBaseCallScope{object: p, installation: p.installation, validate: func(context.Context) error {
		if checks.Add(1) == 2 {
			return bad("OWNED_REVOKED", "owned transient authority revoked")
		}
		return nil
	}}
	raw, _ := json.Marshal(plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/owned", Method: "POST"})
	out, e := a.originalHostSiteHTTPResource(context.Background(), scope, raw)
	if e == nil {
		t.Fatalf("transient scope refusal became ordinary vendor result: guard_checks=%d actualHTTP=%d raw=%s", checks.Load(), hits.Load(), out)
	}
	if hits.Load() != 0 {
		t.Fatal("refused request entered owned peer")
	}
}
func TestWorkAuditTransientOCRGuardMustNotBecomeVendorNone(t *testing.T) {
	var checks, hits atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer peer.Close()
	target, _ := url.Parse(peer.URL)
	out, e := originalOCRRequest(context.Background(), http.MethodPost, target, http.Header{}, []byte("owned"), func(context.Context) error {
		if checks.Add(1) == 2 {
			return bad("OWNED_REVOKED", "owned transient authority revoked")
		}
		return nil
	}, 1024)
	if e == nil {
		t.Fatalf("transient OCR scope refusal became vendor None: guard_checks=%d actualHTTP=%d response_nil=%v", checks.Load(), hits.Load(), out == nil)
	}
}
