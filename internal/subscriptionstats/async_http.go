package subscriptionstats

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/idna"
)

const asyncTimeout = 5 * time.Second

var errAsyncInvalidURL = errors.New("source_async_invalid_url")
var errAsyncProtocol = errors.New("source_async_unsupported_protocol")

// AsyncClient is the installed AsyncRequestUtils POST path used by the UI's
// awaited async_sub_reg. Keep one instance per App and Close it at shutdown.
// A fresh cookie jar is used per call, while the HTTP/1.1 and HTTP/2 transport
// pool is shared. No environment proxy is applied: the source supplies a custom
// AsyncHTTPTransport and resolves its explicit proxy through settings.PROXY.
// Base and Guard must remain immutable after the first call.
type AsyncClient struct {
	Base   *http.Transport
	Guard  func(context.Context, *url.URL) error
	mu     sync.Mutex
	pools  map[string]*asyncPool
	clock  uint64
	closed bool
}

type asyncPool struct {
	transport *http.Transport
	slots     chan struct{}
	last      uint64
}

// Close stops future enabled-call pool admission and closes idle sockets.
// The App must first cancel and join its statistics jobs; already admitted
// calls retain their parent context. Retired pools close idle sockets when
// those calls finish, including after LRU eviction.
func (c *AsyncClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, pool := range c.pools {
		pool.transport.CloseIdleConnections()
	}
	c.pools = nil
}

func (c *AsyncClient) pool(proxy string) (*asyncPool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrDependency
	}
	if c.pools == nil {
		c.pools = make(map[string]*asyncPool)
	}
	c.clock++
	if p := c.pools[proxy]; p != nil {
		p.last = c.clock
		return p, nil
	}
	base := c.Base
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	base = base.Clone()
	base.Proxy = nil
	if proxy != "" {
		u, e := asyncProxyURL(proxy)
		if e != nil {
			return nil, e
		}
		base.Proxy = http.ProxyURL(u)
	}

	base.DisableCompression = true
	base.ForceAttemptHTTP2 = true
	base.TLSNextProto = nil
	base.TLSHandshakeTimeout = asyncTimeout
	base.ResponseHeaderTimeout = asyncTimeout
	base.MaxIdleConns, base.MaxIdleConnsPerHost, base.MaxConnsPerHost = 20, 20, 40
	base.IdleConnTimeout = 30 * time.Second
	if base.TLSClientConfig == nil {
		base.TLSClientConfig = &tls.Config{}
	} else {
		base.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	base.TLSClientConfig.InsecureSkipVerify = true // Source-scoped verify=False.
	base.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}
	dial := base.DialContext
	if dial == nil {
		dial = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext
	}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, asyncTimeout)
		defer cancel()
		return dial(ctx, network, address)
	}
	if len(c.pools) == 32 {
		var oldest string
		var last uint64 = ^uint64(0)
		for key, p := range c.pools {
			if p.last < last {
				oldest, last = key, p.last
			}
		}
		c.pools[oldest].transport.CloseIdleConnections()
		delete(c.pools, oldest)
	}
	p := &asyncPool{transport: base, slots: make(chan struct{}, 40), last: c.clock}
	c.pools[proxy] = p
	return p, nil
}

func asyncProxyURL(proxy string) (*url.URL, error) {
	if proxy == "" {
		return nil, nil
	}
	u, e := url.Parse(proxy)
	if e != nil {
		return nil, errAsyncInvalidURL
	}
	if u.Hostname() == "" {
		return nil, errors.New("source_async_proxy_value_error")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	default:
		return nil, errors.New("source_async_proxy_value_error")
	}
}

func asyncProxyTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

func asyncProxy(setting SettingsSnapshot) string {
	if value := asyncProxyTrim(setting.Proxies["https"]); value != "" {
		return value
	}
	return asyncProxyTrim(setting.Proxies["http"])
}

func asyncTarget(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, errAsyncInvalidURL
	}
	if u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errAsyncProtocol
	}
	if u.User != nil {
		return nil, ErrDependency
	}
	host, e := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
	if e != nil {
		return nil, errAsyncInvalidURL
	}
	port := u.Port()
	if u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	u.RawQuery = strings.ReplaceAll(u.RawQuery, " ", "%20")
	return u, nil
}

