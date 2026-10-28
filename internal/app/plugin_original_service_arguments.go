package app

import (
	"bytes"
	"encoding/json"
)

// Scheduler.start converts configured func_kwargs with dict(value or {}). Go
// carries that ordinary keyword dictionary as the method's JSON input object.
func originalServiceArguments(raw json.RawMessage) (json.RawMessage, error) {
	if !originalViewJSONTruth(raw) {
		return json.RawMessage(`{}`), nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		return raw, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "定时方法参数不能转换为关键字对象")
	}
	keys := []string{}
	values := map[string]json.RawMessage{}
	for _, entry := range entries {
		var pair []json.RawMessage
		entry = bytes.TrimSpace(entry)
		if len(entry) > 0 && entry[0] == '{' {
			for _, key := range originalFolderInputKeys(entry) {
				encoded, _ := json.Marshal(key)
				pair = append(pair, encoded)
			}
		} else if len(entry) > 0 && entry[0] == '"' {
			var text string
			_ = json.Unmarshal(entry, &text)
			for _, character := range text {
				encoded, _ := json.Marshal(string(character))
				pair = append(pair, encoded)
			}
		} else if json.Unmarshal(entry, &pair) != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "定时方法参数项不可迭代")
		}
		if len(pair) != 2 {
			return nil, bad("INVALID_PLUGIN_VALUE", "定时方法参数项必须包含键和值")
		}
		var key string
		if json.Unmarshal(pair[0], &key) != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "定时方法关键字必须为字符串")
		}
		if _, exists := values[key]; !exists {
			keys = append(keys, key)
		}
		values[key] = pair[1]
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		out.Write(name)
		out.WriteByte(':')
		out.Write(values[key])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
