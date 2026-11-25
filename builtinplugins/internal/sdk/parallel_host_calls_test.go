package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func parallelManifest() Manifest {
	return Manifest{SchemaVersion: 1, PluginID: "ParallelFixture", Name: "Owned work overlay", Version: "1.0", Runtime: "go-executable", Entry: "fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "phase", "state", "stop"}, Capabilities: []string{"test.host", BackgroundSubmitCapability}}
}
func pRaw(x any) json.RawMessage { b, _ := json.Marshal(x); return b }

type parallelInput struct {
	Op     string
	Count  int
	Offset int
	Early  bool
	Blob   string
}
type parallelResult struct {
	Results           []json.RawMessage
	Errors            []string
	Before, After     HostCallBudgetSnapshot
	BeforeOK, AfterOK bool
}

// This child is a separate actual Go process using the overlay SDK. The only
// synthetic layer is its finite test handler/owned HTTP resource implementation.
func TestParallelOverlayFixtureWorker(t *testing.T) {
	if len(os.Args) < 2 || !strings.HasPrefix(os.Args[len(os.Args)-1], "owned-parallel-") {
		t.Skip("child-only entry")
	}
	mode := strings.TrimPrefix(os.Args[len(os.Args)-1], "owned-parallel-")
	var staleCtx context.Context
	var stale HostCaller
	var stateMu sync.Mutex
	var pass sync.WaitGroup
	var h Handler
	h = func(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if len(os.Args) > 2 && strings.HasPrefix(os.Args[len(os.Args)-2], "owned-marker=") {
			os.WriteFile(strings.TrimPrefix(os.Args[len(os.Args)-2], "owned-marker="), []byte("business invoked"), 0600)
		}

		if method == "stop" {
			return json.RawMessage(`null`), nil
		}
		if method == "state" {
			stateMu.Lock()
			oldctx, old := staleCtx, stale
			stateMu.Unlock()
			_, ok := CurrentHostCallBudget(oldctx)
			_, e := old(context.Background(), "test.host", json.RawMessage(`-9`))
			return pRaw(map[string]any{"budget": ok, "error": e != nil}), nil
		}
		var in parallelInput
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		if in.Op == "pass" {
			pass.Add(2)
			if _, e := host(ctx, BackgroundSubmitCapability, pRaw(0)); e != nil {
				return nil, e
			}
			pass.Wait()
			return json.RawMessage(`true`), nil
		}
		if in.Op == "sequential" {
			var e error
			var r json.RawMessage
			for i := 0; i < in.Count; i++ {
				r, e = host(ctx, "test.host", pRaw(i))
				if e != nil {
					return pRaw(e.Error()), nil
				}
			}
			return r, nil
		}
		out := parallelResult{Results: make([]json.RawMessage, in.Count), Errors: make([]string, in.Count)}
		out.Before, out.BeforeOK = CurrentHostCallBudget(ctx)
		stateMu.Lock()
		staleCtx, stale = ctx, host
		stateMu.Unlock()
		var wg sync.WaitGroup
		next := make(chan struct{})
		wg.Add(in.Count)
		for i := 0; i < in.Count; i++ {
			go func(i int) {
				defer wg.Done()
				if in.Op == "publisher" && i == 64 {
					<-next
				}
				payload := pRaw(in.Offset + i)
				if in.Blob != "" {
					payload = pRaw(in.Blob)
				}
				r, e := host(ctx, "test.host", payload)
				if in.Op == "publisher" && i == 0 {
					close(next)
				}
				if e != nil {
					out.Errors[i] = e.Error()
				} else {
					out.Results[i] = r
				}
			}(i)
		}
		if in.Early {
			// The host only replies to this fence after every child has physically
			// entered; returning now intentionally leaves registered reverse calls.
			if _, e := host(ctx, BackgroundSubmitCapability, json.RawMessage(`null`)); e != nil {
				return nil, e
			}
			return json.RawMessage(`true`), nil
		}
		if method == "phase" && in.Offset == 0 {
			if _, e := host(ctx, BackgroundSubmitCapability, pRaw(1)); e != nil {
				return nil, e
			}
		}
		wg.Wait()
		out.After, out.AfterOK = CurrentHostCallBudget(ctx)
		if method == "phase" {
			pass.Done()
		}
		return pRaw(out), nil
	}
	var e error
	if mode == "legacy" {
		e = ServeConcurrent(context.Background(), os.Stdin, os.Stdout, parallelManifest(), h)
	} else {
		e = ServeConcurrentWithParallelHostCalls(context.Background(), os.Stdin, os.Stdout, parallelManifest(), h)
	}
	if e != nil {
		os.Exit(23)
	}
	os.Exit(0)
}
func pStart(t *testing.T, runner Runner, mode string) *Client {
	t.Helper()
	runner.ConcurrentCalls = true
	runner.RequireConcurrent = true
	if runner.Timeout == 0 {
		runner.Timeout = 5 * time.Second
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c, e := runner.Start(context.Background(), Command{Path: exe, Args: []string{"-test.run=^TestParallelOverlayFixtureWorker$", "--", "owned-parallel-" + mode}}, parallelManifest(), SessionBinding{Identity: Identity{PluginID: "ParallelFixture", InstanceID: "ParallelFixture.clone", Version: "1.0"}, Generation: 9})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := c.Stop(ctx); e != nil {
			t.Error("physical child cleanup", e)
		}
		n, over := c.stderr.snapshot()
		if n != 0 || over {
			t.Errorf("child stderr/race bytes=%d limit=%v", n, over)
		}
	})
	return c
}
func pCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func pAwait(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(4 * time.Second):
		t.Fatal("barrier timeout")
	}
}
func pCall(t *testing.T, c *Client, in parallelInput, host HostHandler) (RunResult, parallelResult) {
	t.Helper()
	out, e := c.CallWithOptions(pCtx(t), "invoke", pRaw(in), host, CallOptions{CallerDeadlineOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	var result parallelResult
	if e = json.Unmarshal(out.Result, &result); e != nil {
		t.Fatal(e, string(out.Result))
	}
	return out, result
}

func TestParallelOverlayNegotiationDefaultWire(t *testing.T) {
	for _, v := range []any{Handshake{}, concurrentProposal{Version: 1, MaxCalls: 32, MaxHostCalls: 64}} {
		b, _ := json.Marshal(v)
		if bytes.Contains(b, []byte("parallel_host")) {
			t.Fatal("default wire changed", string(b))
		}
	}
	c := pStart(t, Runner{}, "legacy")
	if c.SupportsParallelHostCalls() {
		t.Fatal("legacy was upgraded")
	}
	_, r := pCall(t, c, parallelInput{Count: 2}, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`true`), nil
	})
	if r.BeforeOK || r.AfterOK {
		t.Fatal("legacy exposes opted-in budget")
	}
	if _, ok := CurrentHostCallBudget(nil); ok {
		t.Fatal("nil budget")
	}
	if _, ok := CurrentHostCallBudget(context.Background()); ok {
		t.Fatal("fake context got budget")
	}
}

