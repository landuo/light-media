package plugins

import (
	"context"
	"encoding/json"
)

func (m *concurrentClient) callMaterial(call *concurrentHostCall, frame Frame, count concurrentWireCount) {
	defer m.hosts.Done()
	defer call.hosts.Done()
	reply, err := call.material.handle(call.ctx, frame)
	if chargeErr := call.account.materialChargeRead(count.bytes, err == nil); chargeErr != nil {
		err = chargeErr
	}
	if err == nil {
		err = call.material.check(call.ctx)
	}
	// Release callback admission before publishing the reply: a peer can send
	// its next page as soon as it reads the bytes, before Write returns here.
	call.mu.Lock()
	call.inHost = false
	closed := call.closed || call.ctx.Err() != nil
	call.mu.Unlock()
	if err == nil && !closed {
		err = m.transport.materialWrite(call.ctx, reply, &call.account)
	}
	// Per-invocation cancellation revokes this scope, not another invocation.
	// Invalid frames/IO remain transport failures. No source error is fabricated.
	if err != nil && !closed {
		m.abort(err)
	}
}

type materialPendingReply struct {
	request Frame
	reply   chan concurrentOutcome
}

func (w *concurrentWorker) materialContext(call *concurrentWorkerCall) context.Context {
	if !w.transport.wire.materialEnabled {
		return call.ctx
	}
	bridge := materialPeerBridge(func(ctx context.Context, grant MaterialGrant, operation string, offset int64, data []byte) (materialResponse, error) {
		if ctx == nil || ctx.Err() != nil || call.ctx.Err() != nil || call.phase.closed.Load() {
			return materialResponse{}, failure("execution_canceled")
		}
		select {
		case call.gate <- struct{}{}:
		case <-ctx.Done():
			return materialResponse{}, failure("execution_canceled")
		case <-call.ctx.Done():
			return materialResponse{}, failure("execution_canceled")
		}
		defer func() { <-call.gate }()
		if call.ctx.Err() != nil || call.phase.closed.Load() || ctx.Err() != nil {
			return materialResponse{}, failure("execution_canceled")
		}
		request, err := materialPeerRequest(w.transport.wire.identity, call.request.ID, grant, operation, offset, data)
		if err != nil {
			return materialResponse{}, err
		}
		pending := &materialPendingReply{request: request, reply: make(chan concurrentOutcome, 1)}
		call.mu.Lock()
		if call.materialPending[request.ID] != nil {
			call.mu.Unlock()
			return materialResponse{}, failure("invalid_material_sequence")
		}
		call.materialPending[request.ID] = pending
		call.mu.Unlock()
		defer func() { call.mu.Lock(); delete(call.materialPending, request.ID); call.mu.Unlock() }()
		if err = w.transport.materialWrite(ctx, request, &call.account); err != nil {
			w.finish(err)
			return materialResponse{}, err
		}
		select {
		case outcome := <-pending.reply:
			if ctx.Err() != nil || call.ctx.Err() != nil || call.phase.closed.Load() {
				return materialResponse{}, failure("execution_canceled")
			}
			if outcome.err != nil {
				return materialResponse{}, outcome.err
			}
			return materialAcceptReply(request, outcome.frame)
		case <-ctx.Done():
			call.phase.closed.Store(true)
			call.cancel()
			if _, err = w.transport.write(Frame{Protocol: ProtocolVersion, ID: call.request.ID, Type: "host_call", Identity: w.transport.wire.identity, Method: "transport.cancel", Payload: json.RawMessage(`null`)}, nil); err != nil {
				w.finish(err)
			}
			return materialResponse{}, failure("execution_canceled")
		case <-call.ctx.Done():
			return materialResponse{}, failure("execution_canceled")
		}
	})
	return context.WithValue(call.ctx, materialPeerKey{}, bridge)
}
