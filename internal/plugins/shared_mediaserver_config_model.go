package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"light-media/internal/notice"
	"light-media/internal/releasemeta/legacyanime"
)

// MediaServerConfig preserves the six source fields. RawMessage fields are
// owned JSON values: nullable strings, object, list, and arbitrary-size integer.
// In particular Type is not a vendor enum and SyncLibraries is not []string.
type MediaServerConfig struct {
	Name          json.RawMessage `json:"name"`
	Type          json.RawMessage `json:"type"`
	Config        json.RawMessage `json:"config"`
	Enabled       *bool           `json:"enabled"`
	SyncLibraries json.RawMessage `json:"sync_libraries"`
	SyncInterval  json.RawMessage `json:"sync_interval"`
}

// A slice, not map[string], preserves None versus empty-string keys and the
// source dictionary's first position / last value on duplicate names.
type MediaServerConfigEntry struct {
	Key       json.RawMessage   `json:"key"`
	Value     MediaServerConfig `json:"value"`
	FieldsSet []string          `json:"fields_set"`
}
type MediaServerConfigIssue struct {
	Field string `json:"field"`
	Kind  string `json:"kind"`
}

// Diagnostics deliberately exclude raw values, names, and config credentials.
// They preserve skipped-entry order and ordinary warning/validation classes,
// not Pydantic's credential-bearing textual representation or Python traceback.
type MediaServerConfigDiagnostic struct {
	Index    int                      `json:"index"`
	Severity string                   `json:"severity"`
	Class    string                   `json:"class"`
	Issues   []MediaServerConfigIssue `json:"issues"`
}
type MediaServerConfigsResult struct {
	Entries     []MediaServerConfigEntry      `json:"entries"`
	Diagnostics []MediaServerConfigDiagnostic `json:"diagnostics"`
}

// These errors escape the original get_configs ValidationError-only catch.
// Class is source classification; Error never includes the supplied value.
type MediaServerConfigSourceError struct{ Class string }

func (e *MediaServerConfigSourceError) Error() string {
	return "media server configuration: " + e.Class
}

// ParseMediaServerConfigs is the pure ServiceConfigHelper + ServiceBaseHelper
// projection. It does not perform the admin endpoint's filter(None), publish a
// cache, authorize a caller, or construct a service instance. No size quota is
// introduced here. Input and all returned buffers are independent.
func ParseMediaServerConfigs(raw json.RawMessage, includeDisabled bool) (MediaServerConfigsResult, error) {
	out := MediaServerConfigsResult{Entries: []MediaServerConfigEntry{}, Diagnostics: []MediaServerConfigDiagnostic{}}
	normalized, _, err := mediaServerConfigJSON(raw)
	if err != nil {
		return out, err
	}
	var items []json.RawMessage
	switch normalized[0] {
	case 'n', 'f':
		return out, nil
	case 't':
		return out, &MediaServerConfigSourceError{Class: "TypeError"}
	case '[':
		if err = json.Unmarshal(normalized, &items); err != nil {
			return out, err
		}
	case '{':
		members, e := mediaServerConfigMembers(normalized)
		if e != nil {
			return out, e
		}
		for _, member := range members {
			items = append(items, member.key)
		}
	case '"':
		points, e := DecodeSystemConfigJSONString(normalized)
		if e != nil {
			return out, e
		}
		for _, point := range points {
			item, e := EncodeSystemConfigJSONString([]rune{point})
			if e != nil {
				return out, e
			}
			items = append(items, item)
		}
	default:
		zero := false
		if bytes.ContainsAny(normalized, ".eE") {
			value, _ := strconv.ParseFloat(string(normalized), 64)
			zero = value == 0
		} else {
			value, ok := new(big.Int).SetString(string(normalized), 10)
			zero = ok && value.Sign() == 0
		}
		if zero {
			return out, nil
		}
		return out, &MediaServerConfigSourceError{Class: "TypeError"}
	}
	positions := map[string]int{}
	for index, item := range items {
		if item[0] != '{' {
			out.Diagnostics = append(out.Diagnostics, MediaServerConfigDiagnostic{Index: index, Severity: "warning", Class: "NonDictEntry", Issues: []MediaServerConfigIssue{}})
			continue
		}
		model, fieldsSet, issues, e := mediaServerConfigModel(item)
		if e != nil {
			return MediaServerConfigsResult{Entries: []MediaServerConfigEntry{}, Diagnostics: out.Diagnostics}, e
		}
		if len(issues) != 0 {
			out.Diagnostics = append(out.Diagnostics, MediaServerConfigDiagnostic{Index: index, Severity: "error", Class: "ValidationError", Issues: issues})
			continue
		}
		if !includeDisabled && (!mediaServerConfigNonemptyString(model.Name) || !mediaServerConfigNonemptyString(model.Type) || model.Enabled == nil || !*model.Enabled) {
			continue
		}
		entry := MediaServerConfigEntry{Key: append(json.RawMessage(nil), model.Name...), Value: model, FieldsSet: fieldsSet}
		key := string(entry.Key)
		if position, found := positions[key]; found {
			out.Entries[position] = entry
		} else {
			positions[key] = len(out.Entries)
			out.Entries = append(out.Entries, entry)
		}
	}
	return out, nil
}
func mediaServerConfigNonemptyString(raw json.RawMessage) bool { return len(raw) > 2 && raw[0] == '"' }
func mediaServerConfigModel(raw json.RawMessage) (MediaServerConfig, []string, []MediaServerConfigIssue, error) {
	enabled := false
	out := MediaServerConfig{Name: json.RawMessage(`null`), Type: json.RawMessage(`null`), Config: json.RawMessage(`{}`), Enabled: &enabled, SyncLibraries: json.RawMessage(`[]`), SyncInterval: json.RawMessage(`null`)}
	members, err := mediaServerConfigMembers(raw)
	if err != nil {
		return out, nil, nil, err
	}
	values := map[string]json.RawMessage{}
	for _, member := range members {
		values[string(member.key)] = member.value
	}
	fieldsSet := []string{}
	issues := []MediaServerConfigIssue{}
	for _, field := range []string{"name", "type", "config", "enabled", "sync_libraries", "sync_interval"} {
		encoded, _ := json.Marshal(field)
		value, supplied := values[string(encoded)]
		if !supplied {
			continue
		}
		fieldsSet = append(fieldsSet, field)
		null := bytes.Equal(value, []byte(`null`))
		switch field {
		case "name", "type":
			if !null && value[0] != '"' {
				issues = append(issues, MediaServerConfigIssue{Field: field, Kind: "string_type"})
				continue
			}
			if field == "name" {
				out.Name = append(json.RawMessage(nil), value...)
			} else {
				out.Type = append(json.RawMessage(nil), value...)
			}
		case "config":
			if !null && value[0] != '{' {
				issues = append(issues, MediaServerConfigIssue{Field: field, Kind: "dict_type"})
				continue
			}
			out.Config = append(json.RawMessage(nil), value...)
		case "enabled":
			if null {
				out.Enabled = nil
				continue
			}
			converted, kind := mediaServerConfigBoolean(value)
			if kind != "" {
				issues = append(issues, MediaServerConfigIssue{Field: field, Kind: kind})
				continue
			}
			out.Enabled = &converted
		case "sync_libraries":
			if !null && value[0] != '[' {
				issues = append(issues, MediaServerConfigIssue{Field: field, Kind: "list_type"})
				continue
			}
			out.SyncLibraries = append(json.RawMessage(nil), value...)
		case "sync_interval":
			out.SyncInterval, err = mediaServerConfigInterval(value)
			if err != nil {
				return out, fieldsSet, issues, err
			}
		}
	}
	sort.Strings(fieldsSet)
	return out, fieldsSet, issues, nil
}

