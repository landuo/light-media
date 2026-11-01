package app

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

	"light-media/internal/networkproxy"
)

type originalRequestUtilsTestRoundTrip func(*http.Request) (*http.Response, error)

func (f originalRequestUtilsTestRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// No constructor listener with an IPv6 fallback; no upstream client, CONNECT,
// forwarding, environment rewrite, UID discovery or production App startup.
func originalRequestUtilsOwnedTLS(t *testing.T, handler http.Handler) *httptest.Server {
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

func originalRequestUtilsTestApp(proxy string) *App {
	empty := ""
	return &App{config: Settings{ProxyHost: proxy, PluginServerURL: &empty}}
}

func TestOriginalRequestUtilsPrivateTLSBaseIsolation(t *testing.T) {
	original := http.DefaultTransport.(*http.Transport)
	originalTLS := original.TLSClientConfig
	originalSkip := originalTLS != nil && originalTLS.InsecureSkipVerify
	first, second := originalPluginSourceTLSBase(), originalPluginSourceTLSBase()
	defer first.CloseIdleConnections()
	defer second.CloseIdleConnections()
	if first == original || second == original || first == second || first.TLSClientConfig == nil || second.TLSClientConfig == nil || first.TLSClientConfig == second.TLSClientConfig || !first.TLSClientConfig.InsecureSkipVerify || !second.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("source TLS templates are not private")
	}
	first.TLSClientConfig.InsecureSkipVerify = false
	if !second.TLSClientConfig.InsecureSkipVerify || http.DefaultTransport != original || original.TLSClientConfig != originalTLS || (originalTLS != nil && originalTLS.InsecureSkipVerify) != originalSkip {
		t.Fatal("TLS policy leaked across templates or default transport")
	}
	// Construct A and two independent strict generic clients without dispatch.
	// The original B server hook and Wiki both retain the generic strict policy.
	a := originalRequestUtilsTestApp("")
	material, err := a.originalPluginMaterialHTTPClient(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer material.CloseIdleConnections()
	strictSecond, err := a.originalPluginHTTPClient(time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	defer strictSecond.CloseIdleConnections()
	strict, err := a.originalPluginHTTPClient(time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	defer strict.CloseIdleConnections()
	if material.Transport == strictSecond.Transport || strict.Transport == strictSecond.Transport || material.Transport == strict.Transport {
		t.Fatal("consumer transports unexpectedly shared")
	}
}

func TestOriginalRequestUtilsOwnedHTTPSMaterial(t *testing.T) {
	var calls atomic.Int32
	peer := originalRequestUtilsOwnedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.IsAbs() || (r.URL.Path != "/index" && r.URL.Path != "/received503") || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("fixed owned HTTPS endpoint rejected unknown request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		calls.Add(1)
		if r.URL.Path == "/received503" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = io.WriteString(w, "owned-response")
	}))
	a := originalRequestUtilsTestApp("")
	material, err := a.originalPluginMaterialHTTPClient(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer material.CloseIdleConnections()
	request := func(ctx context.Context, path string) *http.Request {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, peer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Resolve with the exact production requests environment resolver. If
		// the current environment no longer directly authorizes this owned
		// endpoint via its existing NO_PROXY, stop BEFORE any dispatch. Never
		// modify env, select a substitute route, retry direct, or use CONNECT.
		proxy, err := networkproxy.RequestsProxy(r)
		if err != nil || proxy != nil {
			t.Fatal("owned endpoint blocked by inherited proxy route; no request sent")
		}
		return r
	}
	for _, row := range []struct {
		path   string
		status int
	}{{"/index", 200}, {"/received503", 503}} {
		response, err := material.Do(request(context.Background(), row.path))
		if err != nil {
			t.Fatal("owned target HTTPS failed", err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != row.status || string(body) != "owned-response" {
			t.Fatal("received response changed", response.StatusCode, readErr)
		}
	}
	// Exact existing strict environment transport, same inherited route. This
	// is a strict-template negative control, NOT a Wiki/B real request claim.
	strict := &http.Client{Transport: originalPluginEnvironmentTransport(), Timeout: 3 * time.Second}
	defer strict.CloseIdleConnections()
	response, err := strict.Do(request(context.Background(), "/index"))
	if response != nil {
		response.Body.Close()
	}
	var untrusted x509.UnknownAuthorityError
	if err == nil || !errors.As(err, &untrusted) || calls.Load() != 2 {
		t.Fatal("strict template changed or unexpected HTTP reached owned endpoint", calls.Load(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err = material.Do(request(ctx, "/index"))
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, context.Canceled) || calls.Load() != 2 {
		t.Fatal("canceled request reached owned endpoint", calls.Load(), err)
	}
}

type originalRequestUtilsTestBody struct {
	io.Reader
	closed bool
}

func (b *originalRequestUtilsTestBody) Close() error { b.closed = true; return nil }

func TestOriginalRequestUtilsFallbackSourceResponses(t *testing.T) {
	for _, status := range []int{200, 400, 503} {
		body := &originalRequestUtilsTestBody{Reader: strings.NewReader("received")}
		response := &http.Response{StatusCode: status, Body: body}
		fallbackCalls := 0
		transport := &originalPluginFallbackTransport{
			preferred: originalRequestUtilsTestRoundTrip(func(*http.Request) (*http.Response, error) { return response, nil }),
			fallback: originalRequestUtilsTestRoundTrip(func(*http.Request) (*http.Response, error) {
				fallbackCalls++
				return nil, errors.New("unexpected fallback")
			}),
		}
		request, _ := http.NewRequest(http.MethodGet, "http://owned.invalid/recording-only", nil)
		got, err := transport.RoundTrip(request)
		if err != nil || got != response || body.closed || fallbackCalls != 0 {
			t.Fatal("HTTP response must reach caller unchanged", status, err)
		}
		got.Body.Close()
	}
	body := &originalRequestUtilsTestBody{Reader: strings.NewReader("incomplete")}
	first := &http.Response{StatusCode: 200, Body: body}
	last := &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}
	fallbackCalls := 0
	transport := &originalPluginFallbackTransport{
		preferred: originalRequestUtilsTestRoundTrip(func(*http.Request) (*http.Response, error) { return first, io.EOF }),
		fallback: originalRequestUtilsTestRoundTrip(func(*http.Request) (*http.Response, error) {
			fallbackCalls++
			if !body.closed {
				t.Error("failed response body not closed before fallback")
			}
			return last, nil
		}),
	}
	request, _ := http.NewRequest(http.MethodGet, "http://owned.invalid/recording-only", nil)
	got, err := transport.RoundTrip(request)
	if err != nil || got != last || fallbackCalls != 1 || !body.closed {
		t.Fatal("transport exception fallback changed", err)
	}
	got.Body.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request = request.WithContext(ctx)
	canceledBody := &originalRequestUtilsTestBody{Reader: strings.NewReader("caller-owned")}
	canceledResponse := &http.Response{StatusCode: 200, Body: canceledBody}
	transport.preferred = originalRequestUtilsTestRoundTrip(func(*http.Request) (*http.Response, error) { return canceledResponse, io.EOF })
	got, err = transport.RoundTrip(request)
	if got != canceledResponse || err != io.EOF || fallbackCalls != 1 || canceledBody.closed {
		t.Fatal("canceled request entered fallback", err)
	}
	got.Body.Close()
}
