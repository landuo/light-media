package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"testing"
)

func TestOriginalMutableEventDispatchSnapshotAndFailure(t *testing.T) {
	event := &plugins.MutableEvent{Type: "generic", Data: json.RawMessage(`{}`)}
	enabled := true
	calls := 0
	failures := 0
	handlers := []originalMutableEventHandler{
		{Invoke: func(context.Context, *plugins.MutableEvent) error {
			calls++
			enabled = false
			event.Data = json.RawMessage(`[null,9007199254740993123456789]`)
			return errors.New("owned failure")
		}},
		{Enabled: func() bool { return enabled }, Invoke: func(_ context.Context, got *plugins.MutableEvent) error {
			calls++
			if string(got.Data) != `[null,9007199254740993123456789]` {
				t.Fatal("prior mutation lost")
			}
			got.Data = json.RawMessage(`null`)
			return nil
		}},
		{Enabled: func() bool { return false }, Invoke: func(context.Context, *plugins.MutableEvent) error { t.Fatal("disabled handler called"); return nil }},
	}
	if !dispatchOriginalMutableEvent(context.Background(), handlers, event, func(error) { failures++ }) || calls != 2 || failures != 1 || string(event.Data) != "null" {
		t.Fatalf("dispatch calls=%d failures=%d event=%s", calls, failures, event.Data)
	}
	if dispatchOriginalMutableEvent(context.Background(), nil, event, nil) {
		t.Fatal("empty chain reported handled")
	}
}
