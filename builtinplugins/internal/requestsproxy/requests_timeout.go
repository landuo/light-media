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
	base := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &requestsTimeoutConn{Conn: conn, timeout: timeout}, nil
	}
	// requests uses HTTP/1.1. Per-socket deadlines must not affect unrelated
	// multiplexed streams; the per-request transport does not share sessions.
	base.ForceAttemptHTTP2 = false
	base.TLSHandshakeTimeout = timeout
	base.ResponseHeaderTimeout = 0
	return base
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
