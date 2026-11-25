package plugins

import (
	"context"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type cancelFenceWriter struct {
	writes atomic.Int32
	err    error
}

func (w *cancelFenceWriter) Write(data []byte) (int, error) {
	w.writes.Add(1)
	if w.err != nil {
		return 0, w.err
	}
	return len(data), nil
}

func cancelFenceClient(t *testing.T, writer *cancelFenceWriter) (*concurrentClient, *concurrentHostCall) {
	t.Helper()
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx, revoke := context.WithCancel(life)
	t.Cleanup(revoke)
	wire := &wireSession{writer: writer, identity: ownedConcurrentBinding().Identity, ctx: life, maxFrame: 4096, limits: transportLimits{LogicalBytes: 4096}}
	client := &Client{session: wire, life: life, cancel: cancel, binding: ownedConcurrentBinding()}
	m := newConcurrentClient(client, concurrentProposal{Version: 1, MaxCalls: 32, MaxHostCalls: 1})
	call := &concurrentHostCall{id: "cancel-fence-call", ctx: ctx, cancel: revoke, sent: make(chan struct{})}
	close(call.sent)
	m.calls[call.id] = call
	return m, call
}

func TestConcurrentQueuedCancellationRespectsStopFence(t *testing.T) {
	for _, phase := range []string{"stop", "shutdown", "completed"} {
		t.Run(phase, func(t *testing.T) {
			writer := &cancelFenceWriter{err: io.ErrClosedPipe}
			m, call := cancelFenceClient(t, writer)
			m.transport.writeMu.Lock()
			locked := true
			defer func() {
				if locked {
					m.transport.writeMu.Unlock()
				}
			}()
			done := make(chan struct{})
			go func() { defer close(done); m.notifyCancellation(call) }()
			// Observe the actual queued writer, rather than relying on a sleep
			// to assume its old pre-lock liveness check has completed. No
			// production hook or altered transport implementation is needed.
			deadline := time.Now().Add(3 * time.Second)
			for {
				stack := make([]byte, 1<<20)
				stack = stack[:runtime.Stack(stack, true)]
				queued := false
				for _, goroutine := range strings.Split(string(stack), "\n\n") {
					if strings.Contains(goroutine, "(*concurrentClient).notifyCancellation") && strings.Contains(goroutine, "(*concurrentTransport).write") {
						queued = true
					}
				}
				if queued {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("cancellation did not queue at the transport writer")
				}
				runtime.Gosched()
			}
			m.mu.Lock()
			switch phase {
			case "stop":
				m.stopping = true
			case "shutdown":
				m.draining = true
			case "completed":
				delete(m.calls, call.id)
			}
			m.mu.Unlock()
			m.transport.writeMu.Unlock()
			locked = false
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("superseded cancellation blocked")
			}
			if writer.writes.Load() != 0 || m.terminal != nil || m.client.life.Err() != nil {
				t.Fatal("queued stale cancellation wrote after its fence or aborted the session", writer.writes.Load(), m.terminal)
			}
		})
	}
}

func TestConcurrentCancellationRetainsLiveWritesAndIOFailures(t *testing.T) {
	for _, failure := range []bool{false, true} {
		writer := &cancelFenceWriter{}
		if failure {
			writer.err = io.ErrClosedPipe
		}
		m, call := cancelFenceClient(t, writer)
		// A cleanup invocation can still need cancellation while ordinary
		// admission is fenced. Its actual I/O error must remain terminal.
		call.cleanup, m.draining = true, true
		m.notifyCancellation(call)
		if writer.writes.Load() != 1 || (m.terminal != nil) != failure || (m.client.life.Err() != nil) != failure {
			t.Fatal("live cleanup cancellation was dropped or its I/O failure hidden", failure, writer.writes.Load(), m.terminal)
		}
	}
}
