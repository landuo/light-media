package mediaservervendors

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TrimeHTTPHeader struct{ Name, Value string }
type TrimeHTTPCurrent func(context.Context) error

// Response.json is a separate source boundary, never silently approximated by
// Response.text or an unknown server's UTF-8. A source-bound decoder is mandatory.
type TrimeHTTPJSON func([]byte, http.Header) (any, error)
type TrimeHTTPResult struct {
	Status  int
	Headers http.Header
	Body    []byte
}
type TrimeHTTP struct {
	client              *http.Client
	transport           *http.Transport
	origin              *url.URL
	originEncodingError error
	current             TrimeHTTPCurrent
	decode              TrimeHTTPJSON
	redirectLimit       *trimeRedirectLimit
}
type trimeRedirectLimit struct{ marker byte }

func (*trimeRedirectLimit) Error() string { return "Trime private redirect limit" }

// RequestUtils timeout=10 is a socket-phase timeout, not a new total wall-clock
// budget. Transport reads renew the deadline at each physical read; redirects
// and response body are not put under an invented 10-second total deadline.
type trimeReadTimeoutConn struct {
	net.Conn
	phaseMu                     sync.Mutex
	active                      bool
	generation                  uint64
	readDeadline, writeDeadline time.Time
}

// HTTP/1 transport readLoop may already be blocked in Read while the connection
// is idle. Clearing idle deadlines and setting them on actual reuse affects
// that pending read too; it must not inherit the preceding request's budget.
func (c *trimeReadTimeoutConn) activate() (uint64, error) {
	c.phaseMu.Lock()
	defer c.phaseMu.Unlock()
	if c.generation == ^uint64(0) {
		return 0, errors.New("Trime HTTP generation exhausted")
	}
	c.generation++
	c.active = true
	c.readDeadline = time.Now().Add(10 * time.Second)
	return c.generation, c.Conn.SetReadDeadline(c.readDeadline)
}
func (c *trimeReadTimeoutConn) idle(generation uint64) error {
	c.phaseMu.Lock()
	defer c.phaseMu.Unlock()
	if c.generation != generation {
		return nil
	}
	c.active = false
	c.readDeadline = time.Time{}
	c.writeDeadline = time.Time{}
	if e := c.Conn.SetReadDeadline(time.Time{}); e != nil {
		return e
	}
	return c.Conn.SetWriteDeadline(time.Time{})
}
func (c *trimeReadTimeoutConn) Read(p []byte) (int, error) {
	c.phaseMu.Lock()
	if c.active {
		c.readDeadline = time.Now().Add(10 * time.Second)
	} else {
		c.readDeadline = time.Time{}
	}
	e := c.Conn.SetReadDeadline(c.readDeadline)
	c.phaseMu.Unlock()
	if e != nil {
		return 0, e
	}
	n, e := c.Conn.Read(p)
	c.phaseMu.Lock()
	deadline := c.readDeadline
	c.phaseMu.Unlock()
	if !deadline.IsZero() && !time.Now().Before(deadline) && trimeOwnedSocketDeadline(e) {
		e = &trimeSocketTimeout{operation: "read", cause: e}
	}
	return n, e
}
func (c *trimeReadTimeoutConn) Write(p []byte) (int, error) {
	c.phaseMu.Lock()
	c.active = true
	generation := c.generation
	// Reset response-read budget after actual outgoing request activity as well
	// as GotConn; sending headers/body must not consume the following read budget.
	c.readDeadline = time.Now().Add(10 * time.Second)
	c.writeDeadline = c.readDeadline
	e := c.Conn.SetReadDeadline(c.readDeadline)
	if e == nil {
		e = c.Conn.SetWriteDeadline(c.writeDeadline)
	}
	c.phaseMu.Unlock()
	if e != nil {
		return 0, e
	}
	n, e := c.Conn.Write(p)
	c.phaseMu.Lock()
	deadline := c.writeDeadline
	// Request.write's WroteRequest callback runs before Transport's final buffered
	// Flush. Only successful actual Conn.Write resets the following read budget;
	// an old writer cannot modify a newly leased or already-idle connection.
	var resetErr error
	if e == nil && n > 0 && c.active && c.generation == generation {
		c.readDeadline = time.Now().Add(10 * time.Second)
		resetErr = c.Conn.SetReadDeadline(c.readDeadline)
	}
	c.phaseMu.Unlock()
	if resetErr != nil {
		return n, resetErr
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) && trimeOwnedSocketDeadline(e) {
		e = &trimeSocketTimeout{operation: "write", cause: e}
	}
	return n, e
}
func trimeHTTPConnection(c net.Conn) *trimeReadTimeoutConn {
	// HTTPS over a normal HTTPS proxy can contain two standard TLS layers.
	// Only unwrap the concrete stdlib type; no routing changes or arbitrary
	// third-party Unwrap interface are introduced.
	for {
		tlsConn, ok := c.(*tls.Conn)
		if !ok {
			break
		}
		c = tlsConn.NetConn()
	}
	owned, _ := c.(*trimeReadTimeoutConn)
	return owned
}

