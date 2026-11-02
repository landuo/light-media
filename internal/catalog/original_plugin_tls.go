package catalog

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"sync"

	"light-media/internal/logging"
	"light-media/internal/networkproxy"
)

// This marker can only be created by trusted Go host code. No JSON/header or
// user-provided URL enables it; it changes TLS policy, never request authority.
type originalPluginHTTPSKey struct{}

func OriginalPluginContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, originalPluginHTTPSKey{}, true)
}

func originalPluginHTTPS(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(originalPluginHTTPSKey{}).(bool)
	return enabled
}

// The pool belongs to the existing Client/Explorer, not to a second catalog
// Client. Limiter, inflight, next/blocked, provider caches and credentials remain
// on the original object. Its factory is immutable before publication.
type originalPluginHTTPPool struct {
	mu              sync.Mutex
	factory         func() (http.RoundTripper, error)
	suppliedByOwner bool
	client          *http.Client
}

func (p *originalPluginHTTPPool) forContext(ctx context.Context, ordinary *http.Client) (*http.Client, error) {
	if !originalPluginHTTPS(ctx) || p.suppliedByOwner {
		return ordinary, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return p.client, nil
	}
	if p.factory == nil || ordinary == nil {
		return nil, errors.New("plugin_https_transport_unavailable")
	}
	transport, err := p.factory()
	if err != nil {
		return nil, err
	}
	// Preserve all HTTP policy fields (Timeout, Jar and redirect handler).
	client := *ordinary
	client.Transport = logging.Transport(transport)
	p.client = &client
	return p.client, nil
}

func (p *originalPluginHTTPPool) closeIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		p.client.CloseIdleConnections()
	}
}

func originalPluginTLSClone(base *http.Transport) *http.Transport {
	transport := base.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	// User-authorized only for current original-plugin HTTPS consumers.
	transport.TLSClientConfig.InsecureSkipVerify = true
	return transport
}

func originalPluginBaseFactory(base http.RoundTripper) func() (http.RoundTripper, error) {
	// Do not reinterpret an opaque transport or silently discard its route.
	transport, ok := base.(*http.Transport)
	if !ok || transport == nil {
		return nil
	}
	return func() (http.RoundTripper, error) { return originalPluginTLSClone(transport), nil }
}

func originalPluginProxyFactory(config networkproxy.Config) func() (http.RoundTripper, error) {
	// Config.Transport owns separate HTTP/HTTPS clones and keeps the original
	// explicit proxy mapping, including an explicitly empty route.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return nil
	}
	return func() (http.RoundTripper, error) {
		return config.Transport(originalPluginTLSClone(base))
	}
}

// NewOriginalPluginExplorer retains strict ordinary calls. Only the native
// fallback's OriginalPluginContext selects the owned private TLS pool.
func NewOriginalPluginExplorer(tmdb *Client, config networkproxy.Config) (*Explorer, error) {
	transport, err := config.Transport(nil)
	if err != nil {
		return nil, err
	}
	e := NewExplorer(tmdb, transport)
	e.pluginHTTP.suppliedByOwner = false
	e.pluginHTTP.factory = originalPluginProxyFactory(config)
	return e, nil
}
