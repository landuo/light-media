package plugins

import (
	"context"
	"encoding/json"
	"testing"
)

// Serialization-only checks. These never stand in for real App/SDK permission
// or persistence acceptance, which must use the owned executable fixture.
func TestBaseWrapperPreservesTargetAndNone(t *testing.T) {
	calls := 0
	b := Base{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		switch method {
		case BaseGetConfig:
			if _, ok := payload["plugin_id"]; ok {
				t.Fatal("default target invented by SDK")
			}
			return json.RawMessage(`null`), nil
		case BaseUpdateConfig:
			if string(payload["plugin_id"]) != `"ExplicitOther"` || string(payload["config"]) != `{}` {
				t.Fatalf("explicit target/config lost %s", raw)
			}
			return json.RawMessage(`null`), nil
		case BaseDelData:
			if string(payload["key"]) != `null` {
				t.Fatalf("null key lost %s", raw)
			}
			return json.RawMessage(`null`), nil
		default:
			t.Fatalf("unexpected method %s", method)
		}
		return nil, nil
	}}
	ctx := context.Background()
	got, err := b.GetConfig(ctx)
	if err != nil || string(got) != "null" {
		t.Fatal(string(got), err)
	}
	changed, err := b.UpdateConfig(ctx, map[string]any{}, "ExplicitOther")
	if err != nil || changed != nil {
		t.Fatal(changed, err)
	}
	if err = b.DelData(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	if _, err = b.GetConfig(ctx, "a", "b"); err == nil || calls != 3 {
		t.Fatal("multiple targets accepted")
	}
}
func TestBaseMethodVocabularyIsBounded(t *testing.T) {
	for _, method := range BaseMethods() {
		if !IsBaseMethod(method) {
			t.Fatal(method)
		}
	}
	for _, method := range []string{"config.patch", "plugin.base.post_message", "plugin.base.unknown", "transport.chunk"} {
		if IsBaseMethod(method) {
			t.Fatalf("unknown/native method admitted %s", method)
		}
	}
}
