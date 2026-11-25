package mpserverstatus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/mpserverstatus/requestsproxy"
)

// NativeNetwork is created for exactly one active worker call. Its host caller
// and the private environment snapshot never enter persistent Machine state.
// The worker only calls it with the original fixed, public, credential-free URL.
// Details use direct DNS/TLS, as the original socket probes do.
type NativeNetwork struct {
	caller sdk.HostCaller
	// Unit fixtures supply only owned loopback routing, never protocol input.
	environment *[]string
	dial        func(context.Context, string, string) (net.Conn, error)
	lookup      func(context.Context, string) ([]net.IPAddr, error)
	roots       *tls.Config
	proxyRoots  *x509.CertPool
}

func (n *NativeNetwork) log(ctx context.Context, level sdk.LogLevel, message string) error {
	if n.caller == nil {
		return nil
	}
	return (sdk.Logger{Host: n.caller}).Log(ctx, level, message)
}
func (n *NativeNetwork) Status(ctx context.Context, target string, timeout time.Duration) (*Response, error) {
	var environment []string
	if n.environment != nil {
		environment = append([]string{}, (*n.environment)...)
	} else {
		settings, e := (sdk.Network{Host: n.caller}).ProxySettings(ctx)
		if e != nil {
			return nil, HostBoundaryError{e}
		}
		// RequestUtils(proxies=None) uses requests trust_env. MoviePilot's explicit
		// Settings.PROXY dictionary is deliberately not passed by this plugin.
		environment = settings.Environment
	}
	return n.status(ctx, target, timeout, environment)
}
func (n *NativeNetwork) status(ctx context.Context, target string, timeout time.Duration, environment []string) (*Response, error) {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	u, e := url.Parse(target)
	if e != nil {
		return nil, e
	}
	if u.User != nil {
		return nil, HostBoundaryError{errors.New("source_probe_credentials_forbidden")}
	}
	base := requestsproxy.NewRequestsTimeoutTransport(timeout)
	// Only this source HTTP status request uses verify=False. The independent
	// certificate probe below retains system validation and SNI.
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}} // #nosec G402: exact uncredentialed source probe
	base.DisableCompression = true
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, e := n.connect(ctx, network, address, timeout)
		if e != nil {
			return nil, e
		}
		return &sourceIdleConn{Conn: connection, timeout: timeout}, nil
	}
	transport, e := requestsproxy.NewRequestsProxyTransport(base, environment, nil, u)
	if e != nil {
		return n.networkFailure(ctx, e, environment...)
	}
	transport.SetHTTPSProxyRoots(n.proxyRoots)
	transport.SetSourceOriginWrappers(func(ctx context.Context, c net.Conn, host string) (net.Conn, error) {
		secured := tls.Client(c, &tls.Config{InsecureSkipVerify: true, ServerName: host, NextProtos: []string{"http/1.1"}})
		if e := secured.HandshakeContext(ctx); e != nil {
			c.Close()
			return nil, e
		}
		return wrapSourceHeaders(secured), nil
	}, wrapSourceHeaders, sourceProxyResponse)
	defer transport.CloseIdleConnections()
	jar := newSourceCookieJar()
	client := &http.Client{Transport: sourceNoRetryTransport{transport}, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Referer")
		if req.URL.User != nil || req.Header.Get("Authorization") != "" {
			return HostBoundaryError{errors.New("source_probe_credentials_forbidden")}
		}
		if len(via) > 30 {
			return errors.New("source_redirect_limit")
		}
		if req.Response != nil && req.Response.StatusCode != 307 && req.Response.StatusCode != 308 {
			req.Header.Del("Content-Length")
			req.Header.Del("Content-Type")
			req.Header.Del("Transfer-Encoding")
		}
		return nil
	}}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if e != nil {
		return nil, e
	}
	request.Header.Set("User-Agent", "python-urllib3/2.7.0")
	request.Header.Set("Accept-Encoding", "gzip, deflate, br")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("Connection", "keep-alive")
	response, e := client.Do(request)
	if e != nil {
		var boundary HostBoundaryError
		if errors.As(e, &boundary) {
			return nil, e
		}
		if errors.Is(e, requestsproxy.ErrRequestsProxyAuthEncoding) {
			return nil, e
		}
		return n.networkFailure(ctx, e, environment...)
	}
	defer response.Body.Close()
	data, e := io.ReadAll(response.Body)
	if e != nil {
		return n.networkFailure(ctx, e, environment...)
	}
	data, e = decodeContent(data, response.Header.Get("Content-Encoding"))
	if e != nil {
		return n.networkFailure(ctx, e, environment...)
	}
	contentType := httpHeaderText(response.Header.Get("Content-Type"))
	headers := map[string]string{}
	for key, values := range response.Header {
		headers[key] = httpHeaderText(strings.Join(values, ", "))
	}
	protocol := response.ProtoMajor*10 + response.ProtoMinor
	return &Response{Status: response.StatusCode, TextDecode: func() (string, error) { return responseText(data, contentType) }, Content: data, Headers: headers, Protocol: protocol}, nil
}
func (n *NativeNetwork) networkFailure(ctx context.Context, cause error, environment ...string) (*Response, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Source RequestUtils catches ordinary RequestException and returns None.
	// Proxy endpoints can contain credentials: the public diagnostic is finite,
	// Go-native diagnostics retain detail with proxy userinfo redacted. The
	// transport also uses fixed public errors around credential-bearing routes.
	detail := cause.Error()
	for _, line := range environment {
		_, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		u, e := url.Parse(raw)
		if e != nil || u.User == nil {
			continue
		}
		clean := *u
		clean.User = nil
		detail = strings.ReplaceAll(detail, u.String(), clean.String())
		username := u.User.Username()
		password, _ := u.User.Password()
		for _, secret := range []string{u.User.String(), username, password} {
			if secret != "" {
				detail = strings.ReplaceAll(detail, secret, "[redacted]")
			}
		}
	}
	message := "同步请求失败(不重试): " + detail
	if e := n.log(ctx, sdk.LogDebug, message); e != nil {
		return nil, HostBoundaryError{e}
	}
	return nil, nil
}
func (n *NativeNetwork) DNS(ctx context.Context, host string, _ int) ([]string, error) {
	addresses, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil {
		return nil, e
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address.IP.String())
	}
	return result, nil
}
func (n *NativeNetwork) TLS(ctx context.Context, host string, port int, timeout time.Duration) (Certificate, error) {
	socket, e := n.connect(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if e != nil {
		return Certificate{}, e
	}
	defer socket.Close()
	stop := context.AfterFunc(ctx, func() { socket.Close() })
	defer stop()
	if e = socket.SetDeadline(time.Now().Add(timeout)); e != nil {
		return Certificate{}, e
	}
	config := &tls.Config{ServerName: host}
	if n.roots != nil {
		config = n.roots.Clone()
		config.ServerName = host
	}
	connection := tls.Client(socket, config)
	if e = connection.HandshakeContext(ctx); e != nil {
		return Certificate{}, e
	}
	state := connection.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return Certificate{}, errors.New("TLS peer did not supply a certificate")
	}
	cert := state.PeerCertificates[0]
	version := "-"
	switch state.Version {
	case tls.VersionTLS10:
		version = "TLSv1"
	case tls.VersionTLS11:
		version = "TLSv1.1"
	case tls.VersionTLS12:
		version = "TLSv1.2"
	case tls.VersionTLS13:
		version = "TLSv1.3"
	}
	return Certificate{Version: version, Issuer: certificateNames(cert.RawIssuer), Subject: certificateNames(cert.RawSubject), NotAfter: cert.NotAfter.UTC().Format("Jan _2 15:04:05 2006 GMT")}, nil
}
func certificateNames(raw []byte) [][][2]string {
	var sequence pkix.RDNSequence
	if _, e := asn1.Unmarshal(raw, &sequence); e != nil {
		return nil
	}
	names := map[string]string{"2.5.4.3": "commonName", "2.5.4.6": "countryName", "2.5.4.7": "localityName", "2.5.4.8": "stateOrProvinceName", "2.5.4.10": "organizationName", "2.5.4.11": "organizationalUnitName", "1.2.840.113549.1.9.1": "emailAddress", "2.5.4.5": "serialNumber"}
	result := [][][2]string{}
	for _, rdn := range sequence {
		group := [][2]string{}
		for _, entry := range rdn {
			key := names[entry.Type.String()]
			if key == "" {
				key = entry.Type.String()
			}
			group = append(group, [2]string{key, fmt.Sprint(entry.Value)})
		}
		result = append(result, group)
	}
	return result
}

