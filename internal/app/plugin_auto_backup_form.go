package app

import (
	"encoding/json"
	ab "light-media/internal/plugins/autobackup"
)

// The common installer persists the form model, and native Clone copies that
// durable configuration verbatim. Keep a derived directory out of that model:
// empty means current-instance default, while every explicit path remains exact
// (including an explicit path equal to another instance's default directory).
// The original source Form and the common installation/clone mechanisms stay
// unchanged; only this host presentation exposes the effective default as a hint.
func autoBackupHostForm(defaultPath string) (json.RawMessage, error) {
	raw, err := ab.Form("")
	if err != nil {
		return nil, err
	}
	var form map[string]any
	if err = json.Unmarshal(raw, &form); err != nil {
		return nil, err
	}
	var project func(any)
	project = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if props, ok := node["props"].(map[string]any); ok && node["component"] == "VTextField" && props["model"] == "back_path" {
				props["placeholder"] = defaultPath
				props["hint"] = "留空使用当前实例的默认备份目录：" + defaultPath
				props["persistent-hint"] = true
			}
			if children, ok := node["content"]; ok {
				project(children)
			}
		case []any:
			for _, child := range node {
				project(child)
			}
		}
	}
	project(form["schema"])
	return json.Marshal(form)
}
