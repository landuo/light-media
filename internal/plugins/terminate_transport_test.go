package plugins

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type physicalCountedWriter struct {
	io.WriteCloser
	mu       sync.Mutex
	attempts int
	bytes    int
}

func (w *physicalCountedWriter) Write(raw []byte) (int, error) {
	w.mu.Lock()
	w.attempts++
	w.bytes += len(raw)
	w.mu.Unlock()
	return w.WriteCloser.Write(raw)
}

func (w *physicalCountedWriter) reset() {
	w.mu.Lock()
	w.attempts, w.bytes = 0, 0
	w.mu.Unlock()
}

func (w *physicalCountedWriter) assertNoWrites(t *testing.T) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.attempts != 0 || w.bytes != 0 {
		t.Fatalf("physical termination attempted business/transport writes: calls=%d bytes=%d", w.attempts, w.bytes)
	}
}

func ownedPhysicalStart(t *testing.T, legacy bool) *Client {
	t.Helper()
	args := []string{"--cleanup-host"}
	if legacy {
		args = append(args, "--legacy")
	}
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: !legacy}, args...)
	// Registered after the existing helper: always finish our deliberate
	// physical-termination scenario before that helper checks its real reaper.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.TerminateTransport(ctx)
		select {
		case <-c.done:
		default:
			t.Error("physical test cleanup had no actual reaper receipt")
			c.cancel()
			c.session.abort()
			<-c.done
		}
	})
	return c
}

func ownedPhysicalRecorder(t *testing.T, c *Client) *physicalCountedWriter {
	t.Helper()
	// This transparent test observer is installed only with an idle object.
	// Use the same serialization as every writer, not an unsynchronized swap.
	if c.concurrent != nil {
		c.concurrent.transport.writeMu.Lock()
		defer c.concurrent.transport.writeMu.Unlock()
	} else {
		c.gate <- struct{}{}
		defer func() { <-c.gate }()
	}
	closer, ok := c.session.writer.(io.WriteCloser)
	if !ok {
		t.Fatal("owned stdin is not a WriteCloser")
	}
	w := &physicalCountedWriter{WriteCloser: closer}
	c.session.writer = w
	return w
}

func ownedPhysicalReaped(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.done:
	default:
		t.Fatal("termination result returned before actual cmd.Wait")
	}
	// An intentional termination receipt is not a graceful exit receipt.
	if c.waitErr == nil {
		t.Fatal("owned forced termination unexpectedly claimed a zero child exit")
	}
	t.Logf("intentional physical termination observed actual nonzero child exit: %v", c.waitErr)
}

