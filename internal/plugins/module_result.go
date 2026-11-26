package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ModuleTupleKey is reserved only in module return values. An ordinary JSON
// array remains a Python-list equivalent and participates in list aggregation.
const ModuleTupleKey = "__plugin_module_tuple"

// ErrModuleRateLimited is an expected local throttle, not a system fault. Only
// this fixed classification crosses the wire; wrapped error text never does.
var ErrModuleRateLimited = errors.New("plugin module rate limited")

func workerHandlerFailureCode(err error) string {
	if errors.Is(err, ErrModuleRateLimited) {
		return "MODULE_RATE_LIMITED"
	}
	return "OPERATION_FAILED"
}

// ModuleResultTuple expresses a tuple across the Go process boundary. Empty
// tuples and tuples containing only null mean "continue to the next module";
// any non-null member makes the whole tuple a terminal result.
func ModuleResultTuple(values ...any) (json.RawMessage, error) {
	if values == nil {
		values = []any{}
	}
	return json.Marshal(map[string]any{ModuleTupleKey: values})
}

// DecodeModuleResultTuple accepts only the exact envelope emitted above. Extra
// keys, null, scalar values and ordinary arrays remain ordinary module data.
func DecodeModuleResultTuple(raw json.RawMessage) ([]json.RawMessage, bool) {
	if !possibleModuleEnvelope(raw, ModuleTupleKey) {
		return nil, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) != 1 {
		return nil, false
	}
	value := bytes.TrimSpace(object[ModuleTupleKey])
	if len(value) == 0 || value[0] != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(value, &items) != nil {
		return nil, false
	}
	return items, true
}

// An exact single-key envelope must begin with its reserved key. Avoid
// decoding large, ordinary MediaInfo objects just to discover their type.
// Escaped keys still go through the full JSON decoder.
func possibleModuleEnvelope(raw json.RawMessage, key string) bool {
	object := bytes.TrimSpace(raw)
	if len(object) < 2 || object[0] != '{' {
		return false
	}
	first := bytes.TrimSpace(object[1:])
	if len(first) < 2 || first[0] != '"' {
		return false
	}
	name := first[1:]
	end := bytes.IndexAny(name, `"\`)
	return end >= 0 && (name[end] == '\\' || string(name[:end]) == key)
}
