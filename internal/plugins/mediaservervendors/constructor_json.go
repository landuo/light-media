package mediaservervendors

import (
	"bytes"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Constructor values remain owned source JSON until the particular original
// consumer needs a string. Incomplete constructors never visit this boundary.
func constructorJSON(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), raw...)
}
func constructorStringJSON(value string) json.RawMessage {
	points, err := constructorTextPoints(value)
	if err != nil {
		return nil
	}
	raw, _ := plugins.EncodeSystemConfigJSONString(points)
	return raw
}
func constructorSource(class string) error {
	return &mediaserverruntime.SourceError{Class: class, Cause: errors.New("media constructor source " + class)}
}
func constructorFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &fields) != nil {
		return nil, constructorSource("TypeError")
	}
	if _, exists := fields["sync_libraries"]; exists {
		return nil, constructorSource("TypeError")
	}
	return fields, nil
}
func constructorStringProjection(raw json.RawMessage) *string {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return nil
	}
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			return nil
		}
	}
	value := string(points)
	return &value
}

// Exact UrlUtils normalization on Python string codepoints. Non-string truthy
// values fail at endswith; falsey values are returned without coercion.
func constructorStandardURL(raw json.RawMessage) (json.RawMessage, error) {
	if !ejTruth(raw) {
		return constructorJSON(raw), nil
	}
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return nil, constructorSource("AttributeError")
	}
	if points[len(points)-1] != '/' {
		points = append(points, '/')
	}
	has := func(prefix string) bool {
		p := []rune(prefix)
		if len(points) < len(p) {
			return false
		}
		for i, r := range p {
			if points[i] != r {
				return false
			}
		}
		return true
	}
	if !has("http://") && !has("https://") {
		points = append([]rune("http://"), points...)
	}
	return plugins.EncodeSystemConfigJSONString(points)
}
func constructorNativeString(raw json.RawMessage, wrongType string) (string, error) {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return "", constructorSource(wrongType)
	}
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			return "", constructorSource("UnicodeEncodeError")
		}
	}
	return string(points), nil
}
func constructorLibraries(raw json.RawMessage) (json.RawMessage, error) {
	if !ejTruth(raw) {
		return json.RawMessage("[]"), nil
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || bytes.TrimSpace(raw)[0] != '[' {
		return nil, constructorSource("TypeError")
	}
	return constructorJSON(raw), nil
}
func constructorStringMembers(raw json.RawMessage) []string {
	var values []json.RawMessage
	_ = json.Unmarshal(raw, &values)
	out := []string{}
	for _, v := range values {
		if s := constructorStringProjection(v); s != nil {
			out = append(out, *s)
		}
	}
	return out
}
func constructorMember(raw json.RawMessage, value string) bool {
	var values []json.RawMessage
	_ = json.Unmarshal(raw, &values)
	for _, v := range values {
		p, err := plugins.DecodeSystemConfigJSONString(v)
		if err == nil && len(p) == len([]rune(value)) {
			same := true
			for i, r := range []rune(value) {
				if p[i] != r {
					same = false
					break
				}
			}
			if same {
				return true
			}
		}
	}
	return false
}
func constructorTrimURL(raw json.RawMessage) (string, error) {
	normal, err := constructorStandardURL(raw)
	if err != nil {
		return "", err
	}
	text, err := constructorLosslessString(normal, "AttributeError")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(text, "/"), nil
}

func constructorOptionalString(value *string) json.RawMessage {
	if value == nil {
		return json.RawMessage("null")
	}
	return constructorStringJSON(*value)
}
func constructorPrefer(raw json.RawMessage, value *string) json.RawMessage {
	if len(raw) > 0 {
		return constructorJSON(raw)
	}
	return constructorOptionalString(value)
}

// WTF-8 is only an internal representation of original Python codepoints, never
// emitted directly. HTTP URL and header consumers below encode or reject it at
// their original consumption boundary, without replacing it by U+FFFD.
func constructorLosslessString(raw json.RawMessage, wrongType string) (string, error) {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return "", constructorSource(wrongType)
	}
	var b strings.Builder
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			b.WriteByte(byte(0xe0 | (r >> 12)))
			b.WriteByte(byte(0x80 | ((r >> 6) & 63)))
			b.WriteByte(byte(0x80 | (r & 63)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}
func constructorTextPoints(value string) ([]rune, error) {
	points := []rune{}
	for i := 0; i < len(value); {
		if i+2 < len(value) && value[i] == 0xed && value[i+1] >= 0xa0 && value[i+1] <= 0xbf && value[i+2] >= 0x80 && value[i+2] <= 0xbf {
			points = append(points, rune(value[i]&15)<<12|rune(value[i+1]&63)<<6|rune(value[i+2]&63))
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && n == 1 {
			return nil, errors.New("invalid owned media text encoding")
		}
		points = append(points, r)
		i += n
	}
	return points, nil
}
func constructorHTTPURL(value string) (string, error) {
	if utf8.ValidString(value) {
		return value, nil
	}
	points, err := constructorTextPoints(value)
	if err != nil {
		return "", err
	}
	start := strings.Index(value, "://")
	if start < 0 {
		return "", constructorSource("InvalidURL")
	}
	start += 3
	end := len(value)
	if n := strings.IndexAny(value[start:], "/?#"); n >= 0 {
		end = start + n
	}
	authority, err := constructorTextPoints(value[start:end])
	if err != nil {
		return "", err
	}
	for _, r := range authority {
		if r >= 0xd800 && r <= 0xdfff {
			return "", constructorSource("InvalidURL")
		}
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			for _, v := range []byte{byte(0xe0 | (r >> 12)), byte(0x80 | ((r >> 6) & 63)), byte(0x80 | (r & 63))} {
				b.WriteByte('%')
				b.WriteByte(hex[v>>4])
				b.WriteByte(hex[v&15])
			}
		} else {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}
func constructorHeaderValidate(value string) error {
	points, err := constructorTextPoints(value)
	if err != nil {
		return err
	}
	// Requests check_header_validity then CPython http.client Latin-1 encoding.
	if len(points) > 0 && (unicode.IsSpace(points[0]) || (points[0] >= 0x1c && points[0] <= 0x1f)) {
		return constructorSource("InvalidHeader")
	}
	for _, r := range points {
		if r == '\r' || r == '\n' {
			return constructorSource("InvalidHeader")
		}
	}
	return nil
}
func constructorHeader(value string) (string, error) {
	if err := constructorHeaderValidate(value); err != nil {
		return "", err
	}
	points, err := constructorTextPoints(value)
	if err != nil {
		return "", err
	}
	bytes := make([]byte, len(points))
	for i, r := range points {
		if r > 255 {
			return "", constructorSource("UnicodeEncodeError")
		}
		bytes[i] = byte(r)
	}
	return string(bytes), nil
}

// PlexAPI SecretsFilter.add_secret precedes Session construction and Requests
// preparation. JSON lists/dicts are unhashable; all JSON scalar kinds are
// hashable. Secret collection itself is intentionally replaced by redacted
// diagnostics, but its source exception remains at this same consumption point.
func constructorPlexSecret(raw json.RawMessage) error {
	b := bytes.TrimSpace(raw)
	if len(b) > 0 && (b[0] == '[' || b[0] == '{') {
		return constructorSource("TypeError")
	}
	return nil
}