func TestOwnedPhysicalTransportTerminationLegacyNoBusiness(t *testing.T) {
	var missing *Client
	if codeOf(missing.TerminateTransport(context.Background())) != "invalid_invocation" {
		t.Fatal("nil client accepted")
	}
	c := ownedPhysicalStart(t, true)
	if codeOf(c.TerminateTransport(nil)) != "invalid_invocation" || c.stopRequested.Load() {
		t.Fatal("nil context changed live object")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if codeOf(c.TerminateTransport(canceled)) != "transport_termination_pending" || c.stopRequested.Load() {
		t.Fatal("pre-canceled caller received or fabricated a terminal receipt")
	}
	if c.SupportsTransportClose() || codeOf(c.CloseTransport(ownedContext(t))) != "transport_close_unsupported" {
		t.Fatal("legacy advertised graceful retirement")
	}
	if state := ownedState(t, c); state.PID == 0 {
		t.Fatal("unsupported/nil/canceled checks lost the actual worker")
	}
	writes := ownedPhysicalRecorder(t, c)
	var trace []string
	host := func(_ context.Context, _ Identity, method string, _ json.RawMessage) (json.RawMessage, error) {
		trace = append(trace, method)
		return json.RawMessage(`null`), nil
	}
	for _, method := range []string{"close", "stop_service"} {
		if _, err := c.Call(ownedContext(t), method, nil, host); err != nil {
			t.Fatal("source-owned lifecycle failed", method, err)
		}
	}
	if strings.Join(trace, ",") != "test.close,test.stop_service" {
		t.Fatal("unexpected business lifecycle trace", trace)
	}
	writes.reset()
	for i := 0; i < 2; i++ {
		if err := c.TerminateTransport(ownedContext(t)); err != nil {
			t.Fatal("intentional termination was not repeatable", err)
		}
		ownedPhysicalReaped(t, c)
	}
	if codeOf(c.Stop(ownedContext(t))) != intentionalTransportTermination {
		t.Fatal("Stop fabricated clean exit or lost saved intentional terminal")
	}
	if _, err := c.Call(ownedContext(t), "invoke", ownedInput("state", nil), nil); codeOf(err) != "session_closed" {
		t.Fatal("retired serial admission stayed open", err)
	}
	if c.SupportsTransportClose() || codeOf(c.CloseTransport(ownedContext(t))) != "transport_close_unsupported" {
		t.Fatal("physical termination changed legacy negotiation")
	}
	writes.assertNoWrites(t)
}

func TestOwnedPhysicalTransportTerminationExistingConcurrentNoBusiness(t *testing.T) {
	c := ownedPhysicalStart(t, false)
	_ = ownedState(t, c)
	writes := ownedPhysicalRecorder(t, c)
	ctx := ownedContext(t)
	start := make(chan struct{})
	results := make(chan error, 4)
	for i := 0; i < cap(results); i++ {
		go func() { <-start; results <- c.TerminateTransport(ctx) }()
	}
	close(start)
	for i := 0; i < cap(results); i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal("concurrent claimant/observer result", err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent termination did not complete")
		}
	}
	ownedPhysicalReaped(t, c)
	select {
	case <-c.concurrent.readerDone:
	default:
		t.Fatal("physical termination left its reader running")
	}
	if !c.SupportsTransportClose() || codeOf(c.CloseTransport(ownedContext(t))) != intentionalTransportTermination {
		t.Fatal("graceful API did not retain its advertisement and saved terminal")
	}
	if codeOf(c.Stop(ownedContext(t))) != intentionalTransportTermination {
		t.Fatal("Stop repeated business cleanup or lost intentional terminal")
	}
	writes.assertNoWrites(t)
}

