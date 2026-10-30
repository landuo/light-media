package app

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const sharedSubscriptionNestedNonfinite = "source-json-nonfinite"

// SQLAlchemy JSON's Python decoder has arbitrary-precision integers but IEEE
// doubles for decimal/exponent tokens. Keep those distinct recursively. The
// valid JSON 1e999 token below is only a private persistence representation of
// a nonfinite Python float; a separate internal marker prevents exposing it as
// a successful raw-row response. It is not a public tagged JSON convention.
func sharedSubscriptionNormalizeJSON(raw json.RawMessage) (json.RawMessage, bool, error) {
	value, e := sharedSubscriptionDecode(raw)
	if e != nil {
		return nil, false, e
	}
	nonfinite := false
	var normalize func(any) (any, error)
	normalize = func(v any) (any, error) {
		switch x := v.(type) {
		case json.Number:
			if !strings.ContainsAny(string(x), ".eE") {
				return x, nil
			}
			f, e := x.Float64()
			if e != nil && !math.IsInf(f, 0) {
				return nil, e
			}
			if math.IsInf(f, 0) {
				nonfinite = true
				if math.IsInf(f, -1) {
					return json.RawMessage(`-1e999`), nil
				}
				return json.RawMessage(`1e999`), nil
			}
			// Keep an IEEE float token a float on the next JSON decode,
			// including -0.0 and integral finite floats; integer input tokens
			// never pass through this conversion.
			text := strconv.FormatFloat(f, 'g', -1, 64)
			if !strings.ContainsAny(text, ".eE") {
				text += ".0"
			}
			return json.Number(text), nil
		case map[string]any:
			for k, item := range x {
				next, e := normalize(item)
				if e != nil {
					return nil, e
				}
				x[k] = next
			}
			return x, nil
		case []any:
			for i, item := range x {
				next, e := normalize(item)
				if e != nil {
					return nil, e
				}
				x[i] = next
			}
			return x, nil
		default:
			return x, nil
		}
	}
	value, e = normalize(value)
	if e != nil {
		return nil, false, e
	}
	encoded, e := json.Marshal(value)
	return encoded, nonfinite, e
}
