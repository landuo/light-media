package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestMessageInboundBytesValuesAndFiniteAcknowledgement(t *testing.T) {
	values := MessageValues{"source": {"first", "last"}, "empty": {""}}
	if last, ok := values.Get("source"); !ok || last != "last" {
		t.Fatal("source MultiDict last value lost")
	}
	if empty, ok := values.Get("empty"); !ok || empty != "" {
		t.Fatal("empty value confused with missing")
	}
	body := []byte{0xff, 0, 'x'}
	envelope, _ := ModuleResultBytes(body)
	raw, _ := json.Marshal(map[string]any{"args": []any{}, "kwargs": map[string]any{"source": "last", "body": envelope, "form": values, "args": values}})
	input, err := DecodeMessageParserInput(raw)
	if err != nil || !bytes.Equal(input.Body, body) || input.Source == nil || *input.Source != "last" {
		t.Fatal("exact source bytes lost", err)
	}
	for _, reply := range []string{`null`, `{}`, `{"schema_version":1}`, `{"schema_version":1,"accepted":false}`, `{"schema_version":2,"accepted":true}`} {
		_, err := (Messages{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(reply), nil
		}}).Receive(context.Background(), input)
		if err == nil {
			t.Fatal("non-admission response treated as accepted", reply)
		}
	}
	called := false
	_, err = (Messages{Host: func(_ context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
		called = true
		if method != MessageReceiveCapability {
			t.Fatal("arbitrary message RPC")
		}
		return json.RawMessage(`{"schema_version":1,"accepted":true}`), nil
	}}).Receive(context.Background(), input)
	if err != nil || !called {
		t.Fatal("finite host admission failed", err)
	}
}
