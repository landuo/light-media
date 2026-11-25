package iyuuautoseed

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func retryFixtureTransport(t *testing.T, origin string, enabled bool) *APITransport {
	t.Helper()
	tr, err := NewAPITransport(TrustedAPIBinding{Token: "owned-retry-fixture-token"}, APITransportOptions{LoopbackOrigin: origin, Environment: []string{}, Guard: func(context.Context) error { return nil }, Timeout: 120 * time.Millisecond, TransientRetry: enabled})
	if err != nil {
		t.Fatal(err)
	}
	tr.retryDelay = 2 * time.Millisecond
	t.Cleanup(tr.Close)
	return tr
}

func retryFixtureRequest(op Operation) APIRequest {
	switch op {
	case SitesIndex:
		return APIRequest{Operation: op}
	case ReportExisting:
		return APIRequest{Operation: op, Params: map[string]any{"sid_list": []any{json.Number("1"), json.Number("2")}}}
	default:
		return APIRequest{Operation: ReseedIndex, Params: map[string]any{"hash": `["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]`, "sha1": "synthetic-signature", "sid_sha1": "synthetic-sites", "timestamp": json.Number("1234567"), "version": "8.2.0"}}
	}
}

func closeRetryFixture(w http.ResponseWriter, t *testing.T, wire string) {
	t.Helper()
	c, b, e := w.(http.Hijacker).Hijack()
	if e != nil {
		t.Error(e)
		return
	}
	defer c.Close()
	if wire != "" {
		fmt.Fprint(b, wire)
		if e = b.Flush(); e != nil {
			t.Error(e)
		}
	}
}

func TestAPITransportRetryReadStallRebuildsConnectionAndPayload(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			var calls atomic.Int64
			var mu sync.Mutex
			var bodies [][]byte
			var peers []string
			firstClosed := make(chan struct{})
			const decoded = `{"code":0,"data":{"recovered":true}}`
			var encoded bytes.Buffer
			gz := gzip.NewWriter(&encoded)
			gz.Write([]byte(decoded))
			gz.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				body, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				mu.Lock()
				bodies = append(bodies, body)
				peers = append(peers, r.RemoteAddr)
				mu.Unlock()
				if r.Header.Get("token") != "owned-retry-fixture-token" || r.URL.Path != "/reseed/index/index" {
					t.Error("retry changed fixed request authority or operation")
				}
				if n == 1 {
					if phase == "body" {
						fmt.Fprint(w, `{"partial":`)
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
					close(firstClosed)
					return
				}
				select {
				case <-firstClosed:
				case <-time.After(time.Second):
					t.Error("first request was not closed before retry completed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Encoding", "gzip")
				w.Write(encoded.Bytes())
			}))
			defer server.Close()
			tr := retryFixtureTransport(t, server.URL, true)
			var events []apiRetryEvent
			tr.onRetry = func(_ context.Context, event apiRetryEvent) { events = append(events, event) }
			res, e := tr.Call(t.Context(), retryFixtureRequest(ReseedIndex))
			if e != nil || res == nil || string(res.Body) != decoded || calls.Load() != 2 {
				t.Fatalf("recovery failed: calls=%d response=%v err=%v", calls.Load(), res, e)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) || len(peers) != 2 || peers[0] == peers[1] {
				t.Fatal("retry did not preserve exact serialized payload on a fresh TCP connection")
			}
			if len(events) != 1 || events[0].Operation != ReseedIndex || events[0].Attempt != 2 || events[0].Kind != "timeout" {
				t.Fatal("missing safe retry classification")
			}
		})
	}
}

