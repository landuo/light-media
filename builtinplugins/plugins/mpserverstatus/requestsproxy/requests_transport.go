package requestsproxy

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RequestsTransport retains the caller's TLS/dial/redirect policies while
// applying the installed requests/PySocks environment routing semantics. Each
// cached transport belongs only to this client and immutable proxy endpoint.
type RequestsTransport struct {
	mu            sync.Mutex
	base          *http.Transport
	environment   func() []string
	routes        map[string]*http.Transport
	merged        *requestsProxyMerge
	proxyRoots    *x509.CertPool
	originTLS     func(context.Context, net.Conn, string) (net.Conn, error)
	originPlain   func(net.Conn) net.Conn
	proxyResponse func(net.Conn, *http.Request) (net.Conn, error)
}

func NewRequestsTransport(base *http.Transport, environment func() []string) *RequestsTransport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	if environment == nil {
		environment = os.Environ
	}
	return &RequestsTransport{base: base.Clone(), environment: environment, routes: map[string]*http.Transport{}}
}

type requestsTransportError struct{ cause error }

// ErrRequestsProxyAuthEncoding is the source's escaping UnicodeEncodeError,
// before any network request. Its fixed text never contains proxy userinfo.
var ErrRequestsProxyAuthEncoding = errors.New("requests_proxy_auth_unicode_encode_error")

func (e requestsTransportError) Error() string { return "requests_proxy_transport_error" }
func (e requestsTransportError) Unwrap() error { return e.cause }
func (t *RequestsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("requests_proxy_missing_request")
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	proxy, err := t.requestProxy(req.URL)
	if err != nil {
		return nil, err
	}
	key := ""
	if proxy != nil {
		key = proxy.String()
	}
	t.mu.Lock()
	route := t.routes[key]
	if route == nil {
		route, err = requestsRoute(t.base, proxy, t.proxyRoots, t.originTLS, t.originPlain, t.proxyResponse)
		if err == nil {
			t.routes[key] = route
		}
	}
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// net/http deliberately detaches a pending dial from a request cancel so
	// another request can reuse it. requests/PySocks owns its operation instead;
	// preserve that original request context across proxy/TLS handshakes.
	binding := &requestsDialBinding{ctx: req.Context()}
	defer binding.release()
	request := req.Clone(context.WithValue(req.Context(), requestsDialBindingKey{}, binding))
	response, err := route.RoundTrip(request)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, requestsTransportError{err}
	}
	return response, nil
}
func (t *RequestsTransport) CloseIdleConnections() {
	t.mu.Lock()
	routes := make([]*http.Transport, 0, len(t.routes))
	for _, route := range t.routes {
		routes = append(routes, route)
	}
	t.mu.Unlock()
	for _, route := range routes {
		route.CloseIdleConnections()
	}
}
func requestsAuthLatin1(value string) (string, error) {
	bytes := make([]byte, 0, len(value))
	for _, r := range value {
		if r > 255 {
			return "", ErrRequestsProxyAuthEncoding
		}
		bytes = append(bytes, byte(r))
	}
	return string(bytes), nil
}
func requestsSOCKS4(t *http.Transport, proxy *url.URL) *http.Transport {
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	endpoint := proxy.Host
	if proxy.Port() == "" {
		endpoint = net.JoinHostPort(proxy.Hostname(), "1080")
	}
	remoteDNS := proxy.Scheme == "socks4a"
	username := ""
	if proxy.User != nil {
		username = proxy.User.Username()
	}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		port, err := strconv.ParseUint(portText, 10, 16)
		if err != nil {
			return nil, errors.New("requests_socks4_invalid_port")
		}
		if strings.ContainsRune(username, 0) || strings.ContainsRune(host, 0) {
			return nil, errors.New("requests_socks4_invalid_field")
		}
		var ip net.IP
		if numeric, ok := requestsIPv4(host); ok {
			ip = net.IPv4(byte(numeric>>24), byte(numeric>>16), byte(numeric>>8), byte(numeric))
		}
		domain := ""
		if ip == nil {
			if remoteDNS {
				ip = net.IPv4(0, 0, 0, 1)
				domain = host
			} else {
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, errors.New("requests_socks4_dns_empty")
				}
				ip = ips[0]
			}
		}
		ip = ip.To4()
		if ip == nil {
			return nil, errors.New("requests_socks4_ipv4_required")
		}
		conn, err := dial(ctx, network, endpoint)
		if err != nil {
			return nil, err
		}
		success := false
		defer func() {
			if !success {
				conn.Close()
			}
		}()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		if deadline, ok := ctx.Deadline(); ok {
			conn.SetDeadline(deadline)
		}
		request := []byte{4, 1, 0, 0, ip[0], ip[1], ip[2], ip[3]}
		binary.BigEndian.PutUint16(request[2:4], uint16(port))
		request = append(request, []byte(username)...)
		request = append(request, 0)
		if domain != "" {
			request = append(request, []byte(domain)...)
			request = append(request, 0)
		}
		if _, err = io.Copy(conn, strings.NewReader(string(request))); err != nil {
			return nil, err
		}
		var response [8]byte
		if _, err = io.ReadFull(conn, response[:]); err != nil {
			return nil, err
		}
		if response[0] != 0 || response[1] != 0x5a {
			return nil, errors.New("requests_socks4_rejected")
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		conn.SetDeadline(time.Time{})
		success = true
		return conn, nil
	}
	// Otherwise a caller's custom TLS dialer would bypass the selected proxy.
	t.DialTLS = nil
	t.DialTLSContext = nil
	return t
}

// SetHTTPSProxyRoots is a per-client test/system trust input, never a verifier
// override. nil uses system roots. It cannot disable proxy verification.
// Set before using the transport; no process-global roots are changed.
func (t *RequestsTransport) SetHTTPSProxyRoots(roots *x509.CertPool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if roots == nil {
		t.proxyRoots = nil
	} else {
		t.proxyRoots = roots.Clone()
	}
}

// SetSourceOriginWrappers supplies this fixed probe's plaintext header meter
// and source destination TLS. The independently verified HTTPS proxy policy
// stays inside this helper. No SDK/user input can install these functions.
func (t *RequestsTransport) SetSourceOriginWrappers(tlsWrapper func(context.Context, net.Conn, string) (net.Conn, error), plainWrapper func(net.Conn) net.Conn, proxyReader func(net.Conn, *http.Request) (net.Conn, error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.originTLS = tlsWrapper
	t.originPlain = plainWrapper
	t.proxyResponse = proxyReader
}
