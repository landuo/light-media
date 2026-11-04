package mediaservervendors

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	jinjaexec "light-media/internal/jinja/exec"
)

// Existing Python JSON scalar/string encoder + ordered object reader. Compact
// separators and ensure_ascii=False are Ugreen-specific, unlike ZSpace defaults.
func ugreenCompactJSON(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) {
		return "", &EmbyJellyfinBoundaryError{Kind: "invalid owned Ugreen JSON"}
	}
	switch raw[0] {
	case '"':
		text, err := ejText(raw)
		if err != nil {
			return "", err
		}
		return jinjaexec.PythonDumps(text, jinjaexec.JSONOptions{EnsureASCII: false})
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return "", err
		}
		parts := make([]string, len(values))
		for i, v := range values {
			var err error
			parts[i], err = ugreenCompactJSON(v)
			if err != nil {
				return "", err
			}
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case '{':
		fields, err := ejObject(raw)
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(fields.keys))
		for _, key := range fields.keys {
			k, err := ugreenCompactJSON(key)
			if err != nil {
				return "", err
			}
			v, err := ugreenCompactJSON(fields.values[string(key)])
			if err != nil {
				return "", err
			}
			parts = append(parts, k+":"+v)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	case 'n', 't', 'f':
		return string(raw), nil
	default:
		return jinjaexec.PythonDumps(json.Number(raw), jinjaexec.JSONOptions{EnsureASCII: false})
	}
}
func ugreenNormalizeBody(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) {
		return "", &EmbyJellyfinBoundaryError{Kind: "invalid owned Ugreen body"}
	}
	if raw[0] == '"' {
		return ejText(raw)
	}
	return ugreenCompactJSON(raw)
}
func ugreenEncodeQuery(params json.RawMessage) (string, error) {
	if !ejTruth(params) {
		return "", nil
	}
	fields, err := ejObject(params)
	if err != nil {
		return "", ejSource("AttributeError")
	}
	parts := []string{}
	quote := func(text string) string { return strings.ReplaceAll(url.QueryEscape(text), "+", "%20") }
	var flatten func(string, json.RawMessage) error
	flatten = func(prefix string, value json.RawMessage) error {
		raw := bytes.TrimSpace(value)
		if !json.Valid(raw) {
			return &EmbyJellyfinBoundaryError{Kind: "invalid owned Ugreen query"}
		}
		switch raw[0] {
		case '{':
			fields, err := ejObject(raw)
			if err != nil {
				return err
			}
			for _, k := range fields.keys {
				key, err := ejText(k)
				if err != nil {
					return err
				}
				next := key
				if prefix != "" {
					next = prefix + "[" + key + "]"
				}
				if err := flatten(next, fields.values[string(k)]); err != nil {
					return err
				}
			}
			return nil
		case '[':
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				return err
			}
			for _, v := range values {
				if err := flatten(prefix+"[]", v); err != nil {
					return err
				}
			}
			return nil
		}
		text := ""
		if raw[0] == 't' || raw[0] == 'f' {
			text = string(raw)
		} else if raw[0] != 'n' {
			var err error
			text, err = ejPythonText(raw, false)
			if err != nil {
				return err
			}
		}
		parts = append(parts, quote(prefix)+"="+quote(text))
		return nil
	}
	for _, k := range fields.keys {
		key, err := ejText(k)
		if err != nil {
			return "", err
		}
		if err := flatten(key, fields.values[string(k)]); err != nil {
			return "", err
		}
	}
	return strings.Join(parts, "&"), nil
}

// Mechanical body reuse of the existing CPython3.12 a2b_base64(strict=false)
// helper in larkmessager/crypto.go (algorithm reuse, not a byte-identity claim).
// guard remains ASCII-only, nonalphabet bytes ignored, terminal padding stops.
func ugreenBase64Decode(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, ejSource("ValueError")
	}
	for _, r := range s {
		if r > 127 {
			return nil, ejSource("ValueError")
		}
	}
	out := make([]byte, 0, (len(s)+3)/4*3)
	position, left, pads := 0, byte(0), 0
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for _, c := range []byte(s) {
		if c == '=' {
			if position >= 2 {
				pads++
				if position+pads >= 4 {
					return out, nil
				}
			}
			continue
		}
		value := strings.IndexByte(alphabet, c)
		if value < 0 {
			continue
		}
		pads = 0
		v := byte(value)
		switch position {
		case 0:
			position = 1
			left = v
		case 1:
			position = 2
			out = append(out, (left<<2)|(v>>4))
			left = v & 15
		case 2:
			position = 3
			out = append(out, (left<<4)|(v>>2))
			left = v & 3
		case 3:
			position = 0
			out = append(out, (left<<6)|v)
			left = 0
		}
	}
	if position != 0 {
		return nil, ejSource("Error")
	}
	return out, nil
}
