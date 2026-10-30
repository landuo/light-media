package app

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const sharedSubscriptionNestedNonfinite = "source-json-nonfinite"

// SQLAlchemy JSON keeps Python code points and arbitrary-precision integers,
// while decimal/exponent tokens become IEEE doubles. The valid JSON 1e999
// spelling is internal persistence for nonfinite values; response failure is
// separately enforced, just as isolated surrogates persist before UTF-8 fails.
func sharedSubscriptionNormalizeJSON(raw json.RawMessage) (json.RawMessage, bool, error) {
	value, err := parseSharedSubscriptionJSON(raw)
	if err != nil {
		return nil, false, err
	}
	nonfinite := false
	var normalize func(*sharedSubscriptionJSONValue) error
	normalize = func(v *sharedSubscriptionJSONValue) error {
		if v.kind == '0' && strings.ContainsAny(v.text, ".eE") {
			f, e := strconv.ParseFloat(v.text, 64)
			if e != nil && !math.IsInf(f, 0) {
				return e
			}
			if math.IsInf(f, 0) {
				nonfinite = true
				v.text = "1e999"
				if math.IsInf(f, -1) {
					v.text = "-1e999"
				}
			} else {
				v.text = strconv.FormatFloat(f, 'g', -1, 64)
				if !strings.ContainsAny(v.text, ".eE") {
					v.text += ".0"
				}
			}
		}
		for _, item := range v.items {
			if e := normalize(item); e != nil {
				return e
			}
		}
		for _, member := range v.members {
			if e := normalize(member.value); e != nil {
				return e
			}
		}
		return nil
	}
	if err = normalize(value); err != nil {
		return nil, false, err
	}
	return json.RawMessage(value.appendJSON(nil)), nonfinite, nil
}
