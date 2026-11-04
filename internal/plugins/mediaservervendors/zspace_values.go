package mediaservervendors

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/sourcejson"
)

var zspaceNull = json.RawMessage(`null`)

func zspaceCopy(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return append(json.RawMessage(nil), zspaceNull...)
	}
	return append(json.RawMessage(nil), raw...)
}
func zspaceString(value string) json.RawMessage {
	out, _ := plugins.EncodeSystemConfigJSONString([]rune(value))
	return out
}

type zspaceFields map[string]json.RawMessage

func zspaceObject(raw json.RawMessage) (zspaceFields, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return nil, errors.New("expected owned JSON object")
	}
	var fields zspaceFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}
func (v zspaceFields) get(key string) json.RawMessage { return v.getDefault(key, zspaceNull) }
func (v zspaceFields) getDefault(key string, fallback json.RawMessage) json.RawMessage {
	if value, ok := v[key]; ok {
		return value
	}
	return fallback
}
func zspaceConstructorFields(raw json.RawMessage) (zspaceFields, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid owned ZSpace constructor JSON")
	}
	fields, err := zspaceObject(raw)
	if err != nil {
		return nil, &mediaserverruntime.SourceError{Class: "TypeError"}
	}
	for _, name := range []string{"sync_libraries", "self"} {
		if _, found := fields[name]; found {
			return nil, &mediaserverruntime.SourceError{Class: "TypeError"}
		}
	}
	return fields, nil
}
func zspaceArray(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) != 0 && raw[0] == '[' && json.Valid(raw)
}

// JSON-domain truth, not Go's non-nil slice/map truth. Existing codepoint
// decoding is reused; numbers keep arbitrary integer precision/binary64 floats.
func zspaceTruth(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 'n', 'f':
		return false
	case 't':
		return true
	case '"':
		points, err := plugins.DecodeSystemConfigJSONString(raw)
		return err == nil && len(points) != 0
	case '[':
		var values []json.RawMessage
		return json.Unmarshal(raw, &values) == nil && len(values) != 0
	case '{':
		fields, err := zspaceObject(raw)
		return err == nil && len(fields) != 0
	}
	if bytes.ContainsAny(raw, ".eE") {
		value, _ := strconv.ParseFloat(string(raw), 64)
		return value != 0
	}
	value, ok := new(big.Int).SetString(string(raw), 10)
	return ok && value.Sign() != 0
}
func zspaceStandardize(raw json.RawMessage) (json.RawMessage, error) {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
	}
	if len(points) == 0 {
		return zspaceCopy(raw), nil
	}
	if points[len(points)-1] != '/' {
		points = append(points, '/')
	}
	text := string(points)
	if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
		points = append([]rune("http://"), points...)
	}
	return plugins.EncodeSystemConfigJSONString(points)
}
func zspaceConcat(a, b json.RawMessage) (json.RawMessage, error) {
	first, err := plugins.DecodeSystemConfigJSONString(a)
	if err != nil {
		return nil, err
	}
	last, err := plugins.DecodeSystemConfigJSONString(b)
	if err != nil {
		return nil, err
	}
	return plugins.EncodeSystemConfigJSONString(append(first, last...))
}
func zspaceUserPath(user json.RawMessage, suffix string) (json.RawMessage, error) {
	var id json.RawMessage
	trimmed := bytes.TrimSpace(user)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		id = zspaceCopy(trimmed)
	} else {
		text, err := sourcejson.String(user)
		if err != nil {
			return nil, err
		}
		id = zspaceString(text)
	}
	path, err := zspaceConcat(zspaceString("emby/Users/"), id)
	if err != nil {
		return nil, err
	}
	return zspaceConcat(path, zspaceString(suffix))
}