type trimeHTTPLease struct {
	conn       *trimeReadTimeoutConn
	generation uint64
}
type trimeHTTPPhaseFailure struct{ cause error }

func (*trimeHTTPPhaseFailure) Error() string   { return "Trime HTTP phase deadline setup failed" }
func (e *trimeHTTPPhaseFailure) Unwrap() error { return e.cause }

func NewTrimeHTTP(base *http.Transport, host string, current TrimeHTTPCurrent, decode TrimeHTTPJSON) (*TrimeHTTP, error) {
	if base == nil || current == nil || decode == nil {
		return nil, errors.New("Trime HTTP requires owned transport/current/Response.json decoder")
	}
	encodedHost, encodingError := constructorHTTPURL(host)
	origin, e := url.Parse(encodedHost)
	if encodingError == nil && (e != nil || origin.Host == "" || origin.User != nil || origin.Fragment != "" || origin.RawQuery != "" || (origin.Scheme != "http" && origin.Scheme != "https")) {
		return nil, errors.New("Trime HTTP origin is not admitted")
	}
	// Custom TLS dialers need their own explicit source phase adapter. Do not
	// silently bypass it or override the trusted owner's route/proxy choices.
	if base.DialTLS != nil || base.DialTLSContext != nil {
		return nil, errors.New("Trime custom TLS dialer requires explicit phase adapter")
	}
	if base.DialContext == nil && base.Dial != nil {
		return nil, errors.New("Trime deprecated custom Dial requires explicit context phase adapter")
	}
	t := base.Clone()
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	} else {
		t.TLSClientConfig = t.TLSClientConfig.Clone()
	}
	t.TLSClientConfig.InsecureSkipVerify = true
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if e := trimeCurrentAuthority(ctx, current); e != nil {
			return nil, e
		}
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		c, e := dial(bounded, network, address)
		if e != nil {
			return nil, e
		}
		return &trimeReadTimeoutConn{Conn: c, active: true}, nil
	}
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 0
	// requests uses HTTP/1.1, not a Go HTTP/2 multiplexed read deadline.
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	jar, e := cookiejar.New(nil)
	if e != nil {
		return nil, e
	}
	c := &http.Client{Transport: t, Jar: jar}
	limit := &trimeRedirectLimit{marker: 1}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if e := trimeCurrentAuthority(req.Context(), current); e != nil {
			return e
		}
		if req.URL.Scheme != origin.Scheme || !strings.EqualFold(req.URL.Host, origin.Host) || req.URL.User != nil {
			return errors.New("Trime private credential origin changed")
		}
		if len(via) > 30 {
			return limit
		}
		return nil
	}
	return &TrimeHTTP{client: c, transport: t, origin: origin, originEncodingError: encodingError, current: current, decode: decode, redirectLimit: limit}, nil
}
func (c *TrimeHTTP) check(ctx context.Context) error { return trimeCurrentAuthority(ctx, c.current) }

