package app

import (
	"bytes"
	"encoding/json"
)

// Fixed get_plugin_dashboard validates its three items through PluginDashboard:
// falsey cols/attrs become {}, then both require dictionaries; elements allows
// None or a list containing only dictionaries. A malformed plugin result is500.
func originalDashboardTuple(raw json.RawMessage) ([]json.RawMessage, error) {
	invalid := func() ([]json.RawMessage, error) {
		return nil, &APIError{"PLUGIN_VIEW_FAILED", "插件仪表板返回格式无效", 500}
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var tuple []json.RawMessage
	if json.Unmarshal(raw, &tuple) != nil || len(tuple) != 3 {
		return invalid()
	}
	for _, i := range []int{0, 1} {
		if !originalBaseJSONTruthy(tuple[i]) {
			tuple[i] = json.RawMessage(`{}`)
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(tuple[i], &value) != nil || value == nil {
			return invalid()
		}
	}
	if !bytes.Equal(bytes.TrimSpace(tuple[2]), []byte("null")) {
		var rows []json.RawMessage
		if json.Unmarshal(tuple[2], &rows) != nil {
			return invalid()
		}
		for _, raw := range rows {
			var node map[string]json.RawMessage
			if json.Unmarshal(raw, &node) != nil || node == nil {
				return invalid()
			}
		}
	}
	return tuple, nil
}

// Defaults are the complete fixed PluginDashboard.model_dump(), including its
// inherited Plugin metadata. Authorization fields remain inert metadata.
const originalDashboardDefaultJSON = `{"id":null,"plugin_name":null,"plugin_desc":null,"plugin_icon":null,"plugin_version":null,"plugin_label":null,"plugin_author":null,"author_url":null,"plugin_config_prefix":null,"plugin_order":0,"auth_level":0,"installed":false,"state":false,"has_page":false,"has_update":false,"system_version_compatible":true,"system_version_message":null,"system_version":null,"release":false,"is_local":false,"repo_url":null,"install_count":0,"history":{},"add_time":0,"plugin_public_key":null,"name":null,"key":null,"render_mode":"vuetify","attrs":{},"cols":{},"elements":[]}`

func originalDashboardResponse(id, name, key, mode string, tuple []json.RawMessage) map[string]any {
	var result map[string]any
	_ = json.Unmarshal([]byte(originalDashboardDefaultJSON), &result)
	result["id"], result["name"], result["key"], result["render_mode"] = id, name, key, mode
	result["cols"], result["attrs"], result["elements"] = tuple[0], tuple[1], tuple[2]
	return result
}
