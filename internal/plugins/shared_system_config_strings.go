package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

// DecodeSystemConfigJSONString preserves Python string code points, including
// lone UTF-16 surrogates. Standard json.Unmarshal into string replaces those
// code points with U+FFFD. Use RawMessage for values not being inspected.
func DecodeSystemConfigJSONString(raw json.RawMessage) ([]rune, error) {
	raw = bytes.TrimSpace(raw)
	if !utf8.Valid(raw) || !json.Valid(raw) || len(raw) < 2 || raw[0] != '"' {
		return nil, errors.New("not a system config JSON string")
	}
	out := []rune{}
	for i := 1; i < len(raw)-1; {
		r, n := utf8.DecodeRune(raw[i:])
		i += n
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
					lo, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
					if lo >= 0xdc00 && lo <= 0xdfff {
						r = 0x10000 + (r-0xd800)*1024 + rune(lo) - 0xdc00
						i += 6
					}
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// EncodeSystemConfigJSONString encodes a source code-point sequence without
// passing lone surrogates through Go string/JSON replacement. Out-of-range
// integers are not source string code points and are rejected.
func EncodeSystemConfigJSONString(points []rune) (json.RawMessage, error) {
	var b bytes.Buffer
	b.WriteByte('"')
	start := 0
	flush := func(end int) {
		if end > start {
			part, _ := json.Marshal(string(points[start:end]))
			b.Write(part[1 : len(part)-1])
		}
	}
	for i, r := range points {
		if r < 0 || r > utf8.MaxRune {
			return nil, errors.New("system config string code point out of range")
		}
		if r >= 0xd800 && r <= 0xdfff {
			flush(i)
			b.WriteString(`\u` + strconv.FormatInt(int64(r), 16))
			start = i + 1
		}
	}
	flush(len(points))
	b.WriteByte('"')
	return b.Bytes(), nil
}

// ValidateSystemConfigUTF8JSON checks the separate source UTF-8 HTTP rendering
// boundary, including object keys. Internal SDK Get/Set retain escaped lone
// code points; callers that render a Python-like UTF-8 JSON response must not
// mistake successful internal retrieval for a renderable response. This helper
// does not replace JSONHTTPResponse globally or define numeric serialization.
func ValidateSystemConfigUTF8JSON(raw json.RawMessage) error {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return errors.New("invalid system config JSON")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		start := i
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] == '\\' {
				i++
			}
			i++
		}
		points, e := DecodeSystemConfigJSONString(raw[start : i+1])
		if e != nil {
			return e
		}
		for _, r := range points {
			if r >= 0xd800 && r <= 0xdfff {
				return errors.New("SYSTEM_CONFIG_JSON_UNICODE: source value cannot render as UTF-8 JSON")
			}
		}
	}
	return nil
}
