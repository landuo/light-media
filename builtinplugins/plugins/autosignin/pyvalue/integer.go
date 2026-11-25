package pyvalue

import (
	"fmt"
	"math/big"
	"strings"
)

func Integer(value string) (*big.Int, error) {
	value = strings.TrimFunc(value, func(r rune) bool { return pySpace(r) && !(r >= 0x1c && r <= 0x1f) })
	var text strings.Builder
	count := 0
	for _, r := range value {
		if d, ok := pyDecimal[r]; ok {
			text.WriteByte(byte('0' + d))
			count++
		} else {
			text.WriteRune(r)
		}
	}
	fail := func() (*big.Int, error) { return nil, fmt.Errorf("invalid literal for int() with base 10: %q", value) }
	if count > 4300 {
		return nil, fmt.Errorf("Exceeds the limit (4300 digits) for integer string conversion: value has %d digits; use sys.set_int_max_str_digits() to increase the limit", count)
	}
	s := text.String()
	if s == "" {
		return fail()
	}
	for i, r := range s {
		if r == '_' && (i == 0 || i == len(s)-1 || s[i-1] < '0' || s[i-1] > '9' || s[i+1] < '0' || s[i+1] > '9') {
			return fail()
		}
	}
	n, ok := new(big.Int).SetString(strings.ReplaceAll(s, "_", ""), 10)
	if !ok {
		return fail()
	}
	return n, nil
}
