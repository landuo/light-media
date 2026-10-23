package plugins

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Only request/response JSON-RPC is supported. Notifications cannot prove that
// a state-changing operation was accepted, so every call requires an id.
type rpcMessage struct {
	Version string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type rpcValue struct {
	Protocol int `json:"protocol"`
	Identity
	Kind    string          `json:"kind,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Code    string          `json:"code,omitempty"`
}

func strictJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return failure("invalid_frame")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return failure("invalid_frame")
	}
	return nil
}

func (f Frame) MarshalJSON() ([]byte, error) {
	if f.ID == "" || len(f.ID) > 128 {
		return nil, failure("invalid_frame")
	}
	message := rpcMessage{Version: "2.0", ID: f.ID}
	value := rpcValue{Protocol: f.Protocol, Identity: f.Identity}
	switch f.Type {
	case "call", "host_call":
		if !methodPattern.MatchString(f.Method) || f.Result != nil || f.Error != nil {
			return nil, failure("invalid_frame")
		}
		prefix := "plugin."
		if f.Type == "host_call" {
			prefix = "host."
		}
		message.Method, value.Payload = prefix+f.Method, f.Payload
		return marshalRPCValueEnvelope(message.ID, message.Method, value)
	case "result", "host_result":
		if f.Method != "" || f.Payload != nil || (f.Error != nil && f.Result != nil) {
			return nil, failure("invalid_frame")
		}
		value.Kind = f.Type
		if f.Error != nil {
			value.Code = f.Error.Code
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, failure("invalid_frame")
			}
			message.Error = &rpcError{Code: -32000, Message: "plugin operation failed", Data: raw}
		} else {
			if !json.Valid(f.Result) {
				return nil, failure("invalid_frame")
			}
			value.Payload = f.Result
			return marshalRPCValueEnvelope(message.ID, "", value)
		}
	default:
		return nil, failure("invalid_frame")
	}
	return json.Marshal(message)
}

// marshalRPCValueEnvelope accepts a typed value, never prevalidated raw bytes.
// The standard encoder owns validation, compaction and escaping of all payloads.
func marshalRPCValueEnvelope(id, method string, value rpcValue) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, failure("invalid_frame")
	}
	// Adding the envelope can cross encoding/json's nesting limit even when
	// encoding the value alone succeeded. Count a conservative upper bound,
	// including braces in strings, and retain the original encoder near that
	// boundary. This is not a JSON parser; false positives only use the fallback.
	// The 10000 bound is encoding/json/scanner.go maxNestingDepth in our Go
	// toolchain; TestJSONRPCEnvelopeStandardDepthBoundary locks that assumption.
	if bytes.Count(raw, []byte("{"))+bytes.Count(raw, []byte("[")) >= 10000 {
		message := rpcMessage{Version: "2.0", ID: id, Method: method}
		if method != "" {
			message.Params = raw
		} else {
			message.Result = raw
		}
		return json.Marshal(message)
	}
	metadata := struct {
		Version string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method,omitempty"`
	}{"2.0", id, method}
	header, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	field := `,"result":`
	if method != "" {
		field = `,"params":`
	}
	out := make([]byte, 0, len(header)+len(field)+len(raw))
	out = append(out, header[:len(header)-1]...)
	out = append(out, field...)
	out = append(out, raw...)
	out = append(out, '}')
	return out, nil
}

func (f *Frame) UnmarshalJSON(raw []byte) error {
	var message rpcMessage
	if strictJSON(raw, &message) != nil || message.Version != "2.0" || message.ID == "" || len(message.ID) > 128 {
		return failure("invalid_frame")
	}
	var value rpcValue
	decoded := Frame{ID: message.ID}
	if message.Method != "" {
		if message.Result != nil || message.Error != nil || strictJSON(message.Params, &value) != nil || value.Kind != "" || value.Code != "" {
			return failure("invalid_frame")
		}
		switch {
		case strings.HasPrefix(message.Method, "plugin."):
			decoded.Type, decoded.Method = "call", strings.TrimPrefix(message.Method, "plugin.")
		case strings.HasPrefix(message.Method, "host."):
			decoded.Type, decoded.Method = "host_call", strings.TrimPrefix(message.Method, "host.")
		default:
			return failure("invalid_frame")
		}
		if !methodPattern.MatchString(decoded.Method) {
			return failure("invalid_frame")
		}
		decoded.Payload = value.Payload
	} else {
		if message.Params != nil || (message.Result == nil) == (message.Error == nil) {
			return failure("invalid_frame")
		}
		if message.Error != nil {
			if message.Error.Code != -32000 || message.Error.Message != "plugin operation failed" || strictJSON(message.Error.Data, &value) != nil || value.Payload != nil || !identityPattern.MatchString(value.Code) {
				return failure("invalid_frame")
			}
			decoded.Error = &ReplyError{Code: value.Code}
		} else {
			if strictJSON(message.Result, &value) != nil || !json.Valid(value.Payload) || value.Code != "" {
				return failure("invalid_frame")
			}
			decoded.Result = value.Payload
		}
		if value.Kind != "result" && value.Kind != "host_result" {
			return failure("invalid_frame")
		}
		decoded.Type = value.Kind
	}
	decoded.Protocol, decoded.Identity = value.Protocol, value.Identity
	*f = decoded
	return nil
}