func TestParallelOverlayHTTP48ReverseOrderAndBudget(t *testing.T) {
	c := pStart(t, Runner{MaxFrameBytes: 2048, MaxChunkBytes: 96}, "parallel")
	if !c.SupportsParallelHostCalls() {
		t.Fatal("not negotiated")
	}
	entered := make(chan int, 48)
	releases := make([]chan struct{}, 48)
	for i := range releases {
		releases[i] = make(chan struct{})
	}
	defer func() {
		for _, ch := range releases {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}()
	var active, peak atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var i int
		json.NewDecoder(r.Body).Decode(&i)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		entered <- i
		select {
		case <-releases[i]:
		case <-r.Context().Done():
			return
		}
		json.NewEncoder(w).Encode(strings.Repeat(fmt.Sprint(i)+"|", 2000))
	}))
	defer peer.Close()
	finished := make(chan int, 48)
	host := func(ctx context.Context, id Identity, m string, raw json.RawMessage) (json.RawMessage, error) {
		if id.InstanceID != "ParallelFixture.clone" {
			return nil, errors.New("identity")
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL, bytes.NewReader(raw))
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		var i int
		json.Unmarshal(raw, &i)
		finished <- i
		return b, e
	}
	pending, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Count: 48}), host, CallOptions{CallerDeadlineOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	seen := map[int]bool{}
	for i := 0; i < 48; i++ {
		select {
		case n := <-entered:
			seen[n] = true
		case <-time.After(4 * time.Second):
			t.Fatal("network serialized", len(seen))
		}
	}
	if len(seen) != 48 || peak.Load() != 48 {
		t.Fatal("overlap", len(seen), peak.Load())
	}
	for i := 47; i >= 0; i-- {
		close(releases[i])
		select {
		case n := <-finished:
			if n != i {
				t.Fatal("release order", n, i)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("reply did not finish")
		}
	}
	out, e := pending.Wait()
	if e != nil {
		t.Fatal(e)
	}
	var r parallelResult
	if json.Unmarshal(out.Result, &r) != nil {
		t.Fatal("bad result")
	}
	for i := range r.Results {
		var s string
		if json.Unmarshal(r.Results[i], &s) != nil || s != strings.Repeat(fmt.Sprint(i)+"|", 2000) || r.Errors[i] != "" {
			t.Fatal("reply correlation", i, r.Errors[i])
		}
	}
	if out.HostCalls != 48 || out.ReadChunks == 0 || !r.BeforeOK || !r.AfterOK || r.Before.HostCallsCharged != 0 || r.After.HostCallsCharged != 48 || r.After.MaxParallelHostCalls != 64 || r.After.MaxHostCalls != 64 || r.After.HostToWorkerChunksCharged == 0 || r.After.HostToWorkerWireBytesCharged <= r.Before.HostToWorkerWireBytesCharged || r.Before.SessionIncomingEntries != 1 {
		t.Fatalf("account domain mismatch: %+v / %+v / %+v", out, r.Before, r.After)
	}
	state, e := c.Call(pCtx(t), "state", json.RawMessage(`null`), nil)
	if e != nil {
		t.Fatal(e)
	}
	if string(state.Result) != `{"budget":false,"error":true}` {
		t.Fatal("stale caller restored", string(state.Result))
	}
	t.Logf("actual 48 HTTP entered before release; reverse completion 47..0; actual framed chunks=%d; charged48", out.ReadChunks)
}

func TestParallelOverlayCross48PhaseHTTP96(t *testing.T) {
	c := pStart(t, Runner{}, "parallel")
	entered := make(chan struct{}, 96)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Write(b)
	}))
	defer peer.Close()
	var host HostHandler
	var childMu sync.Mutex
	var children []*PendingCall
	host = func(ctx context.Context, id Identity, m string, b json.RawMessage) (json.RawMessage, error) {
		if m == BackgroundSubmitCapability {
			var n int
			json.Unmarshal(b, &n)
			child, e := c.StartCallWithAdmission(ctx, pCtx(t), "phase", pRaw(parallelInput{Count: 48, Offset: n * 48}), host, CallOptions{CallerDeadlineOnly: true})
			if e != nil {
				return nil, e
			}
			childMu.Lock()
			children = append(children, child)
			childMu.Unlock()
			return json.RawMessage(`null`), nil
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL, bytes.NewReader(b))
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			return nil, e
		}
		defer r.Body.Close()
		return io.ReadAll(r.Body)
	}
	root, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Op: "pass"}), host, CallOptions{CallerDeadlineOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 96; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("new phase waited for prior HTTP", i)
		}
	}
	c.concurrent.mu.Lock()
	entries := len(c.concurrent.calls)
	c.concurrent.mu.Unlock()
	if entries != 3 {
		t.Fatal("unexpected incoming entries", entries)
	}
	once.Do(func() { close(release) })
	out, e := root.Wait()
	if e != nil || string(out.Result) != "true" {
		t.Fatal("owner", e, string(out.Result))
	}
	childMu.Lock()
	list := append([]*PendingCall(nil), children...)
	childMu.Unlock()
	total := out.HostCalls
	for _, p := range list {
		out, e := p.Wait()
		if e != nil {
			t.Fatal("child physical join", e)
		}
		if out.HostCalls > 49 {
			t.Fatal("phase budget", out.HostCalls)
		}
		total += out.HostCalls
	}
	if len(list) != 2 || total != 98 {
		t.Fatal("phase accounting", len(list), total)
	}
	t.Log("actual96 owned HTTP overlap, two real admitted phase invocations + owner, each48/49RPC; total98 no pass cumulative64 cap")
}

