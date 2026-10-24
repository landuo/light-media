package plugins

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// PendingCall is an in-memory invocation, not a durable receipt or a queue.
// Wait reports this call's outcome. Cancellation revokes only this call; an
// uncooperative worker handler retains its bounded slot until it really exits.
// Client.Stop still owns final worker join and reap.
type PendingCall struct {
	done chan struct{}
	cancel context.CancelFunc
	result RunResult
	err error
}

func (p *PendingCall) Wait() (RunResult, error) {
	<-p.done
	out := p.result
	out.Result = append(json.RawMessage(nil), out.Result...)
	return out, p.err
}

func (p *PendingCall) Cancel() { p.cancel() }

// StartCall installs a new, independently cancellable invocation. Success
// means actual admission into this Client's bounded concurrent call table,
// never merely launching a goroutine that might later fail admission. It does
// not wait for the worker result, and a full table returns immediately.
// Completion is collected even when the owner does not call Wait.
// Legacy workers explicitly do not support this asynchronous contract.
func (c *Client) StartCall(ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (*PendingCall, error) {
	return c.StartCallWithAdmission(ctx, ctx, method, payload, host, options)
}

// StartCallWithAdmission separates admission cancellation from the accepted
// invocation lifetime. A canceled admission context is rejected under the call
// table lock; cancellation after admission never retroactively cancels work.
func (c *Client) StartCallWithAdmission(admissionCtx, ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (*PendingCall, error) {
	if c == nil || !c.SupportsConcurrent() {
		return nil, failure("concurrent_session_unsupported")
	}
	if options.CallerDeadlineOnly && options.Timeout != 0 {
		return nil, failure("invalid_limits")
	}
	if options.Timeout == 0 && !options.CallerDeadlineOnly {
		options.Timeout = c.runner.Timeout
	}
	if !options.CallerDeadlineOnly && (options.Timeout < time.Millisecond || options.Timeout > 30*time.Minute) {
		return nil, failure("invalid_limits")
	}
	if c.stopRequested.Load() {
		return nil, failure("session_closed")
	}
	if admissionCtx == nil || ctx == nil || !methodPattern.MatchString(method) || method == "handshake" || method == "stop" || strings.HasPrefix(method, "transport.") || !slices.Contains(c.manifest.Methods, method) || len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload) {
		return nil, failure("invalid_invocation")
	}
	owned := append(json.RawMessage(nil), payload...)
	callCtx, cancel := context.WithCancel(ctx)
	pending := &PendingCall{done: make(chan struct{}), cancel: cancel}
	admitted := make(chan error, 1)
	go func() {
		pending.result, pending.err = c.concurrent.callStarted(callCtx, method, owned, host, options, false, admitted, admissionCtx)
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
