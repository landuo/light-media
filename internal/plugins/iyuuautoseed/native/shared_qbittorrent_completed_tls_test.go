package download

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit tcp4 allocation: no automatic IPv6 listener fallback, proxy server,
// CONNECT, forwarding, upstream target, environment rewrite or real credentials.
func completedOwnedTLSPeer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler, ErrorLog: log.New(io.Discard, "", 0)}}
	peer.StartTLS()
	t.Cleanup(peer.Close)
	return peer
}

func TestQBittorrentCompletedPrivateTLSAndScope(t *testing.T) {
	global, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("requires the existing standard default transport")
	}
	// Standard Clone performs the original transport's lazy HTTP/2 setup.
	// Establish that permitted initialization before comparing policy identity;
	// the returned clone has never made a request or opened any connection.
	baselineClone := global.Clone()
	baselineClone.CloseIdleConnections()
	globalTLS := global.TLSClientConfig
	var globalRoots *x509.CertPool
	if globalTLS != nil {
		globalRoots = globalTLS.RootCAs
	}
	if globalTLS != nil && globalTLS.InsecureSkipVerify {
		t.Fatal("existing global transport is unexpectedly non-verifying")
	}
	var calls, otherCalls atomic.Int32
	var redirect, revoked atomic.Bool
	other := completedOwnedTLSPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCalls.Add(1)
		t.Error("completed query followed a different owned origin")
		w.WriteHeader(http.StatusForbidden)
	}))
	const reply = `[{"hash":"owned-row","progress":1,"amount_left":0,"state":"uploading","tags":"owned"}]`
	peer := completedOwnedTLSPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/torrents/info" || r.URL.RawQuery != "" || r.URL.IsAbs() || r.Header.Get("Authorization") != "Bearer owned-completed-key" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected method/path/origin/auth in owned completed request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
		if err != nil || string(body) != "filter=completed" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("source completed form changed", err, string(body))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if redirect.Load() {
			w.Header().Set("Location", other.URL+"/not-followed")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, reply)
	}))
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if revoked.Load() {
			return errors.New("owned completed grant revoked")
		}
		return nil
	}
	client, err := NewQBittorrentCompletedClient(Config{ID: "owned-completed", Kind: "qb", BaseURL: peer.URL}, "owned-completed-key", guard)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.native.client.http.Timeout != 20*time.Second || client.native.client.http.Jar == nil || client.native.client.http.CheckRedirect == nil {
		t.Fatal("existing timeout/session/redirect client policy changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := client.Rows(ctx, nil)
	if err != nil || string(raw) != reply || calls.Load() != 1 {
		t.Fatal("qB-only completed request failed against owned self-signed TLS", err, string(raw), calls.Load())
	}
	// Exact unchanged global transport is a real strict negative control on the
	// same owned endpoint and route. It must fail certificate verification before
	// the HTTP handler, not merely produce an unspecified transport error.
	strict := &http.Client{Transport: global, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, peer.URL+"/api/v2/torrents/info", strings.NewReader("filter=completed"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer owned-completed-key")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, strictErr := strict.Do(req)
	if response != nil {
		response.Body.Close()
	}
	var unknown x509.UnknownAuthorityError
	if !errors.As(strictErr, &unknown) || calls.Load() != 1 {
		t.Fatal("unchanged strict control did not reject owned certificate before HTTP", strictErr, calls.Load())
	}
	// The original mixed qB/TR factory is also left unchanged. No new TLS
	// policy is selected by calling that factory directly.
	old, err := NewIYUUClient(Config{ID: "owned-original-qb", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{APIKey: "owned-completed-key", Guard: guard})
	if err != nil {
		t.Fatal(err)
	}
	// Do not Close this unchanged old client's global pool as test cleanup.
	oldCtx, oldCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, oldErr := old.Torrents(oldCtx, nil, true)
	oldCancel()
	if oldErr == nil || calls.Load() != 1 {
		t.Fatal("old general IYUU client unexpectedly inherited completed TLS policy", oldErr, calls.Load())
	}
	redirect.Store(true)
	redirectCtx, redirectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = client.Rows(redirectCtx, nil)
	redirectCancel()
	if err == nil || calls.Load() != 2 || otherCalls.Load() != 0 {
		t.Fatal("redirect/credential boundary changed", err, calls.Load(), otherCalls.Load())
	}
	revoked.Store(true)
	if _, err = client.Rows(context.Background(), nil); err == nil || calls.Load() != 2 {
		t.Fatal("current owner guard did not prevent dispatch", err, calls.Load())
	}
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err = client.Rows(cancelled, nil); !errors.Is(err, context.Canceled) || calls.Load() != 2 {
		t.Fatal("caller cancellation did not prevent dispatch", err, calls.Load())
	}
	if http.DefaultTransport != global || global.TLSClientConfig != globalTLS || (globalTLS != nil && (globalTLS.InsecureSkipVerify || globalTLS.RootCAs != globalRoots)) {
		t.Fatal("global TLS/transport mutated")
	}
	if _, err = NewQBittorrentCompletedClient(Config{ID: "owned-tr", Kind: "transmission", BaseURL: peer.URL}, "", guard); err == nil {
		t.Fatal("qB-only constructor admitted Transmission")
	}
}
