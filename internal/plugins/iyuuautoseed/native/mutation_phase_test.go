package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type phaseMutationCall struct {
	name string
	call func(*IYUUClient, context.Context) (string, error)
}

func phaseMutationCalls() []phaseMutationCall {
	return []phaseMutationCall{
		{"add", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.AddPaused(ctx, IYUUAddSpec{Magnet: "magnet:?xt=urn:btih:" + nativeHash})
			return r.Outcome, e
		}},
		{"remove-tag", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.RemoveTag(ctx, nativeHash, "temporary")
			return r.Outcome, e
		}},
		{"category", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.SetCategory(ctx, nativeHash, "movie")
			return r.Outcome, e
		}},
		{"recheck", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.Recheck(ctx, []string{nativeHash})
			return r.Outcome, e
		}},
		{"start", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.Start(ctx, []string{nativeHash})
			return r.Outcome, e
		}},
	}
}
func phaseClient(t *testing.T, config Config, options IYUUOptions) *IYUUClient {
	t.Helper()
	c, e := NewIYUUClient(config, options)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	return c
}

func TestIYUUDispatchProofCannotBeForgedOrAggregated(t *testing.T) {
	c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:1"}, IYUUOptions{})
	_, proof := c.RemoveTag(context.Background(), "bad-hash", "temporary")
	if !MutationNotDispatched(proof) || MutationFailureClass(proof) != "native" {
		t.Fatal("missing local proof", proof)
	}
	var underlying *Error
	if !errors.As(proof, &underlying) || underlying.Code != "invalid_hash" {
		t.Fatal("safe error cause lost", proof)
	}
	wire, _ := json.Marshal(proof)
	var fromJSON Error
	_ = json.Unmarshal(wire, &fromJSON)
	for _, e := range []error{nil, &fromJSON, &Error{Operation: "mutation", Code: "not_dispatched"}, &Error{Operation: "request", Code: "http_401"}, fmt.Errorf("wrapped: %w", proof), errors.Join(proof, errors.New("another unknown mutation")), &mutationNotDispatched{cause: errors.New("forged")}, &mutationNotDispatched{cause: errors.New("forged"), seal: &mutationProofSeal{marker: 1}}, &mutationNotDispatched{cause: errors.New("forged"), seal: nativeMutationProofSeal, class: 99}} {
		if MutationNotDispatched(e) || MutationFailureClass(e) != "" {
			t.Fatalf("forged/aggregate proof %T", e)
		}
	}
	var zero *mutationNotDispatched
	if MutationNotDispatched(zero) {
		t.Fatal("nil concrete proof accepted")
	}
}

