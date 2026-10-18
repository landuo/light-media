package app

import (
	"encoding/json"
	"math"
	"testing"
)

func TestBrushFlowJSONComparisonPreservesValuesAndRejectsErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b any
		want bool
	}{
		{"array different", []any{1}, []any{2}, false},
		{"scalar different", "one", "two", false},
		{"equal array", []any{1, "two"}, json.RawMessage(`[1,"two"]`), true},
		{"large integer", json.RawMessage(`{"size":9007199254740993}`), map[string]any{"size": json.Number("9007199254740993")}, true},
		{"large integer differs", json.RawMessage(`{"size":9007199254740993}`), map[string]any{"size": json.Number("9007199254740992")}, false},
		{"number lexeme differs", json.Number("1"), json.Number("1.0"), false},
		{"both invalid float", math.NaN(), math.NaN(), false},
		{"left invalid float", math.NaN(), nil, false},
		{"right invalid float", nil, math.NaN(), false},
		{"both invalid raw", json.RawMessage(`{`), json.RawMessage(`{`), false},
		{"left invalid raw", json.RawMessage(`{`), map[string]any{}, false},
		{"right invalid raw", map[string]any{}, json.RawMessage(`{`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := brushFlowSameJSON(tc.a, tc.b); got != tc.want {
				t.Fatalf("comparison=%v, want %v", got, tc.want)
			}
		})
	}
}
