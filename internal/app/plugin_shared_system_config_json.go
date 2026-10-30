package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"light-media/internal/plugins"
)

// Preserve Python strings' complete code points, including unpaired UTF-16
// escapes. encoding/json's string decoder replaces those with U+FFFD.
func sharedSystemConfigString(raw json.RawMessage) ([]rune, bool, error) {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	invalid := false
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			invalid = true
		}
	}
	return points, invalid, err
}
func sharedSystemConfigQuote(points []rune) json.RawMessage {
	// All inputs come from the validated source JSON/codepoint parser.
	raw, _ := plugins.EncodeSystemConfigJSONString(points)
	return raw
}

type sharedSystemConfigMember struct {
	key   json.RawMessage
	value json.RawMessage
}

func sharedSystemConfigMembers(raw json.RawMessage) ([]sharedSystemConfigMember, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if _, e := d.Token(); e != nil {
		return nil, e
	}
	out := []sharedSystemConfigMember{}
	for d.More() {
		start := d.InputOffset()
		if _, e := d.Token(); e != nil {
			return nil, e
		}
		key := bytes.TrimLeft(raw[start:d.InputOffset()], " \n\r\t,")
		r, _, e := sharedSystemConfigString(key)
		if e != nil {
			return nil, e
		}
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, e
		}
		out = append(out, sharedSystemConfigMember{sharedSystemConfigQuote(r), value})
	}
	return out, nil
}

// Objects remain ordered and duplicate keys retain their first position.
func sharedSystemConfigJSON(raw json.RawMessage) (json.RawMessage, bool, error) {
	raw = bytes.TrimSpace(raw)
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, false, errors.New("invalid configuration JSON")
	}
	switch raw[0] {
	case '"':
		r, _, e := sharedSystemConfigString(raw)
		return sharedSystemConfigQuote(r), false, e
	case '[':
		var values []json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return nil, false, e
		}
		nonfinite := false
		for i, value := range values {
			v, n, e := sharedSystemConfigJSON(value)
			if e != nil {
				return nil, false, e
			}
			values[i] = v
			nonfinite = nonfinite || n
		}
		encoded, e := json.Marshal(values)
		return encoded, nonfinite, e
	case '{':
		members, e := sharedSystemConfigMembers(raw)
		if e != nil {
			return nil, false, e
		}
		keys := []string{}
		values := map[string]json.RawMessage{}
		nonfinite := false
		for _, member := range members {
			key := string(member.key)
			if _, ok := values[key]; !ok {
				keys = append(keys, key)
			}
			value, n, e := sharedSystemConfigJSON(member.value)
			if e != nil {
				return nil, false, e
			}
			values[key] = value
			nonfinite = nonfinite || n
		}
		var b bytes.Buffer
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(key)
			b.WriteByte(':')
			b.Write(values[key])
		}
		b.WriteByte('}')
		return b.Bytes(), nonfinite, nil
	case 'n', 't', 'f':
		return append(json.RawMessage(nil), raw...), false, nil
	default:
		if !bytes.ContainsAny(raw, ".eE") {
			return append(json.RawMessage(nil), raw...), false, nil
		}
		f, e := strconv.ParseFloat(string(raw), 64)
		if e != nil && !math.IsInf(f, 0) {
			return nil, false, e
		}
		if math.IsInf(f, -1) {
			return json.RawMessage(`-1e999`), true, nil
		}
		if math.IsInf(f, 1) {
			return json.RawMessage(`1e999`), true, nil
		}
		text := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return json.RawMessage(text), false, nil
	}
}
func sharedSystemConfigUnicodeInvalid(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case '"':
		_, invalid, _ := sharedSystemConfigString(raw)
		return invalid
	case '[':
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		for _, v := range values {
			if sharedSystemConfigUnicodeInvalid(v) {
				return true
			}
		}
	case '{':
		members, _ := sharedSystemConfigMembers(raw)
		for _, m := range members {
			if sharedSystemConfigUnicodeInvalid(m.key) || sharedSystemConfigUnicodeInvalid(m.value) {
				return true
			}
		}
	}
	return false
}
func sharedSystemConfigEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	a = bytes.TrimSpace(a)
	b = bytes.TrimSpace(b)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	switch a[0] {
	case '"':
		if b[0] != '"' {
			return false
		}
		ar, _, ae := sharedSystemConfigString(a)
		br, _, be := sharedSystemConfigString(b)
		return ae == nil && be == nil && bytes.Equal(sharedSystemConfigQuote(ar), sharedSystemConfigQuote(br))
	case '[':
		if b[0] != '[' {
			return false
		}
		var av, bv []json.RawMessage
		if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !sharedSystemConfigEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case '{':
		if b[0] != '{' {
			return false
		}
		am, ae := sharedSystemConfigMembers(a)
		bm, be := sharedSystemConfigMembers(b)
		if ae != nil || be != nil || len(am) != len(bm) {
			return false
		}
		index := map[string]json.RawMessage{}
		for _, v := range bm {
			index[string(v.key)] = v.value
		}
		for _, v := range am {
			other, ok := index[string(v.key)]
			if !ok || !sharedSystemConfigEqual(v.value, other) {
				return false
			}
		}
		return true
	default:
		return originalBaseJSONEqual(a, b)
	}
}

