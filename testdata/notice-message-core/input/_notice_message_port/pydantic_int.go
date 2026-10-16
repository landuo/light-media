package notice

import (
	"encoding/json"
	"math/big"
	"strings"
)

// Source: Pydantic v2.13.4 pydantic-core/src/input/shared.rs
// clean_int_str/strip_leading_zeros/strip_underscores (MIT), with the consumed
// NumberInt::try_from subset of pinned jiter0.14.0 (MIT). The initial/fallback
// parse have different range error classes; do not replace with a regex/int().
func pydanticStringInt(s string) (any, string) {
	n, kind := jiterInteger(s)
	if kind == "" {
		return n, ""
	}
	if kind == "int_parsing_size" {
		return nil, kind
	}
	cleaned, ok := cleanIntString(s)
	if !ok {
		return nil, "int_parsing"
	}
	n, kind = jiterInteger(cleaned)
	if kind != "" {
		return nil, "int_parsing"
	}
	return n, ""
}
func jiterInteger(s string) (any, string) {
	if len(s) == 0 {
		return nil, "int_parsing"
	}
	i := 0
	if s[0] == '-' {
		i++
	}
	if i == len(s) {
		return nil, "int_parsing"
	}
	if s[i] == '0' {
		if i+1 != len(s) {
			return nil, "int_parsing"
		}
		return json.Number("0"), ""
	}
	if s[i] < '1' || s[i] > '9' {
		return nil, "int_parsing"
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 4300 {
		return nil, "int_parsing_size"
	}
	if i != len(s) {
		return nil, "int_parsing"
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, "int_parsing"
	}
	return json.Number(n.String()), ""
}
func cleanIntString(input string) (string, bool) {
	originalLength := len(input)
	s := strings.TrimSpace(input)
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
	s, ok = stripLeadingZeros(s)
	if !ok {
		return "", false
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		decimal := s[i+1:]
		if len(decimal) > 0 && strings.Trim(decimal, "0") == "" {
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
	if originalLength == len(s) {
		return "", false
	}
	if negative {
		s = "-" + s
	}
	return s, true
}
func stripLeadingZeros(s string) (string, bool) {
	if len(s) == 0 {
		return "", false
	}
	if s[0] != '0' {
		if s[0] >= '1' && s[0] <= '9' || s[0] == '-' {
			return s, true
		}
		return "", false
	}
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '0' || c == '_':
			continue
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