func TestOwnedPhysicalTransportTerminationTerminalAndDeadline(t *testing.T) {
	t.Run("existing_serial_failure", func(t *testing.T) {
		c := ownedPhysicalStart(t, true)
		writes := ownedPhysicalRecorder(t, c)
		entered := make(chan struct{})
		callCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		returned := make(chan error, 1)
		go func() {
			_, err := c.Call(callCtx, "invoke", ownedInput("uncooperative", nil), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				close(entered)
				return json.RawMessage(`null`), nil
			})
			returned <- err
		}()
		ownedAwait(t, entered)
		cancel()
		select {
		case err := <-returned:
			if codeOf(err) != "execution_canceled" {
				t.Fatal("old serial terminal changed", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("actual serial cancellation did not join")
		}
		ownedPhysicalReaped(t, c)
		writes.reset()
		for i := 0; i < 2; i++ {
			if codeOf(c.TerminateTransport(ownedContext(t))) != "execution_canceled" {
				t.Fatal("known earlier failure was mapped to intentional success")
			}
		}
		if codeOf(c.Stop(ownedContext(t))) != "execution_canceled" {
			t.Fatal("earlier failure lost by repeated Stop")
		}
		writes.assertNoWrites(t)
	})

	t.Run("busy_serial_not_a_reap_receipt", func(t *testing.T) {
		c := ownedPhysicalStart(t, true)
		writes := ownedPhysicalRecorder(t, c)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		returned := make(chan error, 1)
		callCtx := ownedContext(t)
		go func() {
			_, err := c.Call(callCtx, "invoke", ownedInput("host", nil), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				close(entered)
				<-release
				return json.RawMessage(`null`), nil
			})
			returned <- err
		}()
		ownedAwait(t, entered)
		writes.reset()
		short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := c.TerminateTransport(short)
		cancel()
		if codeOf(err) != "transport_termination_pending" {
			t.Fatal("busy nonclaimant fabricated terminal receipt", err)
		}
		select {
		case <-c.done:
			t.Fatal("nonclaimant killed an operation it did not own")
		default:
		}
		writes.assertNoWrites(t)
		once.Do(func() { close(release) })
		select {
		case err := <-returned:
			if err != nil {
				t.Fatal("nonclaimant changed the already admitted call", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("held serial call did not finish after release")
		}
		writes.reset()
		if err := c.TerminateTransport(ownedContext(t)); err != nil {
			t.Fatal("fresh claimant could not finish physical termination", err)
		}
		ownedPhysicalReaped(t, c)
		writes.assertNoWrites(t)
	})

	t.Run("concurrent_observer_pending", func(t *testing.T) {
		c := ownedPhysicalStart(t, false)
		writes := ownedPhysicalRecorder(t, c)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		pending, err := c.StartCall(ownedContext(t), "invoke", ownedInput("host", nil), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
			close(entered)
			<-release
			return json.RawMessage(`null`), nil
		}, CallOptions{CallerDeadlineOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		ownedAwait(t, entered)
		writes.reset()
		ownerCtx := ownedContext(t)
		owner := make(chan error, 1)
		go func() { owner <- c.TerminateTransport(ownerCtx) }()
		ownedAwait(t, c.done)
		short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err = c.TerminateTransport(short)
		cancel()
		if codeOf(err) != "transport_termination_pending" {
			t.Fatal("observer bypassed the held claimant completion", err)
		}
		select {
		case err := <-owner:
			t.Fatal("claimant reported drain before held host returned", err)
		default:
		}
		once.Do(func() { close(release) })
		select {
		case err := <-owner:
			if err != nil {
				t.Fatal("actual claimant failed after host exit", err)
			}
		case <-ownerCtx.Done():
			t.Fatal("claimant did not finish after host release")
		}
		if _, err := pending.Wait(); err == nil {
			t.Fatal("terminated invocation fabricated business success")
		}
		ownedAwait(t, pending.Drained())
		ownedPhysicalReaped(t, c)
		if err := c.TerminateTransport(ownedContext(t)); err != nil {
			t.Fatal("observer did not see completed intentional termination", err)
		}
		writes.assertNoWrites(t)
	})
	t.Run("claimant_deadline_still_reaps", func(t *testing.T) {
		c := ownedPhysicalStart(t, false)
		writes := ownedPhysicalRecorder(t, c)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		pending, err := c.StartCall(ownedContext(t), "invoke", ownedInput("host", nil), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
			close(entered)
			<-release
			return json.RawMessage(`null`), nil
		}, CallOptions{CallerDeadlineOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		ownedAwait(t, entered)
		writes.reset()
		short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err = c.TerminateTransport(short)
		cancel()
		if codeOf(err) != "execution_canceled" {
			t.Fatal("claimant did not expose the held-host cleanup deadline", err)
		}
		ownedPhysicalReaped(t, c)
		select {
		case <-c.concurrent.readerDone:
		default:
			t.Fatal("deadline claimant returned before reader termination")
		}
		ownedDrainStillOpen(t, pending.Drained())
		if codeOf(c.TerminateTransport(ownedContext(t))) != "execution_canceled" || codeOf(c.Stop(ownedContext(t))) != "execution_canceled" {
			t.Fatal("claimant cleanup failure was lost on repeated observation")
		}
		once.Do(func() { close(release) })
		if _, err := pending.Wait(); err == nil {
			t.Fatal("deadline termination fabricated business success")
		}
		ownedAwait(t, pending.Drained())
		writes.assertNoWrites(t)
	})

}
