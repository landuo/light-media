package mediaservervendors

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Only values constructed by the original six-operation Api caller are used:
// ordered string-key dicts, lists, strings/Type values, finite ints and null.
// This is not an arbitrary Python json.dumps implementation or public JSON DTO.
type trimeJSONPair struct {
	key   string
	value any
}
type trimeJSONObject []trimeJSONPair

func trimeASCIIJSONString(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("Trime body string is not admitted Unicode")
	}
	var b strings.Builder
	b.WriteByte('"')
	const digits = "0123456789abcdef"
	escaped := func(r rune) {
		b.WriteString(`\u`)
		b.WriteByte(digits[(r>>12)&15])
		b.WriteByte(digits[(r>>8)&15])
		b.WriteByte(digits[(r>>4)&15])
		b.WriteByte(digits[r&15])
	}
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r >= 0x7f {
				if r > 0xffff {
					v := r - 0x10000
					escaped(0xd800 + (v >> 10))
					escaped(0xdc00 + (v & 0x3ff))
				} else {
					escaped(r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}
func trimeJSONBody(value any) (string, error) {
	switch v := value.(type) {
	case json.RawMessage:
		return zspaceHTTPJSON(v)
	case nil:
		return "null", nil
	case string:
		return trimeASCIIJSONString(v)
	case TrimeItemType:
		return trimeASCIIJSONString(string(v))
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case []TrimeItemType:
		out := make([]string, 0, len(v))
		for _, x := range v {
			s, e := trimeJSONBody(x)
			if e != nil {
				return "", e
			}
			out = append(out, s)
		}
		return "[" + strings.Join(out, ", ") + "]", nil
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			s, e := trimeJSONBody(x)
			if e != nil {
				return "", e
			}
			out = append(out, s)
		}
		return "[" + strings.Join(out, ", ") + "]", nil
	case trimeJSONObject:
		out := make([]string, 0, len(v))
		for _, x := range v {
			k, e := trimeASCIIJSONString(x.key)
			if e != nil {
				return "", e
			}
			s, e := trimeJSONBody(x.value)
			if e != nil {
				return "", e
			}
			out = append(out, k+": "+s)
		}
		return "{" + strings.Join(out, ", ") + "}", nil
	default:
		return "", errors.New("Trime body value outside original caller typed domain")
	}
}
