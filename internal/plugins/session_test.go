package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func sessionCommand(t *testing.T, scenario string) Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Path: path, Args: []string{"-test.run=^TestPersistentWorkerFixture$", "--", scenario}}
}
func sessionBinding() SessionBinding {
	return SessionBinding{Identity: Identity{PluginID: "Fixture", InstanceID: "Fixture.clone", Version: "1.2.3"}, Generation: 1}
}
func startSession(t *testing.T, runner Runner, scenario string) *Client {
	t.Helper()
	c, err := runner.Start(context.Background(), sessionCommand(t, scenario), fixtureManifest(), sessionBinding())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.Stop(ctx)
	})
	return c
}
func TestPersistentWorkerFixture(t *testing.T) {
	scenario := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			scenario = os.Args[i+1]
			break
		}
	}
	if scenario == "" {
		return
	}
	count := 0
	var old HostCaller
	err := Serve(context.Background(), os.Stdin, os.Stdout, fixtureManifest(), func(ctx context.Context, method string, payload json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			if scenario == "stop_host" {
				return host(ctx, "data.read", nil)
			}
			if scenario == "stop_error" {
				return nil, errors.New("private error")
			}
			return json.RawMessage(`null`), nil
		}
		count++
		if string(payload) == `"block"` {
			time.Sleep(30 * time.Second)
		}
		if string(payload) == `"fail"` {
			return nil, errors.New("private business error")
		}
		if string(payload) == `"old_callback"` {
			_, err := old(ctx, "data.read", nil)
			return json.Marshal(codeOf(err))
		}
		if string(payload) == `"host"` {
			old = host
			return host(ctx, "data.read", nil)
		}
		return json.RawMessage(strconv.Itoa(count)), nil
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func TestPersistentSessionRetainsObjectAndBusinessError(t *testing.T) {
	c := startSession(t, Runner{}, "state")
	for _, tc := range []struct{ payload, want, code string }{{`null`, `1`, ""}, {`"fail"`, "", "plugin_operation_failed"}, {`null`, `3`, ""}} {
		got, err := c.Call(context.Background(), "invoke", json.RawMessage(tc.payload), nil)
		if string(got.Result) != tc.want || codeOf(err) != tc.code {
			t.Fatalf("result=%s err=%v", got.Result, err)
		}
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "invoke", nil, nil); codeOf(err) != "session_closed" {
		t.Fatal(err)
	}
}
func TestPersistentSessionFreshAuthorityAndBudgets(t *testing.T) {
	c := startSession(t, Runner{MaxHostCalls: 1, MaxFrameBytes: 1024, MaxInputBytes: 2048, MaxOutputBytes: 2048}, "state")
	for i := 0; i < 20; i++ {
		want := strconv.Itoa(i)
		got, err := c.Call(context.Background(), "invoke", json.RawMessage(`"host"`), func(ctx context.Context, id Identity, method string, p json.RawMessage) (json.RawMessage, error) {
			if id != sessionBinding().Identity || method != "data.read" {
				t.Error("identity changed")
			}
			return json.RawMessage(want), nil
		})
		if err != nil || string(got.Result) != want || got.HostCalls != 1 {
			t.Fatalf("call %d: %+v %v", i, got, err)
		}
	}
	got, err := c.Call(context.Background(), "invoke", json.RawMessage(`"old_callback"`), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		t.Error("stale callback escaped phase")
		return nil, nil
	})
	if err != nil || string(got.Result) != `"invalid_host_call"` || got.HostCalls != 0 {
		t.Fatalf("stale callback: %+v %v", got, err)
	}
	_, err = c.Call(context.Background(), "invoke", json.RawMessage(`"host"`), nil)
	if codeOf(err) != "plugin_operation_failed" {
		t.Fatal("previous authority reused", err)
	}
	if _, err = c.Call(context.Background(), "invoke", nil, nil); err != nil {
		t.Fatal("business error discarded worker", err)
	}
}
func TestPersistentSessionIdleDoesNotUseCallTimeout(t *testing.T) {
	c := startSession(t, Runner{Timeout: 200 * time.Millisecond}, "state")
	time.Sleep(400 * time.Millisecond)
	got, err := c.Call(context.Background(), "invoke", nil, nil)
	if err != nil || string(got.Result) != "1" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestPersistentSessionCancellationIsTerminal(t *testing.T) {
	c := startSession(t, Runner{Timeout: time.Second}, "state")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	got, err := c.Call(ctx, "invoke", json.RawMessage(`"block"`), nil)
	if codeOf(err) != "execution_canceled" || got.Result != nil {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err = c.Call(context.Background(), "invoke", nil, nil); err == nil {
		t.Fatal("silently restarted")
	}
	if err = c.Stop(context.Background()); err == nil {
		t.Fatal("terminal failure lost")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("worker not joined")
	}
}
func TestPersistentSessionConcurrentCallsAreSerialized(t *testing.T) {
	c := startSession(t, Runner{}, "state")
	var wg sync.WaitGroup
	results := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := c.Call(context.Background(), "invoke", nil, nil)
			if err != nil {
				t.Error(err)
				return
			}
			results <- string(out.Result)
		}()
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	for value := range results {
		if seen[value] {
			t.Fatal("state repeated")
		}
		seen[value] = true
	}
	if len(seen) != 20 {
		t.Fatal(seen)
	}
}
func TestPersistentSessionStopFailureObservable(t *testing.T) {
	c := startSession(t, Runner{}, "stop_error")
	err := c.Stop(context.Background())
	if codeOf(err) != "plugin_operation_failed" {
		t.Fatal(err)
	}
	if again := c.Stop(context.Background()); codeOf(again) != codeOf(err) {
		t.Fatal("stop failure lost", again)
	}
}
func TestPersistentSessionRefusesLegacyWorker(t *testing.T) {
	_, err := (Runner{}).Start(context.Background(), fixtureCommand(t, "stop_failed"), fixtureManifest(), sessionBinding())
	if codeOf(err) != "persistent_session_unsupported" {
		t.Fatal(err)
	}
}

func TestPersistentSessionQueuedCancellationDoesNotDiscardObject(t *testing.T) {
	c := startSession(t, Runner{}, "state")
	c.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, "invoke", nil, nil); codeOf(err) != "execution_canceled" {
		t.Fatal(err)
	}
	<-c.gate
	out, err := c.Call(context.Background(), "invoke", nil, nil)
	if err != nil || string(out.Result) != "1" {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestPersistentSessionCloneObjectsAreSeparate(t *testing.T) {
	a := startSession(t, Runner{}, "state")
	b := startSession(t, Runner{}, "state")
	for _, c := range []*Client{a, a, b} {
		out, err := c.Call(context.Background(), "invoke", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c == b && string(out.Result) != "1" {
			t.Fatal("state leaked across objects", string(out.Result))
		}
	}
}
func TestPersistentSessionLifeCancellationKillsIdleWorker(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	c, err := (Runner{}).Start(life, sessionCommand(t, "state"), fixtureManifest(), sessionBinding())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle worker not joined")
	}
	if err := c.Stop(context.Background()); codeOf(err) != "execution_canceled" {
		t.Fatal(err)
	}
}

func TestPersistentSessionRejectsPreviousCallCallback(t *testing.T) {
	id := sessionBinding().Identity
	stale, _ := json.Marshal(Frame{Protocol: ProtocolVersion, ID: "previous-call.callback", Type: "host_call", Identity: id, Method: "data.read"})
	var output bytes.Buffer
	s := wireSession{ctx: context.Background(), identity: id, reader: bufio.NewReader(bytes.NewReader(append(stale, '\n'))), writer: &output, maxFrame: 4096, maxOutput: 4096, maxInput: 4096, maxHostCalls: 1, scopedCallbacks: true, host: func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("stale callback authorized")
		return nil, nil
	}}
	if _, err := s.exchange("invoke", nil); codeOf(err) != "invalid_host_call" {
		t.Fatal(err)
	}
}

func TestPersistentSessionChunkEpochIsBoundedAndReusable(t *testing.T) {
	c := startSession(t, Runner{MaxHostCalls: 1, MaxFrameBytes: 1024, MaxInputBytes: 16384, MaxOutputBytes: 16384, MaxChunkBytes: 256, MaxSessionChunks: 24}, "state")
	payload, _ := json.Marshal(strings.Repeat("x", 2048))
	for i := 0; i < 10; i++ {
		out, err := c.Call(context.Background(), "invoke", json.RawMessage(`"host"`), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) { return payload, nil })
		if err != nil || !bytes.Equal(out.Result, payload) || out.SentChunks == 0 || out.ReadChunks == 0 {
			t.Fatalf("call %d: %+v %v", i, out, err)
		}
		if len(c.session.seenTransfers) > c.session.limits.Transfers {
			t.Fatal("unbounded transfer map")
		}
	}
}
func TestPersistentSessionStopCancellationAbortsActiveCall(t *testing.T) {
	c := startSession(t, Runner{}, "state")
	entered := make(chan struct{})
	completed := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), "invoke", json.RawMessage(`"host"`), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		completed <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Stop(ctx); codeOf(err) != "execution_canceled" {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if codeOf(err) != "execution_canceled" {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active call stuck")
	}
	if err := c.Stop(context.Background()); err == nil {
		t.Fatal("cancellation failure lost")
	}
}

