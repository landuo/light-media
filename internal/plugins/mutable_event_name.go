package plugins

import (
	"encoding/json"
	"errors"
)

// MutableEventNameReader models the source plugin's .name attribute access,
// NOT Manifest.Name/get_name(). It is invoked only after the callback fails,
// so a dynamic removal/getter failure is observed at the original point in time.
// present=false means missing; present=true with raw null is a present None.
// Values are finite JSON projections, not arbitrary Python object reflection.
type MutableEventNameReader func() (value json.RawMessage, present bool, err error)

// MutableEventErrorName is the optional v1 failure-only snapshot extension.
// Omitted extensions from older workers mean missing, never manifest fallback.
// State is missing, value, or failed. Only value carries a valid JSON Value.
type MutableEventErrorName struct {
	State string          `json:"state"`
	Value json.RawMessage `json:"value,omitempty"`
}

// HandleMutableEventWithName preserves mutation and an explicit source .name
// lookup after failure. Callback return values are still ignored. Name getter
// errors/panics carry only the failed state, never arbitrary exception text.
func HandleMutableEventWithName(input json.RawMessage, handle func(*MutableEvent) (json.RawMessage, error), name MutableEventNameReader) (json.RawMessage, error) {
	return handleMutableEvent(input, handle, name)
}

func readMutableEventErrorName(read MutableEventNameReader) (result MutableEventErrorName) {
	result.State = "failed"
	defer func() {
		if recover() != nil {
			result = MutableEventErrorName{State: "failed"}
		}
	}()
	value, present, err := read()
	if err != nil {
		return result
	}
	if !present {
		return MutableEventErrorName{State: "missing"}
	}
	if !json.Valid(value) {
		return result
	}
	return MutableEventErrorName{State: "value", Value: append(json.RawMessage(nil), value...)}
}

// DecodeMutableEventErrorName is used only after a validated failed mutation
// snapshot. It does not reinterpret ordinary callback returns. Malformed name
// extensions are protocol errors, distinct from an actual failed getter.
func DecodeMutableEventErrorName(raw json.RawMessage) (MutableEventErrorName, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return MutableEventErrorName{}, errors.New("invalid event error name")
	}
	name, ok := fields["source_error_name"]
	if !ok {
		return MutableEventErrorName{State: "missing"}, nil
	}
	var value MutableEventErrorName
	if json.Unmarshal(name, &value) != nil {
		return value, errors.New("invalid event error name")
	}
	switch value.State {
	case "missing", "failed":
		if len(value.Value) != 0 {
			return value, errors.New("unexpected event error name value")
		}
	case "value":
		if !json.Valid(value.Value) {
			return value, errors.New("invalid event error name value")
		}
	default:
		return value, errors.New("invalid event error name state")
	}
	return value, nil
}