type pBlockedWriter struct {
	to               io.Writer
	entered, release chan struct{}
	once             sync.Once
}

func (w *pBlockedWriter) Close() error {
	if c, ok := w.to.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
func (w *pBlockedWriter) Write(b []byte) (int, error) {
	n, e := w.to.Write(b)
	if bytes.Contains(b, []byte(`"kind":"host_result"`)) {
		w.once.Do(func() { close(w.entered); <-w.release })
	}
	return n, e
}
func TestParallelOverlayPublisher65thNoFalseOverflow(t *testing.T) {
	c := pStart(t, Runner{MaxHostCalls: 256}, "parallel")
	writer := &pBlockedWriter{to: c.session.writer, entered: make(chan struct{}), release: make(chan struct{})}
	c.session.writer = writer
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(writer.release) })
	entered := make(chan int, 65)
	hold := make(chan struct{})
	var holdOnce sync.Once
	defer holdOnce.Do(func() { close(hold) })
	host := func(ctx context.Context, _ Identity, _ string, b json.RawMessage) (json.RawMessage, error) {
		var i int
		json.Unmarshal(b, &i)
		entered <- i
		if i != 0 {
			select {
			case <-hold:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return b, nil
	}
	pending, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Count: 65, Op: "publisher"}), host, CallOptions{CallerDeadlineOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	pAwait(t, writer.entered)
	for i := 0; i < 65; i++ {
		select {
		case <-entered:
		case <-time.After(4 * time.Second):
			t.Fatal("65th rejected or publisher released too late", i)
		}
	}
	c.concurrent.mu.Lock()
	var call *concurrentHostCall
	for _, v := range c.concurrent.calls {
		call = v
	}
	c.concurrent.mu.Unlock()
	call.mu.Lock()
	charged, publishing := call.reversePending, call.reversePublishing
	call.mu.Unlock()
	if charged != 64 || !publishing {
		t.Fatal("publisher accounting", charged, publishing)
	}
	holdOnce.Do(func() { close(hold) })
	releaseOnce.Do(func() { close(writer.release) })
	out, e := pending.Wait()
	if e != nil || out.HostCalls != 65 {
		t.Fatal("join", e, out.HostCalls)
	}
	t.Log("deterministic full reply written but Writer not returned: 64 charged + 1 publisher, 65 cumulative RPC accepted, all joined")
}

