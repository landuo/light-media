package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const MessageReceiveCapability = "message.receive"

// MessageValues retains repeated form/query values. Starlette's get()/dict()
// select the last value; Go's url.Values.Get selects the first instead.
type MessageValues map[string][]string

func (v MessageValues) Get(name string) (string, bool) {
	values, exists := v[name]
	if !exists || len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}

// ReceiveMessageRequestV1 is a channel's ordinary inbound request, before its
// message_parser module. It cannot select a module, event, command, or method.
// Body is exact bytes, including empty/non-UTF8 bodies. Source corresponds to
// args.get("source"); nil and empty are distinct. Form/Args carry no host token.
type ReceiveMessageRequestV1 struct {
	Source *string       `json:"source"`
	Body   []byte        `json:"body"`
	Form   MessageValues `json:"form"`
	Args   MessageValues `json:"args"`
}

type ReceiveMessageResultV1 struct {
	SchemaVersion int  `json:"schema_version"`
	Accepted      bool `json:"accepted"`
}

type Messages struct{ Host HostCaller }

// Receive acknowledges background admission, as POST /api/v1/message does.
// It does not claim parsing or command execution has completed. Background
// failures enter ordinary host logs; accepted requests are not durable jobs.
func (m Messages) Receive(ctx context.Context, input ReceiveMessageRequestV1) (ReceiveMessageResultV1, error) {
	var out ReceiveMessageResultV1
	err := hostBusinessCall(ctx, m.Host, MessageReceiveCapability, input, &out)
	if err == nil && (out.SchemaVersion != 1 || !out.Accepted) {
		err = errors.New("invalid host incoming message response")
	}
	return out, err
}

// DecodeMessageParserInput decodes the fixed module dispatch frame. Body uses
// the existing reserved bytes envelope rather than confusing bytes with a
// JSON string/object. Normal plugin parsers retain all form/query values.
func DecodeMessageParserInput(raw json.RawMessage) (ReceiveMessageRequestV1, error) {
	var call struct {
		Args   []json.RawMessage `json:"args"`
		Kwargs struct {
			Source *string         `json:"source"`
			Body   json.RawMessage `json:"body"`
			Form   MessageValues   `json:"form"`
			Args   MessageValues   `json:"args"`
		} `json:"kwargs"`
	}
	if err := json.Unmarshal(raw, &call); err != nil || len(call.Args) != 0 {
		return ReceiveMessageRequestV1{}, errors.New("invalid message parser call")
	}
	body, ok := DecodeModuleResultBytes(call.Kwargs.Body)
	if !ok {
		return ReceiveMessageRequestV1{}, errors.New("message parser body is not bytes")
	}
	return ReceiveMessageRequestV1{Source: call.Kwargs.Source, Body: body, Form: call.Kwargs.Form, Args: call.Kwargs.Args}, nil
}