// Source schemas.Response/Pydantic serializes nonfinite floats as null. This is
// a response-only view; neither the singleton cache nor its sealed row changes.
func sharedSystemConfigUIJSON(raw json.RawMessage) (json.RawMessage, error) {
	normalized, n, e := sharedSystemConfigJSON(raw)
	if e != nil {
		return nil, e
	}
	if sharedSystemConfigUnicodeInvalid(normalized) {
		return nil, bad("SYSTEM_CONFIG_JSON_UNICODE", "当前识别词无法编码为Unicode JSON")
	}
	if !n {
		return normalized, nil
	}
	if normalized[0] == '[' {
		var values []json.RawMessage
		_ = json.Unmarshal(normalized, &values)
		for i, v := range values {
			values[i], e = sharedSystemConfigUIJSON(v)
			if e != nil {
				return nil, e
			}
		}
		return json.Marshal(values)
	}
	if normalized[0] == '{' {
		members, e := sharedSystemConfigMembers(normalized)
		if e != nil {
			return nil, e
		}
		var b bytes.Buffer
		b.WriteByte('{')
		for i, m := range members {
			if i > 0 {
				b.WriteByte(',')
			}
			v, e := sharedSystemConfigUIJSON(m.value)
			if e != nil {
				return nil, e
			}
			b.Write(m.key)
			b.WriteByte(':')
			b.Write(v)
		}
		b.WriteByte('}')
		return b.Bytes(), nil
	}
	return json.RawMessage(`null`), nil
}

// Projection follows the source iterable and skips only false list entries.
// It preserves code points privately; the ordinary Options string adapter below
// refuses unrepresentable rules rather than laundering them into U+FFFD.
func sharedSystemConfigCodepointRules(raw json.RawMessage) ([][]rune, error) {
	if !originalBaseJSONTruthy(raw) {
		return [][]rune{}, nil
	}
	raw = bytes.TrimSpace(raw)
	switch raw[0] {
	case '"':
		r, _, e := sharedSystemConfigString(raw)
		if e != nil {
			return nil, e
		}
		out := [][]rune{}
		for _, c := range r {
			out = append(out, []rune{c})
		}
		return out, nil
	case '{':
		members, e := sharedSystemConfigMembers(raw)
		if e != nil {
			return nil, e
		}
		out := [][]rune{}
		for _, m := range members {
			r, _, e := sharedSystemConfigString(m.key)
			if e != nil {
				return nil, e
			}
			out = append(out, r)
		}
		return out, nil
	case '[':
		var values []json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return nil, e
		}
		out := [][]rune{}
		for _, v := range values {
			if !originalBaseJSONTruthy(v) {
				continue
			}
			r, _, e := sharedSystemConfigString(v)
			if e != nil {
				return nil, bad("SYSTEM_CONFIG_CONSUMER_ATTRIBUTE", "识别词包含无法读取 startswith 的非文本项")
			}
			out = append(out, r)
		}
		return out, nil
	}
	return nil, bad("SYSTEM_CONFIG_CONSUMER_TYPE", "当前识别词配置不是可迭代值")
}
func sharedSystemConfigRules(raw json.RawMessage) ([]string, error) {
	source, e := sharedSystemConfigCodepointRules(raw)
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, rule := range source {
		for _, r := range rule {
			if r >= 0xd800 && r <= 0xdfff {
				return nil, bad("SYSTEM_CONFIG_CONSUMER_UNICODE", "原识别词不能交给普通Unicode Options；该解析接口未覆盖源非标量码点")
			}
		}
		out = append(out, string(rule))
	}
	return out, nil
}