func TestParallelOverlayHandlerReturnSealsButHostDrainRetained(t *testing.T) {
	c := pStart(t, Runner{}, "parallel")
	entered := make(chan struct{}, 8)
	all := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var running atomic.Int64
	host := func(ctx context.Context, _ Identity, m string, b json.RawMessage) (json.RawMessage, error) {
		if m == BackgroundSubmitCapability {
			for i := 0; i < 8; i++ {
				<-entered
			}
			close(all)
			return json.RawMessage(`null`), nil
		}
		running.Add(1)
		defer running.Add(-1)
		entered <- struct{}{}
		<-release
		return b, nil
	}
	pending, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Count: 8, Early: true}), host, CallOptions{CallerDeadlineOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	pAwait(t, all)
	done := make(chan error, 1)
	go func() { _, e := pending.Wait(); done <- e }()
	select {
	case e := <-done:
		var local *Error
		if e == nil || !errors.As(e, &local) || local.Code != "execution_canceled" {
			t.Fatal("early result falsely completed", e)
		}
	case <-time.After(120 * time.Millisecond):
		// If the result wins the cancellation race, Wait may still join; this
		// branch does not assert that it has physically finished.
	}

	c.concurrent.mu.Lock()
	n := len(c.concurrent.calls)
	c.concurrent.mu.Unlock()
	if n != 1 || running.Load() != 8 {
		t.Fatal("canceled native entries lost", n, running.Load())
	}
	// Release is owned by this test; the SDK cannot kill a native handler.
	for i := 0; i < 8; i++ {
		release <- struct{}{}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.concurrent.mu.Lock()
		entries := len(c.concurrent.calls)
		c.concurrent.mu.Unlock()
		if entries == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical drain retained forever")
		}
		time.Sleep(time.Millisecond)
	}
	if running.Load() != 0 {
		t.Fatal("native callbacks unjoined")
	}

}