func TestIYUUDispatchAllPublicMutationLocalRejections(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("local validation reached network")
	}))
	defer server.Close()
	c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{})
	cases := []phaseMutationCall{
		{"add-uri", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.AddPaused(ctx, IYUUAddSpec{Magnet: "https://unsupported.invalid"})
			return r.Outcome, e
		}},
		{"add-ambiguous", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.AddPaused(ctx, IYUUAddSpec{Magnet: "magnet:?x=1", Torrent: []byte("synthetic")})
			return r.Outcome, e
		}},
		{"add-invalid-utf8", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.AddPaused(ctx, IYUUAddSpec{Magnet: "magnet:?x=1", Tags: []string{string([]byte{0xff})}})
			return r.Outcome, e
		}},
		{"remove-hash", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.RemoveTag(ctx, "bad", "temporary")
			return r.Outcome, e
		}},
		{"remove-tag", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.RemoveTag(ctx, nativeHash, "bad,tag")
			return r.Outcome, e
		}},
		{"category-hash", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.SetCategory(ctx, "other:"+nativeHash, "movie")
			return r.Outcome, e
		}},
		{"recheck-hash", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.Recheck(ctx, []string{"bad"})
			return r.Outcome, e
		}},
		{"start-empty", func(c *IYUUClient, ctx context.Context) (string, error) {
			r, e := c.Start(ctx, nil)
			return r.Outcome, e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, e := tc.call(c, context.Background())
			if out != "rejected" || !MutationNotDispatched(e) || MutationFailureClass(e) != "native" {
				t.Fatalf("outcome=%s proof=%v class=%s err=%v", out, MutationNotDispatched(e), MutationFailureClass(e), e)
			}
		})
	}
	for _, tc := range phaseMutationCalls() {
		t.Run(tc.name+"-cancelled", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			out, e := tc.call(c, ctx)
			if out != "rejected" || !MutationNotDispatched(e) || MutationFailureClass(e) != "cancelled" || !errors.Is(e, context.Canceled) {
				t.Fatalf("outcome=%s proof=%v class=%s err=%v", out, MutationNotDispatched(e), MutationFailureClass(e), e)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal(requests.Load())
	}
}

func TestIYUUDispatchQBNoAuthenticationMutationRetry(t *testing.T) {
	for _, tc := range phaseMutationCalls() {
		for _, mode := range []string{"login-rejected", "401-login-would-fail", "403-second-guard-would-fail", "http-400", "http-429", "http-500", "disconnect", "body-read-failure", "ok"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				var writes, logins, guards atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v2/auth/login" {
						n := logins.Add(1)
						if mode == "login-rejected" || mode == "401-login-would-fail" && n > 1 {
							http.Error(w, "synthetic credential body", 401)
							return
						}
						http.SetCookie(w, &http.Cookie{Name: "SID", Value: "synthetic", Path: "/"})
						io.WriteString(w, "Ok.")
						return
					}
					if !strings.HasPrefix(r.URL.Path, "/api/v2/torrents/") {
						t.Errorf("unexpected %s", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					writes.Add(1)
					switch mode {
					case "401-login-would-fail":
						w.WriteHeader(401)
					case "403-second-guard-would-fail":
						w.WriteHeader(403)
					case "http-400":
						w.WriteHeader(400)
					case "http-429":
						w.WriteHeader(429)
					case "http-500":
						w.WriteHeader(500)
					case "disconnect":
						conn, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						_ = conn.Close()
					case "body-read-failure":
						w.Header().Set("Content-Length", "99")
						w.Header().Set("Connection", "close")
						io.WriteString(w, "O")
					default:
						io.WriteString(w, "Ok.")
					}
				}))
				defer server.Close()
				c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL, Username: "owned", Password: "synthetic"}, IYUUOptions{Guard: func(context.Context) error {
					guards.Add(1)
					if mode == "403-second-guard-would-fail" && writes.Load() > 0 {
						return errors.New("revoked after first selected dispatch")
					}
					return nil
				}})
				c.webVersion = "2.11.0"
				out, e := tc.call(c, context.Background())
				if mode == "login-rejected" {
					if out != "rejected" || !MutationNotDispatched(e) || writes.Load() != 0 || logins.Load() != 1 {
						t.Fatalf("preflight outcome=%s err=%v writes=%d login=%d", out, e, writes.Load(), logins.Load())
					}
					return
				}
				if writes.Load() != 1 || logins.Load() != 1 || guards.Load() != 2 {
					t.Fatalf("unexpected retry writes=%d login=%d guard=%d", writes.Load(), logins.Load(), guards.Load())
				}
				if mode == "ok" {
					if e != nil || out != "confirmed" {
						t.Fatalf("outcome=%s err=%v", out, e)
					}
					return
				}
				if e == nil || out != "unknown" || MutationNotDispatched(e) || MutationFailureClass(e) != "" {
					t.Fatalf("issued mutation outcome=%s proof=%v class=%s err=%v", out, MutationNotDispatched(e), MutationFailureClass(e), e)
				}
				if strings.Contains(e.Error(), "synthetic credential body") {
					t.Fatal("remote credential body escaped safe error")
				}
			})
		}
	}
}

