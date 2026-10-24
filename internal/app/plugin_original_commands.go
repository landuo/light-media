package app

import (
	"context"
	"light-media/internal/commandrouter"
)

// getCommands reads the already initialized native object's actual state and
// invokes the existing original command declaration, never init or get_config.
func (p *originalPluginInstance) getCommands(ctx context.Context) ([]map[string]any, error) {
	p.methodMu.Lock()
	defer p.methodMu.Unlock()
	var commands []map[string]any
	err := p.readSession(ctx, "get_command", &commands)
	return commands, err
}

// initOriginalPluginCommands follows Command.init_commands: submit a complete
// background rebuild, not a synchronous per-pid replacement. Delivery to the
// external CommandChain is a separate remaining adapter, not claimed here.
func (a *App) initOriginalPluginCommands() {
	a.wg.Add(1)
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
				if !ok {
					continue
				}
				row["pid"] = instance
				commands[name] = row
			}
		}
		a.pluginOriginalCommands = commands
	}()
}

func (a *App) registerOriginalPlugin(ctx context.Context, instance string) {
	a.updateOriginalPluginJobs(ctx, instance)
	a.initOriginalPluginCommands()
	a.registerOriginalPluginAPIs(instance)
}

// The existing command ingress consumes the rebuilt instance-bound registry.
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
	data, ok := row["data"].(map[string]any)
	if !ok {
		return commandrouter.PluginCommand{}, "", false
	}
	action, ok := data["action"].(string)
	if !ok {
		return commandrouter.PluginCommand{}, "", false
	}
	desc, _ := row["desc"].(string)
	return commandrouter.PluginCommand{Name: name, PluginID: loaded.(*originalPluginInstance).installation.PluginID, Action: action, Description: desc}, instance, true
}