func TestParallelOverlayExcludeHostTimeUnion(t *testing.T) {
	c := pStart(t, Runner{Timeout: 5 * time.Second, ExcludeHostTime: true}, "parallel")
	entered := make(chan int, 2)
	a, b := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-a:
		default:
			close(a)
		}
		select {
		case <-b:
		default:
			close(b)
		}
	}()
	pending, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Count: 2}), func(ctx context.Context, _ Identity, _ string, raw json.RawMessage) (json.RawMessage, error) {
		var i int
		json.Unmarshal(raw, &i)
		entered <- i
		ch := a
		if i == 1 {
			ch = b
		}
		select {
		case <-ch:
			return raw, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, CallOptions{Timeout: 400 * time.Millisecond, ExcludeHostTime: true})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("overlap")
		}
	}
	close(a)
	time.Sleep(650 * time.Millisecond)
	close(b)
	out, e := pending.Wait()
	if e != nil || out.HostCalls != 2 {
		t.Fatal("last handler did not retain paused budget", e, out.HostCalls)
	}
}

func TestParallelOverlayPerIncomingRPCBudgetAndLowerLimits(t *testing.T) {
	c := pStart(t, Runner{MaxHostCalls: 3, MaxFrameBytes: 1024, MaxMessageBytes: 4096, MaxChunkBytes: 127, MaxInputBytes: 8192, MaxOutputBytes: 8192, MaxSessionChunks: 61}, "parallel")
	out, r := pCall(t, c, parallelInput{Count: 5}, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`1`), nil
	})
	n := 0
	for _, e := range r.Errors {
		if e != "" {
			n++
		}
	}
	if n != 2 || out.HostCalls != 3 || r.After.MaxParallelHostCalls != 3 || r.After.PhysicalFrameBytes != 1024 || r.After.LogicalMessageBytes != 4096 || r.After.RawChunkBytes != 127 || r.After.MaxWorkerToHostWireBytes != 8192 || r.After.MaxHostToWorkerWireBytes != 8192 || r.After.CumulativeChunksPerDirection != 61 {
		t.Fatalf("lower negotiated domains %+v errors=%d calls=%d", r.After, n, out.HostCalls)
	}
	// A new incoming invocation gets its own cumulative budget, not a pass quota.
	out, _ = pCall(t, c, parallelInput{Count: 3}, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`2`), nil
	})
	if out.HostCalls != 3 {
		t.Fatal("new invocation inherited prior charges")
	}
}

func TestParallelOverlayOversizedReplyDenialDoesNotRetryHTTP(t *testing.T) {
	for _, kind := range []string{"logical", "direction", "chunk"} {
		t.Run(kind, func(t *testing.T) {
			runner := Runner{MaxFrameBytes: 1024, MaxMessageBytes: 4096, MaxChunkBytes: 256, MaxOutputBytes: 8192}
			size := 5000
			if kind == "direction" {
				runner.MaxMessageBytes = 8192
				runner.MaxInputBytes = 2048
				runner.MaxOutputBytes = 8192
				size = 3000
			}
			if kind == "chunk" {
				runner.MaxMessageChunks = 1
				size = 1500
			}
			c := pStart(t, runner, "parallel")
			var writes atomic.Int64
			out, err := c.CallWithOptions(pCtx(t), "invoke", pRaw(parallelInput{Op: "sequential", Count: 1}), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				writes.Add(1)
				return pRaw(strings.Repeat("x", size)), nil
			}, CallOptions{CallerDeadlineOnly: true})
			var message string
			json.Unmarshal(out.Result, &message)
			if err != nil || writes.Load() != 1 || out.HostCalls != 1 || message != "plugin: host_operation_failed" {
				t.Fatal("oversized response retried/accepted or wrong error", writes.Load(), out.HostCalls, message, err)
			}

		})
	}
}