func TestIYUUDispatchReadOnlyPreflightAndGuardAreDistinct(t *testing.T) {
	for _, action := range []string{"add", "start"} {
		for _, mode := range []string{"version-http-failure", "version-bad-body", "first-guard", "selected-guard", "cancel-at-selected-guard"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				var reads, writes, guards atomic.Int32
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v2/app/webapiVersion" {
						reads.Add(1)
						if mode == "version-http-failure" {
							w.WriteHeader(500)
							return
						}
						if mode == "version-bad-body" {
							io.WriteString(w, "not-a-version")
							return
						}
						io.WriteString(w, "2.11.0")
						return
					}
					writes.Add(1)
					io.WriteString(w, "Ok.")
				}))
				defer server.Close()
				c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{Guard: func(context.Context) error {
					n := guards.Add(1)
					if mode == "first-guard" || mode == "selected-guard" && n > 1 {
						return errors.New("local owner rejected")
					}
					if mode == "cancel-at-selected-guard" && n > 1 {
						cancel()
					}
					return nil
				}})
				var tc phaseMutationCall
				for _, x := range phaseMutationCalls() {
					if x.name == action {
						tc = x
					}
				}
				out, e := tc.call(c, ctx)
				wantClass := "native"
				if strings.Contains(mode, "guard") {
					wantClass = "owner_denied"
				}
				if mode == "cancel-at-selected-guard" {
					wantClass = "cancelled"
				}
				if e == nil || out != "rejected" || !MutationNotDispatched(e) || MutationFailureClass(e) != wantClass || writes.Load() != 0 {
					t.Fatalf("preflight outcome=%s proof=%v class=%s writes=%d reads=%d err=%v", out, MutationNotDispatched(e), MutationFailureClass(e), writes.Load(), reads.Load(), e)
				}
			})
		}
	}
}

func TestIYUUDispatchTransmissionBothDialectsAndNo409Retry(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, tc := range phaseMutationCalls() {
			if tc.name == "remove-tag" || tc.name == "category" {
				continue
			}
			for _, mode := range []string{"preflight-409-then-guard", "preflight-500", "selected-guard", "selected-409", "selected-409-would-guard-fail", "rpc-error", "wrong-response-id", "invalid-json", "missing-added-object", "body-read-failure", "ok"} {
				if mode == "missing-added-object" && tc.name != "add" {
					continue
				}
				t.Run(fmt.Sprintf("legacy-%v/%s/%s", legacy, tc.name, mode), func(t *testing.T) {
					var reads, writes, guards atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var req map[string]any
						if json.NewDecoder(r.Body).Decode(&req) != nil {
							t.Error("invalid RPC request")
							return
						}
						method, _ := req["method"].(string)
						method = strings.ReplaceAll(method, "-", "_")
						if legacy && req["jsonrpc"] != nil {
							reply(w, map[string]any{"arguments": map[string]any{}, "result": "method name not recognized"})
							return
						}
						respond := func(values map[string]any) {
							if legacy {
								reply(w, map[string]any{"result": "success", "tag": req["tag"], "arguments": values})
							} else {
								reply(w, map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": values})
							}
						}
						if method == "session_get" {
							reads.Add(1)
							if mode == "preflight-409-then-guard" {
								w.Header().Set("X-Transmission-Session-Id", "synthetic-session")
								w.WriteHeader(409)
								return
							}
							if mode == "preflight-500" {
								w.WriteHeader(500)
								return
							}
							respond(map[string]any{"version": "4.1"})
							return
						}
						writes.Add(1)
						switch mode {
						case "selected-409", "selected-409-would-guard-fail":
							w.Header().Set("X-Transmission-Session-Id", "new-session")
							w.WriteHeader(409)
						case "rpc-error":
							if legacy {
								reply(w, map[string]any{"tag": req["tag"], "result": "rejected", "arguments": map[string]any{}})
							} else {
								reply(w, map[string]any{"jsonrpc": "2.0", "id": req["id"], "error": map[string]any{"code": -1, "message": "synthetic remote"}})
							}
						case "wrong-response-id":
							if legacy {
								reply(w, map[string]any{"tag": 999, "result": "success", "arguments": map[string]any{}})
							} else {
								reply(w, map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{}})
							}
						case "invalid-json":
							io.WriteString(w, "{")
						case "body-read-failure":
							w.Header().Set("Content-Length", "99")
							w.Header().Set("Connection", "close")
							io.WriteString(w, "{")
						case "missing-added-object":
							respond(map[string]any{})
						default:
							if tc.name == "add" {
								key, hashKey := "torrent_added", "hash_string"
								if legacy {
									key, hashKey = "torrent-added", "hashString"
								}
								respond(map[string]any{key: map[string]any{hashKey: nativeHash}})
							} else {
								respond(map[string]any{})
							}
						}
					}))
					defer server.Close()
					c := phaseClient(t, Config{ID: "owned-phase", Kind: "transmission", BaseURL: server.URL + "/transmission/rpc"}, IYUUOptions{Guard: func(context.Context) error {
						n := guards.Add(1)
						if mode == "preflight-409-then-guard" && n > 1 || mode == "selected-guard" && reads.Load() > 0 || mode == "selected-409-would-guard-fail" && writes.Load() > 0 {
							return errors.New("local owner denied")
						}
						return nil
					}})
					out, e := tc.call(c, context.Background())
					pre := mode == "preflight-409-then-guard" || mode == "preflight-500" || mode == "selected-guard"
					if pre {
						wantClass := "native"
						if mode != "preflight-500" {
							wantClass = "owner_denied"
						}
						if out != "rejected" || !MutationNotDispatched(e) || MutationFailureClass(e) != wantClass || writes.Load() != 0 {
							t.Fatalf("preflight outcome=%s proof=%v class=%s writes=%d err=%v", out, MutationNotDispatched(e), MutationFailureClass(e), writes.Load(), e)
						}
						return
					}
					if writes.Load() != 1 {
						t.Fatalf("selected RPC retried %d", writes.Load())
					}
					if mode == "ok" {
						if e != nil || out != "confirmed" {
							t.Fatalf("outcome=%s err=%v", out, e)
						}
						return
					}
					if e == nil || out != "unknown" || MutationNotDispatched(e) {
						t.Fatalf("issued RPC outcome=%s proof=%v err=%v", out, MutationNotDispatched(e), e)
					}
				})
			}
		}
	}
}

