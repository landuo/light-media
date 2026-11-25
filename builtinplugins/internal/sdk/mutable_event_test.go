package plugins

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMutableEventSnapshotArbitraryJSONAndFailure(t *testing.T) {
	for _, data := range []string{`null`, `false`, `123456789012345678901234567890`, `"scalar"`, `[null,9007199254740993123456789,false]`, `{"extra":{"nested":[null,false,0]},"large":9007199254740993123456789}`} {
		for _, mode := range []string{"return", "none", "error", "panic"} {
			out, err := HandleMutableEvent(json.RawMessage(`{"type":"any_event","data":{}}`), func(event *MutableEvent) (json.RawMessage, error) {
				event.Data = json.RawMessage(data)
				event.Type = "changed_event"
				if mode == "panic" {
					panic("owned failure")
				}
				if mode == "error" {
					return nil, errors.New("owned failure")
				}
				if mode == "none" {
					return nil, nil
				}
				return json.RawMessage(`{"data":"must not replace mutation"}`), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			event, failed, present, err := DecodeMutableEventSnapshot(out)
			if err != nil || !present || failed != (mode == "error" || mode == "panic") || string(event.Data) != data || event.Type != "changed_event" {
				t.Fatalf("%s %s: %s %v", data, mode, out, err)
			}
		}
	}
}
func TestMutableEventSnapshotBoundary(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `[]`, `3`, `{"event":{"type":"wrong","data":0}}`, `{"data":"ordinary"}`} {
		if _, _, present, err := DecodeMutableEventSnapshot(json.RawMessage(raw)); present || err != nil {
			t.Fatalf("ordinary return interpreted %s", raw)
		}
	}
	for _, raw := range []string{
		`{"event_mutation_contract":"wrong"}`,
		`{"event_mutation_contract":"light-media.mutable-event/v1","event":null,"handler_failed":false}`,
		`{"event_mutation_contract":"light-media.mutable-event/v1","event":{"type":null,"data":0},"handler_failed":false}`,
		`{"event_mutation_contract":"light-media.mutable-event/v1","event":{"type":"x"},"handler_failed":false}`,
		`{"event_mutation_contract":"light-media.mutable-event/v1","event":{"type":"x","data":null},"handler_failed":null}`,
		`{"event_mutation_contract":"light-media.mutable-event/v1","event":{"type":"x","data":null},"handler_failed":"true"}`,
	} {
		if event, _, present, err := DecodeMutableEventSnapshot(json.RawMessage(raw)); err == nil || !present || event.Data != nil {
			t.Fatalf("malformed snapshot accepted %s", raw)
		}
	}
	raw := json.RawMessage("{\"event_mutation_contract\":\"light-media.mutable-event/v1\",\"event\":{\"type\":\"x\",\"data\": null },\"handler_failed\": \n\tfalse\r }")
	if event, failed, present, err := DecodeMutableEventSnapshot(raw); err != nil || !present || failed || string(event.Data) != "null" {
		t.Fatalf("JSON whitespace rejected %v", err)
	}
}
