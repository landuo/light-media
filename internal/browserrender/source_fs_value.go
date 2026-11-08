package browserrender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	"math"
	"strings"
)

// Only the fixed FS envelope's fields are extracted. Standard Go JSON validates
// the structure; equal-width numeric masks admit CPython's three constants
// while offsets still select the original value bytes. No values are evaluated
// or rewritten into the source consumer's result.
func sourceFSJSONMask(raw []byte) []byte {
	out := append([]byte(nil), raw...)
	quoted, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		for _, r := range []struct{ from, to string }{{"-Infinity", "-0.000000"}, {"Infinity", "0.000000"}, {"NaN", "0e0"}} {
			if bytes.HasPrefix(raw[i:], []byte(r.from)) && sourceFSJSONTokenBoundary(raw, i, i+len(r.from)) {
				copy(out[i:i+len(r.from)], r.to)
				i += len(r.from) - 1
				break
			}
		}
	}
	return out
}
func sourceFSJSONFields(raw []byte) (map[string]json.RawMessage, error) {
	masked := sourceFSJSONMask(raw)
	if !json.Valid(masked) || !sourceFSJSONLimits(masked) {
		return nil, fmt.Errorf("invalid FS JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(masked))
	token, e := dec.Token()
	if e != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("FS object required")
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		key, e := dec.Token()
		if e != nil {
			return nil, e
		}
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("FS key required")
		}
		var value json.RawMessage
		if e = dec.Decode(&value); e != nil {
			return nil, e
		}
		end := int(dec.InputOffset())
		start := end - len(value)
		out[name] = append(json.RawMessage(nil), raw[start:end]...)
	}
	if _, e = dec.Token(); e != nil {
		return nil, e
	}
	if _, e = dec.Token(); e != io.EOF {
		return nil, fmt.Errorf("FS trailing JSON")
	}
	return out, nil
}
func sourceFSScalarValue(raw json.RawMessage, decoded any) any {
	switch string(bytes.TrimSpace(raw)) {
	case "NaN":
		return math.NaN()
	case "Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	return decoded
}

// RequestUtils.post_json uses the pinned Requests Response.json selection:
// a configured header encoding skips JSON UTF guessing; only UnicodeDecodeError
// falls through to Response.text apparent-encoding/replacement handling.
func sourceFSResponseText(data []byte, contentType string) ([]byte, error) {
	tokens := strings.Split(contentType, ";")
	media := strings.TrimSpace(tokens[0])
	params := map[string]string{}
	for _, param := range tokens[1:] {
		if key, value, ok := strings.Cut(strings.TrimSpace(param), "="); ok {
			params[strings.ToLower(strings.Trim(key, "\"' "))] = strings.Trim(value, "\"' ")
		}
	}
	name, present := params["charset"]
	if !present {
		if strings.Contains(media, "text") {
			name = "ISO-8859-1"
		} else if strings.Contains(media, "application/json") {
			name = "utf-8"
		}
	}
	if name == "" && len(data) > 3 {
		guessed := ""
		switch {
		case bytes.HasPrefix(data, []byte{0xff, 0xfe, 0, 0}) || bytes.HasPrefix(data, []byte{0, 0, 0xfe, 0xff}):
			guessed = "utf-32"
		case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
			guessed = "utf-8-sig"
		case bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}):
			guessed = "utf-16"
		default:
			sample := data[:4]
			nulls := bytes.Count(sample, []byte{0})
			if nulls == 0 {
				guessed = "utf-8"
			} else if nulls == 2 {
				if sample[0] == 0 && sample[2] == 0 {
					guessed = "utf-16-be"
				} else if sample[1] == 0 && sample[3] == 0 {
					guessed = "utf-16-le"
				}
			} else if nulls == 3 {
				if sample[0] == 0 && sample[1] == 0 && sample[2] == 0 {
					guessed = "utf-32-be"
				} else if sample[1] == 0 && sample[2] == 0 && sample[3] == 0 {
					guessed = "utf-32-le"
				}
			}
		}
		if guessed != "" {
			text, err := encodingdetector.Decode(data, guessed, false)
			if err == nil {
				return []byte(text), nil
			}
			if encodingdetector.ErrorClass(err) != "UnicodeDecodeError" {
				return nil, err
			}
		}
	}
	if name == "" {
		var err error
		name, _, err = encodingdetector.Detect(data)
		if err != nil {
			return nil, err
		}
	}
	text, err := encodingdetector.Decode(data, name, true)
	if encodingdetector.ErrorClass(err) == "LookupError" || encodingdetector.ErrorClass(err) == "TypeError" {
		text, err = encodingdetector.Decode(data, "utf-8", true)
	}
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
}

// The actual fixed Requests/private-FS source accepts 9995 response arrays
// below its two envelope objects; 9996 arrays and 4301-digit integers fail.
func sourceFSJSONLimits(raw []byte) bool {
	quoted, escaped := false, false
	depth := 0
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c == '{' || c == '[' {
			depth++
			if depth > 9997 {
				return false
			}
		} else if c == '}' || c == ']' {
			depth--
		}
		if c == '-' || c >= '0' && c <= '9' {
			start := i
			floating := false
			for i+1 < len(raw) {
				n := raw[i+1]
				if n >= '0' && n <= '9' || n == '.' || n == 'e' || n == 'E' || n == '+' || n == '-' {
					i++
					floating = floating || n == '.' || n == 'e' || n == 'E'
				} else {
					break
				}
			}
			if !floating {
				digits := i - start + 1
				if raw[start] == '-' {
					digits--
				}
				if digits > 4300 {
					return false
				}
			}
		}
	}
	return true
}

// Only full Python JSON constants may be masked. Never turn an invalid
// prefix/suffix or signed NaN into a valid Go number in an unused field.
func sourceFSJSONTokenBoundary(raw []byte, start, end int) bool {
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
	if start > 0 {
		c := raw[start-1]
		if !space(c) && c != '[' && c != ',' && c != ':' {
			return false
		}
	}
	if end < len(raw) {
		c := raw[end]
		if !space(c) && c != ']' && c != '}' && c != ',' {
			return false
		}
	}
	return true
}
