package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Serialization/error checks only; actual admission and clone identity are
// verified by the host's compiled SDK fixture test.
func TestBaseInstanceMetadataWrapperKeepsRealAndDisplayVersionsSeparate(t *testing.T) {
	calls := 0
	b := Base{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != BaseGetInstanceMetadata || string(raw) != "{}" {
			t.Fatalf("unexpected metadata request: %s %s", method, raw)
		}
		return json.RawMessage(`{"plugin_id":"Demo","plugin_version":"1.0","instance_id":"Demob","source_instance_id":"Demo","name":"Second","display_version":"9.0","description":"copied","icon":"demo.svg"}`), nil
	}}
	result, err := b.GetInstanceMetadata(context.Background())
	if err != nil || result.PluginID != "Demo" || result.InstanceID != "Demob" || result.SourceInstanceID != "Demo" || result.Version != "1.0" || result.DisplayVersion != "9.0" || result.Icon != "demo.svg" || result.Description != "copied" {
		t.Fatalf("metadata: %+v %v", result, err)
	}
	name, err := b.GetName(context.Background())
	if err != nil || name != "Second" || calls != 2 {
		t.Fatal(name, calls, err)
	}
	want := errors.New("expired caller")
	b.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, want }
	if _, err = b.GetName(context.Background()); !errors.Is(err, want) {
		t.Fatalf("metadata error masked: %v", err)
	}
	if _, err = (Base{}).GetInstanceMetadata(context.Background()); err == nil {
		t.Fatal("metadata read without a caller")
	}
}
