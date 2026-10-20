package apprise

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/andybalholm/brotli"
	"light-media/internal/networkproxy"
)

//go:embed certifi-ca.pem
var certifiCA []byte

// HTTPTransport owns a requests-style fresh session per send. Authorize runs
// before each destination, including redirects; the App owner binds it to the
// accepted operation. It must not log request URLs, which may contain secrets.
type HTTPTransport struct {
	Base        *http.Transport
	Environment func() []string
	Authorize   func(context.Context, *url.URL) error
}

var errHTTP = errors.New("apprise_http_request_failed")

func (h *HTTPTransport) Send(ctx context.Context, request Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	connect, err := httpTimeout(request.Timeout[0])
	if err != nil {
		return Response{}, err
	}
	read, err := httpTimeout(request.Timeout[1])
	if err != nil {
		return Response{}, err
	}
	environment := h.Environment
	if environment == nil {
		environment = os.Environ
	}
	env := environment()
	var base *http.Transport
	if h.Base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		base = h.Base.Clone()
	}
	base.DisableCompression = true
	base.ForceAttemptHTTP2 = false
	base.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	base.ResponseHeaderTimeout = 0
	base.TLSHandshakeTimeout = connect
	if base.TLSClientConfig == nil {
		base.TLSClientConfig = &tls.Config{}
	} else {
		base.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	base.TLSClientConfig.InsecureSkipVerify = !request.Verify
	base.TLSClientConfig.NextProtos = []string{"http/1.1"}
	if request.Verify {
		caPath := ""
		for _, key := range []string{"REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"} {
			for _, item := range env {
				if k, v, ok := strings.Cut(item, "="); ok && k == key && v != "" {
					caPath = v
					break
				}
			}
			if caPath != "" {
				break
			}
		}
		if caPath != "" {
			raw, e := os.ReadFile(caPath)
			if e != nil {
				return Response{}, errHTTP
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(raw) {
				return Response{}, errHTTP
			}
			base.TLSClientConfig.RootCAs = roots
		} else if base.TLSClientConfig.RootCAs == nil {
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(certifiCA) {
				return Response{}, errHTTP
			}
			base.TLSClientConfig.RootCAs = roots
		}
	}
	dial := base.DialContext
	if dial == nil {
		dial = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext
	}
	var readLimit atomic.Int64
	readLimit.Store(int64(connect))
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, connect)
		defer cancel()
		conn, e := dial(dialCtx, network, address)
		if e != nil {
			return nil, e
		}
		return &readTimeoutConn{Conn: conn, timeout: &readLimit}, nil
	}
	transport := networkproxy.NewRequestsTransport(base, func() []string { return env })
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	target, e := url.Parse(request.URL)
	if e != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return Response{}, errHTTP
	}
	params := request.ParamOrder
	if params == nil && len(request.Params) > 0 {
		keys := make([]string, 0, len(request.Params))
		for k := range request.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			params = append(params, [2]string{k, request.Params[k]})
		}
	}
	query := target.RawQuery
	for _, p := range params {
		if query != "" {
			query += "&"
		}
		query += url.QueryEscape(p[0]) + "=" + url.QueryEscape(p[1])
	}
	target.RawQuery = query
	headers := http.Header{"Accept": {"*/*"}, "Accept-Encoding": {"gzip, deflate, br"}, "Connection": {"keep-alive"}}
	headerPairs := request.HeaderOrder
	if headerPairs == nil {
		keys := make([]string, 0, len(request.Headers))
		for k := range request.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			headerPairs = append(headerPairs, [2]string{k, request.Headers[k]})
		}
	}
	for _, p := range headerPairs {
		if p[1] != "" {
			first := []rune(p[1])[0]
			if sourceSpace(first) {
				return Response{}, errHTTP
			}
		}
		value, e := httpLatin1(p[1])
		if e != nil {
			return Response{}, e
		}
		headers.Set(p[0], value)
	}
	for _, name := range request.RemoveHeaders {
		headers.Del(name)
	}
	if len(request.Auth) == 2 {
		value, e := httpBasicAuth(request.Auth)
		if e != nil {
			return Response{}, e
		}
		headers.Set("Authorization", value)
	}
	method := request.Method
	body := request.Data
	for redirects := 0; ; redirects++ {
		if h.Authorize != nil {
			public := *target
			public.User = nil
			if e := h.Authorize(ctx, &public); e != nil {
				return Response{}, e
			}
		}
		trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { readLimit.Store(int64(read)) }}
		req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, target.String(), bytes.NewReader(body))
		if e != nil {
			return Response{}, errHTTP
		}
		req.Header = headers.Clone()
		if host := req.Header.Get("Host"); host != "" {
			req.Host = host
			req.Header.Del("Host")
		}
		res, e := client.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return Response{}, ctx.Err()
			}
			if errors.Is(e, networkproxy.ErrRequestsProxyAuthEncoding) {
				return Response{}, networkproxy.ErrRequestsProxyAuthEncoding
			}
			return Response{}, errHTTP
		}
		raw, e := io.ReadAll(res.Body)
		closeErr := res.Body.Close()
		if e != nil || closeErr != nil {
			if ctx.Err() != nil {
				return Response{}, ctx.Err()
			}
			return Response{}, errHTTP
		}
		raw, e = decodeHTTPContent(raw, res.Header.Get("Content-Encoding"))
		if e != nil {
			return Response{}, errHTTP
		}
		location := res.Header.Get("Location")
		redirect := res.StatusCode == 301 || res.StatusCode == 302 || res.StatusCode == 303 || res.StatusCode == 307 || res.StatusCode == 308
		if !redirect || location == "" {
			out := Response{Status: res.StatusCode, Content: raw, Headers: map[string]string{}}
			for k, v := range res.Header {
				out.Headers[k] = strings.Join(v, ", ")
			}
			return out, nil
		}
		if redirects >= 30 {
			return Response{}, errHTTP
		}
		next, e := url.Parse(location)
		if e != nil {
			return Response{}, errHTTP
		}
		next = target.ResolveReference(next)
		if next.Fragment == "" && !strings.Contains(location, "#") {
			next.Fragment = target.Fragment
		}
		if stripHTTPAuth(target, next) {
			headers.Del("Authorization")
		}
		if res.StatusCode == 303 && method != "HEAD" || res.StatusCode == 302 && method != "HEAD" || res.StatusCode == 301 && method == "POST" {
			method = "GET"
		}
		if res.StatusCode != 307 && res.StatusCode != 308 {
			body = nil
			for _, k := range []string{"Content-Length", "Content-Type", "Transfer-Encoding"} {
				headers.Del(k)
			}
		}
		headers.Del("Cookie")
		target = next
	}
}
func httpTimeout(v float64) (time.Duration, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v >= float64(math.MaxInt64)/float64(time.Second) {
		return 0, errHTTP
	}
	return time.Duration(v * float64(time.Second)), nil
}

