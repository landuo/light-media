package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
)

type concurrentOutcome struct {
	frame Frame
	err   error
}
type concurrentHostCall struct {
	id                string
	method            string
	ctx               context.Context
	cancel            context.CancelFunc
	finish            context.CancelFunc
	stopLife          func() bool
	budget            *runnerBudget
	host              HostHandler
	account           concurrentAccount
	result            chan concurrentOutcome
	sent              chan struct{}
	mu                sync.Mutex
	closed            bool
	received          bool
	wasCanceled       bool
	cleanup           bool
	inHost            bool
	reversePending    int
	reversePublishing bool
	hostBudgetUsers   int
	seen              map[string]bool
	streamSequence    uint64
	hosts             sync.WaitGroup
	cleanupRoot       *cleanupHostRoot
	cleanupReleased   bool
	cleanupReturned   bool
	cleanupParentID   string
}

type concurrentClient struct {
	publicServiceStop    bool
	privateSchedulerStop bool
	transportClose       bool
	cleanupContinuations bool
	client               *Client
	transport            *concurrentTransport
	proposal             concurrentProposal
	mu                   sync.Mutex
	calls                map[string]*concurrentHostCall
	terminal             error
	stopping             bool
	draining             bool
	shutdownDone         chan struct{}
	shutdownErr          error
	cleanupGate          chan struct{}
	expectedEOF          bool
	stopDone             chan struct{}
	stopErr              error
	readerDone           chan struct{}
	hosts                sync.WaitGroup
	writes               sync.WaitGroup
}

func newConcurrentClient(c *Client, proposal concurrentProposal) *concurrentClient {
	return &concurrentClient{client: c, transport: &concurrentTransport{wire: c.session, ctx: c.life}, proposal: proposal, calls: map[string]*concurrentHostCall{}, cleanupGate: make(chan struct{}, 1), readerDone: make(chan struct{})}
}

// SupportsConcurrent reports completed negotiation, not a manifest claim.
func (c *Client) SupportsConcurrent() bool { return c != nil && c.concurrent != nil }

// SupportsParallelHostCalls reports completed opt-in negotiation only.
func (c *Client) SupportsParallelHostCalls() bool {
	return c != nil && c.concurrent != nil && c.concurrent.proposal.MaxParallelHostCalls > 0
}

func (m *concurrentClient) abort(err error) {
	m.mu.Lock()
	if m.terminal == nil {
		m.terminal = err
	}
	for _, call := range m.calls {
		call.revoke()
	}
	m.mu.Unlock()
	m.client.cancel()
	m.client.session.abort()
}
func (call *concurrentHostCall) revoke() {
	call.mu.Lock()
	call.closed = true
	call.host = nil
	call.cancel()
	call.mu.Unlock()
}

// Caller holds call.mu. A streaming root needs no table of completed callback
// IDs. The serial worker writes one canonical successor after each reply.
func (call *concurrentHostCall) acceptReverseID(id string) bool {
	if call.account.streaming {
		sequence := call.streamSequence + 1
		if sequence == 0 || id != call.id+"."+strconv.FormatUint(sequence, 10) {
			return false
		}
		call.streamSequence = sequence
		return true
	}
	if call.seen[id] {
		return false
	}
	call.seen[id] = true
	return true
}
func (m *concurrentClient) terminalError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminal != nil {
		return m.terminal
	}
	return failure("execution_canceled")
}

