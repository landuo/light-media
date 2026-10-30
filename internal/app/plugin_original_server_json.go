package app

// The center's HTTP client and FastAPI use Python json.loads, not strict JSON:
// bare NaN/Infinity and float overflow are values. Keep this decoder scoped to
// that boundary; the SDK, service progress and browser storage stay unchanged.
import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type originalCenterJSONError struct {
	position int
	message  string
}

func (e *originalCenterJSONError) Error() string { return "invalid plugin center JSON" }

type originalCenterUnpairedString string
type originalCenterJSONParser struct {
	raw        []byte
	pos, depth int
}

func parseOriginalCenterJSON(raw []byte) (any, error) {
	var decodeErr error
	raw, decodeErr = originalCenterJSONBytes(raw)
	if decodeErr != nil {
		return nil, decodeErr
	}
	p := originalCenterJSONParser{raw: raw}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(raw) {
		return nil, p.fail("Extra data", p.pos)
	}
	return v, nil
}
func (p *originalCenterJSONParser) fail(message string, at int) error {
	return &originalCenterJSONError{originalCenterRuneCount(p.raw[:at]), message}
}
func (p *originalCenterJSONParser) space() {
	for p.pos < len(p.raw) && strings.ContainsRune(" \t\r\n", rune(p.raw[p.pos])) {
		p.pos++
	}
}
func (p *originalCenterJSONParser) stringValue() (any, error) {
	start := p.pos
	p.pos++
	for p.pos < len(p.raw) {
		c := p.raw[p.pos]
		if c == '"' {
			p.pos++
			raw := originalCenterQuotedJSON(p.raw[start:p.pos])
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return nil, p.fail("Invalid string", start)
			}
			// Do not silently replace a Python isolated surrogate with RuneError.
			node, _ := parseSharedSubscriptionJSON(raw)
			if node.hasUnpaired() {
				var encoded strings.Builder
				for _, r := range node.points {
					if r >= 0xd800 && r <= 0xdfff {
						encoded.WriteByte(byte(0xe0 | (r >> 12)))
						encoded.WriteByte(byte(0x80 | ((r >> 6) & 0x3f)))
						encoded.WriteByte(byte(0x80 | (r & 0x3f)))
					} else {
						encoded.WriteRune(r)
					}
				}
				return originalCenterUnpairedString(encoded.String()), nil
			}
			return value, nil
		}
		if c < 32 {
			return nil, p.fail("Invalid control character at", p.pos)
		}
		if c == '\\' {
			escape := p.pos
			p.pos++
			if p.pos == len(p.raw) {
				break
			}
			if p.raw[p.pos] == 'u' {
				if p.pos+4 >= len(p.raw) {
					return nil, p.fail("Invalid \\uXXXX escape", p.pos)
				}
				if strings.IndexFunc(string(p.raw[p.pos+1:p.pos+5]), func(r rune) bool { return !strings.ContainsRune("0123456789abcdefABCDEF", r) }) >= 0 {
					return nil, p.fail("Invalid \\uXXXX escape", p.pos)
				}
				p.pos += 5
				continue
			}
			if !strings.ContainsRune(`"\/bfnrt`, rune(p.raw[p.pos])) {
				return nil, p.fail("Invalid \\escape", escape)
			}
		}
		p.pos++
	}
	return nil, p.fail("Unterminated string starting at", start)
}
func (p *originalCenterJSONParser) value() (any, error) {
	p.space()
	start := p.pos
	if start == len(p.raw) {
		return nil, p.fail("Expecting value", start)
	}
	if p.depth >= 1000 {
		return nil, errors.New("plugin center JSON nesting exceeds source decoder limit")
	}
	p.depth++
	defer func() { p.depth-- }()
	switch p.raw[p.pos] {
	case '"':
		return p.stringValue()
	case '{':
		p.pos++
		p.space()
		out := map[string]any{}
		if p.pos < len(p.raw) && p.raw[p.pos] == '}' {
			p.pos++
			return out, nil
		}
		for {
			if p.pos == len(p.raw) || p.raw[p.pos] != '"' {
				return nil, p.fail("Expecting property name enclosed in double quotes", p.pos)
			}
			key, e := p.stringValue()
			if e != nil {
				return nil, e
			}
			name, ok := key.(string)
			if !ok {
				name = string(key.(originalCenterUnpairedString))
			}
			p.space()
			if p.pos == len(p.raw) || p.raw[p.pos] != ':' {
				return nil, p.fail("Expecting ':' delimiter", p.pos)
			}
			p.pos++
			v, e := p.value()
			if e != nil {
				return nil, e
			}
			out[name] = v
			p.space()
			if p.pos < len(p.raw) && p.raw[p.pos] == '}' {
				p.pos++
				return out, nil
			}
			if p.pos == len(p.raw) || p.raw[p.pos] != ',' {
				return nil, p.fail("Expecting ',' delimiter", p.pos)
			}
			p.pos++
			p.space()
		}
	case '[':
		p.pos++
		p.space()
		out := []any{}
		if p.pos < len(p.raw) && p.raw[p.pos] == ']' {
			p.pos++
			return out, nil
		}
		for {
			v, e := p.value()
			if e != nil {
				return nil, e
			}
			out = append(out, v)
			p.space()
			if p.pos < len(p.raw) && p.raw[p.pos] == ']' {
				p.pos++
				return out, nil
			}
			if p.pos == len(p.raw) || p.raw[p.pos] != ',' {
				return nil, p.fail("Expecting ',' delimiter", p.pos)
			}
			p.pos++
		}
	}
	for _, literal := range []struct {
		text  string
		value any
	}{{"null", nil}, {"true", true}, {"false", false}, {"NaN", math.NaN()}, {"Infinity", math.Inf(1)}, {"-Infinity", math.Inf(-1)}} {
		if bytes.HasPrefix(p.raw[start:], []byte(literal.text)) {
			p.pos += len(literal.text)
			return literal.value, nil
		}
	}
	if p.raw[p.pos] == '-' {
		p.pos++
	}
	if p.pos == len(p.raw) || p.raw[p.pos] < '0' || p.raw[p.pos] > '9' {
		p.pos = start
		return nil, p.fail("Expecting value", start)
	}
	if p.raw[p.pos] == '0' {
		p.pos++
	} else {
		for p.pos < len(p.raw) && p.raw[p.pos] >= '0' && p.raw[p.pos] <= '9' {
			p.pos++
		}
	}
	if p.pos+1 < len(p.raw) && p.raw[p.pos] == '.' && p.raw[p.pos+1] >= '0' && p.raw[p.pos+1] <= '9' {
		p.pos += 2
		for p.pos < len(p.raw) && p.raw[p.pos] >= '0' && p.raw[p.pos] <= '9' {
			p.pos++
		}
	}
	if p.pos < len(p.raw) && (p.raw[p.pos] == 'e' || p.raw[p.pos] == 'E') {
		at := p.pos + 1
		if at < len(p.raw) && (p.raw[at] == '+' || p.raw[at] == '-') {
			at++
		}
		first := at
		for at < len(p.raw) && p.raw[at] >= '0' && p.raw[at] <= '9' {
			at++
		}
		if at > first {
			p.pos = at
		}
	}
	text := string(p.raw[start:p.pos])
	if !strings.ContainsAny(text, ".eE") && len(strings.TrimPrefix(text, "-")) > 4300 {
		return nil, errors.New("plugin center integer exceeds source decoder limit")
	}
	if strings.ContainsAny(text, ".eE") {
		value, _ := strconv.ParseFloat(text, 64)
		if math.IsInf(value, 0) {
			return value, nil
		}
	}
	return json.Number(text), nil
}

// Pydantic's JSON serializer converts non-finite values to null recursively.
// Decimal/exponent JSON values have first passed through Python binary64;
// integer lexemes retain arbitrary precision. No diagnostic contains payloads.
func originalCenterJSONValue(value any) (any, error) {
	switch value := value.(type) {
	case originalCenterUnpairedString:
		return nil, errors.New("plugin center Unicode serialization failure")
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, nil
		}
		return value, nil
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			f, e := strconv.ParseFloat(string(value), 64)
			if math.IsInf(f, 0) {
				return nil, nil
			}
			if e != nil && f != 0 {
				return nil, e
			}
			return f, nil
		}
		return value, nil
	case []any:
		out := make([]any, len(value))
		for i, v := range value {
			x, e := originalCenterJSONValue(v)
			if e != nil {
				return nil, e
			}
			out[i] = x
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, v := range value {
			if !utf8.ValidString(k) {
				return nil, errors.New("plugin center Unicode serialization failure")
			}
			x, e := originalCenterJSONValue(v)
			if e != nil {
				return nil, e
			}
			out[k] = x
		}
		return out, nil
	default:
		return value, nil
	}
}
