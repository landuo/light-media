package plugins

import (
	"encoding/json"
	"testing"
)

func TestModuleTuplePreservesExplicitTypeAndValues(t *testing.T) {
	for _, values := range [][]any{nil, {nil, nil}, {false, 0, "", json.RawMessage(`9007199254740993123456789`)}} {
		raw, err := ModuleResultTuple(values...)
		if err != nil {
			t.Fatal(err)
		}
		items, ok := DecodeModuleResultTuple(raw)
		if !ok || len(items) != len(values) {
			t.Fatal(string(raw), items, ok)
		}
		if len(items) == 4 && string(items[3]) != "9007199254740993123456789" {
			t.Fatal("tuple rounded a large number", string(items[3]))
		}
	}
	for _, ordinary := range []string{`[null,null]`, `null`, `{}`, `{"__plugin_module_tuple":null}`, `{"__plugin_module_tuple":false}`, `{"__plugin_module_tuple":[],"data":1}`} {
		if _, ok := DecodeModuleResultTuple(json.RawMessage(ordinary)); ok {
			t.Fatal("ordinary return value reinterpreted as tuple", ordinary)
		}
	}
}