func (m *concurrentClient) readLoop() {
	defer close(m.readerDone)
	defer func() {
		m.mu.Lock()
		for id, call := range m.calls {
			call.revoke()
			call.stopLife()
			call.finish()
			reason := m.terminal
			if reason == nil {
				reason = failure("session_closed")
			}
			m.releaseCleanupChildLocked(call, concurrentOutcome{err: reason})
			delete(m.calls, id)
		}
		m.mu.Unlock()
	}()
	for {
		frame, count, err := m.transport.read()
		if err != nil {
			m.mu.Lock()
			expected := m.expectedEOF
			m.mu.Unlock()
			if !expected || !errors.Is(err, io.EOF) {
				if errors.Is(err, io.EOF) {
					err = failure("worker_io_failed")
				}
				m.abort(err)
			}
			return
		}
		m.mu.Lock()
		call := m.calls[concurrentRoot(frame.ID)]
		m.mu.Unlock()
		if call == nil {
			m.abort(failure("invalid_reply"))
			return
		}
		if err = call.account.receive(count); err != nil {
			m.abort(err)
			return
		}
		if frame.Type == "host_call" && frame.Method == "transport.cancel" {
			if frame.ID != call.id || string(frame.Payload) != "null" {
				m.abort(failure("invalid_host_call"))
				return
			}
			call.revoke()
			continue
		}
		switch frame.Type {
		case "result":
			if frame.ID != call.id || frame.Method != "" || frame.Payload != nil {
				m.abort(failure("invalid_reply"))
				return
			}
			call.mu.Lock()
			if call.received {
				call.mu.Unlock()
				m.abort(failure("invalid_reply"))
				return
			}
			call.received = true
			call.wasCanceled = call.ctx.Err() != nil
			call.mu.Unlock()
			if call.cleanup && call.method == "stop" {
				m.mu.Lock()
				m.expectedEOF = true
				m.mu.Unlock()
			}
			if root := call.cleanupRoot; root != nil && root.root == call && frame.Error == nil {
				// Seal admission immediately, while successful descendants finish
				// their host-side result handoff before root cancellation.
				call.mu.Lock()
				call.closed = true
				call.host = nil
				call.mu.Unlock()
			} else {
				call.revoke()
			}
			// Joining cannot block the sole reader, which may be needed by
			// another call's callback. No callback can be added after revoke.
			go m.complete(call, concurrentOutcome{frame: frame})
		case "host_call":
			if !strings.HasPrefix(frame.ID, call.id+".") || !methodPattern.MatchString(frame.Method) || strings.HasPrefix(frame.Method, "transport.") {
				m.abort(failure("invalid_host_call"))
				return
			}
			call.mu.Lock()
			if m.proposal.MaxParallelHostCalls == 0 && call.inHost || m.proposal.MaxParallelHostCalls > 0 && call.reversePending >= m.proposal.MaxParallelHostCalls || !call.acceptReverseID(frame.ID) {
				call.mu.Unlock()
				m.abort(failure("invalid_host_call"))
				return
			}
			if err = call.account.host(m.proposal.MaxHostCalls); err != nil {
				call.mu.Unlock()
				m.abort(err)
				return
			}
			if call.closed || call.ctx.Err() != nil || call.host == nil {
				call.mu.Unlock()
				if call.account.streaming {
					// The root's cancel frame wakes the serial caller. Do not
					// queue reply writers for already revoked stream input.
					continue
				}
				m.writes.Add(1)
				go func() { defer m.writes.Done(); m.replyDenied(call, frame.ID) }()
				continue
			}
			if m.proposal.MaxParallelHostCalls > 0 {
				call.reversePending++
			} else {
				call.inHost = true
			}
			host := call.host
			call.hosts.Add(1)
			m.hosts.Add(1)
			call.mu.Unlock()
			if m.proposal.MaxParallelHostCalls > 0 {
				go m.callHostParallel(call, host, frame)
			} else {
				go m.callHost(call, host, frame)
			}
		default:
			m.abort(failure("invalid_reply"))
			return
		}
	}
}

