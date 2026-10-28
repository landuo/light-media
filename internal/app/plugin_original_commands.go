package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/commandrouter"
)

// getCommands reads the already initialized native object's actual state and
// invokes the existing original command declaration, never init or get_config.
func (p *originalPluginInstance) getCommands(ctx context.Context) ([]map[string]any, error) {
	unlock := p.lockOriginalMethod()
	defer unlock()
	var raw json.RawMessage
	if err := p.readSession(ctx, "get_command", &raw); err != nil {
		return nil, err
	}
	var commands []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&commands)
	return commands, err
}

// initOriginalPluginCommands follows Command.init_commands: submit a complete
// background rebuild, not a synchronous per-pid replacement. Commands retain
// their original event/data declaration; data.action is not an SDK method.
func (a *App) initOriginalPluginCommands(requested ...string) {
	pid := ""
	if len(requested) > 0 {
		pid = requested[0]
	}
	if !a.admitOriginalBackground() {
		return
	}
	go func() {
		defer a.wg.Done()
		a.pluginOriginalCommandsMu.Lock()
		defer a.pluginOriginalCommandsMu.Unlock()
		commands := make(map[string]map[string]any)
		a.pluginOriginalRunningMu.Lock()
		order := append([]string(nil), a.pluginOriginalRunningOrder...)
		a.pluginOriginalRunningMu.Unlock()
		for _, instance := range order {
			loaded, ok := a.pluginRunningInstances.Load(instance)
			if !ok {
				continue
			}
			object := loaded.(*originalPluginInstance)
			enabled, err := object.getState(context.Background())
			if err != nil {
				a.log("ERROR", "plugins", "commands_failed", "读取插件状态失败", "", "")
				continue
			}
			if !enabled {
				continue
			}
			rows, err := object.getCommands(context.Background())
			if err != nil {
				a.log("ERROR", "plugins", "commands_failed", "读取插件命令失败", "", "")
				continue
			}
			for _, row := range rows {
				name, ok := row["cmd"].(string)
				if !ok || name == "" {
					continue
				}
				row["pid"] = instance
				commands[name] = row
			}
		}
		a.pluginOriginalCommands = commands
		// Execution is rebuilt before the menu-only interception, including cancel.
		if _, err := a.prepareOriginalCommandRegistration(context.Background(), pid); err != nil {
			if errors.Is(err, errOriginalCommandRegisterProviderUnavailable) {
				a.log("WARN", "plugins", "command_registration_unavailable", "命令菜单元数据已准备，核心尚无菜单注册提供方", "", "")
			} else {
				a.log("ERROR", "plugins", "command_registration_failed", "命令菜单处理或提供方执行失败", "", "")
			}
		}
	}()
}

func (a *App) registerOriginalPlugin(ctx context.Context, instance string) {
	a.updateOriginalPluginJobs(ctx, instance)
	a.initOriginalPluginCommands(instance)
	a.registerOriginalPluginAPIs(instance)
}

// lookupOriginalPluginCommand returns registry metadata, never permission to
// call data.action as a method. Original commands may omit action altogether.
func (a *App) lookupOriginalPluginCommand(name string) (commandrouter.PluginCommand, string, bool) {
	a.pluginOriginalCommandsMu.Lock()
	defer a.pluginOriginalCommandsMu.Unlock()
	row, ok := a.pluginOriginalCommands[name]
	if !ok {
		return commandrouter.PluginCommand{}, "", false
	}
	instance, ok := row["pid"].(string)
	if !ok {
		return commandrouter.PluginCommand{}, "", false
	}
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		return commandrouter.PluginCommand{}, "", false
	}
	data, _ := row["data"].(map[string]any)
	action, _ := data["action"].(string)
	desc, _ := row["desc"].(string)
	return commandrouter.PluginCommand{Name: name, PluginID: loaded.(*originalPluginInstance).installation.PluginID, Action: action, Description: desc}, instance, true
}
