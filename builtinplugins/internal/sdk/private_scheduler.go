package plugins

import (
	"context"
	"encoding/json"
	"slices"
)

// SupportsPrivateSchedulerStop is negotiated SDK support, not a manifest claim.
func (c *Client) SupportsPrivateSchedulerStop() bool {
	return c != nil && c.manifest.ManagedPrivateScheduler && c.concurrent != nil && c.concurrent.privateSchedulerStop && c.concurrent.transportClose
}

// CallPrivateSchedulerStop is a host-manager-only pre-stop lane. It uses the
// existing ordinary close/stop_service frame, reserving one slot at a full
// ordinary table without setting the global shutdown fence. Only explicitly
// managed private schedulers support it; cleanup-handoff remains post-fence.
func (c *Client) CallPrivateSchedulerStop(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if ctx == nil || c == nil || !c.SupportsPrivateSchedulerStop() || cleanupDeclared(c.manifest) || method != "close" && method != "stop_service" || !slices.Contains(c.manifest.Methods, method) || len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload) {
		return RunResult{}, failure("invalid_invocation")
	}
	return c.callManagerPreStop(ctx, method, payload, host)
}

// SupportsPublicServiceStop is an actual concurrent SDK feature. No manifest
// claim, private scheduler ownership or cleanup authority is inferred from it.
func (c *Client) SupportsPublicServiceStop() bool {
	return c != nil && c.manifest.ManagedPublicServices && c.concurrent != nil && c.concurrent.publicServiceStop && c.concurrent.transportClose && !cleanupDeclared(c.manifest)
}

// CallPublicServiceStop runs source close/stop_service while accepted public
// callbacks still own their normal phases. It shares the single reserved lane;
// it neither fences ordinary calls nor creates a cleanup continuation grant.
func (c *Client) CallPublicServiceStop(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if ctx == nil || c == nil || !c.SupportsPublicServiceStop() || method != "close" && method != "stop_service" || !slices.Contains(c.manifest.Methods, method) || len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload) {
		return RunResult{}, failure("invalid_invocation")
	}
	return c.callManagerPreStop(ctx, method, payload, host)
}

func (c *Client) callManagerPreStop(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if c.stopRequested.Load() {
		return RunResult{}, failure("session_closed")
	}
	m := c.concurrent
	select {
	case m.cleanupGate <- struct{}{}:
	case <-ctx.Done():
		return RunResult{}, failure("execution_canceled")
	case <-c.life.Done():
		return RunResult{}, failure("session_closed")
	default:
		return RunResult{}, failure("cleanup_call_limit")
	}
	defer func() { <-m.cleanupGate }()
	m.mu.Lock()
	closed := m.draining || m.stopping || m.terminal != nil
	m.mu.Unlock()
	if closed {
		return RunResult{}, failure("session_closed")
	}
	return c.CallWithOptions(ctx, method, payload, host, CallOptions{CallerDeadlineOnly: true, privateSchedulerStop: true})
}
