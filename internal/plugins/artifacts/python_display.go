package artifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// PythonDisplay is an immutable, host-only projection of fields which
// the fixed _process_plugin_info assigns without Pydantic assignment validation.
// Nil means absent in the input; a present null/false/empty value remains stored
// as such. Value applies the original truthiness/default only for presentation.
// It is never an executable Manifest, index, installation or permission grant.
type PythonDisplay struct {
	Name, Description, Icon, Version, Author, History   json.RawMessage
	ID, PublicKey, Level, SystemVersion, Release, Label json.RawMessage
	wire                                                map[string]json.RawMessage
	wireErrors                                          map[string]bool
	VersionKey                                          string
}

func pythonDisplayTruthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("NaN")) || bytes.Equal(raw, []byte("Infinity")) || bytes.Equal(raw, []byte("-Infinity")) {
		return true
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	switch raw[0] {
	case '"':
		var value string
		_ = json.Unmarshal(raw, &value)
		return value != ""
	case '[':
		if prepared, _, err := pythonJSONPrepare(raw); err == nil {
			raw = prepared
		}
		var value []json.RawMessage
		_ = json.Unmarshal(raw, &value)
		return len(value) > 0
	case '{':
		if prepared, _, err := pythonJSONPrepare(raw); err == nil {
			raw = prepared
		}
		var value map[string]json.RawMessage
		_ = json.Unmarshal(raw, &value)
		return len(value) > 0
	case 't':
		return true
	default:
		if strings.ContainsAny(string(raw), ".eE") {
			number, _ := strconv.ParseFloat(string(raw), 64)
			return number != 0
		}
		return strings.Trim(string(raw), "-0") != ""
	}
}
func pythonDisplayValue(raw json.RawMessage, fallback string) json.RawMessage {
	if !pythonDisplayTruthy(raw) {
		return json.RawMessage(fallback)
	}
	return raw
}
func pythonDisplayText(raw json.RawMessage) string {
	var value string
	if len(raw) > 0 && bytes.TrimSpace(raw)[0] == '"' {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}
func (p *PythonDisplay) sourceValues() map[string]json.RawMessage {
	level := p.Level
	if len(level) == 0 {
		level = json.RawMessage("0")
	}
	if bytes.Equal(bytes.TrimSpace(level), []byte("true")) {
		level = json.RawMessage("1")
	}
	if bytes.Equal(bytes.TrimSpace(level), []byte("false")) {
		level = json.RawMessage("0")
	}
	release, _ := json.Marshal(pythonDisplayTruthy(p.Release))
	values := map[string]json.RawMessage{
		"name": pythonDisplayValue(p.Name, "null"), "description": pythonDisplayValue(p.Description, "null"),
		"icon": pythonDisplayValue(p.Icon, "null"), "version": pythonDisplayValue(p.Version, "null"),
		"author": pythonDisplayValue(p.Author, "null"), "history": pythonDisplayValue(p.History, "{}"),
		"author_url": json.RawMessage("null"), "plugin_public_key": pythonDisplayValue(p.PublicKey, "null"), "auth_level": level,
		"system_version": pythonDisplayValue(p.SystemVersion, "null"), "release": release, "plugin_label": pythonDisplayValue(p.Label, "null"),
	}
	if len(p.ID) > 0 {
		values["plugin_id"], values["instance_id"] = p.ID, p.ID
	}
	return values
}
func (p *PythonDisplay) Values() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, 6)
	for _, key := range []string{"name", "description", "icon", "version", "author", "history"} {
		out[key] = p.wire[key]
	}
	return out
}

