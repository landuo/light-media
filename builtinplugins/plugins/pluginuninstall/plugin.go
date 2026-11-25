package pluginuninstall

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	plugins "light-media/builtinplugins/internal/sdk"
)

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "PluginUnInstall", Name: "插件彻底卸载", Description: "删除数据库中已安装插件记录、清理插件文件。", Icon: "https://raw.githubusercontent.com/thsrite/MoviePilot-Plugins/main/icons/uninstall.png", Version: "1.0.0", Author: "thsrite", AuthorURL: "https://github.com/thsrite", PluginOrder: 98, Runtime: "go-executable", Entry: "plugin-uninstall", HostAPIMin: 1, HostAPIMax: 1,
		Methods:        []string{"init", "get_state", "get_form", "get_api", "get_page", "get_render_mode", "stop_service", "close", "stop", "uninstall_continue"},
		Capabilities:   []string{plugins.PluginManagerReadCapability, plugins.PluginManagerControlCapability, plugins.SettingsReadCapability, plugins.SettingsWriteCapability, plugins.BaseUpdateConfig, plugins.LogWriteCapability, plugins.BackgroundSubmitCapability},
		RuntimeSupport: &plugins.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}}}
}

// Passes contain only the locals of a currently executing source initializer.
// They are not persisted, scheduled, or replayed on restart.
type Plugin struct {
	mu       sync.Mutex
	nextPass uint64
	passes   map[uint64]*uninstallPass
}

func New() *Plugin { return &Plugin{passes: map[uint64]*uninstallPass{}} }

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		config, err := ParseConfiguration(raw)
		if err != nil {
			return nil, err
		}
		selected, err := config.Selected()
		if err != nil || !selected {
			return json.RawMessage(`null`), err
		}
		return json.RawMessage(`null`), p.run(ctx, raw, host)
	case "uninstall_continue":
		return json.RawMessage(`null`), p.continueUninstall(ctx, raw, host)
	case "get_state":
		return json.RawMessage(`false`), nil
	case "get_form":
		rows, err := (plugins.PluginHost{Host: host}).Local(ctx)
		if err != nil {
			return nil, err
		}
		local := make([]LocalPlugin, 0, len(rows))
		for _, row := range rows {
			var plugin LocalPlugin
			if err = decode(row, &plugin); err != nil {
				return nil, err
			}
			local = append(local, plugin)
		}
		options, err := LocalOptions(local)
		if err != nil {
			return nil, err
		}
		form, err := Form(options)
		if err != nil {
			return nil, err
		}
		var values struct{ Conf, Model json.RawMessage }
		if err = json.Unmarshal(form, &values); err != nil {
			return nil, err
		}
		return json.Marshal([]json.RawMessage{values.Conf, values.Model})
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_service", "get_api", "get_command", "get_page", "get_dashboard", "stop_service", "close", "stop":
		return json.RawMessage(`null`), nil
	}
	return nil, errors.New("unknown plugin method")
}
