package mediaservervendors

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"reflect"
	"time"
)

// This marker can only originate after this private connection established an
// actual deadline and observed a standard deadline leaf after that deadline.
// It is not a general net.Error/Timeout or arbitrary wrapped-error classifier.
type ejOwnedIOTimeout struct {
	phase string
	cause error
}

func (e *ejOwnedIOTimeout) Error() string { return "private media " + e.phase + " timeout" }
func (e *ejOwnedIOTimeout) Unwrap() error { return e.cause }

type ejConnectBudget struct{}

func (*ejConnectBudget) Error() string { return "private media connect budget" }
func ejStandardIODeadline(err error) bool {
	if err == os.ErrDeadlineExceeded {
		return true
	}
	switch e := err.(type) {
	case *net.OpError:
		return ejStandardIODeadline(e.Err)
	case *os.SyscallError:
		return ejStandardIODeadline(e.Err)
	}
	return false
}

// net/http's unexported exact concrete type is only created by its configured
// TLSHandshakeTimeout timer. The pinned toolchain source is bound separately.
func ejStandardTLSHandshakeTimeout(err error) bool {
	if err == nil {
		return false
	}
	t := reflect.TypeOf(err)
	return t.PkgPath() == "net/http" && t.Name() == "tlsHandshakeTimeoutError"
}

// net.Dialer maps its expired context to this exact private standard-library
// leaf. Its caller MUST first prove this request's own connect cause fired.
func ejStandardConnectTimeout(err error) bool {
	if err == nil {
		return false
	}
	t := reflect.TypeOf(err)
	return t.Kind() == reflect.Pointer && t.Elem().PkgPath() == "net" && t.Elem().Name() == "timeoutError"
}

type ejIdleConn struct {
	net.Conn
	idle time.Duration
}

func (c *ejIdleConn) Read(p []byte) (int, error) {
	deadline := time.Now().Add(c.idle)
	if err := c.Conn.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(p)
	if err != nil && !time.Now().Before(deadline) && ejStandardIODeadline(err) {
		return n, &ejOwnedIOTimeout{phase: "read", cause: err}
	}
	return n, err
}
func (c *ejIdleConn) Write(p []byte) (int, error) {
	// urllib3 sends with conn.timeout=connect_timeout. The scalar RequestUtils
	// timeout supplies the SAME 20s for connect and read; no new write budget.
	deadline := time.Now().Add(c.idle)
	if err := c.Conn.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	n, err := c.Conn.Write(p)
	if err != nil && !time.Now().Before(deadline) && ejStandardIODeadline(err) {
		return n, &ejOwnedIOTimeout{phase: "write", cause: err}
	}
	return n, err
}
func ejPrivateTransport(parent context.Context, base *http.Transport, timeout time.Duration) (*http.Transport, error) {
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
		return &ejIdleConn{Conn: conn, idle: timeout}, nil
	}
	return transport, nil
}
