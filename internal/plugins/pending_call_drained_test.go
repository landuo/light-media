package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func ownedDrainWait(t *testing.T, pending *PendingCall) (RunResult, error) {
	t.Helper()
	type outcome struct {
		result RunResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := pending.Wait()
		finished <- outcome{result, err}
	}()
	select {
	case out := <-finished:
		return out.result, out.err
	case <-time.After(3 * time.Second):
		t.Fatal("caller Wait did not return at its existing boundary")
		return RunResult{}, nil
	}
}

func ownedDrainStillOpen(t *testing.T, drained <-chan struct{}) {
	t.Helper()
	if drained == nil {
		t.Fatal("accepted call has no drain observation")
	}
	select {
	case <-drained:
		t.Fatal("drain reported before the actual completion barrier")
	default:
	}
}

func TestOwnedPendingCallDrainedSuccessAndZeroByteFailure(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true, MaxFrameBytes: 1024, MaxMessageBytes: 1024})
	pending, err := c.StartCall(ownedContext(t), "invoke", ownedInput("host", json.RawMessage(`"owned"`)),
		func(_ context.Context, _ Identity, _ string, payload json.RawMessage) (json.RawMessage, error) {
			return payload, nil
		}, CallOptions{CallerDeadlineOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var observers sync.WaitGroup
	observers.Add(3)
	for i := 0; i < 3; i++ {
		go func() {
			defer observers.Done()
			<-pending.Drained()
		}()
	}
	observed := make(chan struct{})
	go func() { observers.Wait(); close(observed) }()
	out, err := ownedDrainWait(t, pending)
	if err != nil || string(out.Result) != `"owned"` {
		t.Fatal("successful result changed", err, string(out.Result))
	}
	ownedAwait(t, observed)
	out.Result[0] = 'x'
	again, err := pending.Wait()
	if err != nil || string(again.Result) != `"owned"` {
		t.Fatal("drain observation consumed or aliased the stored result", err)
	}

	before := ownedState(t, c)
	value, _ := json.Marshal(strings.Repeat("x", 2048))
	rejected, err := c.StartCall(ownedContext(t), "invoke", ownedInput("host", value), nil, CallOptions{CallerDeadlineOnly: true})
	if err != nil || rejected == nil {
		t.Fatal("local prewrite error was confused with admission rejection", err)
	}
	out, err = ownedDrainWait(t, rejected)
	if codeOf(err) != "message_limit" || out.SentWireBytes != 0 || out.HostCalls != 0 {
		t.Fatal("not the real zero-byte local rejection", out, err)
	}
	ownedAwait(t, rejected.Drained())
	after := ownedState(t, c)
	if before.PID != after.PID || after.Calls != before.Calls+1 {
		t.Fatal("zero-byte rejection invoked or restarted the worker", before, after)
	}
}

func TestOwnedPendingCallDrainedCancellationWaitsForWorkerAndHost(t *testing.T) {
	t.Run("worker_handler", func(t *testing.T) {
		c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
		entered := make(chan struct{})
		pending, err := c.StartCall(ownedContext(t), "invoke", ownedInput("wait_cleanup", nil),
			func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				close(entered)
				return json.RawMessage(`null`), nil
			}, CallOptions{CallerDeadlineOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		var releaseOnce sync.Once
		var releaseErr error
		releaseWorker := func() {
			releaseOnce.Do(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, releaseErr = c.Call(ctx, "close", json.RawMessage(`null`),
					func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
						return json.RawMessage(`null`), nil
					})
			})
		}
		defer releaseWorker()
		ownedAwait(t, entered)
		pending.Cancel()
		if _, err := ownedDrainWait(t, pending); codeOf(err) != "execution_canceled" {
			t.Fatal("caller cancellation changed", err)
		}
		// Existing wait_cleanup cannot leave its worker handler until close
		// releases it, even after its HostCaller and caller Wait are canceled.
		ownedDrainStillOpen(t, pending.Drained())
		releaseWorker()
		if releaseErr != nil {
			t.Fatal("ordinary close did not release the real handler", releaseErr)
		}
		ownedAwait(t, pending.Drained())
		_ = ownedState(t, c)
	})

	t.Run("host_callback", func(t *testing.T) {
		c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
		entered, exited, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		pending, err := c.StartCall(ownedContext(t), "invoke", ownedInput("host", nil),
			func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				close(entered)
				defer close(exited)
				<-release // Deliberately do not treat cancellation as handler exit.
				return json.RawMessage(`"late"`), nil
			}, CallOptions{CallerDeadlineOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		ownedAwait(t, entered)
		pending.Cancel()
		if _, err := ownedDrainWait(t, pending); codeOf(err) != "execution_canceled" {
			t.Fatal(err)
		}
		ownedDrainStillOpen(t, pending.Drained())
		once.Do(func() { close(release) })
		ownedAwait(t, pending.Drained())
		select {
		case <-exited:
		default:
			t.Fatal("host callback still active after drain")
		}
		_ = ownedState(t, c)
	})
}

func TestOwnedPendingCallDrainedTerminalRequiresReaperAndHost(t *testing.T) {
	t.Run("actual_transport", func(t *testing.T) {
		c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
		entered, exited, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		t.Cleanup(func() {
			once.Do(func() { close(release) })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// This cleanup also records Stop's terminal state for the existing
			// owned helper. The deliberately injected error is checked below.
			_ = c.CloseTransport(ctx)
		})
		pending, err := c.StartCall(ownedContext(t), "invoke", ownedInput("host", nil),
			func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				close(entered)
				defer close(exited)
				<-release
				return json.RawMessage(`null`), nil
			}, CallOptions{CallerDeadlineOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		ownedAwait(t, entered)
		c.concurrent.abort(failure("worker_io_failed"))
		if _, err := ownedDrainWait(t, pending); codeOf(err) != "worker_io_failed" {
			t.Fatal("injected hard transport failure was lost", err)
		}
		ownedAwait(t, c.done)
		if c.waitErr == nil {
			t.Fatal("hard transport failure did not observe actual failed worker exit")
		}
		ownedDrainStillOpen(t, pending.Drained())
		once.Do(func() { close(release) })
		ownedAwait(t, pending.Drained())
		select {
		case <-exited:
		default:
			t.Fatal("terminal drain ignored the host callback")
		}
		if err := c.CloseTransport(ownedContext(t)); codeOf(err) != "worker_io_failed" {
			t.Fatal("existing terminal join result changed", err)
		}
	})

	t.Run("controlled_reaper_and_once", func(t *testing.T) {
		// A synchronization unit, not a fabricated cmd.Wait receipt: the
		// preceding subtest separately uses a real independently reaped worker.
		reaped := make(chan struct{})
		var reapedOnce sync.Once
		defer reapedOnce.Do(func() { close(reaped) })
		m := &concurrentClient{client: &Client{done: reaped}}
		call := &concurrentHostCall{drained: make(chan struct{})}
		finished := make(chan struct{})
		go func() { m.observeTerminatedCall(call); close(finished) }()
		select {
		case <-finished:
			t.Fatal("terminal observer ignored the process-reaper barrier")
		case <-time.After(20 * time.Millisecond):
		}
		ownedDrainStillOpen(t, call.drained)
		reapedOnce.Do(func() { close(reaped) })
		ownedAwait(t, finished)
		ownedAwait(t, call.drained)
		var repeated sync.WaitGroup
		raceCall := &concurrentHostCall{drained: make(chan struct{})}
		start := make(chan struct{})
		repeated.Add(2)
		go func() { defer repeated.Done(); <-start; m.observeTerminatedCall(raceCall) }()
		go func() { defer repeated.Done(); <-start; raceCall.notifyDrained() }()
		close(start)
		repeated.Wait()
		ownedAwait(t, raceCall.drained)
	})
}

func TestOwnedPendingCallDrainedUnusedObserverAndShutdownBoundary(t *testing.T) {
	var missing *PendingCall
	if missing.Drained() != nil || new(PendingCall).Drained() != nil {
		t.Fatal("nil/zero call fabricated a completed observation")
	}
	legacy := ownedConcurrentStart(t, Runner{}, "--legacy")
	if p, err := legacy.StartCall(ownedContext(t), "invoke", ownedInput("state", nil), nil, CallOptions{}); p != nil || codeOf(err) != "concurrent_session_unsupported" {
		t.Fatal("legacy admission changed", err)
	}
	_ = ownedState(t, legacy)

	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := c.StartCallWithAdmission(canceled, context.Background(), "invoke", ownedInput("state", nil), nil, CallOptions{CallerDeadlineOnly: true}); p != nil || codeOf(err) != "execution_canceled" {
		t.Fatal("canceled admission changed", err)
	}
	_ = ownedState(t, c) // Ordinary Call supplies no drain observer.
	if _, err := c.CallCleanup(ownedContext(t), "close", nil, nil); codeOf(err) != "shutdown_not_started" {
		t.Fatal("drain API relaxed the cleanup fence", err)
	}
	entered := make(chan struct{})
	ordinary := make(chan error, 1)
	ordinaryContext := ownedContext(t)
	go func() {
		_, err := c.Call(ordinaryContext, "invoke", ownedInput("wait_cleanup", nil),
			func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				close(entered)
				return json.RawMessage(`null`), nil
			})
		ordinary <- err
	}()
	ownedAwait(t, entered)
	if err := c.BeginShutdown(ownedContext(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(ownedContext(t), "invoke", ownedInput("state", nil), nil); codeOf(err) != "session_closed" {
		t.Fatal("shutdown failed to fence ordinary admission", err)
	}
	if _, err := c.CallCleanup(ownedContext(t), "close", nil,
		func(_ context.Context, _ Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
			var state struct {
				Old string `json:"old_callback"`
			}
			if method != "test.close" || json.Unmarshal(payload, &state) != nil || state.Old != "invalid_host_call" {
				t.Error("shutdown failed to revoke the old callback", method, string(payload))
			}
			return json.RawMessage(`null`), nil
		}); err != nil {
		t.Fatal("fresh cleanup lane changed", err)
	}
	select {
	case err := <-ordinary:
		if codeOf(err) != "execution_canceled" {
			t.Fatal("ordinary nil-observer cancellation changed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nil-observer ordinary call did not complete")
	}
	if err := c.Stop(ownedContext(t)); err != nil {
		t.Fatal("existing forced final join changed", err)
	}
	ownedAwait(t, c.done)
}