func TestIYUUDispatchPrivateRequestValidationAndMonotonicity(t *testing.T) {
	t.Run("JSON-body-validation", func(t *testing.T) {
		c := phaseClient(t, Config{ID: "owned-phase", Kind: "transmission", BaseURL: "http://127.0.0.1:1"}, IYUUOptions{})
		ctx, s := beginMutation(context.Background())
		_, e := c.client.trSelectedRPC(ctx, "torrent_add", map[string]any{"unsupported": make(chan int)}, "jsonrpc")
		out := ""
		e = s.finish(e, &out)
		if !MutationNotDispatched(e) || out != "rejected" {
			t.Fatalf("JSON validation %s %v", out, e)
		}
	})
	for _, mode := range []string{"URL", "header", "bearer-origin", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			var next atomic.Int32
			ctx, s := beginMutation(context.Background())
			ctx = mutationSelected(ctx)
			if mode == "cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://owned.invalid/api/v2/torrents/recheck", strings.NewReader("hashes="+nativeHash))
			if mode == "URL" {
				req.URL = &url.URL{Scheme: "ftp", Host: "owned.invalid"}
			}
			if mode == "header" {
				req.Header.Set("Authorization", "Bearer bad\x00value")
			}
			var transport http.RoundTripper = &mutationDispatchTransport{next: phaseRoundTrip(func(*http.Request) (*http.Response, error) { next.Add(1); return nil, errors.New("unexpected network") })}
			if mode == "bearer-origin" {
				transport = &iyuuBearer{next: transport, origin: "http://different.invalid", token: "synthetic"}
			}
			_, e := transport.RoundTrip(req)
			out := ""
			e = s.finish(e, &out)
			if !MutationNotDispatched(e) || out != "rejected" || next.Load() != 0 {
				t.Fatalf("validation outcome=%s proof=%v next=%d err=%v", out, MutationNotDispatched(e), next.Load(), e)
			}
		})
	}
	t.Run("earlier-selected-attempt-cannot-be-forgotten", func(t *testing.T) {
		ctx, s := beginMutation(context.Background())
		var attempts atomic.Int32
		transport := &iyuuGuard{guard: func(context.Context) error {
			if attempts.Load() > 0 {
				return errors.New("second attempt guard failure")
			}
			return nil
		}, next: &mutationDispatchTransport{next: phaseRoundTrip(func(r *http.Request) (*http.Response, error) {
			attempts.Add(1)
			if r.GetBody != nil {
				t.Error("selected request retained replay callback")
			}
			return nil, errors.New("first selected outcome unknown")
		})}}
		for i := 0; i < 2; i++ {
			req, _ := http.NewRequestWithContext(mutationSelected(ctx), http.MethodPost, "http://owned.invalid/mutation", strings.NewReader("nonempty"))
			_, e := transport.RoundTrip(req)
			out := ""
			e = s.finish(e, &out)
			if MutationNotDispatched(e) || out != "unknown" {
				t.Fatalf("attempt %d forgot prior selected dispatch: %s %v", i, out, e)
			}
		}
		if attempts.Load() != 1 {
			t.Fatal(attempts.Load())
		}
	})
	t.Run("unclassified-request-is-conservative", func(t *testing.T) {
		ctx, s := beginMutation(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://owned.invalid/mutation", strings.NewReader("nonempty"))
		transport := &mutationDispatchTransport{next: phaseRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("delegated") })}
		_, e := transport.RoundTrip(req)
		out := ""
		e = s.finish(e, &out)
		if MutationNotDispatched(e) || out != "unknown" {
			t.Fatal("untagged request falsely certified")
		}
	})
}

