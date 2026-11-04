package mediaservervendors

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/releasemeta/legacyanime"
)

func trimeAPISource(class, message string) error {
	return &mediaserverruntime.SourceError{Class: class, Cause: errors.New(message)}
}
func trimeAPITruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case *big.Int:
		return x != nil && x.Sign() != 0
	case json.Number:
		n, e := strconv.ParseFloat(string(x), 64)
		return e != nil || n != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
func trimeAPIInteger(value any) (*big.Int, error) {
	switch v := value.(type) {
	case int:
		return big.NewInt(int64(v)), nil
	case int64:
		return big.NewInt(v), nil
	case *big.Int:
		if v != nil {
			return new(big.Int).Set(v), nil
		}
	case bool:
		if v {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			if ok {
				return n, nil
			}
		}
		number, e := strconv.ParseFloat(string(v), 64)
		if e != nil {
			return nil, e
		}
		return trimeAPIInteger(number)
	case float64:
		if math.IsNaN(v) {
			return nil, trimeAPISource("ValueError", "cannot convert float NaN to integer")
		}
		if math.IsInf(v, 0) {
			return nil, trimeAPISource("OverflowError", "cannot convert float infinity to integer")
		}
		n, _ := new(big.Float).SetFloat64(v).Int(nil)
		return n, nil
	case string:
		n, e := legacyanime.Integer(v)
		if e == nil {
			return n, nil
		}
		if source, ok := e.(legacyanime.SourceError); ok {
			return nil, trimeAPISource(source.Class, "invalid literal for int() with base 10: "+trimeAPIStringRepr(v))
		}
		return nil, e
	}
	return nil, trimeAPISource("TypeError", "int() argument is not an admitted number or string")
}

// Internal exception spelling only; public diagnostics carry Stage/Class and
// never print request URLs, credentials, response bodies or these string values.
func trimeAPIStringRepr(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = `"`
	}
	var b strings.Builder
	b.WriteString(quote)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if string(r) == quote {
				b.WriteByte('\\')
				b.WriteRune(r)
			} else if unicode.IsPrint(r) {
				b.WriteRune(r)
			} else if r <= 0xff {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else if r <= 0xffff {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		}
	}
	b.WriteString(quote)
	return b.String()
}
func trimeAPIObject(value any) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, trimeAPISource("AttributeError", "response data has no get method")
	}
	return object, nil
}
func trimeAPIOptionalString(value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	s, ok := value.(string)
	if !ok {
		return nil, &TrimeArgumentBoundary{Field: "Api dataclass nullable string"}
	}
	return &s, nil
}
func trimeAPIString(value any) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", &TrimeArgumentBoundary{Field: "Api dataclass string"}
	}
	return s, nil
}
func trimeAPIStrings(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, &TrimeArgumentBoundary{Field: "Api dataclass list"}
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, e := trimeAPIString(v)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, nil
}
func trimeAPIQuoteAccess(value string) string {
	const digits = "0123456789ABCDEF"
	var b strings.Builder
	for _, v := range []byte(value) {
		if v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9' || strings.ContainsRune("-._~", rune(v)) {
			b.WriteByte(v)
		} else {
			b.WriteByte('%')
			b.WriteByte(digits[v>>4])
			b.WriteByte(digits[v&15])
		}
	}
	return b.String()
}
