package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
)

type concurrentWorkerCall struct {
	cleanup bool
	request Frame
	ctx     context.Context
	cancel  context.CancelFunc
	phase   callbackPhase
	account concurrentAccount
	gate    chan struct{}
	mu      sync.Mutex
	pending map[string]chan Frame
}

type concurrentWorker struct {
	transport     *concurrentTransport
	ctx           context.Context
	cancel        context.CancelFunc
	manifest      Manifest
	handle        Handler
	proposal      concurrentProposal
	mu            sync.Mutex
	calls         map[string]*concurrentWorkerCall
	stopping      bool
	retiring      bool
	draining      bool
	cleanupActive bool
	handlers      sync.WaitGroup
	done          chan error
}

func serveConcurrentSession(s *wireSession, manifest Manifest, handle Handler, proposal concurrentProposal) error {
	ctx, cancel := context.WithCancel(s.ctx)
	w := &concurrentWorker{transport: &concurrentTransport{wire: s, ctx: ctx}, ctx: ctx, cancel: cancel, manifest: manifest, handle: handle, proposal: proposal, calls: map[string]*concurrentWorkerCall{}, done: make(chan error, 1)}
	readerDone := make(chan struct{})
	go func() { defer close(readerDone); w.readLoop() }()
	select {
	case err := <-w.done:
		w.close()
		s.abort()
		<-readerDone
		w.handlers.Wait()
		return err
	case <-ctx.Done():
		w.close()
		s.abort()
		<-readerDone
		w.handlers.Wait()
		return failure("execution_canceled")
	}
}
func (w *concurrentWorker) finish(err error) {
	select {
	case w.done <- err:
	default:
	}
}
func (w *concurrentWorker) close() {
	w.cancel()
	w.mu.Lock()
	w.stopping = true
	for _, call := range w.calls {
		call.phase.closed.Store(true)
		call.cancel()
	}
	w.mu.Unlock()
}

