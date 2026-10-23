package networkproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestsMergedSourcePriorities(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      []string
		explicit map[string]string
		want     string
	}{
		{"environment", []string{"http_proxy=http://env.invalid"}, nil, "env.invalid"},
		{"explicit scheme", []string{"http_proxy=http://env.invalid"}, map[string]string{"http": "http://explicit.invalid"}, "explicit.invalid"},
		{"environment host outranks explicit scheme", []string{"http://owned.invalid_proxy=http://host.invalid"}, map[string]string{"http": "http://explicit.invalid"}, "host.invalid"},
		{"explicit host outranks environment host", []string{"http://owned.invalid_proxy=http://env.invalid"}, map[string]string{"http://owned.invalid": "http://host.invalid"}, "host.invalid"},
		{"scheme outranks all host", []string{"http_proxy=http://scheme.invalid"}, map[string]string{"all://owned.invalid": "http://host.invalid"}, "scheme.invalid"},
		{"all host outranks all", []string{"all_proxy=http://all.invalid"}, map[string]string{"all://owned.invalid": "http://host.invalid"}, "host.invalid"},
		{"no proxy suppresses environment", []string{"http_proxy=http://env.invalid", "NO_PROXY=*"}, nil, ""},
		{"no proxy retains explicit", []string{"http_proxy=http://env.invalid", "NO_PROXY=*"}, map[string]string{"http": "http://explicit.invalid"}, "explicit.invalid"},
		{"empty explicit suppresses scheme fallback", []string{"http_proxy=http://env.invalid", "all_proxy=http://all.invalid"}, map[string]string{"http": ""}, ""},
		{"explicit no proxy overrides environment", []string{"http_proxy=http://env.invalid", "NO_PROXY=*"}, map[string]string{"no_proxy": "other.invalid"}, "env.invalid"},
		{"explicit empty no proxy overrides environment", []string{"http_proxy=http://env.invalid", "NO_PROXY=*"}, map[string]string{"no_proxy": ""}, "env.invalid"},
		{"explicit no proxy suppresses env not explicit", []string{"http_proxy=http://env.invalid"}, map[string]string{"no_proxy": "owned.invalid", "all": "http://explicit.invalid"}, "explicit.invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := mustAuthURL(t, "http://owned.invalid:8123/test")
			transport, err := NewRequestsProxyTransport(nil, tc.env, tc.explicit, target)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.CloseIdleConnections()
			proxy, err := transport.requestProxy(target)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if proxy != nil {
				got = proxy.Host
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRequestsMergedSnapshotAndSuccessiveRoutes(t *testing.T) {
	env := []string{"http_proxy=http://env.invalid", "https_proxy=http://tls.invalid", "NO_PROXY=start.invalid,skip.invalid", "http://later.invalid_proxy=http://host.invalid"}
	explicit := map[string]string{"all://start.invalid": "http://start.invalid"}
	initial := mustAuthURL(t, "http://start.invalid/")
	transport, err := NewRequestsProxyTransport(nil, env, explicit, initial)
	if err != nil {
		t.Fatal(err)
	}
	env[0] = "http_proxy=http://changed.invalid"
	explicit["http"] = "http://changed.invalid"
	initial.Host = "changed.invalid"
	for _, tc := range []struct{ target, want string }{
		{"http://start.invalid/", "start.invalid"},
		{"http://later.invalid/", "env.invalid"}, // redirect imports only scheme, not host keys
		{"http://skip.invalid/", "env.invalid"},  // NO_PROXY cannot clear existing scheme
		{"https://later.invalid/", "tls.invalid"},
		{"https://skip.invalid/", "tls.invalid"},
	} {
		proxy, err := transport.requestProxy(mustAuthURL(t, tc.target))
		if err != nil {
			t.Fatal(err)
		}
		if proxy == nil || proxy.Host != tc.want {
			t.Fatalf("route mismatch for %s", tc.target)
		}
	}
	if explicit["all://start.invalid"] != "http://start.invalid" || len(explicit) != 2 {
		t.Fatal("caller map mutated")
	}
	if _, err := NewRequestsProxyTransport(nil, nil, nil, nil); err == nil {
		t.Fatal("nil initial URL accepted")
	}
}

func TestRequestsMergedOwnedRedirectAndAuth(t *testing.T) {
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { originCalls.Add(1); w.WriteHeader(500) }))
	defer origin.Close()
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("fake:pass")) {
			t.Error("proxy authentication missing")
		}
		if proxyCalls.Add(1) == 1 {
			http.Redirect(w, r, origin.URL+"/next", 302)
			return
		}
		w.WriteHeader(204)
	}))
	defer proxy.Close()
	parsed := mustAuthURL(t, proxy.URL)
	parsed.User = url.UserPassword("fake", "pass")
	// The first merge includes scheme; redirect into a NO_PROXY destination
	// must still use that existing proxy rather than contacting the origin.
	transport, err := NewRequestsProxyTransport(nil, []string{"http_proxy=" + parsed.String(), "NO_PROXY=127.0.0.1"}, map[string]string{"http": parsed.String()}, mustAuthURL(t, "http://owned.invalid/start"))
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get("http://owned.invalid/start")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 || proxyCalls.Load() != 2 || originCalls.Load() != 0 {
		t.Fatal("redirect proxy route or status wrong")
	}
}