// SubReg returns the awaited source child result. It does not implement
// Thread.start, enqueue admission, the SubscribeAdded event or parent rollback.
// A lost HTTP reply is false/unconfirmed and is never retried.
type asyncPrepared struct {
	target              *url.URL
	headers             http.Header
	pool                *asyncPool
	unsupportedProtocol bool
}

func asyncErrorResult(err error) (Result, error) {
	if errors.Is(err, ErrDependency) {
		return Result{}, err
	}
	kind := "ValueError"
	if errors.Is(err, errAsyncInvalidURL) {
		kind = "InvalidURL"
	}
	return Result{SourceError: kind, Outcome: "not_requested"}, nil
}

func asyncHeaders(setting SettingsSnapshot) (http.Header, error) {
	headers := http.Header{"Accept": {"*/*"}, "Accept-Encoding": {"gzip, deflate, br, zstd"}, "Connection": {"keep-alive"}, "Content-Type": {"application/json"}, "User-Agent": {setting.UserAgent}}
	if setting.UserUID != nil && *setting.UserUID != "" {
		headers.Set("X-MoviePilot-User-Uid", *setting.UserUID)
	}
	for _, values := range headers {
		for _, value := range values {
			for _, ch := range value {
				if ch > 127 {
					return nil, ErrAsyncUnicode
				}
			}
		}
	}
	return headers, nil
}

func (c *AsyncClient) prepare(ctx context.Context, setting SettingsSnapshot) (asyncPrepared, Result, bool, error) {
	var prepared asyncPrepared
	if err := ctx.Err(); err != nil {
		return prepared, Result{}, false, err
	}
	if setting.Share == nil {
		return prepared, Result{}, false, ErrDependency
	}
	if !*setting.Share {
		return prepared, Result{SourceReturned: true, Outcome: "disabled"}, false, nil
	}
	if !setting.Known || c.Guard == nil {
		return prepared, Result{}, false, ErrDependency
	}
	// AsyncRequestUtils constructs its explicit proxy transport before HTTPX
	// builds URL, headers and compact JSON, in that precise exception order.
	pool, err := c.pool(asyncProxy(setting))
	if err != nil {
		result, e := asyncErrorResult(err)
		return prepared, result, false, e
	}
	target, err := asyncTarget(strings.TrimRight(setting.ServerHost, "/") + "/subscribe/add")
	if err != nil && !errors.Is(err, errAsyncProtocol) {
		result, e := asyncErrorResult(err)
		return prepared, result, false, e
	}
	unsupported := errors.Is(err, errAsyncProtocol)
	headers, err := asyncHeaders(setting)
	if err != nil {
		return prepared, Result{SourceError: "UnicodeEncodeError", Outcome: "not_requested"}, false, nil
	}
	return asyncPrepared{target: target, headers: headers, pool: pool, unsupportedProtocol: unsupported}, Result{}, true, nil
}

func (c *AsyncClient) SubRegFields(ctx context.Context, setting SettingsSnapshot, sourceFields json.RawMessage) (Result, error) {
	prepared, result, proceed, err := c.prepare(ctx, setting)
	if !proceed {
		return result, err
	}
	payload, err := BuildRegistrationAsync(sourceFields)
	if err != nil {
		if result, sourceError := AsyncJSONResult(err); sourceError {
			return result, nil
		}
		return Result{}, err
	}
	return c.sendPrepared(ctx, prepared, payload)
}

func (c *AsyncClient) SubReg(ctx context.Context, setting SettingsSnapshot, payload []byte) (Result, error) {
	prepared, result, proceed, err := c.prepare(ctx, setting)
	if !proceed {
		return result, err
	}
	if !utf8.Valid(payload) || !json.Valid(payload) {
		return Result{}, ErrDependency
	}
	return c.sendPrepared(ctx, prepared, payload)
}

