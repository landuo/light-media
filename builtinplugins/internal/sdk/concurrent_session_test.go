package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The fixture is a separately built public-SDK executable, not this test
// binary and not an in-memory/mock transport. Central gates build it both
// normally and with -race before running these tests with the matching host.
func ownedConcurrentCommand(t *testing.T, args ...string) Command {
	t.Helper()
	path := os.Getenv("LIGHT_MEDIA_CONCURRENT_FIXTURE")
	if path == "" {
		t.Skip("build cmd/owned-concurrent-fixture and set LIGHT_MEDIA_CONCURRENT_FIXTURE; this is not a passed fixture gate")
	}
	if !filepath.IsAbs(path) {
		t.Fatal("fixture path must be absolute")
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		t.Fatal("independent fixture binary unavailable", err)
	}
	return Command{Path: path, Args: args}
}
func ownedConcurrentManifest() Manifest {
	return Manifest{SchemaVersion: 1, PluginID: "ConcurrentFixture", Name: "Concurrent SDK fixture", Version: "1.0.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "close", "stop_service", "stop"}, Capabilities: []string{"test.host"}}
}
func ownedConcurrentBinding() SessionBinding {
	return SessionBinding{Identity: Identity{PluginID: "ConcurrentFixture", InstanceID: "ConcurrentFixture.clone", Version: "1.0.0"}, Generation: 7}
}
func ownedConcurrentStart(t *testing.T, runner Runner, args ...string) *Client {
	t.Helper()
	runner.ConcurrentCalls = true
	if runner.Timeout == 0 {
		runner.Timeout = 5 * time.Second
	}
	c, err := runner.Start(context.Background(), ownedConcurrentCommand(t, args...), ownedConcurrentManifest(), ownedConcurrentBinding())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer func() {
			if bytes, exceeded := c.stderr.snapshot(); bytes != 0 || exceeded {
				t.Errorf("independent fixture emitted stderr/race diagnostics: bytes=%d exceeded=%v", bytes, exceeded)
			}
		}()
		finalStop := c.stopRequested.Load()
		if c.concurrent != nil {
			c.concurrent.mu.Lock()
			finalStop = c.concurrent.stopDone != nil
			c.concurrent.mu.Unlock()
		}
		if finalStop {
			select {
			case <-c.done:
			default:
				t.Error("explicit Stop did not reap fixture")
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := c.Stop(ctx); err != nil {
			t.Error("fixture cleanup/child exit failed (including child race reports)", err)
		}
	})
	return c
}
func ownedInput(op string, value json.RawMessage) json.RawMessage {
	raw, _ := json.Marshal(struct {
		Op    string          `json:"op"`
		Value json.RawMessage `json:"value"`
	}{op, value})
	return raw
}
func ownedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func ownedState(t *testing.T, c *Client) struct {
	PID   int `json:"pid"`
	Calls int `json:"calls"`
} {
	t.Helper()
	var state struct {
		PID   int `json:"pid"`
		Calls int `json:"calls"`
	}
	out, err := c.Call(ownedContext(t), "invoke", ownedInput("state", nil), nil)
	if err != nil || json.Unmarshal(out.Result, &state) != nil {
		t.Fatal("state", err, string(out.Result))
	}
	return state
}
func ownedAwait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not reach barrier")
	}
}