func TestPersistentSessionStopUsesFreshCleanupAuthority(t *testing.T) {
	c := startSession(t, Runner{}, "stop_host")
	prior := 0
	_, err := c.Call(context.Background(), "invoke", json.RawMessage(`"host"`), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		prior++
		return json.RawMessage(`null`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanup := 0
	err = c.StopWithHost(context.Background(), func(ctx context.Context, id Identity, method string, _ json.RawMessage) (json.RawMessage, error) {
		cleanup++
		if id != sessionBinding().Identity || method != "data.read" {
			t.Error("wrong cleanup scope")
		}
		return json.RawMessage(`null`), nil
	})
	if err != nil || prior != 1 || cleanup != 1 {
		t.Fatalf("err=%v prior=%d cleanup=%d", err, prior, cleanup)
	}
	if err = c.StopWithHost(context.Background(), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		t.Error("cleanup ran twice")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestPersistentSessionStopDefaultDoesNotReuseAuthority(t *testing.T) {
	c := startSession(t, Runner{}, "stop_host")
	calls := 0
	_, err := c.Call(context.Background(), "invoke", json.RawMessage(`"host"`), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`null`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Stop(context.Background()); codeOf(err) != "plugin_operation_failed" || calls != 1 {
		t.Fatalf("err=%v stale calls=%d", err, calls)
	}
}
