package app

import (
	"encoding/json"
	"math"
	"testing"
)

func TestBrushFlowCanonicalComparisonRejectsDifferentAndUnencodableFacts(t *testing.T) {
	tests := []struct {
		name  string
		a, b  any
		equal bool
	}{
		{"same_object_order", json.RawMessage(`{"a":1,"b":[2,3]}`), json.RawMessage(`{ "b": [2,3], "a": 1 }`), true},
		{"same_array", []any{map[string]any{"title": "owned", "id": json.Number("9007199254740993")}}, json.RawMessage(`[{"id":9007199254740993,"title":"owned"}]`), true},
		{"different_array", json.RawMessage(`[1,2]`), json.RawMessage(`[1,3]`), false},
		{"different_scalar", 1, 2, false},
		{"precise_integers", json.RawMessage(`{"id":9007199254740992}`), json.RawMessage(`{"id":9007199254740993}`), false},
		{"broken_raw_both", json.RawMessage(`{"x":`), json.RawMessage(`{"x":`), false},
		{"nan_both", map[string]any{"x": math.NaN()}, map[string]any{"x": math.NaN()}, false},
		{"unsupported_both", make(chan int), make(chan int), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := brushFlowSameJSON(tt.a, tt.b); got != tt.equal {
				t.Fatal("fact comparison differs", got, tt.equal)
			}
		})
	}
}