func (w *concurrentWorker) readLoop() {
	for {
		request, count, err := w.transport.read()
		if err != nil {
			w.mu.Lock()
			stopping, retiring := w.stopping, w.retiring
			w.mu.Unlock()
			if retiring && errors.Is(err, io.EOF) {
				w.finish(nil)
			} else if !stopping {
				w.finish(err)
			}
			return
		}
		if request.Type == "host_result" {
			w.mu.Lock()
			call := w.calls[concurrentRoot(request.ID)]
			w.mu.Unlock()
			// Late replies after cancellation have no receiver or authority.
			if call == nil {
				continue
			}
			if err = call.account.receive(count); err != nil {
				w.finish(err)
				return
			}
			call.mu.Lock()
			pending := call.pending[request.ID]
			delete(call.pending, request.ID)
			call.mu.Unlock()
			if pending == nil {
				if call.phase.closed.Load() || call.ctx.Err() != nil {
					continue
				}
				w.finish(failure("invalid_reply"))
				return
			}
			pending <- request
			continue
		}
		if request.Type != "call" {
			w.finish(failure("invalid_invocation"))
			return
		}
		if request.Method == "transport.cancel" {
			if request.Payload == nil || string(request.Payload) != "null" || strings.Contains(request.ID, ".") {
				w.finish(failure("invalid_invocation"))
				return
			}
			w.mu.Lock()
			call := w.calls[request.ID]
			w.mu.Unlock()
			if call != nil {
				call.phase.closed.Store(true)
				call.cancel()
			}
			continue
		}
		if request.Method == "transport.retire" {
			var version struct {
				Version int `json:"version"`
			}
			if !identityPattern.MatchString(request.ID) || strings.Contains(request.ID, ".") || strictJSON(request.Payload, &version) != nil || version.Version != 1 {
				w.finish(failure("invalid_invocation"))
				return
			}
			w.mu.Lock()
			if w.stopping || w.retiring || w.calls[request.ID] != nil {
				w.mu.Unlock()
				w.finish(failure("invalid_invocation"))
				return
			}
			w.retiring = true
			w.draining = true
			for _, old := range w.calls {
				old.phase.closed.Store(true)
				old.cancel()
			}
			w.mu.Unlock()
			// Only this explicit transport-only fence authorizes normal EOF.
			// No plugin lifecycle Handler is invoked; EOF cancels and joins.
			if _, err = w.transport.write(Frame{Protocol: ProtocolVersion, ID: request.ID, Type: "result", Identity: w.transport.wire.identity, Result: json.RawMessage(`{"version":1}`)}, nil); err != nil {
				w.finish(err)
				return
			}
			continue
		}
		if request.Method == "transport.shutdown" {
			var version struct {
				Version int `json:"version"`
			}
			if !identityPattern.MatchString(request.ID) || strings.Contains(request.ID, ".") || strictJSON(request.Payload, &version) != nil || version.Version != 1 {
				w.finish(failure("invalid_invocation"))
				return
			}
			w.mu.Lock()
			if w.stopping || w.draining || w.calls[request.ID] != nil {
				w.mu.Unlock()
				w.finish(failure("invalid_invocation"))
				return
			}
			w.draining = true
			for _, old := range w.calls {
				old.phase.closed.Store(true)
				old.cancel()
			}
			w.mu.Unlock()
			// This is a revocation/admission fence ACK, not a claim that the
			// old handlers have joined. Cleanup may be what releases them.
			if _, err = w.transport.write(Frame{Protocol: ProtocolVersion, ID: request.ID, Type: "result", Identity: w.transport.wire.identity, Result: json.RawMessage(`{"version":1}`)}, nil); err != nil {
				w.finish(err)
				return
			}
			continue
		}
		if !identityPattern.MatchString(request.ID) || strings.Contains(request.ID, ".") || !slices.Contains(w.manifest.Methods, request.Method) || request.Method == "handshake" || strings.HasPrefix(request.Method, "transport.") {
			w.finish(failure("invalid_invocation"))
			return
		}
		ctx, cancel := context.WithCancel(w.ctx)
		call := &concurrentWorkerCall{request: request, ctx: ctx, cancel: cancel, account: concurrentAccount{limits: w.transport.wire.limits, worker: true}, gate: make(chan struct{}, 1), pending: map[string]chan Frame{}}
		if err = call.account.receive(count); err != nil {
			cancel()
			w.finish(err)
			return
		}
		w.mu.Lock()
		cleanup := w.draining && (request.Method == "close" || request.Method == "stop_service")
		if w.stopping || w.retiring || w.calls[request.ID] != nil || w.draining && !cleanup && request.Method != "stop" || cleanup && w.cleanupActive || len(w.calls) >= w.proposal.MaxCalls && request.Method != "stop" && !cleanup {
			w.mu.Unlock()
			cancel()
			w.finish(failure("concurrent_call_limit"))
			return
		}
		w.calls[request.ID] = call
		if cleanup {
			call.cleanup = true
			w.cleanupActive = true
		}
		if request.Method == "stop" {
			w.stopping = true
			for id, other := range w.calls {
				if id != request.ID {
					other.phase.closed.Store(true)
					other.cancel()
				}
			}
			w.mu.Unlock()
			// A reserved cleanup phase starts only after all ordinary handlers
			// have joined. The process host owns the bounded kill/reap fallback.
			go func() { w.handlers.Wait(); w.invoke(call); w.finish(nil) }()
			continue
		}
		w.handlers.Add(1)
		w.mu.Unlock()
		go func() { defer w.handlers.Done(); w.invoke(call) }()
	}
}

