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
type trimeCurrentBoundary struct{ cause error }

func (*trimeCurrentBoundary) Error() string   { return "Trime current authority rejected" }
func (e *trimeCurrentBoundary) Unwrap() error { return e.cause }
func trimeCurrentAuthority(ctx context.Context, current TrimeHTTPCurrent) error {
	if e := trimeContext(ctx); e != nil {
		return e
	}
	e := current(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		return &trimeCurrentBoundary{cause: e}
	}
	return nil
}

// Marker originates only at this client's actual ten-second socket operation.
// It deliberately does not trust arbitrary errors exposing Timeout().
type trimeSocketTimeout struct {
	operation string
	cause     error
}

func (e *trimeSocketTimeout) Error() string { return "Trime socket " + e.operation + " deadline" }
func (e *trimeSocketTimeout) Unwrap() error { return e.cause }
func trimeOwnedSocketDeadline(err error) bool {
	switch e := err.(type) {
	case *net.OpError:
		return trimeOwnedSocketDeadline(e.Err)
	case *os.SyscallError:
		return trimeOwnedSocketDeadline(e.Err)
	}
	return err == os.ErrDeadlineExceeded
}

// This deliberately finite source-backed slice is not the full Requests
// exception taxonomy. Before response headers, EOF/protocol disconnection is
// ConnectionError and owned read deadline is ReadTimeout. Response content
// consumption converts interrupted framing to ChunkedEncodingError and a read
// deadline to ConnectionError (Requests Response.iter_content). Pre-GotConn TLS,
// DNS/connect/proxy errors and arbitrary wrappers remain unclassified.
func trimeResponseNetworkError(err error, body, connected bool) error {
	if !body && !connected {
		return err
	}
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
	if timeout, ok := leaf.(*trimeSocketTimeout); ok {
		if timeout.operation == "read" {
			if body {
				class = "ConnectionError"
			} else {
				class = "ReadTimeout"
			}
		} else if timeout.operation == "write" {
			class = "ConnectionError"
		}
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