func TestOwnedConcurrentSameWorkerOverlapAndChunkIsolation(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true, MaxFrameBytes: 1024, MaxChunkBytes: 128})
	if !c.SupportsConcurrent() {
		t.Fatal("negotiation did not enable concurrency")
	}
	before := ownedState(t, c)
	entered := make(chan string, 2)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var wg sync.WaitGroup
	ids := sync.Map{}
	for _, label := range []string{"A", "B"} {
		label := label
		value, _ := json.Marshal(strings.Repeat(label, 48<<10))
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := c.Call(ownedContext(t), "invoke", ownedInput("host", value), func(ctx context.Context, id Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
				if id != ownedConcurrentBinding().Identity || method != "test.host" || !bytes.Equal(payload, value) {
					t.Error("authority/payload crossed call boundary")
				}
				callID := InvocationID(ctx)
				if callID == "" {
					t.Error("missing host generated call identity")
				}
				if _, dup := ids.LoadOrStore(callID, true); dup {
					t.Error("two invocations shared call identity")
				}
				entered <- label
				select {
				case <-release:
					return value, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			if err != nil || !bytes.Equal(out.Result, value) || out.HostCalls != 1 || out.ReadChunks == 0 || out.SentChunks == 0 {
				t.Errorf("%s: hostcalls=%d chunks=%d/%d bytes=%d err=%v", label, out.HostCalls, out.SentChunks, out.ReadChunks, len(out.Result), err)
			}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			once.Do(func() { close(release) })
			wg.Wait()
			t.Fatal("handlers were serialized instead of overlapping")
		}
	}
	once.Do(func() { close(release) })
	wg.Wait()
	after := ownedState(t, c)
	if before.PID != after.PID || before.PID == os.Getpid() || after.Calls != before.Calls+3 {
		t.Fatalf("not one persistent independent worker object: before=%+v after=%+v", before, after)
	}
}

func TestOwnedConcurrentCancelIsolatedAndObjectReusable(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
	before := ownedState(t, c)
	ctx, cancel := context.WithCancel(ownedContext(t))
	defer cancel()
	entered := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, "invoke", ownedInput("wait", nil), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
			close(entered)
			return json.RawMessage(`null`), nil
		})
		finished <- err
	}()
	ownedAwait(t, entered)
	otherEntered := make(chan struct{})
	releaseOther := make(chan struct{})
	otherDone := make(chan error, 1)
	go func() {
		out, err := c.Call(ownedContext(t), "invoke", ownedInput("host", json.RawMessage(`"other"`)), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
			close(otherEntered)
			select {
			case <-releaseOther:
				return json.RawMessage(`"other"`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		if err == nil && string(out.Result) != `"other"` {
			err = failure("wrong_result")
		}
		otherDone <- err
	}()
	ownedAwait(t, otherEntered)
	cancel()
	select {
	case err := <-finished:
		if codeOf(err) != "execution_canceled" {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("single-call cancellation stalled")
	}
	close(releaseOther)
	if err := <-otherDone; err != nil {
		t.Fatal("cancel poisoned other call", err)
	}
	after := ownedState(t, c)
	if before.PID != after.PID {
		t.Fatal("worker silently restarted")
	}
}

func TestOwnedConcurrentErrorsAndRetainedAuthority(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
	ctx := ownedContext(t)
	var granted atomic.Int64
	host := func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		granted.Add(1)
		return json.RawMessage(`"granted"`), nil
	}
	out, err := c.Call(ctx, "invoke", ownedInput("remember", nil), host)
	if err != nil || string(out.Result) != `"granted"` {
		t.Fatal(err)
	}
	out, err = c.Call(ctx, "invoke", ownedInput("reuse", nil), host)
	if err != nil || string(out.Result) != `"invalid_host_call"` || granted.Load() != 1 || out.HostCalls != 0 {
		t.Fatalf("old phase retained authority: %s %v %d", out.Result, err, granted.Load())
	}
	_, err = c.Call(ctx, "invoke", ownedInput("host", nil), nil)
	if codeOf(err) != "plugin_operation_failed" || granted.Load() != 1 {
		t.Fatal("prior HostHandler reused", err)
	}
	failures := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "invoke", ownedInput("fail", nil), host); failures <- err }()
	out, err = c.Call(ctx, "invoke", ownedInput("host", nil), host)
	if err != nil || string(out.Result) != `"granted"` || codeOf(<-failures) != "plugin_operation_failed" {
		t.Fatal("business error crossed invocations", err)
	}
	_ = ownedState(t, c)
}

