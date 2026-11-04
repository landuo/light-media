package networkproxy

import (
	"errors"
	"net/http"
	"net/url"
)

// RequestsProxySession owns a private route pool, not global proxy settings.
// Each logical request obtains its own merge state; redirects share only that
// request's state. Pool reuse does not retain an earlier environment snapshot.
type RequestsProxySession struct{ pool *RequestsTransport }

func NewRequestsProxySession(base *http.Transport) (*RequestsProxySession, error) {
	if base == nil {
		return nil, errors.New("requests_session_missing_private_transport")
	}
	return &RequestsProxySession{pool: NewRequestsTransport(base, func() []string { return nil })}, nil
}

// NewRequestTransport must be called once for each independent request, not for
// each redirect. Inputs are copied by the existing source-derived merge helper.
// It neither changes environment/base.Proxy nor grants a direct fallback.
func (s *RequestsProxySession) NewRequestTransport(environment []string, explicit map[string]string, initialURL *url.URL) (http.RoundTripper, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("requests_session_missing_pool")
	}
	selector, err := NewRequestsProxyTransport(s.pool.base, environment, explicit, initialURL)
	if err != nil {
		return nil, err
	}
	return &requestsSessionRequest{pool: s.pool, selector: selector}, nil
}

// Closing a Requests Session closes idle adapters; it does not make the source
// object permanently unusable. The pool remains private and can redial later.
func (s *RequestsProxySession) CloseIdleConnections() {
	if s != nil && s.pool != nil {
		s.pool.CloseIdleConnections()
	}
}

type requestsSessionRequest struct {
	pool     *RequestsTransport
	selector *RequestsTransport
}

func (r *requestsSessionRequest) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("requests_proxy_missing_request")
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	proxy, err := r.selector.requestProxy(req.URL)
	if err != nil {
		return nil, err
	}
	// The entire route lookup, context-bound dial, cancellation cleanup and exact
	// error wrapper remain the existing implementation, not a parallel HTTP stack.
	return r.pool.roundTripProxy(req, proxy)
}
