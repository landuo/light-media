package app

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

// A finite wire adaptation, not SQLAlchemy's runtime coercion. Go's JSON
// decoder replaces invalid Unicode and folds struct field names, so validate
// exact unique keys and Unicode scalar strings before any decoding or lookup.
func decodeSharedHistoryQuery(raw json.RawMessage, pathKey string) (map[string]*string, error) {
	invalid := func() (map[string]*string, error) {
		return nil, bad("INVALID_PLUGIN_VALUE", "历史查询必须为唯一字段的有效Unicode有界对象")
	}
	if len(raw) == 0 || len(raw) > 128<<10 || !utf8.Valid(raw) || !json.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return invalid()
	}
	values := map[string]*string{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || key != pathKey && key != "storage" {
			return invalid()
		}
		if _, exists := values[key]; exists {
			return invalid()
		}
		var value *string
		if err = decoder.Decode(&value); err != nil {
			return invalid()
		}
		values[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return invalid()
	}
	if _, err = decoder.Token(); err != io.EOF {
		return invalid()
	}
	if _, exists := values[pathKey]; !exists {
		return invalid()
	}
	return values, nil
}

func sharedHistoryScalarEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		for i++; i < len(raw) && raw[i] != '"'; i++ {
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				return false
			}
			if raw[i] != 'u' {
				continue
			}
			if i+4 >= len(raw) {
				return false
			}
			code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil {
				return false
			}
			i += 4
			if code >= 0xdc00 && code <= 0xdfff {
				return false
			}
			if code >= 0xd800 && code <= 0xdbff {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return false
				}
				low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
	}
	return true
}
