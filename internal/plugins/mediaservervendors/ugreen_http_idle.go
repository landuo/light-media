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

// The retained Ugreen Session needs request-generation deadlines, not the
// per-request EJ connection wrapper. This block is mechanically adapted from
// the accepted Trime private connection. Production timeout remains 20s.
type ugreenPersistentConn struct {
	net.Conn
	idleTimeout                 time.Duration
	phaseMu                     sync.Mutex
	active                      bool
	generation                  uint64
	readDeadline, writeDeadline time.Time
}

// HTTP/1 transport readLoop may already be blocked in Read while the connection
// is idle. Clearing idle deadlines and setting them on actual reuse affects
// that pending read too; it must not inherit the preceding request's budget.
func (c *ugreenPersistentConn) activate() (uint64, error) {
	c.phaseMu.Lock()
	defer c.phaseMu.Unlock()
	if c.generation == ^uint64(0) {
		return 0, errors.New("Ugreen HTTP generation exhausted")
	}
	c.generation++
	c.active = true
	c.readDeadline = time.Now().Add(c.idleTimeout)
	return c.generation, c.Conn.SetReadDeadline(c.readDeadline)
}
func (c *ugreenPersistentConn) idle(generation uint64) error {
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
func (c *ugreenPersistentConn) Read(p []byte) (int, error) {
	c.phaseMu.Lock()
	if c.active {
		c.readDeadline = time.Now().Add(c.idleTimeout)
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
	if !deadline.IsZero() && !time.Now().Before(deadline) && ejStandardIODeadline(e) {
		e = &ejOwnedIOTimeout{phase: "read", cause: e}
	}
	return n, e
}
func (c *ugreenPersistentConn) Write(p []byte) (int, error) {
	c.phaseMu.Lock()
	c.active = true
	generation := c.generation
	// Reset response-read budget after actual outgoing request activity as well
	// as GotConn; sending headers/body must not consume the following read budget.
	c.readDeadline = time.Now().Add(c.idleTimeout)
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
		c.readDeadline = time.Now().Add(c.idleTimeout)
		resetErr = c.Conn.SetReadDeadline(c.readDeadline)
	}
	c.phaseMu.Unlock()
	if resetErr != nil {
		return n, resetErr
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) && ejStandardIODeadline(e) {
		e = &ejOwnedIOTimeout{phase: "write", cause: e}
	}
	return n, e
}
func ugreenHTTPConnection(c net.Conn) *ugreenPersistentConn {
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
	owned, _ := c.(*ugreenPersistentConn)
	return owned
}

type ugreenHTTPLease struct {
	conn       *ugreenPersistentConn
	generation uint64
}
type ugreenHTTPPhaseFailure struct{ cause error }

func (*ugreenHTTPPhaseFailure) Error() string   { return "Ugreen HTTP phase deadline setup failed" }
func (e *ugreenHTTPPhaseFailure) Unwrap() error { return e.cause }

func ugreenPrivateTransport(parent context.Context, base *http.Transport, timeout time.Duration) (*http.Transport, error) {
	if err := ejContext(parent); err != nil {
		return nil, err
	}
	if base == nil || timeout <= 0 {
		return nil, &EmbyJellyfinBoundaryError{Kind: "invalid private transport dependency"}
	}
	// These are host constructor constraints, not new user configuration rules.
	// A custom TLS dialer bypasses our TLS/deadline policy; a legacy Dial cannot
	// be silently replaced by a different route. The eventual host must supply
	// the concrete standard/context dial transport it already owns.
	if base.DialTLSContext != nil || base.DialTLS != nil || (base.DialContext == nil && base.Dial != nil) {
		return nil, &EmbyJellyfinBoundaryError{Kind: "unsupported private dial dependency"}
	}
	transport := base.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.ResponseHeaderTimeout = 0
	transport.TLSHandshakeTimeout = timeout
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		linked, cancelLinked := context.WithCancelCause(ctx)
		stop := context.AfterFunc(parent, func() { cancelLinked(context.Cause(parent)) })
		defer stop()
		defer cancelLinked(nil)
		budget := &ejConnectBudget{}
		connect, cancel := context.WithTimeoutCause(linked, timeout, budget)
		defer cancel()
		conn, err := dial(connect, network, address)
		if parent.Err() != nil {
			if conn != nil {
				conn.Close()
			}
			return nil, parent.Err()
		}
		if context.Cause(connect) == budget && (ejOwnedDeadline(err) || err == nil) {
			if conn != nil {
				conn.Close()
			}
			return nil, &ejOwnedIOTimeout{phase: "connect", cause: context.DeadlineExceeded}
		}
		if err != nil {
			return nil, err
		}
		if conn == nil {
			return nil, &EmbyJellyfinBoundaryError{Kind: "nil private dial connection"}
		}
		return &ugreenPersistentConn{Conn: conn, idleTimeout: timeout, active: true}, nil
	}
	return transport, nil
}

// Each logical request owns its trace state. A stale PutIdleConn callback may
// clear only its own lease, never a newer request's active socket deadline.
func ugreenHTTPPhaseRequest(req *http.Request) (*http.Request, func() error, context.CancelFunc) {
	var owner atomic.Pointer[ugreenHTTPLease]
	var failure atomic.Pointer[ugreenHTTPPhaseFailure]
	ctx, cancel := context.WithCancel(req.Context())
	fault := func(err error) {
		if err != nil {
			failure.CompareAndSwap(nil, &ugreenHTTPPhaseFailure{cause: err})
			cancel()
		}
	}
	trace := &httptrace.ClientTrace{
		GetConn: func(string) { owner.Store(nil) },
		GotConn: func(info httptrace.GotConnInfo) {
			if conn := ugreenHTTPConnection(info.Conn); conn != nil {
				generation, err := conn.activate()
				owner.Store(&ugreenHTTPLease{conn: conn, generation: generation})
				fault(err)
			}
		},
		PutIdleConn: func(err error) {
			if err == nil {
				if lease := owner.Load(); lease != nil {
					if e := lease.conn.idle(lease.generation); e != nil {
						fault(e)
						_ = lease.conn.Close()
					}
				}
			}
		},
	}
	check := func() error {
		if e := failure.Load(); e != nil {
			return e
		}
		return nil
	}
	return req.WithContext(httptrace.WithClientTrace(ctx, trace)), check, cancel
}