func TestOwnedConcurrentCallbackDeadlineDoesNotKillOtherCalls(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
	out, err := c.Call(ownedContext(t), "invoke", ownedInput("host_timeout", nil), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if codeOf(err) != "execution_canceled" || out.Result != nil {
		t.Fatal(err, string(out.Result))
	}
	_ = ownedState(t, c)
}

func TestOwnedConcurrentStopRevokesJoinsAndUsesFreshCleanup(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true}, "--cleanup-host")
	entered := make(chan struct{})
	revoked := make(chan struct{})
	finished := make(chan error, 1)
	var oldCalls atomic.Int64
	go func() {
		_, err := c.Call(ownedContext(t), "invoke", ownedInput("host", nil), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
			oldCalls.Add(1)
			close(entered)
			<-ctx.Done()
			close(revoked)
			return nil, ctx.Err()
		})
		finished <- err
	}()
	ownedAwait(t, entered)
	cleanup := 0
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := c.StopWithHost(ctx, func(ctx context.Context, id Identity, method string, _ json.RawMessage) (json.RawMessage, error) {
		cleanup++
		if id != ownedConcurrentBinding().Identity || method != "test.cleanup" || InvocationID(ctx) == "" {
			t.Error("wrong cleanup authority")
		}
		select {
		case <-revoked:
		default:
			t.Error("cleanup ran before ordinary authority revoked")
		}
		return json.RawMessage(`null`), nil
	})
	if err != nil || cleanup != 1 || oldCalls.Load() != 1 {
		t.Fatalf("stop=%v cleanup=%d prior=%d", err, cleanup, oldCalls.Load())
	}
	if codeOf(<-finished) != "execution_canceled" {
		t.Fatal("active call not canceled")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("worker not reaped")
	}
	if err = c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Call(context.Background(), "invoke", ownedInput("state", nil), nil); codeOf(err) != "session_closed" {
		t.Fatal(err)
	}
}

func TestOwnedConcurrentStopDeadlineReapsUncooperativeWorker(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
	entered := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := c.Call(ownedContext(t), "invoke", ownedInput("uncooperative", nil), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
			close(entered)
			return json.RawMessage(`null`), nil
		})
		finished <- err
	}()
	ownedAwait(t, entered)
	// A host-side callback barrier only proves request receipt. It does not
	// prove that the worker consumed the host result and entered the actual
	// uncooperative region. Observe that region through a second real call.
	proofCtx, cancelProof := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelProof()
	blockedPID := 0
	for {
		out, err := c.Call(proofCtx, "invoke", ownedInput("state", nil), nil)
		if err != nil {
			t.Fatal("worker did not prove entry into uncooperative region", err)
		}
		var proof struct {
			Uncooperative bool `json:"uncooperative"`
			PID           int  `json:"pid"`
		}
		if json.Unmarshal(out.Result, &proof) != nil {
			t.Fatal("invalid uncooperative-state proof")
		}
		if proof.Uncooperative {
			blockedPID = proof.PID
			break
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-proofCtx.Done():
			t.Fatal("worker never entered uncooperative region")
		}
	}
	cancelProof()
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := c.Stop(ctx); codeOf(err) != "execution_canceled" {
		t.Fatal(err)
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatal("Stop canceled for a reason other than its resource deadline")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("uncooperative process not reaped")
	}
	if c.waitErr == nil {
		t.Fatal("proved-uncooperative process exited cleanly instead of deadline kill/reap")
	}
	t.Logf("worker pid=%d proved noncooperative before Stop; deadline kill/reap took %s", blockedPID, time.Since(started))
	select {
	case err := <-finished:
		if codeOf(err) != "execution_canceled" {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("call leaked after stop")
	}
}

func TestOwnedConcurrentNegotiatesFallbackAndStrictRequirement(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{}, "--legacy")
	if c.SupportsConcurrent() {
		t.Fatal("legacy worker misrepresented as concurrent")
	}
	_ = ownedState(t, c)
	_, err := (Runner{RequireConcurrent: true, Timeout: 3 * time.Second}).Start(context.Background(), ownedConcurrentCommand(t, "--legacy"), ownedConcurrentManifest(), ownedConcurrentBinding())
	if codeOf(err) != "concurrent_session_unsupported" {
		t.Fatal(err)
	}
}