func (w *concurrentWorker) invoke(call *concurrentWorkerCall) {
	host := func(ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
		return w.host(call, ctx, method, payload)
	}
	result, err := w.handle(call.ctx, call.request.Method, call.request.Payload, host)
	call.phase.closed.Store(true)
	canceled := call.ctx.Err() != nil
	call.cancel()
	// A Handler may have launched a callback goroutine. Revoke first and join
	// that callback before sending its result, so it cannot become a new phase.
	call.gate <- struct{}{}
	reply := Frame{Protocol: ProtocolVersion, ID: call.request.ID, Type: "result", Identity: w.transport.wire.identity}
	if canceled {
		reply.Error = &ReplyError{Code: "EXECUTION_CANCELED"}
	} else if err != nil {
		reply.Error = &ReplyError{Code: workerHandlerFailureCode(err)}
	} else {
		if len(result) == 0 {
			result = json.RawMessage(`null`)
		}
		reply.Result = result
	}
	// Release the completed handler's admission slot before publishing its
	// result. The host may immediately send the next reserved cleanup call.
	w.mu.Lock()
	delete(w.calls, call.request.ID)
	if call.cleanup {
		w.cleanupActive = false
	}
	w.mu.Unlock()
	sent, sendErr := w.transport.write(reply, call.account.send)
	if code := concurrentReplyError(sendErr); sendErr != nil && sent.bytes == 0 && code != "" {
		reply.Result = nil
		reply.Error = &ReplyError{Code: code}
		_, sendErr = w.transport.write(reply, nil)
	}
	<-call.gate
	if sendErr != nil {
		w.finish(sendErr)
	}
}

func (w *concurrentWorker) host(call *concurrentWorkerCall, ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
	if ctx == nil || ctx.Err() != nil || call.phase.closed.Load() || call.ctx.Err() != nil || !methodPattern.MatchString(method) || strings.HasPrefix(method, "transport.") || len(payload) > 0 && !json.Valid(payload) {
		return nil, failure("invalid_host_call")
	}
	select {
	case call.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, failure("execution_canceled")
	case <-call.ctx.Done():
		return nil, failure("execution_canceled")
	}
	defer func() { <-call.gate }()
	if call.phase.closed.Load() || call.ctx.Err() != nil {
		return nil, failure("invalid_host_call")
	}
	if err := call.account.host(w.proposal.MaxHostCalls); err != nil {
		return nil, err
	}
	suffix, err := randomID()
	if err != nil {
		return nil, err
	}
	id := call.request.ID + "." + suffix
	pending := make(chan Frame, 1)
	call.mu.Lock()
	call.pending[id] = pending
	call.mu.Unlock()
	defer func() { call.mu.Lock(); delete(call.pending, id); call.mu.Unlock() }()
	_, err = w.transport.write(Frame{Protocol: ProtocolVersion, ID: id, Type: "host_call", Identity: w.transport.wire.identity, Method: method, Payload: payload}, func(n concurrentWireCount) error {
		if call.phase.closed.Load() || call.ctx.Err() != nil || ctx.Err() != nil {
			return failure("execution_canceled")
		}
		return call.account.send(n)
	})
	if err != nil {
		return nil, err
	}
	select {
	case reply := <-pending:
		if call.phase.closed.Load() || call.ctx.Err() != nil || ctx.Err() != nil {
			return nil, failure("execution_canceled")
		}
		if reply.Error != nil {
			return nil, failure("host_operation_failed")
		}
		return reply.Result, nil
	case <-ctx.Done():
		call.phase.closed.Store(true)
		call.cancel()
		// A callback deadline revokes this invocation, not the process or an
		// unrelated invocation. The host must cancel its in-flight authority.
		_, e := w.transport.write(Frame{Protocol: ProtocolVersion, ID: call.request.ID, Type: "host_call", Identity: w.transport.wire.identity, Method: "transport.cancel", Payload: json.RawMessage(`null`)}, nil)
		if e != nil {
			w.finish(e)
		}
		return nil, failure("execution_canceled")
	case <-call.ctx.Done():
		return nil, failure("execution_canceled")
	}
}
