package app

import (
	"context"
	"encoding/json"
	"reflect"

	"light-media/internal/commandrouter"
)

// Central jobs bind an actual loaded object, just like the original bound
// Python method. A new object with the same textual instance ID is not that
// callback. This is memory-only object ownership, not an installation state.

type originalServiceKey struct{ instance, service string }

func originalObjectStillLoaded(object *originalPluginInstance) bool {
	if object == nil {
		return false
	}
	current, ok := object.app.pluginRunningInstances.Load(object.installation.InstanceID)
	return ok && current == object
}

func (a *App) enqueueOriginalExternalCommand(ctx context.Context, object *originalPluginInstance, command commandrouter.PluginCommand, user string) error {
	current, instance, ok := a.lookupOriginalPluginCommand(command.Name)
	if !ok || instance != object.installation.InstanceID || !reflect.DeepEqual(current, command) || !originalObjectStillLoaded(object) {
		return bad("LARK_PLUGIN_COMMAND_DENIED", "插件命令绑定已变化")
	}
	a.pluginOriginalCommandsMu.Lock()
	row := a.pluginOriginalCommands[command.Name]
	input, err := json.Marshal(row["data"])
	a.pluginOriginalCommandsMu.Unlock()
	if err != nil {
		return err
	}
	var data map[string]any
	if json.Unmarshal(input, &data) != nil || data == nil {
		return bad("LARK_PLUGIN_COMMAND_DENIED", "插件命令参数无效")
	}
	data["channel"], data["user"] = "飞书", user
	input, err = json.Marshal(data)
	if err != nil {
		return err
	}
	// Original command delivery submits the bound method independently.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		_, callErr := object.callOriginalMethod(context.WithoutCancel(ctx), command.Action, input)
		if callErr != nil {
			a.log("ERROR", "plugins", "command_failed", "插件命令执行失败", "", "")
		}
	}()
	return nil
}