func TestOwnedConcurrentCallerDeadlineOnlyIsExplicit(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true, Timeout: 2 * time.Second})
	host := func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
		timer := time.NewTimer(2100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return json.RawMessage(`"done"`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	started := time.Now()
	out, err := c.CallWithOptions(ownedContext(t), "invoke", ownedInput("host", nil), host, CallOptions{CallerDeadlineOnly: true})
	elapsed := time.Since(started)
	if err != nil || string(out.Result) != `"done"` || elapsed <= c.runner.Timeout {
		t.Fatal("caller-owned positive did not exceed the startup/default budget", err, elapsed)
	}
	t.Logf("source-owned call completed in %s beyond %s startup/default budget", elapsed, c.runner.Timeout)
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, err = c.CallWithOptions(short, "invoke", ownedInput("host", nil), host, CallOptions{CallerDeadlineOnly: true})
	cancel()
	if codeOf(err) != "execution_canceled" {
		t.Fatal("CallerDeadlineOnly ignored caller deadline", err)
	}
	_, err = c.CallWithOptions(ownedContext(t), "invoke", ownedInput("host", nil), host, CallOptions{Timeout: 20 * time.Millisecond})
	if codeOf(err) != "execution_canceled" {
		t.Fatal(err)
	}
	_ = ownedState(t, c)
	if _, err = c.CallWithOptions(ownedContext(t), "invoke", ownedInput("state", nil), nil, CallOptions{CallerDeadlineOnly: true, Timeout: time.Second}); codeOf(err) != "invalid_limits" {
		t.Fatal("ambiguous budget accepted", err)
	}
}

func TestOwnedConcurrentFullCapacityDoesNotBlockCleanup(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true}, "--cleanup-host")
	entered := make(chan struct{}, maxConcurrentCalls)
	errors := make(chan error, maxConcurrentCalls)
	for i := 0; i < maxConcurrentCalls; i++ {
		go func() {
			_, err := c.Call(ownedContext(t), "invoke", ownedInput("host", nil), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			})
			errors <- err
		}()
	}
	for i := 0; i < maxConcurrentCalls; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("capacity setup did not enter all handlers")
		}
	}
	if _, err := c.Call(ownedContext(t), "invoke", ownedInput("state", nil), nil); codeOf(err) != "concurrent_call_limit" {
		t.Fatal("resource admission was not bounded", err)
	}
	cleanup := 0
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.StopWithHost(ctx, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		cleanup++
		return json.RawMessage(`null`), nil
	}); err != nil {
		t.Fatal("cleanup blocked behind full call cap", err)
	}
	if cleanup != 1 {
		t.Fatal("cleanup not run exactly once")
	}
	for i := 0; i < maxConcurrentCalls; i++ {
		select {
		case err := <-errors:
			if codeOf(err) != "execution_canceled" {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("invocation not joined")
		}
	}
}

