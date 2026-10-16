package legacy

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
)

// Invocation is private IPC input. It must not be persisted in public job
// payloads or returned from management APIs; Config can contain credentials.
type Invocation struct {
	Action string          `json:"action"`
	Config json.RawMessage `json:"config"`
	Input  json.RawMessage `json:"input,omitempty"`
}

type HostCaller func(context.Context, string, json.RawMessage) (json.RawMessage, error)
type Handler func(context.Context, string, json.RawMessage, HostCaller) (json.RawMessage, error)

// Serve runs one negotiated invocation. The host pins a plugin/version before
// releasing configuration. This SDK intentionally has no shell or HTTP helper:
// native implementations request their scoped host operations explicitly.
func Serve(ctx context.Context, input io.Reader, output io.Writer, manifest Manifest, handle Handler) error {
	if ctx == nil || input == nil || output == nil || handle == nil {
		return failure("invalid_worker")
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	s := wireSession{ctx: ctx, reader: bufio.NewReaderSize(input, 4096), writer: output, maxFrame: 1 << 20, maxOutput: 8 << 20}
	first, err := s.read()
	if err != nil {
		return err
	}
	if first.Protocol != ProtocolVersion || first.Type != "call" || first.Method != "handshake" || first.PluginID != manifest.PluginID || first.Version != manifest.Version || !identityPattern.MatchString(first.InstanceID) {
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
	helloResult, _ := json.Marshal(Handshake{Protocol: ProtocolVersion, PluginID: manifest.PluginID, Version: manifest.Version, HostAPI: HostAPIVersion, Methods: manifest.Methods, Capabilities: manifest.Capabilities})
	if err := s.write(Frame{Protocol: ProtocolVersion, ID: first.ID, Type: "result", Identity: s.identity, Result: helloResult}); err != nil {
		return err
	}
	invoked := false
	for {
		request, err := s.read()
		if err != nil {
			return err
		}
		if request.Protocol != ProtocolVersion || request.Identity != s.identity || request.Type != "call" || !slices.Contains(manifest.Methods, request.Method) || request.Method == "handshake" || (invoked && request.Method != "stop") {
			return failure("invalid_invocation")
		}
		// Never treat a bare stop as a completed business invocation.
		if !invoked && request.Method == "stop" {
			return failure("invalid_invocation")
		}
		callHost := func(callCtx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
			if callCtx == nil || callCtx.Err() != nil || ctx.Err() != nil || !methodPattern.MatchString(method) || (len(payload) > 0 && !json.Valid(payload)) {
				return nil, failure("invalid_host_call")
			}
			nonce := make([]byte, 16)
			if _, err := rand.Read(nonce); err != nil {
				return nil, failure("request_failed")
			}
			id := hex.EncodeToString(nonce)
			if err := s.write(Frame{Protocol: ProtocolVersion, ID: id, Type: "host_call", Identity: s.identity, Method: method, Payload: payload}); err != nil {
				return nil, err
			}
			reply, err := s.read()
			if err != nil {
				return nil, err
			}
			if reply.Protocol != ProtocolVersion || reply.Identity != s.identity || reply.ID != id || reply.Type != "host_result" {
				return nil, failure("identity_mismatch")
			}
			if reply.Error != nil {
				return nil, failure("host_operation_failed")
			}
			return reply.Result, nil
		}
		result, handleErr := handle(ctx, request.Method, request.Payload, callHost)
		reply := Frame{Protocol: ProtocolVersion, ID: request.ID, Type: "result", Identity: s.identity}
		if handleErr != nil {
			reply.Error = &ReplyError{Code: "OPERATION_FAILED"}
		} else {
			if len(result) == 0 {
				result = json.RawMessage("null")
			}
			reply.Result = result
		}
		if err := s.write(reply); err != nil {
			return err
		}
		if handleErr != nil {
			return failure("plugin_operation_failed")
		}
		if request.Method == "stop" {
			return nil
		}
		invoked = true
	}
}
