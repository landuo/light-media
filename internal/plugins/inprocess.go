package plugins

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// InProcessHandler is the host-side entry of one builtin plugin instance: the
// library call that replaces launching an executable. The handler owns its
// own concurrency discipline exactly as the worker process did. It uses the
// same HostCaller shape the standalone worker's SDK uses.
type InProcessHandler func(ctx context.Context, method string, payload json.RawMessage, host HostCaller) (json.RawMessage, error)

// InProcessOptions describes a builtin plugin's negotiated shape. Concurrent
// mirrors the serve mode the standalone worker used: a serial plugin expects
// the app's method lock to serialize calls, exactly like the legacy worker.
type InProcessOptions struct {
	Manifest   Manifest
	Concurrent bool
}

// inProcessSession is one live builtin instance. There is no process, no
// transport and no handshake: identity comes from construction, capabilities
// from the manifest, and every stop lane keeps the wire client's semantics
// (single final join, cleanup lane bypassing the drain fence).
type inProcessSession struct {
	manifest   Manifest
	binding    SessionBinding
	handler    InProcessHandler
	concurrent bool

	mu            sync.Mutex
	stopRequested atomic.Bool
	stopped       bool
	terminal      error
	stopDone      chan struct{}
	draining      bool
	rootCtx       context.Context
	rootCancel    context.CancelFunc
	hostCalls     atomic.Int64
	inflight      sync.WaitGroup
}

// NewInProcess starts a builtin plugin session. The returned Session is
// usable immediately; Stop joins outstanding handler goroutines.
func NewInProcess(binding SessionBinding, options InProcessOptions, handler InProcessHandler) (Session, error) {
	if binding.Generation < 1 || binding.PluginID == "" || binding.InstanceID == "" || handler == nil {
		return nil, failure("invalid_invocation")
	}
	if err := options.Manifest.Validate(); err != nil {
		return nil, err
	}
	if options.Manifest.PluginID != binding.PluginID || options.Manifest.Version != binding.Version {
		return nil, failure("handshake_mismatch")
	}
	root, cancel := context.WithCancel(context.Background())
	return &inProcessSession{manifest: options.Manifest, binding: binding, handler: handler, concurrent: options.Concurrent, rootCtx: root, rootCancel: cancel}, nil
}

func (s *inProcessSession) hostBridge(host HostHandler) HostCaller {
	return func(ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
		if host == nil {
			return nil, failure("invalid_host_call")
		}
		s.hostCalls.Add(1)
		return host(ctx, s.binding.Identity, method, payload)
	}
}

func (s *inProcessSession) validate(method string, payload json.RawMessage) error {
	if s == nil || s.stopRequested.Load() {
		return failure("session_closed")
	}
	if !methodPattern.MatchString(method) || method == "handshake" || method == "stop" || strings.HasPrefix(method, "transport.") {
		return failure("invalid_invocation")
	}
	if !slices.Contains(s.manifest.Methods, method) {
		return failure("invalid_invocation")
	}
	if len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload) {
		return failure("invalid_invocation")
	}
	return nil
}

