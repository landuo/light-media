package autosignin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// sourceCodepoints is private to original HTTP JSON/Python value behavior.
// A lone UTF16 codepoint uses its three WTF-8 bytes internally; valid Unicode
// strings keep their ordinary UTF8 representation. Native Go range is not a
// codepoint iterator for that private representation. Never send these raw
// bytes through ordinary string JSON encoding (which replaces them with FFFD).
func sourceCodepoints(text string) []rune {
	out := make([]rune, 0, len(text))
	for i := 0; i < len(text); {
		if i+2 < len(text) && text[i] == 0xed && text[i+1] >= 0xa0 && text[i+1] <= 0xbf && text[i+2] >= 0x80 && text[i+2] <= 0xbf {
			out = append(out, rune(text[i]&15)<<12|rune(text[i+1]&63)<<6|rune(text[i+2]&63))
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(text[i:])
		out = append(out, r)
		i += n
	}
	return out
}
func sourceWriteCodepoint(out *strings.Builder, r rune) {
	if r >= 0xd800 && r <= 0xdfff {
		out.WriteByte(byte(0xe0 | r>>12))
		out.WriteByte(byte(0x80 | (r>>6)&63))
		out.WriteByte(byte(0x80 | r&63))
		return
	}
	out.WriteRune(r)
}

// sourceJSONString always produces a legal escaped JSON string, including an
// unpaired surrogate. It preserves values without relaxing any IPC decoder.
func sourceJSONString(text string) []byte {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range sourceCodepoints(text) {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 || r >= 0xd800 && r <= 0xdfff {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return []byte(out.String())
}

// sourceHTTPMap retains json.loads insertion order, including the first slot
// of a duplicate key; credentials/configuration/history keep ordinary Object.
// It has no registry, hidden key, global state or independent authority.
type sourceHTTPMap struct {
	values Object
	keys   []string
}

func (m sourceHTTPMap) MarshalJSON() ([]byte, error) { return sourceHTTPJSONBytes(m) }
func sourceHTTPObject(value any) (Object, bool) {
	switch v := value.(type) {
	case Object:
		return v, true
	case sourceHTTPMap:
		return v.values, true
	}
	return nil, false
}
func sourceHTTPJSONBytes(value any) ([]byte, error) {
	var out bytes.Buffer
	var write func(any) error
	write = func(v any) error {
		switch x := v.(type) {
		case string:
			out.Write(sourceJSONString(x))
			return nil
		case sourceHTTPMap:
			out.WriteByte('{')
			for i, k := range x.keys {
				if i > 0 {
					out.WriteByte(',')
				}
				out.Write(sourceJSONString(k))
				out.WriteByte(':')
				if e := write(x.values[k]); e != nil {
					return e
				}
			}
			out.WriteByte('}')
			return nil
		case Object:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return write(sourceHTTPMap{x, keys})
		case []any:
			out.WriteByte('[')
			for i, v := range x {
				if i > 0 {
					out.WriteByte(',')
				}
				if e := write(v); e != nil {
					return e
				}
			}
			out.WriteByte(']')
			return nil
		case float64:
			if math.IsInf(x, 0) || math.IsNaN(x) {
				return fmt.Errorf("nonfinite source HTTP value cannot enter strict IPC JSON")
			}
		case json.Number:
			if strings.ContainsAny(string(x), ".eE") {
				f, _ := strconv.ParseFloat(string(x), 64)
				if math.IsInf(f, 0) || math.IsNaN(f) {
					return fmt.Errorf("nonfinite source HTTP value cannot enter strict IPC JSON")
				}
			}
		}
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		out.Write(b)
		return nil
	}
	if e := write(value); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func sourceContainerRepr(value any) string {
	switch x := value.(type) {
	case string:
		return sourceRepr(x)
	case sourceHTTPMap:
		items := make([]string, 0, len(x.keys))
		for _, k := range x.keys {
			items = append(items, sourceRepr(k)+": "+sourceContainerRepr(x.values[k]))
		}
		return "{" + strings.Join(items, ", ") + "}"
	case Object:
		if x == nil {
			return "None"
		} // Preserve the old Go nil-map JSON-null path.
		// Ordinary pre-existing Object behavior remains deterministic by key. Only
		// decoded HTTP maps carry a source insertion order.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return sourceContainerRepr(sourceHTTPMap{x, keys})
	case []any:
		if x == nil {
			return "None"
		} // Preserve the old Go nil-slice JSON-null path.
		items := make([]string, len(x))
		for i, v := range x {
			items[i] = sourceContainerRepr(v)
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		return pythonScalar(value)
	}
}
func sourceHistoryGet(value any, key string, fallback any) (any, error) {
	if object, ok := value.(Object); ok {
		if v, exists := object[key]; exists {
			return v, nil
		}
		return fallback, nil
	}
	return nil, fmt.Errorf("'%s' object has no attribute 'get'", typeName(value))
}
func sourceHistorySet(value any, key string, item any) error {
	if object, ok := value.(Object); ok {
		object[key] = item
		return nil
	}
	if _, ok := value.([]any); ok {
		return fmt.Errorf("list indices must be integers or slices, not str")
	}
	return fmt.Errorf("'%s' object does not support item assignment", typeName(value))
}

func sourceHasPrivateString(v any) bool {
	switch x := v.(type) {
	case string:
		for _, r := range sourceCodepoints(x) {
			if r >= 0xd800 && r <= 0xdfff {
				return true
			}
		}
	case sourceHTTPMap:
		return true
	case Object:
		for k, v := range x {
			if sourceHasPrivateString(k) || sourceHasPrivateString(v) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if sourceHasPrivateString(v) {
				return true
			}
		}
	}
	return false
}
func sourcePrivateClone(v any) any {
	switch x := v.(type) {
	case Object:
		out := Object{}
		for k, v := range x {
			out[k] = sourcePrivateClone(v)
		}
		return out
	case sourceHTTPMap:
		return sourceHTTPMap{sourcePrivateClone(x.values).(Object), append([]string{}, x.keys...)}
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = sourcePrivateClone(v)
		}
		return out
	default:
		return v
	}
}
func sourceMarshalBusinessJSON(v any) ([]byte, error) {
	if !sourceHasPrivateString(v) {
		return json.Marshal(v)
	}
	return sourceHTTPJSONBytes(v)
}

// Only valid escaped codepoints cross this wrapper. Standard SDK strict JSON
// framing/validation remains unchanged; nonfinite HTTP values cannot use it.
func sourceBusinessValue(v any) any {
	if !sourceHasPrivateString(v) {
		return v
	}
	raw, e := sourceMarshalBusinessJSON(v)
	if e != nil {
		panic(&sourceException{e})
	}
	return json.RawMessage(raw)
}
func sourceRestoreSavedJSON(raw []byte) (any, error) {
	// The standard validation is retained before the private string decoder.
	if !json.Valid(raw) {
		var v any
		e := Decode(raw, &v)
		if e == nil {
			e = fmt.Errorf("invalid strict saved JSON")
		}
		return nil, e
	}
	containsEscape := false
	for i := 0; i+6 <= len(raw); i++ {
		if raw[i] != '\\' || raw[i+1] != 'u' {
			continue
		}
		v, e := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if e == nil && v >= 0xd800 && v <= 0xdfff {
			containsEscape = true
			break
		}
	}
	if !containsEscape {
		var v any
		e := Decode(raw, &v)
		return v, e
	}
	v, e := parseSourceHTTPJSON(string(raw))
	if e != nil {
		return nil, e
	}
	var ordinary func(any) any
	ordinary = func(v any) any {
		switch x := v.(type) {
		case sourceHTTPMap:
			out := Object{}
			for _, k := range x.keys {
				out[k] = ordinary(x.values[k])
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, v := range x {
				out[i] = ordinary(v)
			}
			return out
		}
		return v
	}
	return ordinary(v), nil
}

// This is called only after a page/API result is fully constructed. The actual
// original FastAPI/Pydantic path fails here for unpaired codepoints; history's
// JSON column and internal raw KV have a different, escaping serializer.
func sourceWebSerializationFailure(value any) error {
	var visit func(any) error
	visit = func(v any) error {
		switch x := v.(type) {
		case string:
			points := sourceCodepoints(x)
			for i, r := range points {
				if r < 0xd800 || r > 0xdfff {
					continue
				}
				end := i + 1
				for end < len(points) && points[end] >= 0xd800 && points[end] <= 0xdfff {
					end++
				}
				message := fmt.Sprintf("'utf-8' codec can't encode character '\\u%04x' in position %d: surrogates not allowed", r, i)
				if end > i+1 {
					message = fmt.Sprintf("'utf-8' codec can't encode characters in position %d-%d: surrogates not allowed", i, end-1)
				}
				return &ValueError{"PydanticSerializationError", "Error serializing to JSON: UnicodeEncodeError: " + message}
			}
		case []any:
			for _, v := range x {
				if e := visit(v); e != nil {
					return e
				}
			}
		case Object:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if e := visit(k); e != nil {
					return e
				}
				if e := visit(x[k]); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return visit(value)
}
