package app

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Python float(str), used by SQLAlchemy's FLOAT bind processor, accepts
// Unicode decimal digits and digit-separated underscores but not Go's hex
// float syntax. Normalize digits, then enforce that decimal grammar before
// IEEE conversion (overflow intentionally remains infinity).
var sharedSubscriptionFloatGrammar = regexp.MustCompile(`(?i)^[+-]?(?:(?:[0-9](?:_?[0-9])*(?:\.(?:[0-9](?:_?[0-9])*)?)?|\.[0-9](?:_?[0-9])*)(?:e[+-]?[0-9](?:_?[0-9])*)?|inf(?:inity)?|nan)$`)

func sharedSubscriptionPythonFloat(input string) (float64, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(input) {
		if r > 127 && unicode.Is(unicode.Nd, r) {
			digit := -1
			for _, rg := range unicode.Nd.R16 {
				if uint32(r) >= uint32(rg.Lo) && uint32(r) <= uint32(rg.Hi) && (uint32(r)-uint32(rg.Lo))%uint32(rg.Stride) == 0 {
					digit = int((uint32(r) - uint32(rg.Lo)) / uint32(rg.Stride) % 10)
					break
				}
			}
			if digit < 0 {
				for _, rg := range unicode.Nd.R32 {
					if uint32(r) >= rg.Lo && uint32(r) <= rg.Hi && (uint32(r)-rg.Lo)%rg.Stride == 0 {
						digit = int((uint32(r) - rg.Lo) / rg.Stride % 10)
						break
					}
				}
			}
			if digit >= 0 {
				b.WriteByte('0' + byte(digit))
				continue
			}
		}
		b.WriteRune(r)
	}
	value := b.String()
	if !sharedSubscriptionFloatGrammar.MatchString(value) {
		return 0, errors.New("not a Python decimal float")
	}
	lower := strings.ToLower(strings.TrimLeft(value, "+-"))
	if lower == "nan" {
		return math.NaN(), nil
	}
	if lower == "inf" || lower == "infinity" {
		sign := 1
		if strings.HasPrefix(value, "-") {
			sign = -1
		}
		return math.Inf(sign), nil
	}
	return strconv.ParseFloat(strings.ReplaceAll(value, "_", ""), 64)
}
