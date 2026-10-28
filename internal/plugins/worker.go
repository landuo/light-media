package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync/atomic"
)

type Invocation struct {
	Action string          `json:"action"`
	Config json.RawMessage `json:"config"`
	Input  json.RawMessage `json:"input,omitempty"`
}
type HostCaller func(context.Context, string, json.RawMessage) (json.RawMessage, error)
type Handler func(context.Context, string, json.RawMessage, HostCaller) (json.RawMessage, error)
type callbackPhase struct{ closed atomic.Bool }

func Serve(ctx context.Context, input io.Reader, output io.Writer, manifest Manifest, handle Handler) error {
	return serve(ctx, input, output, manifest, handle, false)
}

// ServeConcurrent opts this one persistent object into overlapping handlers.
// The implementation must synchronize its own mutable object state. Older
// hosts still use the original sequential path unless negotiation succeeds.
func ServeConcurrent(ctx context.Context, input io.Reader, output io.Writer, manifest Manifest, handle Handler) error {
	return serve(ctx, input, output, manifest, handle, true)
}

func serve(ctx context.Context, input io.Reader, output io.Writer, manifest Manifest, handle Handler, concurrent bool) error {
	if ctx == nil || input == nil || output == nil || handle == nil {
		return failure("invalid_worker")
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	s := wireSession{ctx: ctx, reader: bufio.NewReaderSize(input, 4096), writer: output, maxFrame: 1 << 20, maxInput: 8 << 20, maxOutput: 8 << 20, role: "worker"}
	s.closeIO = func() {
		if c, ok := input.(io.Closer); ok {
			c.Close()
		}
		if c, ok := output.(io.Closer); ok {
			c.Close()
		}
	}
	stopIO := context.AfterFunc(ctx, s.abort)
	defer stopIO()
	first, err := s.read()
	if err != nil {
		return err
	}
	if first.Protocol != ProtocolVersion || first.Type != "call" || first.Method != "handshake" || first.PluginID != manifest.PluginID || first.Version != manifest.Version || !ValidInstanceID(first.InstanceID) {
		return failure("handshake_mismatch")
	}
	var hello struct {
		Protocol int `json:"protocol"`
		HostAPI  int `json:"host_api"`
	}
	if strictJSON(first.Payload, &hello) != nil || hello.Protocol != ProtocolVersion || hello.HostAPI != HostAPIVersion {
		return failure("handshake_mismatch")
	}
	s.identity = first.Identity
	advertisedConcurrent := 0
	if concurrent {
		advertisedConcurrent = 1
	}
	helloResult, _ := json.Marshal(Handshake{TransportClose: advertisedConcurrent, ConcurrentSession: advertisedConcurrent, Protocol: ProtocolVersion, PluginID: manifest.PluginID, Version: manifest.Version, HostAPI: HostAPIVersion, Methods: manifest.Methods, Capabilities: manifest.Capabilities, Transport: &transportAdvertisement{Version: 1}, PersistentSession: 1})
	if err = s.write(Frame{Protocol: ProtocolVersion, ID: first.ID, Type: "result", Identity: s.identity, Result: helloResult}); err != nil {
		return err
	}
	gate := make(chan struct{}, 1)
	invoked := false
	persistent := false
	for {
		request, err := s.read()
		if err != nil {
			return err
		}
		if request.Protocol != ProtocolVersion || request.Identity != s.identity || request.Type != "call" {
			return failure("invalid_invocation")
		}
		if request.Method == "transport.negotiate" {
			var limits transportLimits
			if invoked || s.enabled || strictJSON(request.Payload, &limits) != nil || limits.valid() != nil {
				return failure("transport_mismatch")
			}
			raw, _ := json.Marshal(limits)
			if err = s.writePhysical(Frame{Protocol: ProtocolVersion, ID: request.ID, Type: "result", Identity: s.identity, Result: raw}); err != nil {
				return err
			}
			s.enable(limits)
			continue
		}
		if request.Method == "transport.session" {
			var sessionVersion struct {
				Version int `json:"version"`
			}
			if invoked || persistent || !s.enabled || strictJSON(request.Payload, &sessionVersion) != nil || sessionVersion.Version != 1 {
				return failure("session_mismatch")
			}
			if err = s.write(Frame{Protocol: ProtocolVersion, ID: request.ID, Type: "result", Identity: s.identity, Result: json.RawMessage(`{"version":1}`)}); err != nil {
				return err
			}
			persistent = true
			s.resetEpoch()
			continue
		}
		if request.Method == "transport.concurrent" {
			var proposal concurrentProposal
			if !concurrent || !persistent || invoked || strictJSON(request.Payload, &proposal) != nil || !proposal.valid() {
				return failure("concurrent_session_mismatch")
			}
			raw, _ := json.Marshal(proposal)
			if err = s.write(Frame{Protocol: ProtocolVersion, ID: request.ID, Type: "result", Identity: s.identity, Result: raw}); err != nil {
				return err
			}
			return serveConcurrentSession(&s, manifest, handle, proposal)
		}
		if !slices.Contains(manifest.Methods, request.Method) || request.Method == "handshake" || strings.HasPrefix(request.Method, "transport.") || !persistent && (invoked && request.Method != "stop" || !invoked && request.Method == "stop") {
			return failure("invalid_invocation")
		}
		phase := &callbackPhase{}
		host := func(callCtx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
			if callCtx == nil || callCtx.Err() != nil || ctx.Err() != nil || phase.closed.Load() || !methodPattern.MatchString(method) || strings.HasPrefix(method, "transport.") || len(payload) > 0 && !json.Valid(payload) {
				return nil, failure("invalid_host_call")
			}
			select {
			case gate <- struct{}{}:
			case <-callCtx.Done():
				return nil, failure("execution_canceled")
			case <-ctx.Done():
				return nil, failure("execution_canceled")
			}
			defer func() { <-gate }()
			if phase.closed.Load() {
				return nil, failure("invalid_host_call")
			}
			if callCtx.Err() != nil || s.aborted.Load() {
				return nil, failure("execution_canceled")
			}
			cancelIO := context.AfterFunc(callCtx, s.abort)
			defer cancelIO()
			id, err := randomID()
			if err != nil {
				return nil, err
			}
			if persistent {
				id = request.ID + "." + id
			}
			if err = s.write(Frame{Protocol: ProtocolVersion, ID: id, Type: "host_call", Identity: s.identity, Method: method, Payload: payload}); err != nil {
				s.abort()
				return nil, err
			}
			reply, err := s.read()
			if err != nil {
				s.abort()
				return nil, err
			}
			if reply.Protocol != ProtocolVersion || reply.Identity != s.identity || reply.ID != id || reply.Type != "host_result" || reply.Method != "" || reply.Payload != nil {
				s.abort()
				return nil, failure("identity_mismatch")
			}
			if callCtx.Err() != nil || s.aborted.Load() {
				s.abort()
				return nil, failure("execution_canceled")
			}
			if reply.Error != nil {
				return nil, failure("host_operation_failed")
			}
			return reply.Result, nil
		}
		result, handleErr := handle(ctx, request.Method, request.Payload, host)
		phase.closed.Store(true)
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return failure("execution_canceled")
		}
		reply := Frame{Protocol: ProtocolVersion, ID: request.ID, Type: "result", Identity: s.identity}
		if handleErr != nil {
			reply.Error = &ReplyError{Code: workerHandlerFailureCode(handleErr)}
		} else {
			if len(result) == 0 {
				result = json.RawMessage("null")
			}
			reply.Result = result
		}
		err = s.writeWorkerReply(reply)
		<-gate
		if err != nil {
			return err
		}
		if handleErr != nil && (!persistent || request.Method == "stop") {
			return failure("plugin_operation_failed")
		}
		if request.Method == "stop" {
			return nil
		}
		invoked = true
		if persistent {
			s.resetEpoch()
		}
	}
}
