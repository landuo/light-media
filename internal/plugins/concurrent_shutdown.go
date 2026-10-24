package plugins

import (
	"context"
	"encoding/json"
	"slices"
)

// BeginShutdown fences ordinary admission and revokes every ordinary call's
// host authority before asking the worker to cancel its matching phases. ACK
// means that fence was applied, not that handlers have finished. A subsequent
// close/stop_service call may be precisely what allows old handlers to finish.
// Legacy sessions return unsupported without mutating or killing the object.
func (c *Client) BeginShutdown(ctx context.Context) error {
	if ctx == nil {
		return failure("invalid_invocation")
	}
	if !c.SupportsConcurrent() {
		return failure("concurrent_session_unsupported")
	}
	c.stopRequested.Store(true)
	return c.concurrent.beginShutdown(ctx)
}

// CallCleanup uses fresh caller-supplied authority on a single reserved lane.
// Only the host manager can enter this API. No worker-supplied cleanup bit,
// retained HostHandler, or manifest field grants access to this lane. The SDK
// does not choose manager order or decide whether a failure skips a later step.
func (c *Client) CallCleanup(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if ctx == nil || method != "close" && method != "stop_service" || len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload) {
		return RunResult{}, failure("invalid_invocation")
	}
	if !c.SupportsConcurrent() {
		return RunResult{}, failure("concurrent_session_unsupported")
	}
	if !slices.Contains(c.manifest.Methods, method) {
		return RunResult{}, failure("invalid_invocation")
	}
	return c.concurrent.callCleanup(ctx, method, payload, host)
}

func (m *concurrentClient) beginShutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.shutdownDone != nil {
		done := m.shutdownDone
		m.mu.Unlock()
		select {
		case <-done:
			m.mu.Lock()
			err := m.shutdownErr
			m.mu.Unlock()
			return err
		case <-ctx.Done():
			return failure("execution_canceled")
		}
	}
	if m.stopping || m.terminal != nil {
		err := m.terminal
		if err == nil {
			err = failure("session_closed")
		}
		m.mu.Unlock()
		return err
	}
	m.draining = true
	m.shutdownDone = make(chan struct{})
	for _, call := range m.calls {
		call.revoke()
	}
	m.mu.Unlock()
	out, err := m.call(ctx, "transport.shutdown", json.RawMessage(`{"version":1}`), nil, CallOptions{Timeout: m.client.runner.Timeout}, true)
	var accepted struct {
		Version int `json:"version"`
	}
	if err == nil && (strictJSON(out.Result, &accepted) != nil || accepted.Version != 1) {
		err = failure("concurrent_session_mismatch")
	}
	if err != nil {
		m.mu.Lock()
		stopping := m.stopping
		m.mu.Unlock()
		if !stopping {
			m.abort(err)
			<-m.client.done
			<-m.readerDone
		}
	}
	m.mu.Lock()
	m.shutdownErr = err
	close(m.shutdownDone)
	m.mu.Unlock()
	return err
}

func (m *concurrentClient) callCleanup(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	m.mu.Lock()
	done := m.shutdownDone
	m.mu.Unlock()
	if done == nil {
		return RunResult{}, failure("shutdown_not_started")
	}
	select {
	case <-done:
	case <-ctx.Done():
		return RunResult{}, failure("execution_canceled")
	case <-m.client.life.Done():
		return RunResult{}, m.terminalError()
	}
	m.mu.Lock()
	err := m.shutdownErr
	stopping := m.stopping
	m.mu.Unlock()
	if err != nil {
		return RunResult{}, err
	}
	if stopping {
		return RunResult{}, failure("session_closed")
	}
	select {
	case m.cleanupGate <- struct{}{}:
	case <-ctx.Done():
		return RunResult{}, failure("execution_canceled")
	case <-m.client.life.Done():
		return RunResult{}, m.terminalError()
	default:
		return RunResult{}, failure("cleanup_call_limit")
	}
	defer func() { <-m.cleanupGate }()
	// A canceled cleanup call retains its resource slot until the worker has
	// actually replied. Returning cancellation is not proof of handler exit.
	m.mu.Lock()
	pending := false
	for _, call := range m.calls {
		if call.cleanup && (call.method == "close" || call.method == "stop_service") {
			pending = true
			break
		}
	}
	m.mu.Unlock()
	if pending {
		return RunResult{}, failure("cleanup_call_limit")
	}
	return m.call(ctx, method, payload, host, CallOptions{Timeout: m.client.runner.Timeout}, true)
}