func (s *inProcessSession) invoke(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (out RunResult, err error) {
	owned := append(json.RawMessage(nil), payload...)
	s.inflight.Add(1)
	defer s.inflight.Done()
	defer func() {
		if r := recover(); r != nil {
			// A builtin panic must not take the host down. The wire process
			// died and the session failed over; here the failure is terminal
			// for this session and surfaces as the call's error.
			err = failure("worker_failed")
			out.Result = nil
			s.failLocked(err)
		}
	}()
	raw, callErr := s.handler(ctx, method, owned, s.hostBridge(host))
	out.Result = raw
	out.HostCalls = int(s.hostCalls.Load())
	if callErr != nil {
		out.Result = nil
	}
	return out, callErr
}

func (s *inProcessSession) failLocked(err error) {
	s.stopRequested.Store(true)
	s.mu.Lock()
	if s.terminal == nil {
		s.terminal = err
	}
	if s.stopDone != nil {
		close(s.stopDone)
		s.stopDone = nil
	}
	s.mu.Unlock()
	s.rootCancel()
}

func (s *inProcessSession) Call(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	return s.CallWithOptions(ctx, method, payload, host, CallOptions{})
}

func (s *inProcessSession) CallWithOptions(ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (RunResult, error) {
	if ctx == nil {
		return RunResult{}, failure("invalid_invocation")
	}
	if err := s.validate(method, payload); err != nil {
		return RunResult{}, err
	}
	if options.CallerDeadlineOnly && options.Timeout != 0 {
		return RunResult{}, failure("invalid_limits")
	}
	if options.Timeout != 0 && (options.Timeout < time.Millisecond || options.Timeout > 30*time.Minute) {
		return RunResult{}, failure("invalid_limits")
	}
	callCtx := ctx
	if options.Timeout != 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	return s.invoke(callCtx, method, payload, host)
}

func (s *inProcessSession) StartCall(ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (*PendingCall, error) {
	return s.StartCallWithAdmission(ctx, ctx, method, payload, host, options)
}

func (s *inProcessSession) StartCallWithAdmission(admissionCtx, ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (*PendingCall, error) {
	if !s.SupportsConcurrent() {
		return nil, failure("concurrent_session_unsupported")
	}
	if admissionCtx == nil || ctx == nil {
		return nil, failure("invalid_invocation")
	}
	if err := admissionCtx.Err(); err != nil {
		return nil, failure("execution_canceled")
	}
	if err := s.validate(method, payload); err != nil {
		return nil, err
	}
	if options.CallerDeadlineOnly && options.Timeout != 0 {
		return nil, failure("invalid_limits")
	}
	callCtx, cancel := context.WithCancel(ctx)
	pending := &PendingCall{done: make(chan struct{}), cancel: cancel}
	go func() {
		pending.result, pending.err = s.invoke(callCtx, method, payload, host)
		cancel()
		close(pending.done)
	}()
	return pending, nil
}

func (s *inProcessSession) BeginShutdown(ctx context.Context) error {
	if ctx == nil {
		return failure("invalid_invocation")
	}
	if !s.SupportsConcurrent() {
		return failure("concurrent_session_unsupported")
	}
	s.mu.Lock()
	if s.stopDone != nil {
		done := s.stopDone
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.Lock()
			err := s.terminal
			s.mu.Unlock()
			return err
		case <-ctx.Done():
			return failure("execution_canceled")
		}
	}
	if s.stopped || s.terminal != nil {
		err := s.terminal
		s.mu.Unlock()
		if err == nil {
			err = failure("session_closed")
		}
		return err
	}
	s.draining = true
	s.stopRequested.Store(true)
	s.stopDone = make(chan struct{})
	drained := s.stopDone
	s.mu.Unlock()
	// Fence applied: ordinary admission is refused and the root context
	// cancels handler-side phases the way the worker's shutdown ACK did.
	go func() {
		s.inflight.Wait()
		s.mu.Lock()
		close(drained)
		s.mu.Unlock()
	}()
	return nil
}

func (s *inProcessSession) cleanupMethod(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if ctx == nil || method != "close" && method != "stop_service" {
		return RunResult{}, failure("invalid_invocation")
	}
	if !slices.Contains(s.manifest.Methods, method) {
		return RunResult{}, failure("invalid_invocation")
	}
	// The cleanup lane deliberately bypasses the drain fence: fresh caller
	// authority on a reserved lane, exactly like the wire client.
	return s.invoke(ctx, method, payload, host)
}

func (s *inProcessSession) CallCleanup(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if !s.SupportsConcurrent() {
		return RunResult{}, failure("concurrent_session_unsupported")
	}
	return s.cleanupMethod(ctx, method, payload, host)
}

func (s *inProcessSession) CallPrivateSchedulerStop(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if !s.SupportsPrivateSchedulerStop() {
		return RunResult{}, failure("plugin_private_scheduler_unsupported")
	}
	return s.cleanupMethod(ctx, method, payload, host)
}

func (s *inProcessSession) CallPublicServiceStop(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error) {
	if !s.SupportsPublicServiceStop() {
		return RunResult{}, failure("public_service_stop_unsupported")
	}
	return s.cleanupMethod(ctx, method, payload, host)
}

func (s *inProcessSession) CloseTransport(ctx context.Context) error {
	if ctx == nil {
		return failure("invalid_invocation")
	}
	if !s.SupportsTransportClose() {
		return failure("transport_close_unsupported")
	}
	// No transport exists to close; retire the object without running any
	// lifecycle method, mirroring the wire's transport-only fence.
	s.mu.Lock()
	if s.stopDone != nil {
		done := s.stopDone
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return failure("execution_canceled")
		}
	}
	if s.stopped {
		err := s.terminal
		s.mu.Unlock()
		return err
	}
	s.stopRequested.Store(true)
	s.stopped = true
	done := make(chan struct{})
	s.mu.Unlock()
	go func() {
		s.inflight.Wait()
		s.mu.Lock()
		s.rootCancel()
		close(done)
		s.mu.Unlock()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return failure("execution_canceled")
	}
}

func (s *inProcessSession) CleanupContext(parentHostCtx context.Context) (context.Context, error) {
	if !s.SupportsCleanupContinuations() {
		return nil, failure("cleanup_continuation_unsupported")
	}
	if parentHostCtx == nil || parentHostCtx.Err() != nil || !s.draining || s.stopped {
		return nil, failure("cleanup_call_closed")
	}
	return s.rootCtx, nil
}

func (s *inProcessSession) StartCleanupContinuationWithAdmission(parentHostCtx context.Context, method string, payload json.RawMessage, host HostHandler) (*PendingCall, error) {
	if !s.SupportsCleanupContinuations() {
		return nil, failure("cleanup_continuation_unsupported")
	}
	if parentHostCtx == nil || parentHostCtx.Err() != nil {
		return nil, failure("cleanup_call_closed")
	}
	if !cleanupChildMethod(method) || !slices.Contains(s.manifest.Methods, method) || len(payload) > MaxLogicalMessageBytes || len(payload) > 0 && !json.Valid(payload) {
		return nil, failure("invalid_invocation")
	}
	// The continuation inherits the draining root's lifetime; cancellation
	// revokes only this call, as the wire's bounded handoff slots did.
	callCtx, cancel := context.WithCancel(context.WithValue(s.rootCtx, cleanupInvocationKey{}, true))
	pending := &PendingCall{done: make(chan struct{}), cancel: cancel}
	go func() {
		pending.result, pending.err = s.invoke(callCtx, method, payload, host)
		cancel()
		close(pending.done)
	}()
	return pending, nil
}

func (s *inProcessSession) Stop(ctx context.Context) error {
	return s.StopWithHost(ctx, nil)
}

func (s *inProcessSession) StopWithHost(ctx context.Context, host HostHandler) error {
	if ctx == nil {
		return failure("invalid_invocation")
	}
	s.mu.Lock()
	if s.stopDone != nil {
		done := s.stopDone
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.Lock()
			err := s.terminal
			s.stopDone = nil
			s.mu.Unlock()
			return err
		case <-ctx.Done():
			s.mu.Unlock()
			return failure("execution_canceled")
		}
	}
	if s.stopped {
		err := s.terminal
		s.mu.Unlock()
		return err
	}
	s.stopped = true
	s.stopRequested.Store(true)
	done := make(chan struct{})
	s.terminal = nil
	s.mu.Unlock()
	// Final lifecycle call with fresh authority, then a single join.
	if slices.Contains(s.manifest.Methods, "stop") {
		stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, _ = s.invoke(stopCtx, "stop", json.RawMessage(`{}`), host)
		cancel()
	}
	go func() {
		s.inflight.Wait()
		s.mu.Lock()
		s.rootCancel()
		close(done)
		s.mu.Unlock()
	}()
	select {
	case <-done:
		s.mu.Lock()
		s.stopDone = nil
		err := s.terminal
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		s.mu.Lock()
		s.stopDone = done
		s.mu.Unlock()
		return failure("execution_canceled")
	}
}

func (s *inProcessSession) SupportsConcurrent() bool { return s != nil && s.concurrent }

func (s *inProcessSession) SupportsParallelHostCalls() bool { return s.concurrent }

func (s *inProcessSession) SupportsTransportClose() bool { return s.concurrent }

func (s *inProcessSession) SupportsPrivateSchedulerStop() bool {
	return s != nil && s.concurrent && s.manifest.ManagedPrivateScheduler
}

func (s *inProcessSession) SupportsPublicServiceStop() bool {
	return s != nil && s.concurrent && s.manifest.ManagedPublicServices && !cleanupDeclared(s.manifest)
}

func (s *inProcessSession) SupportsCleanupContinuations() bool {
	return s != nil && s.concurrent && cleanupDeclared(s.manifest)
}

var _ Session = (*inProcessSession)(nil)
var _ Session = (*Client)(nil)
