package requestsproxy

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

type requestsDialBindingKey struct{}
type requestsDialBinding struct {
	mu       sync.Mutex
	ctx      context.Context
	released bool
	stops    []func() bool
	conns    []net.Conn
}

func (b *requestsDialBinding) watch(conn net.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		conn.Close()
		return
	}
	b.stops = append(b.stops, context.AfterFunc(b.ctx, func() { conn.Close() }))
	b.conns = append(b.conns, conn)
}
func (b *requestsDialBinding) release() {
	b.mu.Lock()
	b.released = true
	stops, conns := b.stops, b.conns
	canceled := b.ctx.Err() != nil
	b.stops, b.conns = nil, nil
	b.mu.Unlock()
	for i, stop := range stops {
		stop()
		// RoundTrip may return cancellation before AfterFunc has run.
		// Stopping that callback must not orphan its handshake socket.
		if canceled {
			conns[i].Close()
		}
	}
}
func requestsBindDial(transport *http.Transport) *http.Transport {
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	wrap := func(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, network, address string) (net.Conn, error) {
			binding, _ := ctx.Value(requestsDialBindingKey{}).(*requestsDialBinding)
			if binding == nil {
				return dial(ctx, network, address)
			}
			linked, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(binding.ctx, cancel)
			defer stop()
			if deadline, ok := binding.ctx.Deadline(); ok {
				var deadlineCancel context.CancelFunc
				linked, deadlineCancel = context.WithDeadline(linked, deadline)
				defer deadlineCancel()
			}
			conn, err := dial(linked, network, address)
			if err != nil {
				return nil, err
			}
			binding.watch(conn)
			return conn, nil
		}
	}
	transport.DialContext = wrap(dial)
	if transport.DialTLSContext != nil {
		transport.DialTLSContext = wrap(transport.DialTLSContext)
	}
	return transport
}