func TestParallelOverlayCancelIsolatedScopeUnknownPOSTNoRetry(t *testing.T) {
	c := pStart(t, Runner{}, "parallel")
	var posts atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		conn, _, e := w.(http.Hijacker).Hijack()
		if e == nil {
			conn.Close()
		}
	}))
	defer peer.Close()
	host := func(ctx context.Context, _ Identity, _ string, b json.RawMessage) (json.RawMessage, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL, bytes.NewReader(b))
		r, e := http.DefaultClient.Do(req)
		if r != nil {
			r.Body.Close()
		}
		return nil, e
	}
	_, r := pCall(t, c, parallelInput{Count: 1}, host)
	if posts.Load() != 1 || r.Errors[0] == "" {
		t.Fatal("selected POST retried or failure lost", posts.Load(), r.Errors)
	}
	ctx, cancel := context.WithCancel(pCtx(t))
	entered := make(chan struct{}, 6)
	var left atomic.Int64
	pending, e := c.StartCall(ctx, "invoke", pRaw(parallelInput{Count: 6}), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
		entered <- struct{}{}
		<-ctx.Done()
		left.Add(1)
		return nil, ctx.Err()
	}, CallOptions{CallerDeadlineOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 6; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("parallel cancel barrier")
		}
	}
	// A denied typed native response is opaque at this transport layer, never a
	// transport-level business success or an automatic retry instruction.
	_, denied := pCall(t, c, parallelInput{Count: 1}, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("owned current scope revoked")
	})
	if denied.Errors[0] == "" {
		t.Fatal("guard denial swallowed")
	}
	cancel()
	if _, e := pending.Wait(); e == nil {
		t.Fatal("canceled phase succeeded")
	}
	deadline := time.Now().Add(3 * time.Second)
	for left.Load() != 6 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if left.Load() != 6 {
		t.Fatal("native context did not revoke all", left.Load())
	}
	out, _ := pCall(t, c, parallelInput{Count: 1}, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`7`), nil
	})
	if out.HostCalls != 1 {
		t.Fatal("unrelated subsequent call lost authority")
	}
}

func TestParallelOverlayBudgetIsObservationalConcurrentAndSealed(t *testing.T) {
	// Test the exact charged snapshot under its real locks, without providing
	// any HostCaller. Constructed private state only tests access/liveness gates.
	w := &concurrentWorker{proposal: concurrentProposal{Version: 1, MaxCalls: 32, MaxHostCalls: 64, MaxParallelHostCalls: 64}, calls: map[string]*concurrentWorkerCall{}}
	w.transport = &concurrentTransport{wire: &wireSession{limits: transportLimits{FrameBytes: 900, LogicalBytes: 1800, ChunkBytes: 37, MessageChunks: 21, SessionChunks: 45, HostReadBytes: 4096, HostSendBytes: 8192}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	call := &concurrentWorkerCall{request: Frame{ID: "owned"}, ctx: ctx, account: concurrentAccount{worker: true, limits: w.transport.wire.limits}}
	w.calls["owned"] = call
	ctx = context.WithValue(ctx, hostCallBudgetKey{}, &hostCallBudgetView{worker: w, call: call})
	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				switch i {
				case 0:
					call.account.host(64)
				case 1:
					call.account.send(concurrentWireCount{bytes: 2, chunks: 1})
				case 2:
					call.account.receive(concurrentWireCount{bytes: 3, chunks: 1})
				}
				s, ok := CurrentHostCallBudget(ctx)
				if !ok || s.HostCallsCharged > 64 || s.WorkerToHostWireBytesCharged > 40 || s.HostToWorkerWireBytesCharged > 60 {
					t.Error("snapshot torn or forged", s, ok)
				}
			}
		}(i)
	}
	wg.Wait()
	s, ok := CurrentHostCallBudget(ctx)
	if !ok || s.HostCallsCharged != 20 || s.WorkerToHostWireBytesCharged != 40 || s.HostToWorkerWireBytesCharged != 60 || s.PhysicalFrameBytes != 900 || s.SessionIncomingEntries != 1 {
		t.Fatal("actual charges/directions", s, ok)
	}
	// Snapshot reads do not reserve a byte or increment any counter.
	a, _ := CurrentHostCallBudget(ctx)
	b, _ := CurrentHostCallBudget(ctx)
	if a != b {
		t.Fatal("read mutated accounting")
	}
	call.mu.Lock()
	call.reverseClosed = true
	call.mu.Unlock()
	if _, ok := CurrentHostCallBudget(ctx); ok {
		t.Fatal("sealed caller retained budget view")
	}
	if _, ok := CurrentHostCallBudget(context.WithValue(context.Background(), hostCallBudgetKey{}, &hostCallBudgetView{})); ok {
		t.Fatal("empty view forged state")
	}
}
