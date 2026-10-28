package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"light-media/internal/sourcejson"
)

// ModuleTransferInfoKey distinguishes a source TransferInfo instance from an
// ordinary dict. The pinned task callback accesses attributes; a plain dict
// must retain its different failure behavior across the Go process boundary.
const ModuleTransferInfoKey = "__plugin_transfer_info"

func ModuleTransferInfoResult(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields, err := normalizeModuleTransferInfo(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{ModuleTransferInfoKey: fields})
}

// DecodeModuleTransferInfo validates the concrete source model, preserving
// explicit nulls and defaults. A plain dictionary retains a different caller
// outcome and is never promoted to TransferInfo by this decoder.
func DecodeModuleTransferInfo(raw json.RawMessage) (map[string]any, error) {
	var outer map[string]json.RawMessage
	if json.Unmarshal(raw, &outer) != nil || len(outer) != 1 || len(outer[ModuleTransferInfoKey]) == 0 {
		return nil, fmt.Errorf("TransferInfo model required")
	}
	return normalizeModuleTransferInfo(outer[ModuleTransferInfoKey])
}

func normalizeModuleTransferInfo(raw json.RawMessage) (map[string]any, error) {
	value, err := moduleTransferObject(raw)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"success": true, "fileitem": nil, "target_item": nil, "target_diritem": nil, "transfer_type": nil, "file_count": json.Number("0"), "total_size": json.Number("0"), "file_list": []any{}, "file_list_new": []any{}, "fail_list": []any{}, "message": nil, "need_scrape": false, "need_notify": false}
	for key := range out {
		v, present := value[key]
		if !present {
			continue
		}
		if v == nil {
			if key == "success" {
				return nil, fmt.Errorf("TransferInfo.success: boolean required")
			}
			out[key] = nil
			continue
		}
		switch key {
		case "success", "need_scrape", "need_notify":
			v, err = moduleTransferBoolean(v)
		case "file_count", "total_size":
			v, err = sourcejson.ModelInteger(v)
		case "message", "transfer_type":
			if _, ok := v.(string); !ok {
				err = fmt.Errorf("string required")
			}
		case "file_list", "file_list_new", "fail_list":
			if _, ok := v.([]any); !ok {
				err = fmt.Errorf("list required")
			}
		case "fileitem", "target_item", "target_diritem":
			v, err = moduleTransferFileItem(v)
		}
		if err != nil {
			return nil, fmt.Errorf("TransferInfo.%s: %w", key, err)
		}
		out[key] = v
	}
	return out, nil
}

func moduleTransferObject(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, fmt.Errorf("object required")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("object required")
	}
	return value, nil
}

func moduleTransferFileItem(value any) (map[string]any, error) {
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("FileItem object required")
	}
	out := map[string]any{"path": "/", "storage": "local", "children": []any{}}
	for _, key := range []string{"type", "name", "basename", "extension", "size", "modify_time", "fileid", "parent_fileid", "thumbnail", "pickcode", "drive_id", "url"} {
		out[key] = nil
	}
	for key := range out {
		v, present := fields[key]
		if !present {
			continue
		}
		if v == nil {
			out[key] = nil
			continue
		}
		var err error
		switch key {
		case "size":
			v, err = sourcejson.ModelInteger(v)
		case "modify_time":
			v, err = moduleTransferFloat(v)
		case "children":
			if _, ok := v.([]any); !ok {
				err = fmt.Errorf("list required")
			}
		default:
			if _, ok := v.(string); !ok {
				err = fmt.Errorf("string required")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("FileItem.%s: %w", key, err)
		}
		out[key] = v
	}
	return out, nil
}

func moduleTransferBoolean(value any) (any, error) {
	switch value := value.(type) {
	case bool:
		return value, nil
	case json.Number:
		n, err := strconv.ParseFloat(value.String(), 64)
		if (err == nil || n == 0) && n == 0 {
			return false, nil
		}
		if err == nil && n == 1 {
			return true, nil
		}
	case string:
		switch strings.ToLower(value) {
		case "0", "off", "f", "false", "n", "no":
			return false, nil
		case "1", "on", "t", "true", "y", "yes":
			return true, nil
		}
	}
	return nil, fmt.Errorf("boolean required")
}

var moduleTransferFloatText = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:_[0-9]+)*(?:\.[0-9]*(?:_[0-9]+)*)?|\.[0-9]+(?:_[0-9]+)*)(?:[eE][+-]?[0-9]+(?:_[0-9]+)*)?$`)

func moduleTransferFloat(value any) (any, error) {
	var text string
	switch value := value.(type) {
	case bool:
		if value {
			return json.Number("1.0"), nil
		}
		return json.Number("0.0"), nil
	case json.Number:
		text = value.String()
	case string:
		text = strings.TrimSpace(value)
		if !moduleTransferFloatText.MatchString(text) {
			return nil, fmt.Errorf("finite float required")
		}
		text = strings.ReplaceAll(text, "_", "")
	default:
		return nil, fmt.Errorf("float required")
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil && n != 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return nil, fmt.Errorf("finite float required")
	}
	text = strconv.FormatFloat(n, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return json.Number(text), nil
}
