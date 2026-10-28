package app

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Use the existing Chinese host presentation instead of introducing a second
// locale subsystem. Source i18n aliases remain present in the response model.
func originalServiceI18N(value any) any {
	raw, _ := json.Marshal(value)
	var text *string
	if json.Unmarshal(raw, &text) == nil && text != nil && *text != "" {
		return *text
	}
	return nil
}

func normalizeOriginalServiceProgressProjection(out map[string]any) error {
	if out == nil {
		return nil
	}
	for _, key := range []string{"id", "name", "provider", "text", "status", "started_at", "finished_at", "error"} {
		raw, _ := json.Marshal(out[key])
		var text *string
		if json.Unmarshal(raw, &text) != nil {
			return &APIError{"INVALID_PLUGIN_SERVICE_PROGRESS", "服务进度字段类型无效", 500}
		}
	}
	raw, _ := json.Marshal(out["success"])
	success, valid := originalServiceProgressBoolean(raw)
	if !valid {
		return &APIError{"INVALID_PLUGIN_SERVICE_PROGRESS", "服务进度成功状态类型无效", 500}
	}
	out["success"] = originalServiceEncode(success)
	for _, key := range []string{"name", "provider", "text", "error"} {
		out[key+"_i18n"] = originalServiceI18N(out[key])
	}
	if data, ok := out["data"].(map[string]json.RawMessage); ok {
		for _, key := range []string{"error", "message"} {
			var text *string
			if value, exists := data[key]; exists && json.Unmarshal(value, &text) == nil && text != nil {
				data[key+"_i18n"] = originalServiceEncode(*text)
			}
		}
	}
	return nil
}

func originalServiceProgressBoolean(raw json.RawMessage) (any, bool) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	switch typed := value.(type) {
	case nil:
		return nil, true
	case bool:
		return typed, true
	case json.Number:
		number, err := originalRatingFloat(raw)
		if err == nil && number == 0 {
			return false, true
		}
		if err == nil && number == 1 {
			return true, true
		}
	case string:
		switch strings.ToLower(typed) {
		case "0", "off", "f", "false", "n", "no":
			return false, true
		case "1", "on", "t", "true", "y", "yes":
			return true, true
		}
	}
	return nil, false
}
