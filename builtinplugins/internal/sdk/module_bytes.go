package plugins

import (
	"encoding/base64"
	"encoding/json"
)

// ModuleBytesKey is reserved in module argument/result values. JSON strings
// remain strings (including magnet URLs and POSIX paths); byte values require
// this explicit envelope, just as module tuples require their own envelope.
const ModuleBytesKey = "__plugin_module_bytes"

// ModuleResultBytes carries exact binary content through the Go SDK. A nil
// slice here is an empty bytes value, not None; callers use JSON null for None.
// Empty bytes still terminate the module dispatcher and are false only when a
// business consumer subsequently evaluates the decoded value.
func ModuleResultBytes(value []byte) (json.RawMessage, error) {
	return json.Marshal(map[string]string{ModuleBytesKey: base64.StdEncoding.EncodeToString(value)})
}

func DecodeModuleResultBytes(raw json.RawMessage) ([]byte, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) != 1 {
		return nil, false
	}
	var value string
	if json.Unmarshal(object[ModuleBytesKey], &value) != nil || string(object[ModuleBytesKey]) == "null" {
		return nil, false
	}
	data, err := base64.StdEncoding.Strict().DecodeString(value)
	return data, err == nil
}