func (c *AsyncClient) sendPrepared(ctx context.Context, prepared asyncPrepared, payload []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	// HTTPX accepts relative/unsupported-scheme URL objects while building the
	// request, then raises a caught UnsupportedProtocol only after JSON encode.
	if prepared.unsupportedProtocol {
		return Result{SourceReturned: true, Outcome: "not_requested"}, nil
	}
	pool, target, headers := prepared.pool, prepared.target, prepared.headers
	origin := *target
	defer func() {
		c.mu.Lock()
		retired := c.closed
		if !retired {
			retired = true
			for _, current := range c.pools {
				if current == pool {
					retired = false
					break
				}
			}
		}
		c.mu.Unlock()
		if retired {
			pool.transport.CloseIdleConnections()
		}
	}()

	timer := time.NewTimer(asyncTimeout)
	defer timer.Stop()
	select {
	case pool.slots <- struct{}{}:
		defer func() { <-pool.slots }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return Result{SourceReturned: true, Outcome: "unconfirmed"}, nil
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: pool.transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	method, body := "POST", payload
	for redirects := 0; ; redirects++ {
		if target.Scheme != origin.Scheme || target.Host != origin.Host {
			return Result{Outcome: "unconfirmed"}, ErrRedirect
		}
		if e := c.Guard(ctx, target); e != nil {
			return Result{Outcome: "unconfirmed"}, e
		}
		response, nativeError := asyncRequest(ctx, client, method, target, headers, body)
		if nativeError != nil {
			if ctx.Err() != nil {
				return Result{Outcome: "unconfirmed"}, ctx.Err()
			}
			return Result{SourceReturned: true, Outcome: "unconfirmed"}, nil
		}
		location := response.Header.Get("Location")
		status := response.StatusCode
		redirect := status == 301 || status == 302 || status == 303 || status == 307 || status == 308
		if !redirect || location == "" {
			return Result{SourceReturned: true, SourceValue: status == 200, Outcome: "response", HTTPStatus: status}, nil
		}
		if redirects >= 20 {
			return Result{SourceReturned: true, Outcome: "unconfirmed"}, nil
		}
		next, err := url.Parse(location)
		if err != nil {
			return Result{SourceReturned: true, Outcome: "unconfirmed"}, nil
		}
		target = target.ResolveReference(next)
		if status == 303 && method != "HEAD" || status == 302 && method != "HEAD" || status == 301 && method == "POST" {
			method, body = "GET", nil
		}
	}
}

// Cancellation is per request/stream, never a read deadline on a multiplexed
// HTTP/2 connection. Each body read resets its inactivity timer.
func asyncRequest(parent context.Context, client *http.Client, method string, target *url.URL, headers http.Header, body []byte) (*http.Response, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var mu sync.Mutex
	var timer *time.Timer
	reset := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(asyncTimeout, cancel)
	}
	stop := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
	}
	defer stop()
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { reset() }, WroteRequest: func(httptrace.WroteRequestInfo) { reset() }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.GetBody = nil // No POST stale-connection replay; redirects are explicit.
	req.Header = headers.Clone()
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	read := &asyncActivityReader{reader: res.Body, reset: reset}
	if err = asyncDrain(read, res.Header.Get("Content-Encoding")); err != nil {
		return nil, err
	}
	return res, nil
}

type asyncActivityReader struct {
	reader io.Reader
	reset  func()
}

func (r *asyncActivityReader) Read(p []byte) (int, error) { r.reset(); return r.reader.Read(p) }

func asyncDrain(body io.Reader, encoding string) error {
	reader := body
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	codings := strings.Split(strings.ToLower(encoding), ",")
	for i := len(codings) - 1; i >= 0; i-- {
		switch strings.TrimSpace(codings[i]) {
		case "gzip":
			r, e := gzip.NewReader(reader)
			if e != nil {
				return e
			}
			closers = append(closers, r)
			reader = r
		case "deflate":
			peek := bufio.NewReader(reader)
			header, _ := peek.Peek(2)
			if len(header) == 2 && header[0]&15 == 8 && (int(header[0])*256+int(header[1]))%31 == 0 {
				r, e := zlib.NewReader(peek)
				if e != nil {
					return e
				}
				closers = append(closers, r)
				reader = r
			} else {
				r := flate.NewReader(peek)
				closers = append(closers, r)
				reader = r
			}
		case "br":
			reader = brotli.NewReader(reader)
		case "zstd":
			r, e := zstd.NewReader(reader, zstd.WithDecoderConcurrency(1))
			if e != nil {
				return e
			}
			defer r.Close()
			reader = r
		}
	}
	_, err := io.Copy(io.Discard, reader)
	return err
}
