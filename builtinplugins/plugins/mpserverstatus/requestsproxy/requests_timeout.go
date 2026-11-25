package requestsproxy

import (
	"context"
	"net"
	"net/http"
	"time"
)

// NewRequestsTimeoutTransport preserves requests' scalar timeout: connection
// establishment and each socket read/write may wait this long. A response
// making progress, or a redirect chain, has no additional total time limit.
// The caller's context still cancels the entire operation. Use one instance
// per logical request and pass it into NewRequestsProxyTransport.
func NewRequestsTimeoutTransport(timeout time.Duration) *http.Transport {
	return NewRequestsTimeoutTransportFrom(http.DefaultTransport.(*http.Transport), timeout)
}

// NewRequestsTimeoutTransportFrom applies the same scalar timeout while keeping
// the base transport's dialer, so address selection (including an isolated
// test redirect) still belongs to the caller. The connect phase is bounded by
// the timeout through the dial context, and established sockets keep the
// per-operation deadline.
func NewRequestsTimeoutTransportFrom(base *http.Transport, timeout time.Duration) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	transport := base.Clone()
	dial := transport.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
		dial = dialer.DialContext
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		conn, err := dial(dialContext, network, address)
		if err != nil {
			return nil, err
		}
		return &requestsTimeoutConn{Conn: conn, timeout: timeout}, nil
	}
	// requests uses HTTP/1.1. Per-socket deadlines must not affect unrelated
	// multiplexed streams; the per-request transport does not share sessions.
	transport.ForceAttemptHTTP2 = false
	transport.TLSHandshakeTimeout = timeout
	transport.ResponseHeaderTimeout = 0
	return transport
}

type requestsTimeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (c *requestsTimeoutConn) Read(buffer []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(buffer)
}

func (c *requestsTimeoutConn) Write(buffer []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(buffer)
}
