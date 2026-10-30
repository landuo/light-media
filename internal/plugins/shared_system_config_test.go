package plugins

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSharedSystemConfigTypedRawOrderAndNullableResult(t *testing.T) {
	ctx := context.Background()
	calls := 0
	s := SharedSystemConfig{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		switch method {
		case SharedSystemConfigSet:
			var in SharedSystemConfigSetV1
			if e := json.Unmarshal(raw, &in); e != nil {
				t.Fatal(e)
			}
			if in.Key != SystemConfigCustomIdentifiers {
				t.Fatal(in.Key)
			}
			if string(in.Value) != `{"z":1,"a":2}` {
				t.Fatalf("raw order lost %s", in.Value)
			}
			return json.RawMessage(`null`), nil
		case SharedSystemConfigGet:
			return json.RawMessage(`{"z":1,"a":2}`), nil
		}
		t.Fatal(method)
		return nil, nil
	}}
	result, e := s.Set(ctx, SystemConfigCustomIdentifiers, json.RawMessage(`{"z":1,"a":2}`))
	if e != nil || result != nil {
		t.Fatal(result, e)
	}
	raw, e := s.Get(ctx, SystemConfigCustomIdentifiers)
	if e != nil || string(raw) != `{"z":1,"a":2}` || calls != 2 {
		t.Fatal(string(raw), e, calls)
	}
}
func TestSharedSystemConfigRequiresActiveCaller(t *testing.T) {
	s := SharedSystemConfig{}
	if _, e := s.Get(context.Background(), SystemConfigCustomIdentifiers); e == nil {
		t.Fatal("nil caller accepted")
	}
	if _, e := s.Set(context.Background(), SystemConfigCustomIdentifiers, nil); e == nil {
		t.Fatal("nil caller accepted")
	}
}
