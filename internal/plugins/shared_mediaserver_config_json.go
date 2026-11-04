package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Mechanically relocated from the current App raw-system-config JSON helpers.
// Only private prefixes and same-package public helper qualifiers differ.
// Preserve Python strings' complete code points, including unpaired UTF-16
// escapes. encoding/json's string decoder replaces those with U+FFFD.
func mediaServerConfigString(raw json.RawMessage) ([]rune, bool, error) {
	points, err := DecodeSystemConfigJSONString(raw)
	invalid := false
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			invalid = true
		}
	}
	return points, invalid, err
}
func mediaServerConfigQuote(points []rune) json.RawMessage {
	// All inputs come from the validated source JSON/codepoint parser.
	raw, _ := EncodeSystemConfigJSONString(points)
	return raw
}

type mediaServerConfigMember struct {
	key   json.RawMessage
	value json.RawMessage
}

func mediaServerConfigMembers(raw json.RawMessage) ([]mediaServerConfigMember, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if _, e := d.Token(); e != nil {
		return nil, e
	}
	out := []mediaServerConfigMember{}
	for d.More() {
		start := d.InputOffset()
		if _, e := d.Token(); e != nil {
			return nil, e
		}
		key := bytes.TrimLeft(raw[start:d.InputOffset()], " \n\r\t,")
		r, _, e := mediaServerConfigString(key)
		if e != nil {
			return nil, e
		}
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, e
		}
		out = append(out, mediaServerConfigMember{mediaServerConfigQuote(r), value})
	}
	return out, nil
}

// Objects remain ordered and duplicate keys retain their first position.
func mediaServerConfigJSON(raw json.RawMessage) (json.RawMessage, bool, error) {
	raw = bytes.TrimSpace(raw)
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, false, errors.New("invalid configuration JSON")
	}
	switch raw[0] {
	case '"':
		r, _, e := mediaServerConfigString(raw)
		return mediaServerConfigQuote(r), false, e
	case '[':
		var values []json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return nil, false, e
		}
		nonfinite := false
		for i, value := range values {
			v, n, e := mediaServerConfigJSON(value)
			if e != nil {
				return nil, false, e
			}
			values[i] = v
			nonfinite = nonfinite || n
		}
		encoded, e := json.Marshal(values)
		return encoded, nonfinite, e
	case '{':
		members, e := mediaServerConfigMembers(raw)
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
			value, n, e := mediaServerConfigJSON(member.value)
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