func (m *concurrentClient) complete(call *concurrentHostCall, outcome concurrentOutcome) {
	call.hosts.Wait()
	if root := call.cleanupRoot; root != nil && root.root == call {
		root.children.Wait()
		call.revoke()
	}
	m.mu.Lock()
	if root := call.cleanupRoot; root != nil && root.root == call {
		if m.terminal != nil {
			outcome.err = m.terminal
		} else if outcome.err == nil && outcome.frame.Error == nil {
			outcome.err = root.err
		}
	}
	m.releaseCleanupChildLocked(call, outcome)
	if m.calls[call.id] == call {
		delete(m.calls, call.id)
	}
	m.mu.Unlock()
	select {
	case call.result <- outcome:
	default:
	}
	call.stopLife()
	call.finish()
}
func (m *concurrentClient) replyDenied(call *concurrentHostCall, id string) {
	_, err := m.transport.write(Frame{Protocol: ProtocolVersion, ID: id, Type: "host_result", Identity: m.client.binding.Identity, Error: &ReplyError{Code: "HOST_DENIED"}}, call.account.send)
	if err != nil {
		m.abort(err)
	}
}
func (m *concurrentClient) callHost(call *concurrentHostCall, host HostHandler, frame Frame) {
	defer m.hosts.Done()
	defer call.hosts.Done()
	reply := Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: "host_result", Identity: m.client.binding.Identity}
	var err error
	if call.budget != nil && !call.budget.pause() {
		err = failure("execution_canceled")
	} else {
		if call.ctx.Err() == nil {
			reply.Result, err = host(call.ctx, m.client.binding.Identity, frame.Method, frame.Payload)
		} else {
			err = call.ctx.Err()
		}
		if call.budget != nil {
			call.budget.resume()
		}
	}
	call.mu.Lock()
	if !call.account.streaming {
		call.inHost = false
	}
	closed := call.closed || call.ctx.Err() != nil
	call.mu.Unlock()
	if closed {
		return
	}
	if err != nil {
		reply.Result = nil
		reply.Error = &ReplyError{Code: "HOST_DENIED"}
	} else if len(reply.Result) == 0 {
		reply.Result = json.RawMessage(`null`)
	}
	var publish []func() error
	dropped := false
	if call.account.streaming {
		publish = append(publish, func() error {
			call.mu.Lock()
			defer call.mu.Unlock()
			if call.closed || call.ctx.Err() != nil {
				dropped = true
				return failure("execution_canceled")
			}
			// writeMu has already been acquired. Until now even a peer
			// pipelining IDs could not queue more response goroutines.
			call.inHost = false
			return nil
		})
	}
	sent, sendErr := m.transport.write(reply, call.account.send, publish...)
	if sendErr != nil && sent.bytes == 0 && concurrentReplyError(sendErr) != "" {
		reply.Result = nil
		reply.Error = &ReplyError{Code: "HOST_DENIED"}
		_, sendErr = m.transport.write(reply, nil, publish...)
	}
	if sendErr != nil && !dropped {
		m.abort(sendErr)
	}
}

func (m *concurrentClient) call(ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions, cleanup bool) (RunResult, error) {
	return m.callStarted(ctx, method, payload, host, options, cleanup, nil, nil)
}

