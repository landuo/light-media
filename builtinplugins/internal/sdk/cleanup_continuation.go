package plugins

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
)

const CleanupSubmitCapability = "plugin.cleanup.submit"

const cleanupHandoffSlots = 2

type cleanupInvocationKey struct{}

// IsCleanupInvocation identifies SDK-created root/descendant cleanup contexts.
// It is a dispatch hint, not authority: the host independently binds the live
// exact Client/root and manager stop scope on every admitted continuation.
func IsCleanupInvocation(ctx context.Context) bool {
	return ctx != nil && ctx.Value(cleanupInvocationKey{}) == true
}

func (m Manager) SubmitCleanup(ctx context.Context, method string, payload any) error {
	if m.Host == nil {
		return failure("invalid_host_call")
	}
	raw, err := json.Marshal(struct {
		Method  string `json:"method"`
		Payload any    `json:"payload"`
	}{method, payload})
	if err != nil {
		return err
	}
	_, err = m.Host(ctx, CleanupSubmitCapability, raw)
	return err
}

func cleanupDeclared(m Manifest) bool {
	return slices.Contains(m.Capabilities, CleanupSubmitCapability)
}
func cleanupChildMethod(method string) bool {
	return methodPattern.MatchString(method) && method != "init" && method != "handshake" && method != "close" && method != "stop_service" && method != "stop" && !strings.HasPrefix(method, "transport.")
}

type cleanupHostRoot struct {
	root        *concurrentHostCall
	slots       int // guarded by concurrentClient.mu, retained until actual reply/drain
	children    sync.WaitGroup
	slotChanged chan struct{}
	err         error
}
type cleanupHostBinding struct {
	client *Client
	call   *concurrentHostCall
	root   *cleanupHostRoot
}
type cleanupHostBindingKey struct{}

func (c *Client) SupportsCleanupContinuations() bool {
	return c != nil && c.concurrent != nil && c.concurrent.cleanupContinuations
}
func (m *concurrentClient) cleanupRootLocked(ctx context.Context) (*cleanupHostRoot, error) {
	if ctx == nil || ctx.Err() != nil || !m.cleanupContinuations || !m.draining || m.stopping || m.terminal != nil {
		return nil, failure("cleanup_call_closed")
	}
	b, ok := ctx.Value(cleanupHostBindingKey{}).(*cleanupHostBinding)
	if !ok || b == nil || b.client != m.client || b.call == nil || b.root == nil || b.call.cleanupRoot != b.root || m.calls[b.call.id] != b.call || m.calls[b.root.root.id] != b.root.root {
		return nil, failure("cleanup_call_closed")
	}
	for _, call := range []*concurrentHostCall{b.call, b.root.root} {
		call.mu.Lock()
		closed := call.closed || call.ctx.Err() != nil
		call.mu.Unlock()
		if closed {
			return nil, failure("cleanup_call_closed")
		}
	}
	return b.root, nil
}

// CleanupContext returns only the live, SDK-bound root context of this exact
// Client. A method name, payload bit, copied call ID or another Client cannot
// create it. The manager uses it to construct a fresh scoped HostHandler.
func (c *Client) CleanupContext(parentHostCtx context.Context) (context.Context, error) {
	if !c.SupportsCleanupContinuations() {
		return nil, failure("cleanup_continuation_unsupported")
	}
	c.concurrent.mu.Lock()
	defer c.concurrent.mu.Unlock()
	root, err := c.concurrent.cleanupRootLocked(parentHostCtx)
	if err != nil {
		return nil, err
	}
	return root.root.ctx, nil
}

// StartCleanupContinuationWithAdmission inherits the real root's deadline and
// lifetime. It reserves one of two finite handoff slots under the call-table
// lock. A canceled call holds that slot until its actual reply/callback drain.
func (c *Client) StartCleanupContinuationWithAdmission(parentHostCtx context.Context, method string, payload json.RawMessage, host HostHandler) (*PendingCall, error) {
	if !c.SupportsCleanupContinuations() {
		return nil, failure("cleanup_continuation_unsupported")
	}
	if !cleanupChildMethod(method) || !slices.Contains(c.manifest.Methods, method) || len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload) {
		return nil, failure("invalid_invocation")
	}
	m := c.concurrent
	m.mu.Lock()
	root, err := m.cleanupRootLocked(parentHostCtx)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(root.root.ctx)
	pending := &PendingCall{done: make(chan struct{}), cancel: cancel}
	admitted := make(chan error, 1)
	owned := append(json.RawMessage(nil), payload...)
	go func() {
		pending.result, pending.err = m.callStarted(ctx, method, owned, host, CallOptions{CallerDeadlineOnly: true}, true, admitted, parentHostCtx, root)
		cancel()
		close(pending.done)
	}()
	if err := <-admitted; err != nil {
		cancel()
		<-pending.done
		return nil, err
	}
	return pending, nil
}

// Called under m.mu after a true reply/drain, or terminal session teardown.
// Cancellation alone does not release a handoff slot.
func (m *concurrentClient) releaseCleanupChildLocked(call *concurrentHostCall, outcome concurrentOutcome) {
	root := call.cleanupRoot
	if root == nil || root.root == call || call.cleanupReleased {
		return
	}
	call.cleanupReleased = true
	call.mu.Lock()
	canceled := call.wasCanceled
	call.mu.Unlock()
	err := outcome.err
	if err == nil && canceled {
		err = failure("execution_canceled")
	}
	if err == nil && outcome.frame.Error != nil {
		err = workerReplyFailure(outcome.frame.Error.Code)
	}
	if root.err == nil {
		root.err = err
	}
	root.slots--
	close(root.slotChanged)
	root.slotChanged = make(chan struct{})
	if call.cleanupReturned {
		root.children.Done()
	}
}

type cleanupWorkerRoot struct {
	root         *concurrentWorkerCall
	slots        int // guarded by concurrentWorker.mu
	bodyReturned bool
	closed       bool
	children     sync.WaitGroup
}

func (w *concurrentWorker) finishCleanupRoot(call *concurrentWorkerCall, handlerErr error) {
	root := call.cleanupRoot
	if root == nil || root.root != call {
		return
	}
	// Revoke the returned handler's retained HostCaller immediately. Its
	// live root context continues only for already admitted descendants.
	w.mu.Lock()
	call.phase.closed.Store(true)
	root.bodyReturned = true
	if root.slots == 0 {
		root.closed = true
	}
	if handlerErr != nil || call.ctx.Err() != nil {
		root.closed = true
		call.cancel()
	}
	w.mu.Unlock()
	root.children.Wait()
	w.mu.Lock()
	root.closed = true
	w.mu.Unlock()
}
func (w *concurrentWorker) releaseCleanupChild(call *concurrentWorkerCall) {
	root := call.cleanupRoot
	if root == nil || root.root == call {
		return
	}
	// The slot is released after actual handler/callback drain, before its
	// reply is published. Joining still waits until that physical write ends.
	root.children.Done()
}
