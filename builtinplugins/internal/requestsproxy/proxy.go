// Package networkproxy implements MoviePilot's explicit PROXY mapping without
// changing process globals. Configuration and transports are immutable snapshots.
package requestsproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

type Config struct{ HTTP, HTTPS string }

func (Config) String() string   { return "proxy configuration [redacted]" }
func (Config) GoString() string { return "networkproxy.Config{[redacted]}" }

// Resolve follows core/config.py PROXY: a nonblank setting wins for both
// schemes, otherwise uppercase precedes lowercase and either environment
// scheme fills the missing other scheme. NO_PROXY applies to the separate
// requests trust-env path, not to this explicitly supplied proxy dictionary.
func Resolve(setting string, getenv func(string) string) (Config, error) {
	first := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(getenv(name)); value != "" {
				return value
			}
		}
		return ""
	}
	c := Config{}
	if value := strings.TrimSpace(setting); value != "" {
		c.HTTP, c.HTTPS = value, value
	} else if getenv != nil {
		c.HTTP, c.HTTPS = first("HTTP_PROXY", "http_proxy"), first("HTTPS_PROXY", "https_proxy")
		if c.HTTP == "" {
			c.HTTP = c.HTTPS
		}
		if c.HTTPS == "" {
			c.HTTPS = c.HTTP
		}
	}
	for _, value := range []string{c.HTTP, c.HTTPS} {
		if value != "" {
			if _, err := Parse(value); err != nil {
				return Config{}, err
			}
		}
	}
	return c, nil
}

func Parse(raw string) (*url.URL, error) {
	if len(raw) > 8192 {
		return nil, errors.New("proxy_invalid_url")
	}
	for _, c := range raw {
		if c <= 32 || c == 127 {
			return nil, errors.New("proxy_invalid_url")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("proxy_invalid_url")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy_invalid_scheme")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("proxy_invalid_port")
		}
	}
	return u, nil
}

func Endpoint(raw string) string {
	u, err := Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

func (c Config) URL(target *url.URL) string {
	if target != nil && target.Scheme == "https" {
		return c.HTTPS
	}
	return c.HTTP
}

type routedTransport struct{ http, https *http.Transport }

// Keep the public diagnostic fixed while retaining the internal network class
// required by source-compatible idempotent request retries.
type routedTransportError struct{ cause error }

func (e routedTransportError) Error() string    { return "proxy_transport_error" }
func (e routedTransportError) GoString() string { return "proxy_transport_error" }
func (e routedTransportError) Unwrap() error    { return e.cause }

func (r *routedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t := r.http
	if req.URL.Scheme == "https" {
		t = r.https
	}
	response, err := t.RoundTrip(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, routedTransportError{cause: err}
	}
	return response, nil
}
func (r *routedTransport) CloseIdleConnections() {
	r.http.CloseIdleConnections()
	if r.https != r.http {
		r.https.CloseIdleConnections()
	}
}

// Transport clones the caller's TLS and dial policy. SOCKS5h keeps destination
// DNS remote; SOCKS5 explicitly resolves it locally, matching requests/PySocks.
// No package-global default transport or environment cache is mutated.
func (c Config) Transport(base *http.Transport) (http.RoundTripper, error) {
	a, err := Apply(base, c.HTTP)
	if err != nil {
		return nil, err
	}
	b := a
	if c.HTTPS != c.HTTP {
		b, err = Apply(base, c.HTTPS)
		if err != nil {
			a.CloseIdleConnections()
			return nil, err
		}
	}
	return &routedTransport{http: a, https: b}, nil
}

func Apply(base *http.Transport, raw string) (*http.Transport, error) {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	t := base.Clone()
	t.Proxy = nil
	if raw == "" {
		return t, nil
	}
	u, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "socks5" {
		t.Proxy = http.ProxyURL(u)
		return t, nil
	}
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	address := u.Host
	if u.Port() == "" {
		address = net.JoinHostPort(u.Hostname(), "1080")
	}
	var auth *proxy.Auth
	if u.User != nil {
		password, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: password}
	}
	socks, err := proxy.SOCKS5("tcp", address, auth, contextDialer{dial})
	if err != nil {
		return nil, errors.New("proxy_invalid_url")
	}
	contextSocks, ok := socks.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("proxy_context_dialer_required")
	}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if net.ParseIP(host) == nil {
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				conn, err := contextSocks.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
			if last != nil {
				return nil, last
			}
			return nil, errors.New("proxy_dns_empty")
		}
		return contextSocks.DialContext(ctx, network, address)
	}
	// A custom TLS dialer would bypass the SOCKS connection for HTTPS.
	t.DialTLSContext = nil
	t.DialTLS = nil
	return t, nil
}

type contextDialer struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d contextDialer) Dial(network, address string) (net.Conn, error) {
	return d.dial(context.Background(), network, address)
}
func (d contextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.dial(ctx, network, address)
}
