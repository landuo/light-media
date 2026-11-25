package sourcejson

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// ModelInteger follows the source Pydantic model boundary. JSON integer/string
// values may be arbitrary precision; conversion from a binary64 float is
// restricted to the source validator's signed 64-bit range.
func ModelInteger(value any) (json.Number, error) {
	var text string
	switch value := value.(type) {
	case bool:
		if value {
			return "1", nil
		}
		return "0", nil
	case json.Number:
		text = value.String()
		if strings.ContainsAny(text, ".eE") {
			number, err := strconv.ParseFloat(text, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number >= 9223372036854775808.0 || number <= -9223372036854775808.0 {
				return "", fmt.Errorf("integer required")
			}
			n, _ := new(big.Float).SetFloat64(number).Int(nil)
			return json.Number(n.String()), nil
		}
	case string:
		text = strings.TrimSpace(value)
		if !modelIntegerString.MatchString(text) {
			return "", fmt.Errorf("integer required")
		}
	default:
		return "", fmt.Errorf("integer required")
	}
	if strings.ContainsAny(text, "/xXpP") {
		return "", fmt.Errorf("invalid integer")
	}
	rational, ok := new(big.Rat).SetString(text)
	if !ok || !rational.IsInt() {
		return "", fmt.Errorf("integer required")
	}
	return json.Number(rational.Num().String()), nil
}

var modelIntegerString = regexp.MustCompile(`^[+-]?[0-9]+(?:_[0-9]+)*(?:\.0+)?$`)
