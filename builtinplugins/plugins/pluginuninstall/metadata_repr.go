package pluginuninstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Private Python container repr for source get_form metadata. The finite JSON
// decoder/formatter is reused from the source-verified torrenttransfer formatter;
// it preserves dictionary order, numeric kind and nested string escaping.
type metadataText []rune
type metadataPair struct {
	key   metadataText
	value any
}
type metadataObject []metadataPair

func decodeMetadataValue(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing JSON wire value")
	}
	if raw[0] == '"' {
		return decodeMetadataText(raw)
	}
	if raw[0] == '{' || raw[0] == '[' {
		d := json.NewDecoder(bytes.NewReader(raw))
		if _, e := d.Token(); e != nil {
			return nil, e
		}
		if raw[0] == '{' {
			out := metadataObject{}
			for d.More() {
				before := d.InputOffset()
				if _, e := d.Token(); e != nil {
					return nil, e
				}
				end := d.InputOffset()
				keyRaw := bytes.TrimLeft(raw[before:end], ", \t\r\n")
				key, e := decodeMetadataText(keyRaw)
				if e != nil {
					return nil, e
				}
				var child json.RawMessage
				if e = d.Decode(&child); e != nil {
					return nil, e
				}
				v, e := decodeMetadataValue(bytes.TrimSpace(child))
				if e != nil {
					return nil, e
				}
				// json.loads dict duplicate keys overwrite without moving insertion order.
				replaced := false
				for i := range out {
					if slices.Equal(out[i].key, key) {
						out[i].value = v
						replaced = true
						break
					}
				}
				if !replaced {
					out = append(out, metadataPair{key, v})
				}
			}
			_, e := d.Token()
			return out, e
		}
		out := []any{}
		for d.More() {
			var child json.RawMessage
			if e := d.Decode(&child); e != nil {
				return nil, e
			}
			v, e := decodeMetadataValue(bytes.TrimSpace(child))
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		_, e := d.Token()
		return out, e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	return v, nil
}
func decodeMetadataText(raw []byte) (metadataText, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, errors.New("invalid JSON string")
	}
	out := metadataText{}
	for i := 1; i < len(raw)-1; {
		if raw[i] != '\\' {
			r, n := utf8.DecodeRune(raw[i : len(raw)-1])
			if n == 0 {
				return nil, errors.New("invalid text")
			}
			out = append(out, r)
			i += n
			continue
		}
		i++
		if i >= len(raw)-1 {
			return nil, errors.New("invalid JSON escape")
		}
		switch raw[i] {
		case '"', '\\', '/':
			out = append(out, rune(raw[i]))
			i++
		case 'b':
			out = append(out, '\b')
			i++
		case 'f':
			out = append(out, '\f')
			i++
		case 'n':
			out = append(out, '\n')
			i++
		case 'r':
			out = append(out, '\r')
			i++
		case 't':
			out = append(out, '\t')
			i++
		case 'u':
			if i+5 > len(raw)-1 {
				return nil, errors.New("invalid unicode escape")
			}
			n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if e != nil {
				return nil, e
			}
			i += 5
			r := rune(n)
			if r >= 0xd800 && r <= 0xdbff && i+6 <= len(raw)-1 && raw[i] == '\\' && raw[i+1] == 'u' {
				low, e := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if e == nil && low >= 0xdc00 && low <= 0xdfff {
					r = 0x10000 + (r-0xd800)*0x400 + rune(low) - 0xdc00
					i += 6
				}
			}
			out = append(out, r)
		default:
			return nil, errors.New("invalid JSON escape")
		}
	}
	return out, nil
}
func pythonMetadataRepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case metadataText:
		return pythonTextRepr(x)
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			n, ok := new(big.Int).SetString(string(x), 10)
			if ok {
				return n.String()
			}
		}
		f, _ := strconv.ParseFloat(string(x), 64)
		if math.IsInf(f, 1) {
			return "inf"
		}
		if math.IsInf(f, -1) {
			return "-inf"
		}
		if math.IsNaN(f) {
			return "nan"
		}
		scientific := strconv.FormatFloat(f, 'e', -1, 64)
		idx := strings.LastIndexByte(scientific, 'e')
		exp, _ := strconv.Atoi(scientific[idx+1:])
		if exp >= -4 && exp < 16 {
			value := strconv.FormatFloat(f, 'f', -1, 64)
			if !strings.ContainsRune(value, '.') {
				value += ".0"
			}
			return value
		}
		return scientific
	case []any:
		parts := make([]string, len(x))
		for i, v := range x {
			parts[i] = pythonMetadataRepr(v)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case metadataObject:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = pythonTextRepr(p.key) + ": " + pythonMetadataRepr(p.value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return "None"
}
func pythonTextRepr(text metadataText) string {
	quote := rune('\'')
	hasSingle, hasDouble := false, false
	for _, r := range text {
		hasSingle = hasSingle || r == '\''
		hasDouble = hasDouble || r == '"'
	}
	if hasSingle && !hasDouble {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range text {
		switch {
		case r == quote || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case utf8.ValidRune(r) && unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}

// Only metadata display attributes need container repr; order keys and IDs
// retain their ordinary JSON types for comparison and option values.
type metadataContainer json.RawMessage

func (p *LocalPlugin) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	out := LocalPlugin{}
	for key, raw := range fields {
		if (key == "plugin_name" || key == "plugin_version") && len(raw) > 0 && (raw[0] == '[' || raw[0] == '{') {
			out[key] = metadataContainer(bytes.Clone(raw))
			continue
		}
		var v any
		if err := decode(raw, &v); err != nil {
			return err
		}
		out[key] = v
	}
	*p = out
	return nil
}
func (v metadataContainer) MarshalJSON() ([]byte, error) { return []byte(v), nil }
