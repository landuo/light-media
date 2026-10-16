package uninstall

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

const ApplyCapability = "plugins.uninstall.apply"
const OptionsCapability = "plugins.uninstall.options"

// State matches get_state: this initialization-only plugin is never a running
// periodic service, even while an explicitly saved selection is being applied.
func State() bool { return false }

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "PluginUnInstall", Name: "插件彻底卸载", Description: "删除数据库中已安装插件记录、清理插件文件。", Icon: "uninstall.png", Version: "2.2", Author: "thsrite", AuthorURL: "https://github.com/thsrite", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{OptionsCapability, ApplyCapability}}
}

//go:embed form.json
var sourceForm json.RawMessage

func Form(options []Option) (json.RawMessage, error) {
	if options == nil {
		options = []Option{}
	}
	var form struct {
		Conf  []map[string]any `json:"conf"`
		Model map[string]any   `json:"model"`
	}
	if json.Unmarshal(sourceForm, &form) != nil {
		return nil, errors.New("invalid uninstall form")
	}
	var visit func(any)
	visit = func(node any) {
		switch n := node.(type) {
		case []any:
			for _, child := range n {
				visit(child)
			}
		case map[string]any:
			if props, ok := n["props"].(map[string]any); ok && props["model"] == "plugin_ids" {
				props["items"] = options
			}
			visit(n["content"])
		}
	}
	for _, root := range form.Conf {
		visit(root)
	}
	return json.Marshal(form)
}

// Handle has only fixed, empty-argument reverse calls. The host must load its
// own pinned run/config to select destructive targets; Invocation.Config is
// never an authorization for a worker-chosen plugin ID.
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if method == "config_form" {
		if host == nil {
			return nil, errors.New("missing uninstall host")
		}
		reply, err := host(ctx, OptionsCapability, json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		var inventory struct {
			Plugins []LocalPlugin `json:"plugins"`
		}
		if decode(reply, &inventory) != nil {
			return nil, errors.New("invalid uninstall options")
		}
		options, err := LocalOptions(inventory.Plugins)
		if err != nil {
			return nil, err
		}
		return Form(options)
	}
	var invocation plugins.Invocation
	if method != "invoke" || decode(raw, &invocation) != nil || (invocation.Action != "init" && invocation.Action != "reload") {
		return nil, errors.New("invalid uninstall invocation")
	}
	config, err := ParseConfiguration(invocation.Config)
	if err != nil {
		return nil, err
	}
	selected, err := config.Selected()
	if err != nil {
		return nil, err
	}
	if !selected {
		return json.RawMessage(`null`), nil
	}
	if host == nil {
		return nil, errors.New("missing uninstall host")
	}
	return host(ctx, ApplyCapability, json.RawMessage(`{}`))
}
