package plugins

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
)

// ParseMediaServerConfigRows implements ServiceConfigHelper's validated list,
// before ServiceBaseHelper's enabled/name projection or any module filtering.
// Entries are rows, not a dictionary: duplicate, disabled, null-name and empty-
// name rows stay in their original positions. It reuses the exact model/JSON
// helpers already used by ParseMediaServerConfigs; that API is unchanged.
// Diagnostics contain only index/field/class, never raw config credentials.
func ParseMediaServerConfigRows(raw json.RawMessage) (MediaServerConfigsResult, error) {
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
		entry := MediaServerConfigEntry{Key: append(json.RawMessage(nil), model.Name...), Value: model, FieldsSet: fieldsSet}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}