// CardValues includes the additional inert fields assigned by the source.
func (p *PythonDisplay) CardValues() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(p.wire))
	for key, value := range p.wire {
		out[key] = value
	}
	return out
}
func (p *PythonDisplay) StringVersion() (string, bool) {
	raw := pythonDisplayValue(p.Version, "null")
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	return p.VersionKey, true
}
func (p *PythonDisplay) initialize() error {
	key, err := pythonDisplayVersionKey(pythonDisplayValue(p.Version, "null"))
	if err != nil {
		return err
	}
	p.VersionKey = key
	p.wire = make(map[string]json.RawMessage, 6)
	p.wireErrors = make(map[string]bool)
	for field, raw := range p.sourceValues() {
		normalized, invalidUnicode, err := pythonDisplayWire(raw)
		if err != nil {
			return err
		}
		// The source cache owns the successfully decoded dictionary even when its
		// surviving output later cannot be serialized. Overwritten values do not
		// participate in that rendering failure.
		if invalidUnicode {
			p.wireErrors[field] = true
			p.wire[field] = json.RawMessage("null")
		} else {
			p.wire[field] = normalized
		}
	}

	return nil
}

// Python json.loads retains integers but rounds decimal/exponent tokens to
// binary64. Pydantic's response serializer emits nonfinite values as null.
// This affects wire values only: raw fields still determine truthiness, concat
// keys and version comparisons. Object insertion order and last duplicate value
// follow the original JSON dictionary, rather than sorted Go map output.
func pythonDisplayWire(raw json.RawMessage) (json.RawMessage, bool, error) {
	prepared, constants, err := pythonJSONPrepare(raw)
	if err != nil {
		return nil, false, err
	}
	for marker := range constants {
		prepared = bytes.ReplaceAll(prepared, []byte(marker), []byte("1e999"))
	}
	raw = prepared
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, invalid, err := pythonDisplayWireValue(decoder, raw)
	if err != nil {
		return nil, false, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, false, errors.New("trailing Python display value")
	}
	return value, invalid, nil
}
func pythonDisplayTokenRaw(input []byte, start, end int64) json.RawMessage {
	return bytes.TrimLeft(input[start:end], " \t\r\n,:")
}
func pythonDisplayWireValue(decoder *json.Decoder, input []byte) (json.RawMessage, bool, error) {
	start := decoder.InputOffset()
	token, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}
	switch value := token.(type) {
	case string:
		raw := pythonDisplayTokenRaw(input, start, decoder.InputOffset())
		_, invalid := pythonDisplaySurrogates(raw, nil)
		encoded, err := json.Marshal(value)
		return encoded, invalid > 0, err
	case json.Number:
		if strings.ContainsAny(value.String(), ".eE") {
			number, err := strconv.ParseFloat(value.String(), 64)
			if math.IsInf(number, 0) || math.IsNaN(number) {
				return json.RawMessage("null"), false, nil
			}
			if err != nil {
				return nil, false, err
			}
			encoded, err := json.Marshal(number)
			return encoded, false, err
		}
		integer, ok := new(big.Int).SetString(value.String(), 10)
		if !ok {
			return nil, false, errors.New("invalid Python display integer")
		}
		return json.RawMessage(integer.String()), false, nil
	case json.Delim:
		switch value {
		case '[':
			values := []json.RawMessage{}
			invalid := false
			for decoder.More() {
				item, bad, err := pythonDisplayWireValue(decoder, input)
				if err != nil {
					return nil, false, err
				}
				values = append(values, item)
				invalid = invalid || bad
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
				return nil, false, errors.New("invalid Python display array")
			}
			encoded, err := json.Marshal(values)
			return encoded, invalid, err
		case '{':
			type member struct {
				key, value json.RawMessage
				invalid    bool
			}
			order := []string{}
			values := map[string]member{}
			for decoder.More() {
				start := decoder.InputOffset()
				token, err := decoder.Token()
				key, ok := token.(string)
				if err != nil || !ok {
					return nil, false, errors.New("invalid Python display key")
				}
				rawKey := pythonDisplayTokenRaw(input, start, decoder.InputOffset())
				identity, err := pythonDisplayVersionKey(rawKey)
				if err != nil {
					return nil, false, err
				}
				_, badKey := pythonDisplaySurrogates(rawKey, nil)
				item, badValue, err := pythonDisplayWireValue(decoder, input)
				if err != nil {
					return nil, false, err
				}
				quoted, _ := json.Marshal(key)
				if _, exists := values[identity]; !exists {
					order = append(order, identity)
				}
				// Like json.loads, a later equal key replaces both the old value and
				// its error state, while retaining that key's first insertion position.
				values[identity] = member{quoted, item, badKey > 0 || badValue}
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
				return nil, false, errors.New("invalid Python display object")
			}
			var output bytes.Buffer
			output.WriteByte('{')
			invalid := false
			for i, key := range order {
				if i > 0 {
					output.WriteByte(',')
				}
				v := values[key]
				output.Write(v.key)
				output.WriteByte(':')
				output.Write(v.value)
				invalid = invalid || v.invalid
			}
			output.WriteByte('}')
			return output.Bytes(), invalid, nil
		}
	}
	encoded, err := json.Marshal(token)
	return encoded, false, err
}

