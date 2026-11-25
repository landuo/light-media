package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func cleanupTestManifest(feature bool) Manifest {
	m := Manifest{SchemaVersion: 1, PluginID: "CleanupContinuationFixture", Name: "Owned cleanup fixture", Version: "1.0", Runtime: "go-executable", Entry: "cleanup-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "close", "stop_service", "stop", "child"}, Capabilities: []string{"fixture.write", "fixture.enter", BackgroundSubmitCapability}}
	if feature {
		m.Capabilities = append(m.Capabilities, CleanupSubmitCapability)
	}
	return m
}
func TestCleanupContinuationWorkerFixture(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "cleanup-continuation-worker") {
		return
	}
	feature := strings.Contains(strings.Join(os.Args, " "), "--feature")
	publishPath, publishBefore := "", false
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--publish-before=") {
			publishPath = strings.TrimPrefix(arg, "--publish-before=")
			publishBefore = true
		}
		if strings.HasPrefix(arg, "--publish-after=") {
			publishPath = strings.TrimPrefix(arg, "--publish-after=")
		}
	}
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--fixture-stack=") {
			path := strings.TrimPrefix(arg, "--fixture-stack=")
			go func() {
				for {
					if _, e := os.Stat(path + ".request"); e == nil {
						buf := make([]byte, 1<<20)
						n := runtime.Stack(buf, true)
						_ = os.WriteFile(path+".stack", buf[:n], 0600)
						return
					}
					time.Sleep(time.Millisecond)
				}
			}()
		}
	}
	var mu sync.Mutex
	var done chan struct{}
	var wanted, finished int
	var first error
	handle := func(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
		var in struct {
			Count, N   int
			Mode, Blob string
		}
		_ = json.Unmarshal(raw, &in)
		switch method {
		case "invoke":
			return json.Marshal(map[string]bool{"cleanup": IsCleanupInvocation(ctx)})
		case "stop":
			return json.RawMessage(`null`), nil
		case "close":
			return json.RawMessage(`null`), nil
		case "stop_service":
			if !IsCleanupInvocation(ctx) {
				return json.Marshal(map[string]bool{"cleanup": false})
			}
			if in.Count == 0 {
				return json.RawMessage(`null`), nil
			}
			mu.Lock()
			done = make(chan struct{})
			wanted = in.Count
			finished = 0
			first = nil
			wait := done
			mu.Unlock()
			if _, e := host(ctx, "fixture.enter", json.RawMessage(`{}`)); e != nil {
				return nil, e
			}
			if in.Mode != "manual" {
				n := 1
				if in.Mode == "two" {
					n = 2
				}
				for i := 1; i <= n; i++ {
					if e := (Manager{Host: host}).SubmitCleanup(ctx, "child", map[string]any{"n": i, "count": in.Count, "mode": in.Mode}); e != nil {
						return nil, e
					}
				}
			}
			if in.Mode == "nojoin" {
				return json.RawMessage(`null`), nil
			}
			select {
			case <-wait:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			mu.Lock()
			err := first
			mu.Unlock()
			return json.RawMessage(`null`), err
		case "child":
			if publishPath != "" {
				_ = os.WriteFile(publishPath+".child-"+strconv.Itoa(in.N), []byte("entered"), 0600)
			}
			if !IsCleanupInvocation(ctx) {
				return nil, errors.New("child has no cleanup context")
			}
			payload, _ := json.Marshal(map[string]int{"n": in.N, "blob_len": len(in.Blob)})
			var err error
			if in.Mode != "instant" {
				_, err = host(ctx, "fixture.write", payload)
			}
			if err == nil && in.Mode != "two" && in.N < in.Count {
				err = (Manager{Host: host}).SubmitCleanup(ctx, "child", map[string]any{"n": in.N + 1, "count": in.Count, "mode": in.Mode})
			}
			mu.Lock()
			if first == nil {
				first = err
			}
			finished++
			if err != nil || finished >= wanted {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			mu.Unlock()
			if in.Mode == "publishing" && err == nil {
				return json.RawMessage(`{"publication":true}`), nil
			}
			return json.RawMessage(`null`), err
		}
		return nil, errors.New("unknown fixture method")
	}
	var output io.Writer = os.Stdout
	if publishPath != "" {
		output = &cleanupFixturePublishWriter{Writer: os.Stdout, path: publishPath, before: publishBefore}
	}
	if err := ServeConcurrent(context.Background(), os.Stdin, output, cleanupTestManifest(feature), handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type cleanupTestPeer struct {
	t                                             *testing.T
	ctx                                           context.Context
	cancel                                        context.CancelFunc
	client                                        *Client
	identity                                      Identity
	mu                                            sync.Mutex
	rootCtx                                       context.Context
	pending                                       []*PendingCall
	writes                                        []int
	blobs                                         []int
	block                                         bool
	ignoreCancel                                  bool
	writeBeforeBlock                              bool
	reject                                        bool
	entered                                       chan int
	release                                       chan struct{}
	rootEntered                                   chan struct{}
	rootOnce                                      sync.Once
	canceled                                      chan struct{}
	cancelOnce                                    sync.Once
	handoffHeld, releaseHandoff, successorWaiting chan struct{}
}

func newCleanupPeer(t *testing.T, feature bool, extra ...string) *cleanupTestPeer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := cleanupTestManifest(feature)
	id := Identity{PluginID: m.PluginID, InstanceID: "OwnedCleanup", Version: m.Version}
	args := []string{"-test.run=^TestCleanupContinuationWorkerFixture$", "--", "cleanup-continuation-worker"}
	if feature {
		args = append(args, "--feature")
	}
	args = append(args, extra...)
	client, err := (Runner{Timeout: 3 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}).Start(ctx, Command{Path: exe, Args: args}, m, SessionBinding{Identity: id, Generation: 1})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	f := &cleanupTestPeer{t: t, ctx: ctx, cancel: cancel, client: client, identity: id, entered: make(chan int, 8), release: make(chan struct{}), rootEntered: make(chan struct{}), canceled: make(chan struct{})}
	t.Cleanup(func() {
		closeCtx, x := context.WithTimeout(context.Background(), 3*time.Second)
		defer x()
		_ = client.Stop(closeCtx)
		cancel()
	})
	return f
}
func (f *cleanupTestPeer) host(ctx context.Context, id Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
	if id != f.identity || ctx.Err() != nil {
		return nil, errors.New("fixture authority rejected")
	}
	if method == CleanupSubmitCapability {
		var in struct {
			Method  string
			Payload json.RawMessage
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		if _, err := f.client.CleanupContext(ctx); err != nil {
			return nil, err
		}
		var next struct{ N int }
		_ = json.Unmarshal(in.Payload, &next)
		if f.successorWaiting != nil && next.N == 3 {
			close(f.successorWaiting)
		}
		pending, err := f.client.StartCleanupContinuationWithAdmission(ctx, in.Method, in.Payload, f.host)
		if err != nil {
			f.t.Logf("actual cleanup admission child=%d error=%v", next.N, err)
			return nil, err
		}
		f.mu.Lock()
		f.pending = append(f.pending, pending)
		f.mu.Unlock()
		if f.handoffHeld != nil && next.N == 2 {
			close(f.handoffHeld)
			select {
			case <-f.releaseHandoff:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return json.RawMessage(`{"accepted":true}`), nil
	}
	if method == BackgroundSubmitCapability {
		return nil, errors.New("ordinary submit remains denied")
	}
	if method == "fixture.enter" {
		f.mu.Lock()
		f.rootCtx = ctx
		f.mu.Unlock()
		f.rootOnce.Do(func() { close(f.rootEntered) })
		return json.RawMessage(`null`), nil
	}
	if method == "fixture.write" {
		var in struct{ N, BlobLen int }
		var fields map[string]int
		_ = json.Unmarshal(raw, &fields)
		in.N = fields["n"]
		in.BlobLen = fields["blob_len"]
		f.mu.Lock()
		reject := f.reject
		block, ignore, before := f.block, f.ignoreCancel, f.writeBeforeBlock
		if before && !reject {
			f.writes = append(f.writes, in.N)
			f.blobs = append(f.blobs, in.BlobLen)
		}
		root := f.rootCtx
		f.mu.Unlock()
		if reject {
			return nil, errors.New("synthetic current guard rejected")
		}
		if root != nil {
			a, ok := root.Deadline()
			b, ok2 := ctx.Deadline()
			if ok != ok2 || ok && !a.Equal(b) {
				return nil, errors.New("cleanup child extended root deadline")
			}
		}
		if block {
			f.entered <- in.N
			if ignore {
				<-f.release
			} else {
				select {
				case <-f.release:
				case <-ctx.Done():
					f.cancelOnce.Do(func() { close(f.canceled) })
					return nil, ctx.Err()
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !before {
			f.mu.Lock()
			f.writes = append(f.writes, in.N)
			f.blobs = append(f.blobs, in.BlobLen)
			f.mu.Unlock()
		}
		return json.RawMessage(`null`), nil
	}
	return nil, errors.New("unknown fixture resource")
}
func (f *cleanupTestPeer) startRoot(ctx context.Context, count int, mode string) <-chan error {
	f.t.Helper()
	if err := f.client.BeginShutdown(ctx); err != nil {
		f.t.Fatal(err)
	}
	done := make(chan error, 1)
	raw, _ := json.Marshal(map[string]any{"count": count, "mode": mode})
	go func() { _, err := f.client.CallCleanup(ctx, "stop_service", raw, f.host); done <- err }()
	return done
}
func (f *cleanupTestPeer) awaitEntered(n int) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-f.entered:
		case <-f.ctx.Done():
			f.t.Fatal("cleanup child did not enter")
		}
	}
}

func TestCleanupContinuationActualRunnerOver64AndOwnerJoin(t *testing.T) {
	f := newCleanupPeer(t, true)
	done := f.startRoot(f.ctx, 180, "")
	select {
	case err := <-done:
		if err != nil {
			f.mu.Lock()
			pending := append([]*PendingCall(nil), f.pending...)
			f.mu.Unlock()
			for i, child := range pending {
				if _, e := child.Wait(); e != nil {
					t.Logf("child %d: %v", i+1, e)
				}
			}
			t.Fatal(err)
		}
	case <-f.ctx.Done():
		t.Fatal("root did not join")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 180 {
		t.Fatal("root completed before final effect", len(f.writes))
	}
	for i, n := range f.writes {
		if n != i+1 {
			t.Fatal("source order or replay", i, n)
		}
	}
}
func TestCleanupContinuationRootJoinsEvenHandlerReturns(t *testing.T) {
	f := newCleanupPeer(t, true)
	f.block = true
	done := f.startRoot(f.ctx, 1, "nojoin")
	f.awaitEntered(1)
	select {
	case err := <-done:
		t.Fatal("root returned before admitted child", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(f.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	ctx := f.rootCtx
	writes := len(f.writes)
	f.mu.Unlock()
	if writes != 1 {
		t.Fatal(writes)
	}
	if _, err := f.client.CleanupContext(ctx); err == nil {
		t.Fatal("returned root retained authority")
	}
	if _, err := f.client.StartCleanupContinuationWithAdmission(ctx, "child", json.RawMessage(`{"n":2}`), f.host); err == nil {
		t.Fatal("admission after root reply")
	}
}
func TestCleanupContinuationOrdinaryFakeStopWrongClientAndGuard(t *testing.T) {
	f := newCleanupPeer(t, true)
	for _, method := range []string{"invoke", "stop_service"} {
		out, err := f.client.Call(f.ctx, method, json.RawMessage(`{"cleanup_root":"forged","count":0}`), f.host)
		if err != nil || string(out.Result) != `{"cleanup":false}` {
			t.Fatal("method/payload created cleanup", method, string(out.Result), err)
		}
	}
	if _, err := f.client.CleanupContext(context.Background()); err == nil {
		t.Fatal("unbound context accepted")
	}
	f.block = true
	done := f.startRoot(f.ctx, 1, "nojoin")
	f.awaitEntered(1)
	other := newCleanupPeer(t, true)
	f.mu.Lock()
	root := f.rootCtx
	f.mu.Unlock()
	if _, err := other.client.CleanupContext(root); err == nil {
		t.Fatal("foreign exact client accepted root")
	}
	if _, err := f.client.StartCall(f.ctx, "child", json.RawMessage(`{}`), f.host, CallOptions{CallerDeadlineOnly: true}); err == nil {
		t.Fatal("ordinary call admitted while draining")
	}
	close(f.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	g := newCleanupPeer(t, true)
	g.reject = true
	guard := g.startRoot(g.ctx, 4, "")
	if err := <-guard; err == nil {
		t.Fatal("source guard became success")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.writes) != 0 || len(g.pending) != 1 {
		t.Fatal("guard caused retry/later writes", g.writes, len(g.pending))
	}
}
func TestCleanupContinuationTwoSlotsRetainCanceledActualCallback(t *testing.T) {
	f := newCleanupPeer(t, true)
	f.block = true
	f.ignoreCancel = true
	f.writeBeforeBlock = true
	done := f.startRoot(f.ctx, 2, "two")
	f.awaitEntered(2)
	f.mu.Lock()
	pending := append([]*PendingCall(nil), f.pending...)
	root := f.rootCtx
	f.mu.Unlock()
	if len(pending) != 2 {
		t.Fatal("two child admissions missing", len(pending))
	}
	pending[0].Cancel()
	if _, err := pending[0].Wait(); err == nil {
		t.Fatal("canceled child succeeded")
	}
	if _, err := f.client.StartCleanupContinuationWithAdmission(root, "child", json.RawMessage(`{"n":3}`), f.host); err == nil {
		t.Fatal("canceled but live callback released slot")
	}
	select {
	case err := <-done:
		t.Fatal("root returned before callback drain", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(f.release)
	if err := <-done; err == nil {
		t.Fatal("unknown/canceled committed write became success")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 2 {
		t.Fatal("unknown write replayed/lost", f.writes)
	}
}
func TestCleanupContinuationLargeLogicalHeaderAndDeadline(t *testing.T) {
	f := newCleanupPeer(t, true)
	done := f.startRoot(f.ctx, 1, "manual")
	select {
	case <-f.rootEntered:
	case <-f.ctx.Done():
		t.Fatal("root not entered")
	}
	f.mu.Lock()
	root := f.rootCtx
	f.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"n": 1, "count": 1, "blob": strings.Repeat("x", 2<<20)})
	pending, err := f.client.StartCleanupContinuationWithAdmission(root, "child", raw, f.host)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pending.Wait()
	if err != nil || out.SentChunks == 0 {
		t.Fatal("large cleanup lost header/chunks", out, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if len(f.blobs) != 1 || f.blobs[0] != 2<<20 {
		t.Fatal("large copied payload lost", f.blobs)
	}
	f.mu.Unlock()
	g := newCleanupPeer(t, true)
	g.block = true
	g.writeBeforeBlock = true
	ctx, cancel := context.WithTimeout(g.ctx, 100*time.Millisecond)
	defer cancel()
	deadline := g.startRoot(ctx, 1, "")
	g.awaitEntered(1)
	if err := <-deadline; err == nil {
		t.Fatal("root deadline ignored")
	}
	select {
	case <-g.canceled:
	case <-g.ctx.Done():
		t.Fatal("child did not inherit root cancellation")
	}
	g.mu.Lock()
	old := g.rootCtx
	g.mu.Unlock()
	if _, err := g.client.StartCleanupContinuationWithAdmission(old, "child", json.RawMessage(`{"n":2}`), g.host); err == nil {
		t.Fatal("deadline root extended")
	}
	close(g.release)
}
func TestCleanupContinuationLegacyAndForgedHeaderActualRunner(t *testing.T) {
	legacy := newCleanupPeer(t, false)
	if legacy.client.SupportsCleanupContinuations() {
		t.Fatal("nonconsumer advertised new support")
	}
	if err := legacy.client.BeginShutdown(legacy.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.client.CleanupContext(legacy.ctx); err == nil {
		t.Fatal("legacy acquired root")
	}
	f := newCleanupPeer(t, true)
	if err := f.client.BeginShutdown(f.ctx); err != nil {
		t.Fatal(err)
	}
	_, err := f.client.concurrent.transport.write(Frame{Protocol: ProtocolVersion, ID: "forgedChild", Identity: f.identity, Type: "call", Method: "child", Payload: json.RawMessage(`{"n":1}`), CleanupRoot: "inventedRoot"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.client.done:
	case <-f.ctx.Done():
		t.Fatal("forged root not rejected")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 0 {
		t.Fatal("forged header wrote")
	}
}

// Delay only the owned physical writer return AFTER a child frame is delivered,
// until the sole reader has processed its immediate real worker reply. This
// makes normal call-local cancellation precede the admission ACK deterministically.
type cleanupDelayedWrite struct {
	io.Writer
	after func([]byte)
}

func (w cleanupDelayedWrite) Write(b []byte) (int, error) {
	n, e := w.Writer.Write(b)
	if e == nil {
		w.after(b)
	}
	return n, e
}
func TestCleanupContinuationFastReplyBeforeAdmissionAcknowledgement(t *testing.T) {
	f := newCleanupPeer(t, true)
	done := f.startRoot(f.ctx, 1, "manual")
	select {
	case <-f.rootEntered:
	case <-f.ctx.Done():
		t.Fatal("root did not enter")
	}
	f.mu.Lock()
	root := f.rootCtx
	f.mu.Unlock()
	m := f.client.concurrent
	observed := make(chan struct{})
	var once sync.Once
	m.transport.writeMu.Lock()
	original := m.transport.wire.writer
	m.transport.wire.writer = cleanupDelayedWrite{Writer: original, after: func(b []byte) {
		var frame Frame
		if json.Unmarshal(b, &frame) != nil || frame.CleanupRoot == "" {
			return
		}
		for {
			m.mu.Lock()
			call := m.calls[frame.ID]
			m.mu.Unlock()
			closed := call == nil
			if call != nil {
				call.mu.Lock()
				closed = call.closed
				call.mu.Unlock()
			}
			if closed {
				once.Do(func() { close(observed) })
				return
			}
			select {
			case <-f.ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}}
	m.transport.writeMu.Unlock()
	pending, err := f.client.StartCleanupContinuationWithAdmission(root, "child", json.RawMessage(`{"n":1,"count":1,"mode":"instant"}`), f.host)
	if err != nil {
		t.Fatal("normal child reply canceled admission", err)
	}
	if _, err := pending.Wait(); err != nil {
		t.Fatal("normal child reply became failure", err)
	}
	select {
	case <-observed:
	default:
		t.Fatal("owned scheduling gate did not observe real early reply")
	}
	if err := <-done; err != nil {
		t.Fatal("root did not join successful early reply", err)
	}
}

func TestCleanupContinuationPredecessorReplyHandoffUsesOnlyTwoSlots(t *testing.T) {
	f := newCleanupPeer(t, true)
	f.handoffHeld = make(chan struct{})
	f.releaseHandoff = make(chan struct{})
	f.successorWaiting = make(chan struct{})
	done := f.startRoot(f.ctx, 3, "")
	select {
	case <-f.handoffHeld:
	case <-f.ctx.Done():
		t.Fatal("predecessor ACK not held")
	}
	select {
	case <-f.successorWaiting:
	case <-f.ctx.Done():
		t.Fatal("successor did not reach handoff")
	}
	select {
	case err := <-done:
		t.Fatal("root returned while predecessor callback live", err)
	case <-time.After(20 * time.Millisecond):
	}
	f.client.concurrent.mu.Lock()
	slots := 0
	for _, c := range f.client.concurrent.calls {
		if c.cleanupRoot != nil && c.cleanupRoot.root == c {
			slots = c.cleanupRoot.slots
		}
	}
	f.client.concurrent.mu.Unlock()
	if slots != 2 {
		t.Fatal("handoff exceeded/released live slots", slots)
	}
	close(f.releaseHandoff)
	if err := <-done; err != nil {
		t.Fatal("valid predecessor handoff rejected", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 3 {
		t.Fatal("handoff lost/replayed effect", f.writes)
	}
}

// Test-only worker stdout barrier. Before means no reply bytes reached host;
// after means bytes were delivered but the exact Write call has not returned.
type cleanupFixturePublishWriter struct {
	io.Writer
	path   string
	before bool
	once   sync.Once
}

func (w *cleanupFixturePublishWriter) Write(b []byte) (n int, err error) {
	var frame Frame
	selected := json.Unmarshal(b, &frame) == nil && frame.Type == "result" && string(frame.Result) == `{"publication":true}`
	hold := func() {
		w.once.Do(func() {
			_ = os.WriteFile(w.path+".entered", []byte("blocked"), 0600)
			for {
				if _, e := os.Stat(w.path + ".release"); e == nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
	if selected && w.before {
		hold()
	}
	n, err = w.Writer.Write(b)
	if selected && !w.before && err == nil {
		hold()
	}
	return
}
func cleanupAwaitOwnedFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	for {
		if _, e := os.Stat(path); e == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("owned worker publication barrier absent", path)
		case <-time.After(time.Millisecond):
		}
	}
}
func TestCleanupContinuationBlockedReplyWriterBoundsAndCanceledSlot(t *testing.T) {
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			gate := t.TempDir() + "/publish"
			f := newCleanupPeer(t, true, "--publish-"+mode+"="+gate)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			rootDone := f.startRoot(ctx, 100, "manual")
			select {
			case <-f.rootEntered:
			case <-f.ctx.Done():
				t.Fatal("root absent")
			}
			f.mu.Lock()
			root := f.rootCtx
			f.mu.Unlock()
			start := func(n int) (*PendingCall, error) {
				raw, _ := json.Marshal(map[string]any{"n": n, "mode": "publishing"})
				return f.client.StartCleanupContinuationWithAdmission(root, "child", raw, f.host)
			}
			first, err := start(1)
			if err != nil {
				t.Fatal(err)
			}
			cleanupAwaitOwnedFile(t, f.ctx, gate+".entered")
			if mode == "after" {
				if _, err := first.Wait(); err != nil {
					t.Fatal("delivered first reply failed", err)
				}
			}
			second, err := start(2)
			if err != nil {
				t.Fatal(err)
			}
			cleanupAwaitOwnedFile(t, f.ctx, gate+".child-2")
			blocked := second
			maximum := 2
			if mode == "after" {
				third, err := start(3)
				if err != nil {
					t.Fatal(err)
				}
				blocked = third
				maximum = 3
				cleanupAwaitOwnedFile(t, f.ctx, gate+".child-3")
			}
			for i := maximum + 1; i < maximum+20; i++ {
				if _, err := start(i); err == nil {
					t.Fatal("blocked publication admitted unbounded calls", i)
				}
			}
			blocked.Cancel()
			if _, err := blocked.Wait(); err == nil {
				t.Fatal("canceled outstanding publication succeeded")
			}
			if _, err := start(99); err == nil {
				t.Fatal("cancel freed unreturned host slot")
			}
			if _, err := os.Stat(gate + ".child-99"); !os.IsNotExist(err) {
				t.Fatal("rejected callback entered", err)
			}
			select {
			case err := <-rootDone:
				t.Fatal("root returned through blocked actual publication", err)
			default:
			}
			cancel()
			if err := os.WriteFile(gate+".release", []byte("release owned writer"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := <-rootDone; err == nil {
				t.Fatal("unknown/canceled root became success")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.writes) != 1 {
				t.Fatal("blocked/unknown mutation replayed or later write entered", f.writes)
			}
			t.Logf("physical publication %s: actual worker entries<=%d; host charged slots=2; cancellation retained unreturned slot; first confirmed write1", mode, maximum)
		})
	}
}

// Deterministic SDK component guard probe, separate from actual Runner tests:
// observe finishCleanupRoot's bodyReturned flag while a child join is held.
func TestCleanupContinuationReturnedRootCannotRetainHostCallerWhileJoining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	limits := transportLimits{LogicalBytes: 8192, HostSendBytes: 8192, HostReadBytes: 8192, SessionChunks: 64}
	writer := &cancelFenceWriter{}
	wire := &wireSession{ctx: ctx, writer: writer, identity: ownedConcurrentBinding().Identity, maxFrame: 4096, limits: limits}
	call := &concurrentWorkerCall{request: Frame{ID: "ownedRoot", Method: "stop_service"}, ctx: ctx, cancel: cancel, gate: make(chan struct{}, 1), pending: map[string]chan Frame{}, account: concurrentAccount{limits: limits, worker: true}, cleanup: true}
	call.cleanupRoot = &cleanupWorkerRoot{root: call, slots: 1}
	call.cleanupRoot.children.Add(1)
	retained := make(chan HostCaller, 1)
	w := &concurrentWorker{ctx: ctx, cancel: cancel, transport: &concurrentTransport{wire: wire, ctx: ctx}, proposal: concurrentProposal{Version: 1, MaxCalls: 32, MaxHostCalls: 64}, calls: map[string]*concurrentWorkerCall{call.request.ID: call}, done: make(chan error, 1), handle: func(_ context.Context, _ string, _ json.RawMessage, h HostCaller) (json.RawMessage, error) {
		retained <- h
		return json.RawMessage(`null`), nil
	}}
	joined := make(chan struct{})
	go func() { defer close(joined); w.invoke(call) }()
	host := <-retained
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		returned := call.cleanupRoot.bodyReturned
		w.mu.Unlock()
		if returned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual root body not returned")
		}
		time.Sleep(time.Millisecond)
	}
	probe, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err := host(probe, "fixture.write", json.RawMessage(`{}`))
	stop()
	writes := writer.writes.Load()
	call.cleanupRoot.children.Done()
	<-joined
	if err == nil || writes != 0 {
		t.Fatalf("returned root retained HostCaller during descendant join: err=%v physicalwrites=%d", err, writes)
	}
}

func TestCleanupContinuationReturnedRootStillOwnsGrandchildren(t *testing.T) {
	stackPath := t.TempDir() + "/diagnostic"
	f := newCleanupPeer(t, true, "--fixture-stack="+stackPath)
	f.block = true
	done := f.startRoot(f.ctx, 180, "nojoin")
	f.awaitEntered(1)
	select {
	case err := <-done:
		t.Fatal("root returned before source child", err)
	case <-time.After(20 * time.Millisecond):
	}
	f.mu.Lock()
	f.block = false
	f.mu.Unlock()
	close(f.release)
	var err error
	select {
	case err = <-done:
	case <-time.After(250 * time.Millisecond):
		f.mu.Lock()
		t.Logf("owned fixture before deadline: writes=%d pending=%d entered-channel=%d", len(f.writes), len(f.pending), len(f.entered))
		f.mu.Unlock()
		hostStack := make([]byte, 1<<20)
		hostStack = hostStack[:runtime.Stack(hostStack, true)]
		for _, g := range strings.Split(string(hostStack), "\n\n") {
			if strings.Contains(g, "(*cleanupTestPeer).host") {
				t.Logf("owned host callback stack: %s", g)
			}
		}
		_ = os.WriteFile(stackPath+".request", []byte("owned diagnostic"), 0600)
		cleanupAwaitOwnedFile(t, f.ctx, stackPath+".stack")
		raw, _ := os.ReadFile(stackPath + ".stack")
		t.Logf("owned worker stack before root deadline: %s", raw)
		err = <-done
	}
	if err != nil {
		t.Fatal("joining root rejected live grandchildren", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 180 {
		t.Fatal("returned handler did not join full descendant chain", len(f.writes))
	}
}
