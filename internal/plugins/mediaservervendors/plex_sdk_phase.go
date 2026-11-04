package mediaservervendors

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"
)

// The existing source-bound Trime phase/idle lease implementation is retained,
// mechanically renamed and parameterized by this Plex profile's own timeout.
// No ten/twenty-second value, total Client.Timeout or new route is inherited.
type plexSDKReadTimeoutConn struct {
	net.Conn
	timeout                     time.Duration
	phaseMu                     sync.Mutex
	active                      bool
	generation                  uint64
	readDeadline, writeDeadline time.Time
}

// HTTP/1 transport readLoop may already be blocked in Read while the connection
// is idle. Clearing idle deadlines and setting them on actual reuse affects
// that pending read too; it must not inherit the preceding request's budget.
func (c *plexSDKReadTimeoutConn) activate() (uint64, error) {
	c.phaseMu.Lock()
	defer c.phaseMu.Unlock()
	if c.generation == ^uint64(0) {
		return 0, errors.New("Plex SDK HTTP generation exhausted")
	}
	c.generation++
	c.active = true
	c.readDeadline = time.Now().Add(c.timeout)
	return c.generation, c.Conn.SetReadDeadline(c.readDeadline)
}
func (c *plexSDKReadTimeoutConn) idle(generation uint64) error {
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
func (c *plexSDKReadTimeoutConn) Read(p []byte) (int, error) {
	c.phaseMu.Lock()
	if c.active {
		c.readDeadline = time.Now().Add(c.timeout)
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
	if !deadline.IsZero() && !time.Now().Before(deadline) && plexSDKOwnedSocketDeadline(e) {
		e = &plexSDKSocketTimeout{operation: "read", cause: e}
	}
	return n, e
}
func (c *plexSDKReadTimeoutConn) Write(p []byte) (int, error) {
	c.phaseMu.Lock()
	c.active = true
	generation := c.generation
	// Reset response-read budget after actual outgoing request activity as well
	// as GotConn; sending headers/body must not consume the following read budget.
	c.readDeadline = time.Now().Add(c.timeout)
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
		c.readDeadline = time.Now().Add(c.timeout)
		resetErr = c.Conn.SetReadDeadline(c.readDeadline)
	}
	c.phaseMu.Unlock()
	if resetErr != nil {
		return n, resetErr
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) && plexSDKOwnedSocketDeadline(e) {
		e = &plexSDKSocketTimeout{operation: "write", cause: e}
	}
	return n, e
}
func plexSDKHTTPConnection(c net.Conn) *plexSDKReadTimeoutConn {
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
	owned, _ := c.(*plexSDKReadTimeoutConn)
	return owned
}

type plexSDKHTTPLease struct {
	conn       *plexSDKReadTimeoutConn
	generation uint64
}
type plexSDKHTTPPhaseFailure struct{ cause error }

func (*plexSDKHTTPPhaseFailure) Error() string   { return "Plex SDK HTTP phase deadline setup failed" }
func (e *plexSDKHTTPPhaseFailure) Unwrap() error { return e.cause }

func plexSDKPhaseTransport(base *http.Transport, timeout time.Duration, current PlexSDKCurrentGuard) (*http.Transport, error) {
	if base.DialTLS != nil || base.DialTLSContext != nil {
		return nil, errors.New("Plex custom TLS dialer requires explicit phase adapter")
	}
	if base.DialContext == nil && base.Dial != nil {
		return nil, errors.New("Plex deprecated Dial requires explicit phase adapter")
	}
	t := base // already an owned clone; Proxy and trusted DialContext remain unchanged.
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := plexSDKCurrentAuthority(ctx, current); err != nil {
			return nil, err
		}
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		conn, err := dial(bounded, network, address)
		if check := plexSDKCurrentAuthority(ctx, current); check != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, check
		}
		if err != nil {
			if bounded.Err() == context.DeadlineExceeded && plexSDKOwnedConnectDeadline(err) {
				return nil, &plexSDKConnectTimeout{cause: err}
			}
			return nil, err
		}
		return &plexSDKReadTimeoutConn{Conn: conn, timeout: timeout, active: true}, nil
	}
	t.TLSHandshakeTimeout = timeout
	t.ResponseHeaderTimeout = 0
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return t, nil
}

type plexSDKRequestPhases struct {
	connected atomic.Bool
	owner     atomic.Pointer[plexSDKHTTPLease]
	fault     atomic.Pointer[plexSDKHTTPPhaseFailure]
}

func (p *plexSDKRequestPhases) failure() error {
	if v := p.fault.Load(); v != nil {
		return v
	}
	return nil
}
func plexSDKTrace(ctx context.Context) (context.Context, context.CancelFunc, *plexSDKRequestPhases) {
	p := &plexSDKRequestPhases{}
	request, cancel := context.WithCancel(ctx)
	phaseFault := func(err error) {
		if err != nil {
			p.fault.CompareAndSwap(nil, &plexSDKHTTPPhaseFailure{cause: err})
			cancel()
		}
	}
	trace := &httptrace.ClientTrace{
		GetConn: func(string) { p.connected.Store(false); p.owner.Store(nil) },
		GotConn: func(info httptrace.GotConnInfo) {
			p.connected.Store(true)
			if conn := plexSDKHTTPConnection(info.Conn); conn != nil {
				generation, err := conn.activate()
				p.owner.Store(&plexSDKHTTPLease{conn: conn, generation: generation})
				phaseFault(err)
			}
		},
		PutIdleConn: func(err error) {
			if err == nil {
				if lease := p.owner.Load(); lease != nil {
					if failure := lease.conn.idle(lease.generation); failure != nil {
						phaseFault(failure)
						_ = lease.conn.Close()
					}
				}
			}
		},
	}
	return httptrace.WithClientTrace(request, trace), cancel, p
}
