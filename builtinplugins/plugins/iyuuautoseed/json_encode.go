package iyuuautoseed

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type jsonField struct {
	key   string
	value any
}

// orderedJSON preserves Python requests' json.dumps default formatting and
// ensure_ascii=True. Object order here is explicit rather than Go map order.
func orderedJSON(fields []jsonField, ascii bool) ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		if e := appendJSONString(&b, f.key, ascii); e != nil {
			return nil, e
		}
		b.WriteString(": ")
		if e := appendJSONValue(&b, f.value, ascii); e != nil {
			return nil, e
		}
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}
func appendJSONString(b *strings.Builder, s string, ascii bool) error {
	if !utf8.ValidString(s) {
		return errors.New("invalid UTF-8")
	}
	b.WriteByte('"')
	hex := "0123456789abcdef"
	escape := func(r rune) {
		b.WriteString("\\u")
		for n := 12; n >= 0; n -= 4 {
			b.WriteByte(hex[(r>>n)&15])
		}
	}
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString("\\b")
		case '\f':
			b.WriteString("\\f")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 32 || ascii && r >= 127 {
				if r > 0xffff {
					a, c := utf16.EncodeRune(r)
					escape(a)
					escape(c)
				} else {
					escape(r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return nil
}
func appendJSONValue(b *strings.Builder, v any, ascii bool) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		return appendJSONString(b, x, ascii)
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		if _, e := decodeJSON([]byte(x)); e != nil {
			return e
		}
		b.WriteString(x.String())
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case []string:
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			if e := appendJSONString(b, s, ascii); e != nil {
				return e
			}
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, n := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			if e := appendJSONValue(b, n, ascii); e != nil {
				return e
			}
		}
		b.WriteByte(']')
	default:
		return errors.New("unsupported payload JSON type")
	}
	return nil
}
