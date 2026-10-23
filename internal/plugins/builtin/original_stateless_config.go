package builtin

import "encoding/json"

// Preserve Python's if config distinction between a nonempty dict and empty /
// None. This only decodes the invocation; no persisted configuration is read.
func originalStatelessConfigFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	err := json.Unmarshal(raw, &fields)
	return fields, err
}