func TestAPITransportRetryClosedOperationSet(t *testing.T) {
	for _, op := range []Operation{SitesIndex, ReportExisting, ReseedIndex} {
		t.Run(string(op), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					closeRetryFixture(w, t, "")
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{}}`)
			}))
			defer server.Close()
			tr := retryFixtureTransport(t, server.URL, true)
			response, e := tr.Call(t.Context(), retryFixtureRequest(op))
			if e != nil || response == nil || calls.Load() != 2 {
				t.Fatalf("validated operation did not recover: calls=%d err=%v", calls.Load(), e)
			}
		})
	}
}

func TestAPITransportRetryTLSHandshakeStall(t *testing.T) {
	var hellos, connections, requests atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		if hellos.Add(1) == 1 {
			time.Sleep(360 * time.Millisecond) // Controlled origin TLS stall, not an HTTP stall.
		}
		return nil, nil
	}}
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	tr := retryFixtureTransport(t, server.URL, true)
	tr.rootCAs = x509.NewCertPool()
	tr.rootCAs.AddCert(server.Certificate())
	response, err := tr.Call(t.Context(), retryFixtureRequest(SitesIndex))
	if err != nil || response == nil || hellos.Load() != 2 || connections.Load() != 2 || requests.Load() != 1 {
		t.Fatalf("TLS stall did not recover before HTTP: hellos=%d conns=%d HTTP=%d err=%v", hellos.Load(), connections.Load(), requests.Load(), err)
	}
}

func TestAPITransportRetrySourceDefaultAndSuccess(t *testing.T) {
	for _, row := range []struct {
		name          string
		enabled, fail bool
		want          int64
	}{{"default-preserves-one-attempt", false, true, 1}, {"host-success-is-one-attempt", true, false, 1}} {
		t.Run(row.name, func(t *testing.T) {
			var calls, events atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if row.fail {
					closeRetryFixture(w, t, "")
					return
				}
				fmt.Fprint(w, `{}`)
			}))
			defer server.Close()
			tr := retryFixtureTransport(t, server.URL, row.enabled)
			tr.retryDelay = time.Hour
			tr.onRetry = func(context.Context, apiRetryEvent) { events.Add(1) }
			res, e := tr.Call(t.Context(), retryFixtureRequest(SitesIndex))
			if e != nil || (res == nil) != row.fail || calls.Load() != row.want || events.Load() != 0 {
				t.Fatal("default compatibility or success request count changed")
			}
		})
	}
}

func TestAPITransportRetryExhaustionPropagatesThroughHelper(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); closeRetryFixture(w, t, "") }))
	defer server.Close()
	tr := retryFixtureTransport(t, server.URL, true)
	h, e := NewHelper(tr, nil)
	if e != nil {
		t.Fatal(e)
	}
	h.sidSHA1 = "owned-synthetic-site-state"
	result, e := h.SeedInfo(t.Context(), []string{strings.Repeat("a", 40)})
	var exhausted *APITransientRetryError
	if !errors.As(e, &exhausted) || exhausted.Operation != ReseedIndex || exhausted.Attempts != 2 || exhausted.Kind != "eof" || calls.Load() != 2 || result.Failure != nil || result.Data != nil {
		t.Fatalf("network exhaustion became a business empty result: calls=%d result=%+v err=%v", calls.Load(), result, e)
	}
	if errors.Unwrap(e) != nil || strings.Contains(e.Error(), server.URL) || strings.Contains(e.Error(), tr.token) {
		t.Fatal("public retry exhaustion retains private transport details")
	}
}

func TestAPITransportRetryRejectsSemanticAndDecoderFailures(t *testing.T) {
	var gzipGood bytes.Buffer
	gz := gzip.NewWriter(&gzipGood)
	gz.Write([]byte(`{}`))
	gz.Close()
	truncatedGzip := gzipGood.Bytes()[:gzipGood.Len()-5]
	var expandedLimit bytes.Buffer
	limitWriter := gzip.NewWriter(&expandedLimit)
	limitWriter.Write(bytes.Repeat([]byte(" "), maxAPIResponse+1))
	limitWriter.Close()
	for _, row := range []struct {
		name     string
		status   int
		encoding string
		body     []byte
		wantKind string
		limit    bool
	}{
		{"http-400", 400, "", []byte(`{"code":0}`), "http", false},
		{"http-503", 503, "", []byte(`{"code":0}`), "http", false},
		{"api-code-400", 200, "", []byte(`{"code":400,"msg":"synthetic rejected"}`), "api", false},
		{"invalid-json", 200, "", []byte(`{bad`), "json", false},
		{"gzip-format", 200, "gzip", []byte("not-gzip"), "no_response", false},
		{"gzip-decoder-unexpected-eof", 200, "gzip", truncatedGzip, "no_response", false},
		{"body-limit", 200, "", bytes.Repeat([]byte(" "), maxAPIResponse+1), "", true},
		{"decoded-body-limit", 200, "gzip", expandedLimit.Bytes(), "", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			var calls, events atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if row.encoding != "" {
					w.Header().Set("Content-Encoding", row.encoding)
				}
				w.WriteHeader(row.status)
				w.Write(row.body)
			}))
			defer server.Close()
			tr := retryFixtureTransport(t, server.URL, true)
			tr.timeout = time.Second
			tr.retryDelay = time.Hour
			tr.onRetry = func(context.Context, apiRetryEvent) { events.Add(1) }
			h, e := NewHelper(tr, nil)
			if e != nil {
				t.Fatal(e)
			}
			result, e := h.request(t.Context(), retryFixtureRequest(SitesIndex))
			if row.limit {
				if !errors.Is(e, errAPIResponseLimit) {
					t.Fatalf("response limit lost: %v", e)
				}
			} else if e != nil || result.Failure == nil || result.Failure.Kind != row.wantKind {
				t.Fatalf("source failure semantics changed: %+v %v", result, e)
			}
			if calls.Load() != 1 || events.Load() != 0 {
				t.Fatal("HTTP, business or decoder failure was retried")
			}
		})
	}
}

func TestAPITransportRetryWireTruncationNotDecodedTruncation(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			closeRetryFixture(w, t, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	tr := retryFixtureTransport(t, server.URL, true)
	res, e := tr.Call(t.Context(), retryFixtureRequest(SitesIndex))
	if e != nil || res == nil || string(res.Body) != "{}" || calls.Load() != 2 {
		t.Fatal("actual wire Content-Length truncation was not recovered")
	}
}

func TestAPITransportRetryCancellationAndRevocation(t *testing.T) {
	for _, mode := range []string{"cancel-backoff", "deadline-backoff", "revoke-before-backoff", "revoke-during-backoff"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			var revoked atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); closeRetryFixture(w, t, "") }))
			defer server.Close()
			tr := retryFixtureTransport(t, server.URL, true)
			tr.retryDelay = time.Hour
			tr.guard = func(context.Context) error {
				if revoked.Load() {
					return errors.New("owned revocation")
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "deadline-backoff" {
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
			}
			scheduled := make(chan struct{})
			tr.onRetry = func(context.Context, apiRetryEvent) {
				if mode == "revoke-before-backoff" {
					revoked.Store(true)
				}
				close(scheduled)
			}
			if mode == "revoke-during-backoff" {
				tr.retryDelay = 60 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { _, e := tr.Call(ctx, retryFixtureRequest(SitesIndex)); done <- e }()
			select {
			case <-scheduled:
			case <-time.After(2 * time.Second):
				t.Fatal("retry was not scheduled")
			}
			start := time.Now()
			if mode == "cancel-backoff" {
				time.Sleep(10 * time.Millisecond)
				cancel()
			}
			if mode == "revoke-during-backoff" {
				time.Sleep(10 * time.Millisecond)
				revoked.Store(true)
			}
			select {
			case e := <-done:
				if mode == "cancel-backoff" && !errors.Is(e, context.Canceled) {
					t.Fatalf("cancel lost: %v", e)
				}
				if mode == "deadline-backoff" && !errors.Is(e, context.DeadlineExceeded) {
					t.Fatalf("deadline lost: %v", e)
				}
				if strings.HasPrefix(mode, "revoke") && (e == nil || e.Error() != "IYUU owner no longer authorized") {
					t.Fatalf("guard revocation lost: %v", e)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("cancellation/revocation waited for full retry backoff")
			}
			if calls.Load() != 1 || time.Since(start) > 500*time.Millisecond {
				t.Fatal("retry sent token after cancellation or revoked owner")
			}
		})
	}
}

func TestAPITransportRetryNoHTTPStatusRetryOnBodyTimeout(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		fmt.Fprint(w, "{")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	tr := retryFixtureTransport(t, server.URL, true)
	tr.retryDelay = time.Hour
	res, e := tr.Call(t.Context(), retryFixtureRequest(SitesIndex))
	if e != nil || res != nil || calls.Load() != 1 {
		t.Fatal("HTTP error body stall was retried or changed source behavior")
	}
}

func TestAPITransportRetryTLSAndProxyDenials(t *testing.T) {
	for _, hostname := range []bool{false, true} {
		t.Run(fmt.Sprint("tls-hostname-", hostname), func(t *testing.T) {
			var connections atomic.Int64
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid certificate reached application HTTP") }))
			if hostname {
				seed := httptest.NewTLSServer(http.NotFoundHandler())
				cert := seed.TLS.Certificates[0]
				tmpl := *seed.Certificate()
				seed.Close()
				tmpl.IPAddresses = nil
				tmpl.DNSNames = []string{"other.invalid"}
				der, e := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, tmpl.PublicKey, cert.PrivateKey)
				if e != nil {
					t.Fatal(e)
				}
				cert.Certificate = [][]byte{der}
				srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			}
			srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					connections.Add(1)
				}
			}
			srv.StartTLS()
			defer srv.Close()
			tr := retryFixtureTransport(t, srv.URL, true)
			tr.retryDelay = time.Hour
			if hostname {
				tr.rootCAs = x509.NewCertPool()
				tr.rootCAs.AddCert(srv.Certificate())
			}
			res, e := tr.Call(t.Context(), retryFixtureRequest(SitesIndex))
			if res != nil || e == nil || e.Error() != "IYUU TLS verification failed" || connections.Load() != 1 {
				t.Fatal("certificate/hostname failure was retried or verification weakened")
			}
		})
	}
	for _, status := range []int{403, 407} {
		t.Run(fmt.Sprint("proxy-", status), func(t *testing.T) {
			var calls atomic.Int64
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "CONNECT" || r.Header.Get("token") != "" {
					t.Error("CONNECT leaked origin token")
				}
				w.WriteHeader(status)
			}))
			defer proxy.Close()
			tr, e := NewAPITransport(TrustedAPIBinding{Token: "owned-token"}, APITransportOptions{Guard: func(context.Context) error { return nil }, Environment: []string{"https_proxy=" + proxy.URL}, TransientRetry: true})
			if e != nil {
				t.Fatal(e)
			}
			tr.retryDelay = time.Hour
			res, e := tr.Call(t.Context(), retryFixtureRequest(SitesIndex))
			if e != nil || res != nil || calls.Load() != 1 {
				t.Fatal("proxy refusal/authentication was retried")
			}
		})
	}
}

func TestAPITransientFailureWhitelist(t *testing.T) {
	for _, row := range []struct {
		name string
		err  error
		want string
	}{
		{"read-timeout", &net.OpError{Op: "read", Err: &net.DNSError{IsTimeout: true}}, "timeout"},
		{"header-eof", io.EOF, "eof"}, {"body-truncated", io.ErrUnexpectedEOF, "eof"}, {"reset", syscall.ECONNRESET, "reset"}, {"pipe", syscall.EPIPE, "broken_pipe"},
		{"cancel", context.Canceled, ""}, {"deadline-not-idle", context.DeadlineExceeded, ""}, {"limit", errAPIResponseLimit, ""}, {"redirect", errAPIRedirectDenied, ""}, {"certificate", x509.HostnameError{}, ""}, {"permanent", errors.New("owned permanent error"), ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			wrapped := &url.Error{Op: "owned", URL: "https://owned.invalid/private", Err: row.err}
			if got := apiTransientFailureKind(wrapped); got != row.want {
				t.Fatalf("class=%q want=%q", got, row.want)
			}
		})
	}
}

func TestAPITransportRetryNeverSendsInvalidOrInitiallyDeniedRequest(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	tr := retryFixtureTransport(t, server.URL, true)
	for _, request := range []APIRequest{{Operation: Operation("arbitrary")}, {Operation: SitesIndex, Params: map[string]any{"token": "synthetic-extra"}}, {Operation: ReportExisting}, {Operation: ReseedIndex}} {
		if response, err := tr.Call(t.Context(), request); response != nil || err == nil {
			t.Fatal("invalid operation reached retry path")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := tr.Call(ctx, retryFixtureRequest(SitesIndex)); !errors.Is(err, context.Canceled) {
		t.Fatal("initial cancellation changed")
	}
	tr.guard = func(context.Context) error { return errors.New("owned initial denial") }
	if _, err := tr.Call(t.Context(), retryFixtureRequest(SitesIndex)); err == nil {
		t.Fatal("initial owner denial ignored")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid or denied call sent a request")
	}
}

func TestAPITransportRetryHostOnlyAndCurrentRunLog(t *testing.T) {
	caller := orderedIYUUHost(t, iyuuSDKStep{method: sdk.NetworkProxyReadCapability, reply: string(mustJSONProxy(t, sdk.NetworkProxySettingsV1{SchemaVersion: 1, Environment: []string{}, Proxies: map[string]string{}}))})
	var oldLogs atomic.Int64
	initCtx := WithSourceLogSink(t.Context(), func(context.Context, SourceLog) { oldLogs.Add(1) })
	tr, e := newHostAPITransport(initCtx, caller, "owned-host-retry-token", func(context.Context) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if !tr.transientRetry || tr.retryDelay != 2*time.Second || tr.onRetry == nil {
		t.Fatal("SDK host did not enable bounded policy and default backoff")
	}
	plain, e := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{Guard: func(context.Context) error { return nil }})
	if e != nil {
		t.Fatal(e)
	}
	if plain.transientRetry || plain.onRetry != nil {
		t.Fatal("standalone transport gained host reliability policy")
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			closeRetryFixture(w, t, "")
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	tr.origin = server.URL
	tr.retryDelay = 2 * time.Millisecond
	var logs []SourceLog
	runCtx := WithSourceLogSink(t.Context(), func(_ context.Context, log SourceLog) { logs = append(logs, log) })
	res, e := tr.Call(runCtx, retryFixtureRequest(SitesIndex))
	if e != nil || res == nil || oldLogs.Load() != 0 || len(logs) != 1 || logs[0].Level != "WARN" || logs[0].Line != 0 || !strings.Contains(logs[0].Message, "尝试2/2") || strings.Contains(logs[0].Message, tr.token) || strings.Contains(logs[0].Message, server.URL) {
		t.Fatal("host retry warning used stale run context, forged source line, or leaked private values")
	}
}
