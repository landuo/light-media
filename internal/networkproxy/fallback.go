package networkproxy

import (
	"net/http"
	"sync"
	"time"
)

// A deployment behind a transparent proxy often also carries a vendor proxy in
// its environment, and that vendor endpoint can die while the transparent path
// keeps working. Failing every request after a five-second dial timeout makes
// the whole product look broken, so a configured proxy falls back to a direct
// connection when it cannot be reached, and remembers the failure so the next
// requests go straight out instead of waiting again.
const (
	// proxyFailureCooldown skips a proxy that just refused or timed out.
	proxyFailureCooldown = 60 * time.Second
	// proxyFailureThreshold is one transport failure: a dead endpoint fails
	// the same way on every attempt, so a single sample is enough to step
	// aside for the cooldown.
	proxyFailureThreshold = 1
)

// fallbackTransport tries the proxy first and, on a transport failure of an
// idempotent request, retries once directly. Business responses (including
// 4xx/5xx) are never retried: only the proxy being unreachable is handled.
type fallbackTransport struct {
	proxy  http.RoundTripper
	direct http.RoundTripper

	mu        sync.Mutex
	failures  int
	skipUntil time.Time
}

func newFallbackTransport(proxy, direct http.RoundTripper) http.RoundTripper {
	if proxy == nil {
		return direct
	}
	return &fallbackTransport{proxy: proxy, direct: direct}
}

// proxySkipped reports whether the proxy is in its cooldown window.
func (t *fallbackTransport) proxySkipped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.skipUntil.IsZero() && time.Now().Before(t.skipUntil)
}

// proxyUsable reports whether the proxy should be tried now. An expired
// cooldown re-arms it: the next request probes the proxy again, and a success
// clears the failure count.
func (t *fallbackTransport) proxyUsable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.skipUntil.IsZero() {
		return t.failures < proxyFailureThreshold
	}
	if time.Now().Before(t.skipUntil) {
		return false
	}
	t.failures = 0
	t.skipUntil = time.Time{}
	return true
}

func (t *fallbackTransport) recordProxyFailure() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures++
	if t.failures >= proxyFailureThreshold {
		t.skipUntil = time.Now().Add(proxyFailureCooldown)
	}
}

func (t *fallbackTransport) recordProxySuccess() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures = 0
	t.skipUntil = time.Time{}
}

// replayable reports whether the request can be sent twice without changing
// an outcome the caller did not ask for twice.
func replayable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return req.Body == nil || req.Body == http.NoBody
	}
	return false
}

func (t *fallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.proxyUsable() {
		return t.direct.RoundTrip(req)
	}
	res, err := t.proxy.RoundTrip(req)
	if err == nil {
		t.recordProxySuccess()
		return res, nil
	}
	if !replayable(req) {
		return res, err
	}
	// The proxy could not carry this request. Step aside and try the direct
	// path while the transparent route handles the traffic.
	directRes, directErr := t.direct.RoundTrip(req.Clone(req.Context()))
	if directErr != nil {
		// Both paths failed: report the proxy error, which is the actionable
		// one for an operator who configured that endpoint.
		return res, err
	}
	t.recordProxyFailure()
	return directRes, nil
}

func (t *fallbackTransport) CloseIdleConnections() {
	if closer, ok := t.proxy.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	if closer, ok := t.direct.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
