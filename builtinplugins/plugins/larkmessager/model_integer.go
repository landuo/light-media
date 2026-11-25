package larkmessager

import (
	"encoding/json"
	"math"
	"math/big"
	"strings"
)

// pydanticInteger ports installed pydantic-core2.46.4 shared.rs str_as_int,
// clean_int_str, float_as_int and the consumed jiter0.14.0 NumberInt parser.
// It deliberately differs from CPython int() used by MessageImage.normalize.
func pydanticInteger(v any) (json.Number, error) {
	switch v := v.(type) {
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case int:
		return json.Number(str(v)), nil
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			f, _ := v.Float64()
			return pydanticFloatInteger(f)
		}
		n, ok := new(big.Int).SetString(string(v), 10)
		if !ok {
			return "", errInvalidMessage
		}
		return json.Number(n.String()), nil
	case float64:
		return pydanticFloatInteger(v)
	case string:
		if n, kind := jiterInteger(v); kind == "" {
			return n, nil
		} else if kind == "size" {
			return "", errInvalidMessage
		}
		cleaned, ok := cleanModelInt(v)
		if !ok {
			return "", errInvalidMessage
		}
		if n, kind := jiterInteger(cleaned); kind == "" {
			return n, nil
		}
		return "", errInvalidMessage
	}
	return "", errInvalidMessage
}
func pydanticFloatInteger(f float64) (json.Number, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Mod(f, 1) != 0 || !(float64(math.MinInt64) < f && f < float64(math.MaxInt64)) {
		return "", errInvalidMessage
	}
	return json.Number(big.NewInt(int64(f)).String()), nil
}
func jiterInteger(s string) (json.Number, string) {
	if s == "" {
		return "", "invalid"
	}
	i := 0
	if s[0] == '-' {
		i++
		if i == len(s) {
			return "", "invalid"
		}
	}
	if s[i] == '0' {
		if i+1 != len(s) {
			return "", "invalid"
		}
		return "0", ""
	}
	if s[i] < '1' || s[i] > '9' {
		return "", "invalid"
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 4300 {
		return "", "size"
	}
	if i != len(s) {
		return "", "invalid"
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return "", "invalid"
	}
	return json.Number(n.String()), ""
}
func modelSpace(r rune) bool {
	return r >= '\t' && r <= '\r' || r == ' ' || r == 0x85 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
}
func cleanModelInt(s string) (string, bool) {
	before := len(s)
	s = strings.TrimFunc(s, modelSpace)
	if strings.HasPrefix(s, "+") {
		s = s[1:]
		if strings.HasPrefix(s, "-") {
			return "", false
		}
	}
	negative := false
	if strings.HasPrefix(s, "-") {
		s = s[1:]
		if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
			return "", false
		}
		negative = true
	}
	var ok bool
	s, ok = stripModelZeros(s)
	if !ok {
		return "", false
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		decimal := s[i+1:]
		all := decimal != ""
		for _, c := range decimal {
			if c != '0' {
				all = false
				break
			}
		}
		if all {
			s = s[:i]
		}
	}
	if !strings.HasPrefix(s, "_") && !strings.HasSuffix(s, "_") && strings.Contains(s, "_") && !strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "_", "")
		if negative {
			s = "-" + s
		}
		return s, true
	}
	if before == len(s) {
		return "", false
	}
	if negative {
		s = "-" + s
	}
	return s, true
}
func stripModelZeros(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	if s[0] != '0' {
		if s[0] >= '1' && s[0] <= '9' || s[0] == '-' {
			return s, true
		}
		return "", false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '0' || c == '_':
		case c >= '1' && c <= '9' || c == '-':
			return s[i:], true
		case c == '.':
			return s[i-1:], true
		default:
			return "", false
		}
	}
	return s[len(s)-1:], true
}

// SourceUnionID returns exactly the JSON representation of Union[str,int].
func sourceUnionID(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	return pydanticInteger(v)
}