// admitted is notified exactly once, before any worker completion is awaited.
// An accepted call already owns the same bounded table slot as ordinary Call.
func (m *concurrentClient) callStarted(ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions, cleanup bool, admitted chan<- error, admissionCtx context.Context, ancestor ...*cleanupHostRoot) (out RunResult, err error) {
	announced := false
	defer func() {
		if admitted != nil && !announced {
			admitted <- err
		}
	}()
	if ctx.Err() != nil {
		return out, failure("execution_canceled")
	}
	id, err := randomID()
	if err != nil {
		return out, err
	}
	parent, cancel := context.WithCancel(context.WithValue(ctx, concurrentCallIDKey{}, id))
	stopLife := context.AfterFunc(m.client.life, cancel)
	callCtx, finish, budget := invocationContext(parent, options)
	call := &concurrentHostCall{id: id, method: method, ctx: callCtx, cancel: cancel, finish: finish, stopLife: stopLife, budget: budget, host: host, cleanup: cleanup, account: concurrentAccount{limits: m.client.session.limits, streaming: m.proposal.streamsHostCalls(method)}, result: make(chan concurrentOutcome, 1), sent: make(chan struct{}), seen: map[string]bool{}}
	m.mu.Lock()
	if admissionCtx != nil && admissionCtx.Err() != nil {
		m.mu.Unlock()
		stopLife()
		cancel()
		finish()
		return out, failure("execution_canceled")
	}
	if len(ancestor) > 0 {
		var root *cleanupHostRoot
		var bindErr error
		for {
			root, bindErr = m.cleanupRootLocked(admissionCtx)
			if bindErr != nil || root != ancestor[0] {
				break
			}
			binding := admissionCtx.Value(cleanupHostBindingKey{}).(*cleanupHostBinding)
			if root.slots < cleanupHandoffSlots {
				call.cleanupParentID = binding.call.id
				break
			}
			// Only the current chain's predecessor can be awaiting its ACK/
			// actual reply. Siblings or a root's third child are not queued.
			previous := m.calls[binding.call.cleanupParentID]
			if binding.call == root.root || previous == nil || previous == root.root || previous.cleanupRoot != root {
				bindErr = failure("cleanup_call_limit")
				break
			}
			changed := root.slotChanged
			m.mu.Unlock()
			select {
			case <-changed:
			case <-ctx.Done():
				bindErr = failure("execution_canceled")
			}
			m.mu.Lock()
			if bindErr != nil {
				break
			}
		}
		if bindErr != nil || root != ancestor[0] {
			m.mu.Unlock()
			stopLife()
			cancel()
			finish()
			if bindErr != nil {
				return out, bindErr
			}
			return out, failure("cleanup_call_closed")
		}
		call.cleanupRoot = root
	} else if cleanup && m.cleanupContinuations && (method == "close" || method == "stop_service") {
		call.cleanupRoot = &cleanupHostRoot{root: call, slotChanged: make(chan struct{})}
	}
	if m.terminal != nil || (m.draining || m.stopping) && !cleanup || m.stopping && method != "stop" || len(m.calls) >= m.proposal.MaxCalls && !cleanup && !options.privateSchedulerStop {
		reason := m.terminal
		if reason == nil {
			if m.draining || m.stopping {
				reason = failure("session_closed")
			} else {
				reason = failure("concurrent_call_limit")
			}
		}
		m.mu.Unlock()
		stopLife()
		cancel()
		finish()
		return out, reason
	}
	m.calls[id] = call
	if root := call.cleanupRoot; root != nil {
		if root.root != call {
			root.slots++
			root.children.Add(1)
		}
		call.ctx = context.WithValue(call.ctx, cleanupHostBindingKey{}, &cleanupHostBinding{client: m.client, call: call, root: root})
	}
	m.writes.Add(1)
	m.mu.Unlock()
	if root := call.cleanupRoot; root != nil && root.root != call {
		defer func() {
			m.mu.Lock()
			call.cleanupReturned = true
			if call.cleanupReleased {
				root.children.Done()
			}
			m.mu.Unlock()
		}()
	}
	if admitted != nil && len(ancestor) == 0 {
		announced = true
		admitted <- nil
	}
	before, _ := m.client.stderr.snapshot()
	defer func() {
		result := out.Result
		out = call.account.snapshot()
		out.Result = result
		after, exceeded := m.client.stderr.snapshot()
		out.StderrBytes = after - before
		if exceeded {
			err = failure("stderr_limit")
			m.abort(err)
		}
		if err != nil {
			out.Result = nil
		}
	}()
	// Own input bytes: cancellation can return while a blocked pipe writer is
	// being reaped, so the caller must be free to reuse its original buffer.
	request := Frame{Protocol: ProtocolVersion, ID: id, Type: "call", Identity: m.client.binding.Identity, Method: method, Payload: append(json.RawMessage(nil), payload...)}
	if root := call.cleanupRoot; root != nil && root.root != call {
		request.CleanupRoot = root.root.id
	}
	sendReady := make(chan error, 1)
	go func() {
		defer m.writes.Done()
		n, sendErr := m.transport.write(request, func(n concurrentWireCount) error {
			if call.ctx.Err() != nil {
				return failure("execution_canceled")
			}
			return call.account.send(n)
		})
		close(call.sent)
		sendReady <- sendErr
		if sendErr != nil {
			if n.bytes == 0 {
				call.mu.Lock()
				call.received = true
				call.wasCanceled = call.ctx.Err() != nil
				call.mu.Unlock()
				call.revoke()
				m.complete(call, concurrentOutcome{err: sendErr})
			} else {
				m.abort(sendErr)
			}
		}
	}()
	if admitted != nil && len(ancestor) > 0 {
		// Publish the child frame before acknowledging the parent host call.
		// Otherwise a root whose body returns after SubmitCleanup can seal its
		// worker lineage before the admitted child reaches the reader.
		var sendErr error
		select {
		case sendErr = <-sendReady:
		// A fast child reply seals its own callCtx normally. Only the
		// inherited caller/root context can cancel admission.
		case <-ctx.Done():
			sendErr = failure("execution_canceled")
		}
		announced = true
		admitted <- sendErr
	}
	var outcome concurrentOutcome
	select {
	case outcome = <-call.result:
		if ctx.Err() != nil {
			return out, failure("execution_canceled")
		}
	case <-callCtx.Done():
		call.mu.Lock()
		received := call.received
		call.mu.Unlock()
		if received {
			outcome = <-call.result
			break
		}
		m.mu.Lock()
		terminal := m.terminal
		m.mu.Unlock()
		if terminal != nil {
			<-m.client.done
			return out, terminal
		}
		call.revoke()
		m.mu.Lock()
		notify := !m.stopping && (!m.draining || cleanup) && m.terminal == nil
		if notify {
			m.writes.Add(1)
		}
		m.mu.Unlock()
		if notify {
			go func() {
				defer m.writes.Done()
				m.notifyCancellation(call)
			}()
		}
		return out, failure("execution_canceled")
	case <-m.client.life.Done():
		call.revoke()
		<-m.client.done
		return out, m.terminalError()
	}
	call.mu.Lock()
	canceled := call.wasCanceled
	call.mu.Unlock()
	if canceled || ctx.Err() != nil {
		return out, failure("execution_canceled")
	}
	if outcome.err != nil {
		return out, outcome.err
	}
	if outcome.frame.Error != nil {
		if outcome.frame.Error.Code == "EXECUTION_CANCELED" {
			return out, failure("execution_canceled")
		}
		return out, workerReplyFailure(outcome.frame.Error.Code)
	}
	out.Result = outcome.frame.Result
	return out, nil
}