// Numeric boolean conversion reuses the existing notice Pydantic integer
// conversion; spelling and classifications follow its boolean helper.
func mediaServerConfigBoolean(raw json.RawMessage) (bool, string) {
	switch raw[0] {
	case 't':
		return true, ""
	case 'f':
		return false, ""
	case '"':
		points, err := DecodeSystemConfigJSONString(raw)
		if err != nil {
			return false, "bool_type"
		}
		for i, r := range points {
			if r >= 'A' && r <= 'Z' {
				points[i] = r + ('a' - 'A')
			}
		}
		switch string(points) {
		case "1", "true", "on", "yes", "y", "t":
			return true, ""
		case "0", "false", "off", "no", "n", "f":
			return false, ""
		default:
			return false, "bool_parsing"
		}
	case '[', '{', 'n':
		return false, "bool_type"
	default:
		number, ok := notice.CoerceSourceInteger(json.Number(raw))
		if !ok {
			return false, "bool_type"
		}
		if _, err := strconv.ParseInt(string(number), 10, 64); err != nil {
			return false, "bool_type"
		}
		switch number {
		case "0":
			return false, ""
		case "1":
			return true, ""
		}
		return false, "bool_parsing"
	}
}

// The source before-validator calls Python int(value), catches TypeError and
// ValueError to None, and leaves OverflowError uncaught. Do not replace this
// with the different Pydantic model integer conversion or an int64 bound.
func mediaServerConfigInterval(raw json.RawMessage) (json.RawMessage, error) {
	switch raw[0] {
	case 'n', '[', '{':
		return json.RawMessage(`null`), nil
	case 't':
		return json.RawMessage(`1`), nil
	case 'f':
		return json.RawMessage(`0`), nil
	case '"':
		points, err := DecodeSystemConfigJSONString(raw)
		if err != nil {
			return nil, err
		}
		text := legacyanime.Strip(string(points))
		if text == "" {
			return json.RawMessage(`null`), nil
		}
		value, err := legacyanime.Integer(text)
		if err != nil {
			var source legacyanime.SourceError
			if errors.As(err, &source) && (source.Class == "TypeError" || source.Class == "ValueError") {
				return json.RawMessage(`null`), nil
			}
			return nil, err
		}
		return json.RawMessage(value.String()), nil
	default:
		if !strings.ContainsAny(string(raw), ".eE") {
			value, ok := new(big.Int).SetString(string(raw), 10)
			if !ok {
				return nil, errors.New("invalid media server integer JSON")
			}
			return json.RawMessage(value.String()), nil
		}
		value, err := strconv.ParseFloat(string(raw), 64)
		if math.IsInf(value, 0) {
			return nil, &MediaServerConfigSourceError{Class: "OverflowError"}
		}
		if err != nil {
			return nil, err
		}
		integer, _ := new(big.Float).SetFloat64(value).Int(nil)
		return json.RawMessage(integer.String()), nil
	}
}