type phaseRoundTrip func(*http.Request) (*http.Response, error)

func (f phaseRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIYUUDispatchSourceNegativeAndScopeIsolation(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/app/webapiVersion" {
			io.WriteString(w, "2.11.0")
			return
		}
		writes.Add(1)
		io.WriteString(w, "Fails.")
	}))
	defer server.Close()
	var revoked atomic.Bool
	c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{Guard: func(context.Context) error {
		if revoked.Load() {
			return errors.New("owner denied")
		}
		return nil
	}})
	r, e := c.AddPaused(context.Background(), IYUUAddSpec{Magnet: "magnet:?xt=urn:btih:" + nativeHash})
	if e != nil || r.Accepted || !r.SourceTruthy || r.Outcome != "rejected" || MutationNotDispatched(e) || writes.Load() != 1 {
		t.Fatalf("source false tuple %+v %v", r, e)
	}
	revoked.Store(true)
	effect, e := c.Recheck(context.Background(), []string{nativeHash})
	if !MutationNotDispatched(e) || MutationFailureClass(e) != "owner_denied" || effect.Outcome != "rejected" || writes.Load() != 1 {
		t.Fatalf("new public call inherited old scope: %+v %v", effect, e)
	}
}

func TestIYUUDispatchConcurrentPublicCallsHaveIndependentScopes(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writes.Add(1); io.WriteString(w, "Ok.") }))
	defer server.Close()
	c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{})
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				r, e := c.Recheck(ctx, []string{nativeHash})
				if !MutationNotDispatched(e) || MutationFailureClass(e) != "cancelled" || r.Outcome != "rejected" {
					errs <- fmt.Errorf("cancel scope %+v %v", r, e)
				}
				return
			}
			r, e := c.Recheck(context.Background(), []string{nativeHash})
			if e != nil || r.Outcome != "confirmed" {
				errs <- fmt.Errorf("live scope %+v %v", r, e)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if writes.Load() != 8 {
		t.Fatal("wire request count", writes.Load())
	}
}

func TestIYUUDispatchEveryPublicGuardAndWaitingCancellation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("guard/local cancellation reached network")
	}))
	defer server.Close()
	c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{Guard: func(context.Context) error { return errors.New("trusted owner rejected") }})
	c.webVersion = "2.11.0"
	for _, tc := range phaseMutationCalls() {
		t.Run(tc.name, func(t *testing.T) {
			out, e := tc.call(c, context.Background())
			if !MutationNotDispatched(e) || MutationFailureClass(e) != "owner_denied" || out != "rejected" {
				t.Fatalf("outcome=%s class=%s err=%v", out, MutationFailureClass(e), e)
			}
		})
	}
	c.client.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	out, e := phaseMutationCalls()[3].call(c, ctx)
	cancel()
	c.client.mu.Unlock()
	if !MutationNotDispatched(e) || MutationFailureClass(e) != "cancelled" || !errors.Is(e, context.DeadlineExceeded) || out != "rejected" {
		t.Fatalf("lock wait outcome=%s class=%s err=%v", out, MutationFailureClass(e), e)
	}
	if requests.Load() != 0 {
		t.Fatal(requests.Load())
	}
}