func (m *concurrentClient) notifyCancellation(call *concurrentHostCall) {
	<-call.sent
	superseded := false
	_, err := m.transport.write(Frame{Protocol: ProtocolVersion, ID: call.id, Type: "call", Identity: m.client.binding.Identity, Method: "transport.cancel", Payload: json.RawMessage(`null`)}, func(concurrentWireCount) error {
		// This check must run under the transport writer lock. A queued cancel
		// can otherwise overtake its earlier live check, then write after Stop
		// has received its final reply and the worker has closed the pipe.
		m.mu.Lock()
		live := m.calls[call.id] == call && !m.stopping && (!m.draining || call.cleanup) && m.terminal == nil
		m.mu.Unlock()
		if !live {
			superseded = true
			return failure("execution_canceled")
		}
		return nil
	})
	if err != nil && !superseded {
		m.abort(err)
	}
}

func invocationContext(parent context.Context, options CallOptions) (context.Context, context.CancelFunc, *runnerBudget) {
	if options.CallerDeadlineOnly {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, nil
	}
	return runnerContext(parent, options.Timeout, options.ExcludeHostTime)
}

func (m *concurrentClient) stop(ctx context.Context, host HostHandler) error {
	m.mu.Lock()
	if m.stopDone != nil {
		done := m.stopDone
		m.mu.Unlock()
		select {
		case <-done:
			m.mu.Lock()
			err := m.stopErr
			m.mu.Unlock()
			return err
		case <-ctx.Done():
			return failure("execution_canceled")
		}
	}
	m.stopping = true
	m.stopDone = make(chan struct{})
	for _, call := range m.calls {
		call.revoke()
	}
	m.mu.Unlock()
	// Stop has its own resource deadline and a reserved call slot. It cannot
	// wait forever behind a canceled handler or a saturated invocation cap.
	stopCtx, cancel := context.WithTimeout(ctx, m.client.runner.Timeout)
	defer cancel()
	_, err := m.call(stopCtx, "stop", nil, host, CallOptions{Timeout: m.client.runner.Timeout}, true)
	if err == nil {
		if closer, ok := m.client.session.writer.(io.Closer); ok {
			closer.Close()
		}
		select {
		case <-m.client.done:
			if m.client.waitErr != nil {
				err = failure("worker_failed")
			}
		case <-stopCtx.Done():
			err = failure("execution_canceled")
		}
	}
	// Always revoke cleanup authority as well, close pipes, kill if needed,
	// and observe the one process reaper before returning.
	if err != nil {
		m.abort(err)
	} else {
		m.client.cancel()
		m.client.session.abort()
	}
	<-m.client.done
	<-m.readerDone
	joined := make(chan struct{})
	go func() { m.writes.Wait(); m.hosts.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-stopCtx.Done():
		if err == nil {
			err = failure("execution_canceled")
		}
	}
	m.mu.Lock()
	m.stopErr = err
	close(m.stopDone)
	m.mu.Unlock()
	return err
}

