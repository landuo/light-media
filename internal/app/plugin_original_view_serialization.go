package app

import (
	"bytes"
	"encoding/json"
)

// The original endpoints catch plugin method errors, then FastAPI/Pydantic
// serializes their returned object outside that catch. Python strings can
// contain surrogate code points; its HTTP serializer rejects unpaired ones.
// Keep this check at the successful HTTP response boundary, never RawKV/RPC.
func originalViewSerializedResponse(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if originalViewInvalidUnicode(raw) {
		return nil, originalViewSerializationError()
	}
	return originalViewNumericJSON(raw), nil
}

func originalViewSerializationError() error {
	return &APIError{"PLUGIN_VIEW_SERIALIZATION_FAILED", "插件视图响应包含无效的 Unicode 字符", 500}
}

// Call only on valid JSON. Escape-aware scanning preserves the distinction
// between a literal backslash-u, an actual replacement character, a valid
// surrogate pair, and an unpaired surrogate in a RawMessage key or value.
func originalViewInvalidUnicode(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			i++
			if i >= len(raw) || raw[i] != 'u' {
				continue
			}
			code := originalViewHex4(raw[i+1:])
			i += 4
			if code >= 0xdc00 && code <= 0xdfff {
				return true
			}
			if code < 0xd800 || code > 0xdbff {
				continue
			}
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return true
			}
			low := originalViewHex4(raw[i+3:])
			if low < 0xdc00 || low > 0xdfff {
				return true
			}
			i += 6
		}
	}
	return false
}

func originalViewHex4(raw []byte) uint16 {
	var n uint16
	for _, b := range raw[:4] {
		n <<= 4
		switch {
		case b >= '0' && b <= '9':
			n |= uint16(b - '0')
		case b >= 'a' && b <= 'f':
			n |= uint16(b-'a') + 10
		default:
			n |= uint16(b-'A') + 10
		}
	}
	return n
}

func originalViewInvalidObjectKey(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return false
		}
		if originalViewInvalidUnicode(raw[start:decoder.InputOffset()]) {
			return true
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
	}
	return false
}