func TestOwnedConcurrentShutdownReservedManagerPhasesAtCapacity(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true}, "--cleanup-host")
	entered := make(chan struct{}, maxConcurrentCalls)
	finished := make(chan error, maxConcurrentCalls)
	var prior atomic.Int64
	for i := 0; i < maxConcurrentCalls; i++ {
		go func() {
			_, err := c.Call(ownedContext(t), "invoke", ownedInput("wait_cleanup", nil), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				prior.Add(1)
				entered <- struct{}{}
				return json.RawMessage(`null`), nil
			})
			finished <- err
		}()
	}
	for i := 0; i < maxConcurrentCalls; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("not all 32 real handlers entered cleanup-dependent work")
		}
	}
	if _, err := c.Call(ownedContext(t), "invoke", ownedInput("state", nil), nil); codeOf(err) != "concurrent_call_limit" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.BeginShutdown(ctx); err != nil {
		t.Fatal("shutdown fence waited for handlers that only close can release", err)
	}
	if _, err := c.Call(ctx, "invoke", ownedInput("state", nil), nil); codeOf(err) != "session_closed" {
		t.Fatal("ordinary admission reopened", err)
	}
	var order []string
	fresh := func(want string) HostHandler {
		return func(ctx context.Context, id Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
			if id != ownedConcurrentBinding().Identity || method != want || InvocationID(ctx) == "" {
				t.Error("wrong fresh cleanup authority", method)
			}
			if want == "test.close" || want == "test.stop_service" {
				var result struct {
					Old string `json:"old_callback"`
				}
				if json.Unmarshal(payload, &result) != nil || result.Old != "invalid_host_call" {
					t.Error("old caller survived shutdown fence", string(payload))
				}
			}
			order = append(order, want)
			return json.RawMessage(`null`), nil
		}
	}
	if _, err := c.CallCleanup(ctx, "close", nil, fresh("test.close")); err != nil {
		t.Fatal("close blocked by full ordinary capacity", err)
	}
	if _, err := c.CallCleanup(ctx, "stop_service", nil, fresh("test.stop_service")); err != nil {
		t.Fatal(err)
	}
	if err := c.StopWithHost(ctx, fresh("test.cleanup")); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "test.close,test.stop_service,test.cleanup" {
		t.Fatal("manager-owned order changed", order)
	}
	if prior.Load() != maxConcurrentCalls {
		t.Fatal("ordinary authority was reused", prior.Load())
	}
	for i := 0; i < maxConcurrentCalls; i++ {
		select {
		case err := <-finished:
			if codeOf(err) != "execution_canceled" {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("ordinary call did not cancel")
		}
	}
	select {
	case <-c.done:
	default:
		t.Fatal("cleanup did not reap worker")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal("repeated Stop changed result", err)
	}
}

func TestOwnedConcurrentShutdownCloseFailurePolicyStaysInManager(t *testing.T) {
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true}, "--cleanup-host", "--fail-close")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.BeginShutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var order []string
	host := func(_ context.Context, _ Identity, method string, _ json.RawMessage) (json.RawMessage, error) {
		order = append(order, method)
		return json.RawMessage(`null`), nil
	}
	_, closeErr := c.CallCleanup(ctx, "close", nil, host)
	if codeOf(closeErr) != "plugin_operation_failed" {
		t.Fatal("owned close failure not propagated", closeErr)
	}
	// This is deliberately the caller's manager policy, not SDK behavior.
	if closeErr == nil {
		if _, err := c.CallCleanup(ctx, "stop_service", nil, host); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.StopWithHost(ctx, host); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "test.close,test.cleanup" {
		t.Fatal("failed close did not skip service in manager order", order)
	}
}

func TestOwnedConcurrentShutdownAPIAuthorityAndLegacyBoundary(t *testing.T) {
	legacy := ownedConcurrentStart(t, Runner{}, "--legacy")
	if err := legacy.BeginShutdown(ownedContext(t)); codeOf(err) != "concurrent_session_unsupported" {
		t.Fatal(err)
	}
	if _, err := legacy.CallCleanup(ownedContext(t), "close", nil, nil); codeOf(err) != "concurrent_session_unsupported" {
		t.Fatal(err)
	}
	_ = ownedState(t, legacy)
	c := ownedConcurrentStart(t, Runner{RequireConcurrent: true})
	if _, err := c.CallCleanup(ownedContext(t), "close", nil, nil); codeOf(err) != "shutdown_not_started" {
		t.Fatal("cleanup was authorized before host shutdown", err)
	}
	_ = ownedState(t, c)
	if err := c.BeginShutdown(ownedContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := c.BeginShutdown(ownedContext(t)); err != nil {
		t.Fatal("shutdown fence not idempotent", err)
	}
	if _, err := c.CallCleanup(ownedContext(t), "invoke", ownedInput("state", nil), nil); codeOf(err) != "invalid_invocation" {
		t.Fatal("arbitrary method accessed cleanup lane", err)
	}
	if err := c.Stop(ownedContext(t)); err != nil {
		t.Fatal(err)
	}
}