type concurrentCallIDKey struct{}

// InvocationID identifies the host-generated concurrent call in a HostHandler
// context. It is diagnostic scope, never authority supplied by the worker.
func InvocationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(concurrentCallIDKey{}).(string)
	return id
}

// Only the negotiated path uses these counters. The serial path and its
// budget pause behavior remain unchanged.
func (call *concurrentHostCall) beginParallelHostBudget() bool {
	call.mu.Lock()
	defer call.mu.Unlock()
	if call.closed || call.ctx.Err() != nil {
		return false
	}
	if call.hostBudgetUsers == 0 && call.budget != nil && !call.budget.pause() {
		return false
	}
	call.hostBudgetUsers++
	return true
}
func (call *concurrentHostCall) endParallelHostBudget() {
	call.mu.Lock()
	defer call.mu.Unlock()
	call.hostBudgetUsers--
	if call.hostBudgetUsers == 0 && call.budget != nil {
		call.budget.resume()
	}
}
func (m *concurrentClient) callHostParallel(call *concurrentHostCall, host HostHandler, frame Frame) {
	defer m.hosts.Done()
	defer call.hosts.Done()
	released := false
	defer func() {
		call.mu.Lock()
		if !released {
			call.reversePending--
			released = true
		}
		call.mu.Unlock()
	}()
	reply := Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: "host_result", Identity: m.client.binding.Identity}
	var err error
	if !call.beginParallelHostBudget() {
		err = failure("execution_canceled")
	} else {
		func() {
			defer call.endParallelHostBudget()
			reply.Result, err = host(call.ctx, m.client.binding.Identity, frame.Method, frame.Payload)
		}()
	}
	call.mu.Lock()
	closed := call.closed || call.ctx.Err() != nil
	call.mu.Unlock()
	if closed {
		return
	}
	if err != nil {
		reply.Result = nil
		reply.Error = &ReplyError{Code: "HOST_DENIED"}
	} else if len(reply.Result) == 0 {
		reply.Result = json.RawMessage(`null`)
	}
	dropped := false
	before := func() error {
		call.mu.Lock()
		defer call.mu.Unlock()
		if call.closed || call.ctx.Err() != nil {
			dropped = true
			return failure("execution_canceled")
		}
		if !released {
			call.reversePending--
			released = true
		}
		call.reversePublishing = true
		return nil
	}
	after := func() error { call.mu.Lock(); call.reversePublishing = false; call.mu.Unlock(); return nil }
	sent, sendErr := m.transport.write(reply, call.account.send, before, after)
	if dropped && sent.bytes == 0 {
		return
	}
	if sendErr != nil && sent.bytes == 0 && concurrentReplyError(sendErr) != "" {
		reply.Result = nil
		reply.Error = &ReplyError{Code: "HOST_DENIED"}
		sent, sendErr = m.transport.write(reply, nil, before, after)
		if dropped && sent.bytes == 0 {
			return
		}
	}
	if sendErr != nil {
		m.abort(sendErr)
	}
}