func TestIYUUDispatchReadonlyNegotiationMayRetryWithoutSelectedReplay(t *testing.T) {
	t.Run("qb-read-auth-refresh", func(t *testing.T) {
		var reads, logins, writes atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				logins.Add(1)
				http.SetCookie(w, &http.Cookie{Name: "SID", Value: "synthetic", Path: "/"})
				io.WriteString(w, "Ok.")
			case "/api/v2/app/webapiVersion":
				if reads.Add(1) == 1 {
					w.WriteHeader(403)
					return
				}
				io.WriteString(w, "2.11.0")
			case "/api/v2/torrents/add":
				writes.Add(1)
				io.WriteString(w, "Ok.")
			default:
				t.Error("unexpected route", r.URL.Path)
			}
		}))
		defer server.Close()
		c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL, Username: "owned", Password: "synthetic"}, IYUUOptions{})
		r, e := c.AddPaused(context.Background(), IYUUAddSpec{Magnet: "magnet:?xt=urn:btih:" + nativeHash})
		if e != nil || r.Outcome != "confirmed" || reads.Load() != 2 || logins.Load() != 2 || writes.Load() != 1 {
			t.Fatalf("result=%+v err=%v reads=%d login=%d writes=%d", r, e, reads.Load(), logins.Load(), writes.Load())
		}
	})
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("tr-read-session-legacy-%v", legacy), func(t *testing.T) {
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q map[string]any
				if json.NewDecoder(r.Body).Decode(&q) != nil {
					t.Error("RPC request")
					return
				}
				if legacy && q["jsonrpc"] != nil {
					reply(w, map[string]any{"arguments": map[string]any{}, "result": "unknown method"})
					return
				}
				method, _ := q["method"].(string)
				method = strings.ReplaceAll(method, "-", "_")
				respond := func(value map[string]any) {
					if legacy {
						reply(w, map[string]any{"tag": q["tag"], "result": "success", "arguments": value})
					} else {
						reply(w, map[string]any{"jsonrpc": "2.0", "id": q["id"], "result": value})
					}
				}
				if method == "session_get" {
					reads.Add(1)
					if r.Header.Get("X-Transmission-Session-Id") != "synthetic-session" {
						w.Header().Set("X-Transmission-Session-Id", "synthetic-session")
						w.WriteHeader(409)
						return
					}
					respond(map[string]any{"version": "4.1"})
					return
				}
				writes.Add(1)
				respond(map[string]any{})
			}))
			defer server.Close()
			c := phaseClient(t, Config{ID: "owned-phase", Kind: "transmission", BaseURL: server.URL}, IYUUOptions{})
			r, e := c.Recheck(context.Background(), []string{nativeHash})
			if e != nil || r.Outcome != "confirmed" || reads.Load() != 2 || writes.Load() != 1 {
				t.Fatalf("result=%+v err=%v reads=%d writes=%d", r, e, reads.Load(), writes.Load())
			}
		})
	}
}

func TestIYUUDispatchResponseAndTransportErrorsAreNotLocalProof(t *testing.T) {
	t.Run("unsupported-source-added-id-object", func(t *testing.T) {
		var writes atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writes.Add(1)
			io.WriteString(w, `{"added_torrent_ids":1}`)
		}))
		defer server.Close()
		c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{})
		c.webVersion = "2.11.0"
		r, e := c.AddPaused(context.Background(), IYUUAddSpec{Magnet: "magnet:?xt=urn:btih:" + nativeHash})
		if e == nil || MutationNotDispatched(e) || r.Outcome != "unknown" || !r.SourceTruthy || writes.Load() != 1 {
			t.Fatalf("result=%+v err=%v writes=%d", r, e, writes.Load())
		}
	})
	t.Run("delegated-connection-failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		endpoint := server.URL
		server.Close()
		c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: endpoint}, IYUUOptions{})
		r, e := c.SetCategory(context.Background(), nativeHash, "movie")
		if e == nil || MutationNotDispatched(e) || r.Outcome != "unknown" {
			t.Fatalf("network error misclassified %+v %v", r, e)
		}
	})
	t.Run("credential-header-fails-before-network", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
		defer server.Close()
		c := phaseClient(t, Config{ID: "owned-phase", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{APIKey: "bad\x00value"})
		r, e := c.SetCategory(context.Background(), nativeHash, "movie")
		if !MutationNotDispatched(e) || MutationFailureClass(e) != "native" || r.Outcome != "rejected" || requests.Load() != 0 {
			t.Fatalf("header validation %+v %v requests=%d", r, e, requests.Load())
		}
	})
}
