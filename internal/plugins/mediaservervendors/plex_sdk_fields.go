package mediaservervendors

import (
	"math"
	"math/big"
	"strings"
	"time"
)

func plexSDKParseInteger(value string) (*big.Int, bool) {
	normalized, ok := plexSDKNormalizeInteger(value)
	if !ok || normalized == "" {
		return nil, false
	}
	digits := normalized
	if digits[0] == '+' || digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 || len(digits) > 4300 {
		return nil, false
	}
	for _, c := range []byte(digits) {
		if c < '0' || c > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(normalized, 10)
	return n, ok
}
func plexSDKLoadFields(node *plexSDKXML, specs []plexSDKFieldSpec, location *time.Location) (map[string]any, error) {
	out := map[string]any{}
	if node == nil {
		return out, nil
	} // original PlexObject skips _loadData for None
	for _, spec := range specs {
		value, present := node.attributes[spec.name]
		var result any
		switch spec.kind {
		case "string":
			if present {
				result = value
			}
		case "bool":
			if present {
				switch value {
				case "1", "true":
					result = true
				case "0", "false":
					result = false
				default:
					return nil, plexSDKSourceError("ValueError", value)
				}
			}
		case "int", "strict-int-default-zero":
			if spec.kind == "strict-int-default-zero" && !present {
				value, present = "0", true
			}
			if present {
				number, ok := plexSDKParseInteger(value)
				if !ok {
					if spec.kind == "int" {
						result = math.NaN()
					} else {
						return nil, plexSDKSourceError("ValueError", "invalid literal for int() with base 10")
					}
				} else {
					result = number
				}
			}
		case "list":
			values := []string{}
			for _, part := range strings.Split(value, ",") {
				if part != "" {
					values = append(values, part)
				}
			}
			result = values
		case "date":
			if present {
				if location == nil {
					return nil, &PlexArgumentBoundary{Field: "SDK timestamp timezone profile"}
				}
				number, ok := plexSDKParseInteger(value)
				if ok && number.IsInt64() {
					date := time.Unix(number.Int64(), 0).In(location)
					if date.Year() >= 1 && date.Year() <= 9999 {
						result = date
					}
				}
				// Original invalid/overflow timestamp returns None. Its informational log
				// is not yet projected by this finite no-date observation slice.
			}
		default:
			return nil, &PlexArgumentBoundary{Field: "unbound SDK attribute conversion"}
		}
		out[spec.name] = result
	}
	return out, nil
}
