package app

import "encoding/json"

// The original form's field order and controls are retained, but a saved token
// never leaves encrypted host storage. An empty submission retains that value.
func projectIYUUForm(conf json.RawMessage, model map[string]json.RawMessage) (json.RawMessage, error) {
	var token string
	saved := json.Unmarshal(model["token"], &token) == nil && token != ""
	model["token"] = json.RawMessage(`""`)
	var nodes []any
	if err := json.Unmarshal(conf, &nodes); err != nil {
		return nil, bad("PLUGIN_FORM_FAILED", "插件配置表单返回格式无效")
	}
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if props, ok := node["props"].(map[string]any); ok && props["model"] == "token" {
				props["type"] = "password"
				if saved {
					props["placeholder"] = "已保存，留空保留原凭据"
				}
			}
			if content, ok := node["content"]; ok {
				visit(content)
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(nodes)
	return json.Marshal(nodes)
}

func retainIYUUToken(value map[string]any, stored PluginStoredValue) error {
	token, exists := value["token"]
	text, isString := token.(string)
	if exists && token != nil && (!isString || text != "") {
		return nil
	}
	if len(stored.Value) == 0 {
		return nil
	}
	previous, err := iyuuSourceMap(stored.Value)
	if err != nil {
		return err
	}
	if token, exists := previous["token"]; exists {
		value["token"] = token
	}
	return nil
}
