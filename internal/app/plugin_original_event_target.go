package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

// originalBroadcastEventData implements the pinned EventManager's top-level
// dictionary copy/removal, not a new event payload schema. Every dictionary
// broadcast removes this internal key; only MessageAction uses it as a filter.
// RawMessage values keep exact integers and nested values without float64 loss.
// Neither the caller's bytes nor nested dictionaries are modified here.
func originalBroadcastEventData(event string, data json.RawMessage) (json.RawMessage, string, bool, error) {
	var err error
	data, err = originalNewEventData(data)
	if err != nil {
		return nil, "", false, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return data, "", false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, "", false, err
	}
	raw, present := fields["__mp_target_plugin_id"]
	if !present {
		return data, "", false, nil
	}
	delete(fields, "__mp_target_plugin_id")
	clean, err := json.Marshal(fields)
	if err != nil || event != "message.action" {
		return clean, "", false, err
	}
	var target any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&target); err != nil {
		return nil, "", false, err
	}
	name, restricted, err := originalBroadcastTarget(target)
	return clean, name, restricted, err
}

// Event.__init__ uses event_data or {}. Normalize only when creating an event;
// a chain handler's subsequent falsey mutation is preserved for later handlers.
func originalNewEventData(data json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid event data")
	}
	if !originalBaseJSONTruthy(data) {
		return json.RawMessage(`{}`), nil
	}
	return data, nil
}

// The source tests Python truthiness before str(target). Only strings, True,
// and overflowing positive JSON floats can spell a valid installed identifier;
// finite numbers and nonempty containers cannot match the manifest's existing
// ASCII identifier grammar. They remain restricted to no matching instance,
// never interpreted as missing or as an instruction to broadcast to everybody.
func originalBroadcastTarget(target any) (string, bool, error) {
	if number, ok := target.(json.Number); ok && strings.ContainsAny(string(number), ".eE") {
		// Python's JSON decoder converts decimal/exponent numbers to binary64:
		// underflow is false and positive infinity stringifies as "inf".
		value, err := strconv.ParseFloat(string(number), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return "", false, err
		}
		if value == 0 {
			return "", false, nil
		}
		if math.IsInf(value, 1) {
			return "inf", true, nil
		}
		return "", true, nil
	}
	if !sourceSubscriptionTruth(target) {
		return "", false, nil
	}
	switch value := target.(type) {
	case string:
		return value, true, nil
	case bool:
		return "True", true, nil
	default:
		return "", true, nil
	}
}