// socket.create_connection resolves before applying its per-address connect
// timeout and tries addresses serially. A Go Dialer whole-operation timeout
// would also bound DNS and race address families, changing that source chain.
func (n *NativeNetwork) connect(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	lookup := n.lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	addresses, e := lookup(ctx, host)
	if e != nil {
		return nil, e
	}
	dial := n.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: timeout, KeepAlive: -1}).DialContext
	}
	var last error
	for _, ip := range addresses {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		host := ip.IP.String()
		if ip.Zone != "" {
			host += "%" + ip.Zone
		}
		connection, e := dial(ctx, network, net.JoinHostPort(host, port))
		if e == nil {
			return connection, nil
		}
		last = e
	}
	if last != nil {
		return nil, last
	}
	return nil, errors.New("getaddrinfo returns an empty list")
}

type sourceIdleConn struct {
	net.Conn
	timeout time.Duration
}

func (c *sourceIdleConn) Read(p []byte) (int, error) {
	if e := c.SetReadDeadline(time.Now().Add(c.timeout)); e != nil {
		return 0, e
	}
	return c.Conn.Read(p)
}
func (c *sourceIdleConn) Write(p []byte) (int, error) {
	if e := c.SetWriteDeadline(time.Now().Add(c.timeout)); e != nil {
		return 0, e
	}
	return c.Conn.Write(p)
}

