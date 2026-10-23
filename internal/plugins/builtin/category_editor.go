package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
)

func CategoryEditorManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, ConfigDefaults: plugins.ConfigDefaultsPure, PluginID: "CategoryEditor", Name: "二级分类策略", Description: "编辑下载和整理时自动二级分类的目录规则。", Version: "1.3", Icon: "Bookstack_A.png", Author: "jxxghp", AuthorURL: "https://github.com/jxxghp", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{"category.read", "category.write", "config.patch", "notification.post"}}
}

type categoryConfig struct {
	Enabled bool   `json:"enabled"`
	Content string `json:"content"`
}

const categoryForm = `[{"component":"VForm","content":[
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"md":6},"content":[{"component":"VSwitch","props":{"model":"enabled","label":"启用插件（写入配置文件）"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VAceEditor","props":{"modelvalue":"content","lang":"yaml","theme":"monokai","style":"height: 30rem"}}]}]}
]}]`

// HandleCategoryEditor follows installed CategoryEditor 1.3: saving enabled
// applies the YAML immediately, resets the switch and posts a system message.
// It has no scheduler, page or generic manual-run button in the original.
func HandleCategoryEditor(ctx context.Context, method string, payload json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "stop":
		return json.RawMessage(`null`), nil
	case "config_form":
		raw, err := host(ctx, "category.read", json.RawMessage(`{"default":true}`))
		if err != nil {
			return nil, err
		}
		var defaultPolicy struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &defaultPolicy) != nil {
			return nil, errors.New("invalid category defaults")
		}
		// Same Vuetify nodes, model names, labels and dimensions as get_form().
		return json.Marshal(map[string]any{"conf": json.RawMessage(categoryForm), "model": categoryConfig{Content: defaultPolicy.Content}})
	case "invoke":
		var input plugins.Invocation
		if json.Unmarshal(payload, &input) != nil || (input.Action != "init" && input.Action != "reload") {
			return nil, errors.New("unsupported category action")
		}
		var config categoryConfig
		if json.Unmarshal(input.Config, &config) != nil {
			return nil, errors.New("invalid category configuration")
		}
		if !config.Enabled {
			return json.RawMessage(`{"enabled":false,"applied":false}`), nil
		}
		request, _ := json.Marshal(map[string]string{"content": config.Content})
		if _, err := host(ctx, "category.write", request); err != nil {
			return nil, err
		}
		notice, _ := json.Marshal(map[string]string{"title": "二级分类策略", "message": "二级分类策略已更新，请注意同步调整目录设置，插件将恢复关闭状态！"})
		if _, err := host(ctx, "notification.post", notice); err != nil {
			return nil, err
		}
		// Source order is write/reload, system message, then close the one-shot
		// switch and persist config. A failed message must not clear the switch.
		if _, err := host(ctx, "config.patch", json.RawMessage(`{"enabled":false}`)); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"enabled":false,"applied":true}`), nil
	default:
		return nil, errors.New("unsupported category method")
	}
}