// Exchange is real net/http on the retained private client/cookie jar. API
// method selection, source RequestUtils retry classification, JSON admission
// and safe logging are handled by the next Api slice, not faked here.
func (c *TrimeHTTP) exchange(ctx context.Context, method, address string, headers []TrimeHTTPHeader, body *string) (*TrimeHTTPResult, error) {
	if e := c.check(ctx); e != nil {
		return nil, e
	}
	if c.originEncodingError != nil {
		return nil, c.originEncodingError
	}
	encodedAddress, e := constructorHTTPURL(address)
	if e != nil {
		return nil, e
	}
	address = encodedAddress
	u, e := url.Parse(address)
	if e != nil || u.Scheme != c.origin.Scheme || !strings.EqualFold(u.Host, c.origin.Host) || u.User != nil {
		return nil, errors.New("Trime request outside private origin")
	}
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(*body)
	}
	req, e := http.NewRequestWithContext(ctx, strings.ToUpper(method), address, reader)
	if e != nil {
		return nil, e
	}
	// Recovered Requests Session defaults survive ordinary Api request headers.
	// Request entries below override case-insensitively; access-code None
	// removals for Accept/User-Agent/Referer remain resolved by its own profile.
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Connection", "keep-alive")
	for _, h := range headers {
		encoded, e := constructorHeader(h.Value)
		if e != nil {
			return nil, e
		}
		req.Header.Set(h.Name, encoded)
	}
	var connected atomic.Bool
	var owner atomic.Pointer[trimeHTTPLease]
	var phaseFailure atomic.Pointer[trimeHTTPPhaseFailure]
	requestContext, cancelRequest := context.WithCancel(req.Context())
	defer cancelRequest()
	phaseFault := func(err error) {
		if err != nil {
			phaseFailure.CompareAndSwap(nil, &trimeHTTPPhaseFailure{cause: err})
			cancelRequest()
		}
	}
	trace := &httptrace.ClientTrace{
		GetConn: func(string) { connected.Store(false); owner.Store(nil) },
		GotConn: func(info httptrace.GotConnInfo) {
			connected.Store(true)
			conn := trimeHTTPConnection(info.Conn)
			if conn != nil {
				generation, err := conn.activate()
				owner.Store(&trimeHTTPLease{conn: conn, generation: generation})
				phaseFault(err)
			}
		},
		PutIdleConn: func(err error) {
			if err == nil {
				if lease := owner.Load(); lease != nil {
					if failure := lease.conn.idle(lease.generation); failure != nil {
						phaseFault(failure)
						_ = lease.conn.Close()
					}
				}
			}
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(requestContext, trace))
	res, e := c.client.Do(req)
	if e != nil {
		if current := c.check(ctx); current != nil {
			return nil, current
		}
		if failure := phaseFailure.Load(); failure != nil {
			return nil, failure
		}
		return nil, trimeResponseNetworkError(e, false, connected.Load())
	}
	defer res.Body.Close()
	if e = c.check(ctx); e != nil {
		return nil, e
	}
	raw, e := MediaServerRequestsReadContent(ctx, res.Body, res.Header.Get("Content-Encoding"), RequestsContentCurrent(c.check))
	if e != nil {
		if current := c.check(ctx); current != nil {
			return nil, current
		}
		if failure := phaseFailure.Load(); failure != nil {
			return nil, failure
		}
		return nil, trimeResponseNetworkError(e, true, true)
	}
	if e = c.check(ctx); e != nil {
		return nil, e
	}
	if failure := phaseFailure.Load(); failure != nil {
		return nil, failure
	}
	return &TrimeHTTPResult{Status: res.StatusCode, Headers: res.Header.Clone(), Body: raw}, nil
}
func (c *TrimeHTTP) json(ctx context.Context, response *TrimeHTTPResult) (any, error) {
	if e := c.check(ctx); e != nil {
		return nil, e
	}
	if response == nil {
		return nil, errors.New("Trime JSON requires response")
	}
	value, e := c.decode(bytes.Clone(response.Body), response.Headers.Clone())
	if current := c.check(ctx); current != nil {
		return nil, current
	}
	if e != nil {
		return nil, e
	}
	return value, nil
}

// requests.Session.close releases connection pools but retains the Session and
// cookies; a later request may reopen connections. Do not clear authentication.
func (c *TrimeHTTP) Close(ctx context.Context) error {
	if e := c.check(ctx); e != nil {
		return e
	}
	c.transport.CloseIdleConnections()
	return c.check(ctx)
}
