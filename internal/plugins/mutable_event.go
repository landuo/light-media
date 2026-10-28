package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
)

const mutableEventContract = "light-media.mutable-event/v1"

// MutableEvent carries a synchronous chain event across the process boundary.
// Data supports every JSON value, including null, arrays and scalars. RawMessage
// avoids float64 coercion and preserves arbitrary-size JSON integer values.
type MutableEvent struct {
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data"`
	EventID  string          `json:"event_id,omitempty"`
	Priority *int            `json:"priority,omitempty"`
}

type mutableEventSnapshot struct {
	Contract string       `json:"event_mutation_contract"`
	Event    MutableEvent `json:"event"`
	Failed   bool         `json:"handler_failed"`
}

// HandleMutableEvent explicitly bridges event mutation, not callback returns.
// Ordinary return values are ignored, as in the original EventManager. Valid
// mutations made before a callback error or panic are still transported. This
// helper is generic: it knows no plugin IDs, Reset flags or business schemas.
func HandleMutableEvent(input json.RawMessage, handle func(*MutableEvent) (json.RawMessage, error)) (json.RawMessage, error) {
	event, err := decodeMutableEvent(input)
	if err != nil || handle == nil {
		return nil, errors.New("invalid mutable event")
	}
	failed := false
	func() {
		defer func() {
			if recover() != nil {
				failed = true
			}
		}()
		_, err := handle(&event)
		failed = err != nil
	}()
	// A nil RawMessage has the same JSON representation as null.
	if len(event.Data) == 0 {
		event.Data = json.RawMessage(`null`)
	}
	if !json.Valid(event.Data) {
		return nil, errors.New("invalid mutable event data")
	}
	return json.Marshal(mutableEventSnapshot{Contract: mutableEventContract, Event: event, Failed: failed})
}

// DecodeMutableEventSnapshot recognizes only the explicit SDK mutation
// contract. Arbitrary handler return values (including maps/null) are ignored.
// Malformed tagged results are rejected before any host state is changed.
func DecodeMutableEventSnapshot(raw json.RawMessage) (event MutableEvent, failed, present bool, err error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return event, false, false, nil
	}
	tag, ok := fields["event_mutation_contract"]
	if !ok {
		return event, false, false, nil
	}
	var contract string
	if json.Unmarshal(tag, &contract) != nil || contract != mutableEventContract {
		return event, false, true, errors.New("invalid mutable event contract")
	}
	flag := bytes.Trim(fields["handler_failed"], " \t\r\n")
	if !bytes.Equal(flag, []byte("true")) && !bytes.Equal(flag, []byte("false")) {
		return event, false, true, errors.New("invalid mutable event failure flag")
	}
	event, err = decodeMutableEvent(fields["event"])
	if err != nil {
		return MutableEvent{}, false, true, err
	}
	return event, bytes.Equal(flag, []byte("true")), true, nil
}

func decodeMutableEvent(raw json.RawMessage) (MutableEvent, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return MutableEvent{}, errors.New("invalid mutable event")
	}
	var event MutableEvent
	kind, ok := fields["type"]
	if !ok || bytes.Equal(bytes.TrimSpace(kind), []byte("null")) || json.Unmarshal(kind, &event.Type) != nil {
		return event, errors.New("invalid mutable event type")
	}
	if !json.Valid(fields["data"]) {
		return event, errors.New("invalid mutable event data")
	}
	event.Data = append(json.RawMessage(nil), fields["data"]...)
	if raw, ok := fields["event_id"]; ok {
		if json.Unmarshal(raw, &event.EventID) != nil {
			return event, errors.New("invalid event ID")
		}
	}
	if raw, ok := fields["priority"]; ok {
		if json.Unmarshal(raw, &event.Priority) != nil {
			return event, errors.New("invalid event priority")
		}
	}
	return event, nil
}
