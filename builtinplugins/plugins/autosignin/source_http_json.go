package autosignin

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Only site HTTP response text enters this source-compatible decoder. The SDK
// request/response envelope, plugin configuration and Decode keep their existing
// strict IPC JSON validation. Error positions count Python string characters.
//
// The recursive object/array grammar and error wording follow CPython3.12's
// json.decoder/scanner; licenses/CPython-PSF.txt retains its notice.
type sourceHTTPJSON struct {
	text       string
	pos, depth int
}

func parseSourceHTTPJSON(text string) (any, error) {
	if strings.HasPrefix(text, "\ufeff") {
		return nil, jsonPositionError("Unexpected UTF-8 BOM (decode using utf-8-sig)", text, 0)
	}
	p := sourceHTTPJSON{text: text}
	p.space()
	value, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(text) {
		return nil, p.failure("Extra data", p.pos)
	}
	return value, nil
}
func (p *sourceHTTPJSON) failure(message string, pos int) error {
	return jsonPositionError(message, p.text, pos)
}
func (p *sourceHTTPJSON) space() {
	for p.pos < len(p.text) && strings.ContainsRune(" \t\r\n", rune(p.text[p.pos])) {
		p.pos++
	}
}
func (p *sourceHTTPJSON) value() (any, error) {
	if p.pos >= len(p.text) {
		return nil, p.failure("Expecting value", p.pos)
	}
	switch p.text[p.pos] {
	case '"':
		return p.stringValue()
	case '{':
		return p.object()
	case '[':
		return p.array()
	}
	for _, literal := range []struct {
		word  string
		value any
	}{{"null", nil}, {"true", true}, {"false", false}, {"NaN", math.NaN()}, {"Infinity", math.Inf(1)}, {"-Infinity", math.Inf(-1)}} {
		if strings.HasPrefix(p.text[p.pos:], literal.word) {
			p.pos += len(literal.word)
			return literal.value, nil
		}
	}
	if p.text[p.pos] == '-' || p.text[p.pos] >= '0' && p.text[p.pos] <= '9' {
		start := p.pos
		i := start
		if p.text[i] == '-' {
			i++
		}
		if i < len(p.text) && p.text[i] >= '0' && p.text[i] <= '9' {
			if p.text[i] == '0' {
				i++
			} else {
				for i < len(p.text) && p.text[i] >= '0' && p.text[i] <= '9' {
					i++
				}
			}
			if i+1 < len(p.text) && p.text[i] == '.' && p.text[i+1] >= '0' && p.text[i+1] <= '9' {
				i += 2
				for i < len(p.text) && p.text[i] >= '0' && p.text[i] <= '9' {
					i++
				}
			}
			if i < len(p.text) && (p.text[i] == 'e' || p.text[i] == 'E') {
				end := i + 1
				if end < len(p.text) && (p.text[end] == '+' || p.text[end] == '-') {
					end++
				}
				digits := end
				for end < len(p.text) && p.text[end] >= '0' && p.text[end] <= '9' {
					end++
				}
				if end > digits {
					i = end
				}
			}
			p.pos = i
			raw := p.text[start:i]
			if raw == "-0" {
				raw = "0"
			} // json.loads constructs Python int0.
			if !strings.ContainsAny(raw, ".eE") {
				digits := len(strings.TrimPrefix(raw, "-"))
				if digits > 4300 {
					return nil, &ValueError{"ValueError", fmt.Sprintf("Exceeds the limit (4300 digits) for integer string conversion: value has %d digits; use sys.set_int_max_str_digits() to increase the limit", digits)}
				}
			}
			return json.Number(raw), nil
		}
	}
	return nil, p.failure("Expecting value", p.pos)
}
func (p *sourceHTTPJSON) stringValue() (string, error) {
	start := p.pos
	p.pos++
	var out strings.Builder
	for p.pos < len(p.text) {
		at := p.pos
		ch := p.text[p.pos]
		p.pos++
		if ch == '"' {
			return out.String(), nil
		}
		if ch < 0x20 {
			return "", p.failure("Invalid control character at", at)
		}
		if ch != '\\' {
			out.WriteByte(ch)
			continue
		}
		if p.pos >= len(p.text) {
			break
		}
		escaped := p.text[p.pos]
		p.pos++
		switch escaped {
		case '"', '\\', '/':
			out.WriteByte(escaped)
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'u':
			if p.pos+4 > len(p.text) {
				return "", p.failure(`Invalid \uXXXX escape`, at+1)
			}
			for _, ch := range p.text[p.pos : p.pos+4] {
				if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
					return "", p.failure(`Invalid \uXXXX escape`, at+1)
				}
			}
			v, e := strconv.ParseUint(p.text[p.pos:p.pos+4], 16, 16)
			if e != nil {
				return "", p.failure(`Invalid \uXXXX escape`, at+1)
			}
			p.pos += 4
			r := rune(v)
			if r >= 0xd800 && r <= 0xdbff && p.pos+6 <= len(p.text) && p.text[p.pos:p.pos+2] == `\u` {
				low, e := strconv.ParseUint(p.text[p.pos+2:p.pos+6], 16, 16)
				if e == nil && low >= 0xdc00 && low <= 0xdfff {
					r = 0x10000 + (r-0xd800)*0x400 + rune(low) - 0xdc00
					p.pos += 6
				}
			}
			sourceWriteCodepoint(&out, r)
		default:
			return "", p.failure(`Invalid \escape`, at)
		}
	}
	return "", p.failure("Unterminated string starting at", start)
}
func (p *sourceHTTPJSON) enter() error {
	p.depth++
	// Captured original Requests/Hares path in the fixed3.12 image accepts
	//9996 nested arrays below its root object, and fails on the next level.
	// This is that source runtime boundary, not Go's recursion capacity.
	if p.depth > 9997 {
		kind := "object"
		if p.text[p.pos] == '[' {
			kind = "array"
		}
		return &ValueError{"RecursionError", "maximum recursion depth exceeded while decoding a JSON " + kind + " from a unicode string"}
	}
	return nil
}
func (p *sourceHTTPJSON) object() (any, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	p.pos++
	p.space()
	out := sourceHTTPMap{values: Object{}, keys: []string{}}
	if p.pos < len(p.text) && p.text[p.pos] == '}' {
		p.pos++
		return out, nil
	}
	for {
		if p.pos >= len(p.text) || p.text[p.pos] != '"' {
			return nil, p.failure("Expecting property name enclosed in double quotes", p.pos)
		}
		key, err := p.stringValue()
		if err != nil {
			return nil, err
		}
		p.space()
		if p.pos >= len(p.text) || p.text[p.pos] != ':' {
			return nil, p.failure("Expecting ':' delimiter", p.pos)
		}
		p.pos++
		p.space()
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		if _, exists := out.values[key]; !exists {
			out.keys = append(out.keys, key)
		}
		out.values[key] = value
		p.space()
		if p.pos < len(p.text) && p.text[p.pos] == '}' {
			p.pos++
			return out, nil
		}
		if p.pos >= len(p.text) || p.text[p.pos] != ',' {
			return nil, p.failure("Expecting ',' delimiter", p.pos)
		}
		p.pos++
		p.space()
	}
}
func (p *sourceHTTPJSON) array() (any, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	p.pos++
	p.space()
	out := []any{}
	if p.pos < len(p.text) && p.text[p.pos] == ']' {
		p.pos++
		return out, nil
	}
	for {
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, value)
		p.space()
		if p.pos < len(p.text) && p.text[p.pos] == ']' {
			p.pos++
			return out, nil
		}
		if p.pos >= len(p.text) || p.text[p.pos] != ',' {
			return nil, p.failure("Expecting ',' delimiter", p.pos)
		}
		p.pos++
		p.space()
	}
}
