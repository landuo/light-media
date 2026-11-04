package mediaservervendors

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"

	"light-media/internal/plugins/mediaserverruntime"
)

// Caller authority is not a source Requests failure, even if a caller happens
// to return a SourceError. Never let a transient guard failure enter retry or
// Api.request's ordinary-error catch after a later healthy guard check.
type plexSDKCurrentBoundary struct{ cause error }

func (*plexSDKCurrentBoundary) Error() string   { return "Plex SDK current authority rejected" }
func (e *plexSDKCurrentBoundary) Unwrap() error { return e.cause }
func plexSDKCurrentAuthority(ctx context.Context, current PlexSDKCurrentGuard) error {
	if e := plexContext(ctx); e != nil {
		return e
	}
	e := current(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		return &plexSDKCurrentBoundary{cause: e}
	}
	return nil
}

// Marker originates only at this client's actual profile-selected socket operation.
// It deliberately does not trust arbitrary errors exposing Timeout().
type plexSDKSocketTimeout struct {
	operation string
	cause     error
}

func (e *plexSDKSocketTimeout) Error() string { return "Plex SDK socket " + e.operation + " deadline" }
func (e *plexSDKSocketTimeout) Unwrap() error { return e.cause }
func plexSDKOwnedSocketDeadline(err error) bool {
	switch e := err.(type) {
	case *net.OpError:
		return plexSDKOwnedSocketDeadline(e.Err)
	case *os.SyscallError:
		return plexSDKOwnedSocketDeadline(e.Err)
	}
	return err == os.ErrDeadlineExceeded
}

// Only concrete standard-library transport wrappers are traversed. Authority,
// decoder errors, unknown injected errors and caller cancellation remain intact.
// Connect refusal/DNS failure are Requests ConnectionError; only our owned dial
// deadline is ConnectTimeout. Header/read and body errors retain source classes.
func plexSDKResponseNetworkError(err error, body, connected bool) error {
	leaf := err
	for {
		switch e := leaf.(type) {
		case *url.Error:
			leaf = e.Err
		case *net.OpError:
			leaf = e.Err
		case *os.SyscallError:
			leaf = e.Err
		default:
			goto classify
		}
	}
classify:
	class := ""
	if _, ok := leaf.(*plexSDKConnectTimeout); ok && !body {
		class = "ConnectTimeout"
	} else if timeout, ok := leaf.(*plexSDKSocketTimeout); ok {
		if timeout.operation == "read" {
			if body {
				class = "ConnectionError"
			} else {
				class = "ReadTimeout"
			}
		} else if timeout.operation == "write" {
			class = "ConnectionError"
		}
	} else if !body && (leaf == syscall.ECONNREFUSED || leaf == syscall.ENETUNREACH || leaf == syscall.EHOSTUNREACH) {
		class = "ConnectionError"
	} else if dns, ok := leaf.(*net.DNSError); ok && !body && (dns.IsNotFound || dns.IsTemporary) {
		class = "ConnectionError"
	} else if leaf == io.EOF || leaf == io.ErrUnexpectedEOF || leaf == syscall.ECONNRESET || leaf == syscall.ECONNABORTED || leaf == syscall.EPIPE {
		if body {
			class = "ChunkedEncodingError"
		} else {
			class = "ConnectionError"
		}
	}
	if class == "" {
		return err
	}
	return &mediaserverruntime.SourceError{Class: class, Cause: err}
}

// This marker can originate only in this client's bounded real DialContext.
type plexSDKConnectTimeout struct{ cause error }

func (e *plexSDKConnectTimeout) Error() string { return "Plex owned connect deadline" }
func (e *plexSDKConnectTimeout) Unwrap() error { return e.cause }
func plexSDKOwnedConnectDeadline(err error) bool {
	switch e := err.(type) {
	case *net.OpError:
		return plexSDKOwnedConnectDeadline(e.Err)
	case *os.SyscallError:
		return plexSDKOwnedConnectDeadline(e.Err)
	case *net.DNSError:
		return e.IsTimeout
	}
	return err == context.DeadlineExceeded || err == os.ErrDeadlineExceeded
}