func (p *PythonDisplay) WireError(fields ...string) error {
	if len(fields) == 0 {
		for field := range p.wireErrors {
			fields = append(fields, field)
		}
	}
	for _, field := range fields {
		if p.wireErrors[field] {
			return errors.New("source Python display cannot be serialized")
		}
	}
	return nil
}

// Only isolated escaped UTF-16 code units are rewritten. Escaped backslashes
// and well-formed pairs retain their original bytes. The callback's replacement
// is owned ASCII within an already valid JSON string, never executable content.
func pythonDisplaySurrogates(raw []byte, replace func(uint16) string) ([]byte, int) {
	var out bytes.Buffer
	last, count := 0, 0
	quoted := false
	for i := 0; i < len(raw); {
		if raw[i] == '"' {
			quoted = !quoted
			i++
			continue
		}
		if !quoted || raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			break
		}
		if raw[i+1] != 'u' || i+6 > len(raw) {
			i += 2
			continue
		}
		code, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			i += 2
			continue
		}
		if code < 0xd800 || code > 0xdfff {
			i += 6
			continue
		}
		if code <= 0xdbff && i+12 <= len(raw) && raw[i+6] == '\\' && raw[i+7] == 'u' {
			low, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err == nil && low >= 0xdc00 && low <= 0xdfff {
				i += 12
				continue
			}
		}
		count++
		if replace != nil {
			out.Write(raw[last:i])
			out.WriteString(replace(uint16(code)))
			last = i + 6
		}
		i += 6
	}
	if replace == nil {
		return nil, count
	}
	out.Write(raw[last:])
	return out.Bytes(), count
}
func pythonDisplayVersionKey(raw json.RawMessage) (string, error) {
	plain, err := pythonSourceString(raw)
	if err != nil {
		return "", err
	}
	if _, count := pythonDisplaySurrogates(raw, nil); count == 0 {
		return plain, nil
	}
	prefix := fmt.Sprintf("__source_surrogate_%x_", sha256.Sum256(raw))
	for strings.Contains(plain, prefix) {
		prefix += "_"
	}
	rootString := bytes.TrimSpace(raw)[0] == '"'
	replacements := []string{}
	markers := map[uint16]string{}
	rewritten, _ := pythonDisplaySurrogates(raw, func(code uint16) string {
		if marker, ok := markers[code]; ok {
			return marker
		}
		marker := fmt.Sprintf("%s%d__", prefix, len(replacements)/2)
		markers[code] = marker
		value := fmt.Sprintf(`\u%04x`, code)
		if rootString {
			value = string([]byte{byte(0xe0 | code>>12), byte(0x80 | (code>>6)&0x3f), byte(0x80 | code&0x3f)})
		}
		replacements = append(replacements, marker, value)
		return marker
	})
	result, err := pythonSourceString(rewritten)
	if err != nil {
		return "", err
	}
	return strings.NewReplacer(replacements...).Replace(result), nil
}