type readTimeoutConn struct {
	net.Conn
	timeout *atomic.Int64
}

func (c *readTimeoutConn) Read(b []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(time.Duration(c.timeout.Load())))
	return c.Conn.Read(b)
}
func httpBasicAuth(auth []any) (string, error) {
	value := func(x any) ([]byte, error) {
		s := "None"
		if x != nil {
			var ok bool
			s, ok = x.(string)
			if !ok {
				return nil, errHTTP
			}
		}
		out := make([]byte, 0, len(s))
		for _, r := range s {
			if r > 255 {
				return nil, errHTTP
			}
			out = append(out, byte(r))
		}
		return out, nil
	}
	user, e := value(auth[0])
	if e != nil {
		return "", e
	}
	pass, e := value(auth[1])
	if e != nil {
		return "", e
	}
	raw := append(append(user, ':'), pass...)
	return "Basic " + base64.StdEncoding.EncodeToString(raw), nil
}
func httpLatin1(s string) (string, error) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 255 {
			return "", errHTTP
		}
		out = append(out, byte(r))
	}
	return string(out), nil
}
func stripHTTPAuth(old, next *url.URL) bool {
	if !strings.EqualFold(old.Hostname(), next.Hostname()) {
		return true
	}
	standard := func(u *url.URL, p string) bool { return u.Port() == "" || u.Port() == p }
	if old.Scheme == "http" && standard(old, "80") && next.Scheme == "https" && standard(next, "443") {
		return false
	}
	if old.Scheme == next.Scheme {
		p := "80"
		if old.Scheme == "https" {
			p = "443"
		}
		if standard(old, p) && standard(next, p) {
			return false
		}
	}
	return old.Scheme != next.Scheme || old.Port() != next.Port()
}
func decodeHTTPContent(data []byte, encoding string) ([]byte, error) {
	encodings := strings.Split(encoding, ",")
	for i := len(encodings) - 1; i >= 0; i-- {
		enc := strings.TrimSpace(strings.ToLower(encodings[i]))
		var reader io.Reader
		var close func() error
		switch enc {
		case "gzip", "x-gzip":
			r, e := gzip.NewReader(bytes.NewReader(data))
			if e != nil {
				return nil, e
			}
			reader = r
			close = r.Close
		case "deflate":
			r, e := zlib.NewReader(bytes.NewReader(data))
			if e != nil {
				raw := flate.NewReader(bytes.NewReader(data))
				reader = raw
				close = raw.Close
			} else {
				reader = r
				close = r.Close
			}
		case "br":
			reader = brotli.NewReader(bytes.NewReader(data))
		default:
			continue
		}
		decoded, e := io.ReadAll(reader)
		if close != nil {
			_ = close()
		}
		if e != nil {
			return nil, e
		}
		data = decoded
	}
	return data, nil
}
