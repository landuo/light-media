package networkproxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFallbackRetriesDirectOnProxyFailure(t *testing.T) {
	var proxied, directed int32
	proxy := roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&proxied, 1)
		return nil, errors.New("dial tcp 172.30.0.2:1082: connect: connection refused")
	})
	direct := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&directed, 1)
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		return rec.Result(), nil
	})
	transport := newFallbackTransport(proxy, direct)

	res, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.themoviedb.org/3/x", nil))
	if err != nil {
		t.Fatalf("an unreachable proxy must fall back, got %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if atomic.LoadInt32(&proxied) != 1 || atomic.LoadInt32(&directed) != 1 {
		t.Fatalf("proxy=%d direct=%d", proxied, directed)
	}
	// The failure is remembered: the next request goes straight out.
	if _, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.themoviedb.org/3/y", nil)); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&proxied) != 1 {
		t.Fatalf("a failed proxy must be skipped during cooldown, proxied=%d", proxied)
	}
	if atomic.LoadInt32(&directed) != 2 {
		t.Fatalf("direct=%d", directed)
	}
}

func TestFallbackNeverReplaysNonIdempotent(t *testing.T) {
	var directed int32
	proxy := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	direct := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&directed, 1)
		return httptest.NewRecorder().Result(), nil
	})
	transport := newFallbackTransport(proxy, direct)
	req := httptest.NewRequest(http.MethodPost, "https://api.themoviedb.org/3/x", nil)
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("a POST must surface the proxy failure instead of replaying")
	}
	if atomic.LoadInt32(&directed) != 0 {
		t.Fatalf("POST must not be replayed directly, directed=%d", directed)
	}
}

func TestFallbackKeepsBusinessResponses(t *testing.T) {
	var directed int32
	proxy := roundTripFunc(func(*http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusUnauthorized)
		return rec.Result(), nil
	})
	direct := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&directed, 1)
		return httptest.NewRecorder().Result(), nil
	})
	transport := newFallbackTransport(proxy, direct)
	res, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "https://api.themoviedb.org/3/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	concrete, ok := transport.(*fallbackTransport)
	if !ok {
		t.Fatalf("expected the fallback wrapper, got %T", transport)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a business status must pass through, got %d", res.StatusCode)
	}
	if atomic.LoadInt32(&directed) != 0 {
		t.Fatal("a 401 is not a proxy failure and must not be retried")
	}
	// A proxy that answers proves it is alive: no cooldown afterwards.
	if concrete.proxySkipped() {
		t.Fatal("a working proxy must not be skipped")
	}
}

func TestFallbackCooldownExpires(t *testing.T) {
	transport := &fallbackTransport{
		proxy:     roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("down") }),
		direct:    roundTripFunc(func(*http.Request) (*http.Response, error) { return httptest.NewRecorder().Result(), nil }),
		failures:  proxyFailureThreshold,
		skipUntil: time.Now().Add(-time.Second),
	}
	if transport.proxySkipped() {
		t.Fatal("an expired cooldown must retry the proxy")
	}
	if !transport.proxyUsable() {
		t.Fatal("expired cooldown must consider the proxy usable again")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