// requests' default HTTPAdapter has max_retries=0. net/http would otherwise
// transparently replay GET after a reused redirect-session socket fails. A
// transport-local empty, non-replayable body inhibits that retry without
// changing the client's original bodyless GET/307/308 redirect semantics.
// TransferWriter probes EOF and emits neither body nor entity-length headers.
type sourceNoRetryTransport struct{ inner http.RoundTripper }

func (t sourceNoRetryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var headerConn *sourceHeaderConn
	var mu sync.Mutex
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if c, ok := info.Conn.(*sourceHeaderConn); ok {
			c.arm()
			mu.Lock()
			headerConn = c
			mu.Unlock()
		}
	}}
	copy := request.Clone(httptrace.WithClientTrace(request.Context(), trace))
	copy.Body = sourceNoRetryBody{}
	copy.GetBody = nil
	copy.ContentLength = 0
	response, e := t.inner.RoundTrip(copy)
	if e == nil && response != nil {
		mu.Lock()
		c := headerConn
		mu.Unlock()
		if c != nil {
			m := c.head()
			if m.status != 0 {
				response.StatusCode = m.status
				response.ProtoMajor = m.protocol / 10
				response.ProtoMinor = m.protocol % 10
				response.Header = m.headers
			}
		}
	}
	return response, e
}

type sourceNoRetryBody struct{}

func (sourceNoRetryBody) Read([]byte) (int, error) { return 0, io.EOF }
func (sourceNoRetryBody) Close() error             { return nil }

// http.client's source header parser decodes ISO-8859-1; Go retains the raw
// octets. Convert before the plugin renders Server/Date/Content-Type strings.
func httpHeaderText(raw string) string {
	var text strings.Builder
	for i := 0; i < len(raw); i++ {
		text.WriteRune(rune(raw[i]))
	}
	return text.String()
}
