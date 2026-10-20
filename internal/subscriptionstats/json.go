package subscriptionstats

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type sourceField struct {
	name string
	raw  json.RawMessage
}

// requests serializes these scalar source fields with Python json.dumps'
// default spacing and ensure_ascii=True. Decode string escapes without Go's
// lossy surrogate replacement, and never route source integer IDs via float64.
func marshalSourceFields(fields []sourceField) ([]byte, error) {
	var out strings.Builder
	out.WriteByte('{')
	for i, f := range fields {
		if i != 0 {
			out.WriteString(", ")
		}
		out.WriteString(strconv.Quote(f.name))
		out.WriteString(": ")
		if err := appendSourceScalar(&out, f.raw); err != nil {
			return nil, err
		}
	}
	out.WriteByte('}')
	return []byte(out.String()), nil
}

func appendSourceScalar(out *strings.Builder, raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) || !utf8.Valid(raw) {
		return ErrDependency
	}
	if bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false")) {
		out.Write(raw)
		return nil
	}
	if raw[0] == '"' {
		out.WriteByte('"')
		for i := 1; i < len(raw)-1; {
			r, size := utf8.DecodeRune(raw[i:])
			i += size
			if r == '\\' {
				escape := raw[i]
				i++
				switch escape {
				case '"', '\\', '/':
					r = rune(escape)
				case 'b':
					r = '\b'
				case 'f':
					r = '\f'
				case 'n':
					r = '\n'
				case 'r':
					r = '\r'
				case 't':
					r = '\t'
				case 'u':
					v, _ := strconv.ParseUint(string(raw[i:i+4]), 16, 16)
					i += 4
					r = rune(v)
					if r >= 0xd800 && r <= 0xdbff && i+6 <= len(raw)-1 && raw[i] == '\\' && raw[i+1] == 'u' {
						low, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
						if err == nil && low >= 0xdc00 && low <= 0xdfff {
							r = utf16.DecodeRune(r, rune(low))
							i += 6
						}
					}
				}
			}
			appendSourceRune(out, r)
		}
		out.WriteByte('"')
		return nil
	}
	if raw[0] == '{' || raw[0] == '[' {
		return ErrDependency
	}
	if !bytes.ContainsAny(raw, ".eE") {
		number, ok := new(big.Int).SetString(string(raw), 10)
		if !ok {
			return ErrDependency
		}
		out.WriteString(number.String())
		return nil
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return ErrInvalidJSON
	}
	if err != nil {
		return ErrDependency
	}
	format := byte('f')
	if f != 0 && (math.Abs(f) < 1e-4 || math.Abs(f) >= 1e16) {
		format = 'e'
	}
	s := strconv.FormatFloat(f, format, -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	out.WriteString(s)
	return nil
}

func appendSourceRune(out *strings.Builder, r rune) {
	switch r {
	case '"', '\\':
		out.WriteByte('\\')
		out.WriteRune(r)
	case '\b':
		out.WriteString("\\b")
	case '\f':
		out.WriteString("\\f")
	case '\n':
		out.WriteString("\\n")
	case '\r':
		out.WriteString("\\r")
	case '\t':
		out.WriteString("\\t")
	default:
		if r < 32 || r > 126 {
			if r > 0xffff {
				high, low := utf16.EncodeRune(r)
				appendSourceRune(out, high)
				appendSourceRune(out, low)
				return
			}
			hex := strconv.FormatInt(int64(r), 16)
			out.WriteString("\\u" + strings.Repeat("0", 4-len(hex)) + hex)
		} else {
			out.WriteRune(r)
		}
	}
}