func TestRequestsMergedFailuresNeverDirect(t *testing.T) {
	var calls atomic.Int32
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		calls.Add(1)
		if address != "proxy.invalid:8123" {
			t.Error("attempted direct route")
		}
		return nil, errors.New("owned proxy failure")
	}
	for _, tc := range []struct {
		proxy string
		want  error
		dials int32
	}{
		{"http://proxy.invalid:8123", nil, 1},
		{"http://%E4%B8%AD:fake@proxy.invalid:8123", ErrRequestsProxyAuthEncoding, 0},
		{"unsupported://proxy.invalid:8123", nil, 0},
		{"://synthetic-secret", nil, 0},
	} {
		calls.Store(0)
		target := mustAuthURL(t, "http://origin.invalid/")
		transport, err := NewRequestsProxyTransport(base, nil, map[string]string{"http": tc.proxy}, target)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("GET", target.String(), nil)
		_, err = transport.RoundTrip(req)
		transport.CloseIdleConnections()
		if err == nil || tc.want != nil && !errors.Is(err, tc.want) || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("wrong sanitized failure: %v", err)
		}
		if calls.Load() != tc.dials {
			t.Fatal("wrong dial count")
		}
	}
}

func TestRequestsMergedCancellationClosesActualHandshake(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks4", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			entered, closed := make(chan struct{}), make(chan struct{})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(4 * time.Second))
				var first [1]byte
				if _, err = io.ReadFull(conn, first[:]); err != nil {
					return
				}
				close(entered)
				_, err = io.Copy(io.Discard, conn)
				if err == nil {
					close(closed)
				} // EOF proves caller closed, not our test deadline
			}()
			target := mustAuthURL(t, "https://127.0.0.1:8123/test")
			transport, err := NewRequestsProxyTransport(nil, nil, map[string]string{"https": scheme + "://" + listener.Addr().String()}, target)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.CloseIdleConnections()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
			done := make(chan error, 1)
			go func() { _, err := transport.RoundTrip(req); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("handshake never started")
			}
			cancel()
			select {
			case err = <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("request stuck")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("actual socket not closed by cancellation")
			}
		})
	}
}

func TestRequestsMergedPreservesBaseAndClosesIdle(t *testing.T) {
	closed := make(chan struct{}, 1)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	origin.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	origin.StartTLS()
	defer origin.Close()
	base := origin.Client().Transport.(*http.Transport).Clone()
	base.MaxIdleConns = 7
	base.MaxConnsPerHost = 3
	base.ResponseHeaderTimeout = time.Second
	base.Proxy = func(*http.Request) (*url.URL, error) { return nil, errors.New("base policy not used") }
	initial := mustAuthURL(t, origin.URL)
	transport, err := NewRequestsProxyTransport(base, nil, nil, initial)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	// Mutating the caller's TLS config after construction cannot disable the
	// cloned trust roots or change the transport snapshot's limits.
	base.MaxConnsPerHost = 19
	if transport.base.MaxConnsPerHost != 3 || transport.base.MaxIdleConns != 7 || transport.base.ResponseHeaderTimeout != time.Second || transport.base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("base policy snapshot lost")
	}
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("TLS response wrong")
	}
	transport.CloseIdleConnections()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("CloseIdleConnections did not close owned route")
	}
	if _, err = base.Proxy(nil); err == nil {
		t.Fatal("caller proxy policy changed")
	}
}
