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
type originalBoundExecutionKey struct{}

type originalServiceKey struct{ instance, service string }
type originalServiceBinding struct {
	object *originalPluginInstance
	digest string
}
type originalCommandBinding struct {
	object  *originalPluginInstance
	command commandrouter.PluginCommand
	input   json.RawMessage
}

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
	if err = a.pluginStore.checkPluginFormWrite(ctx, instance, pluginFormWriteLifecycle); err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	run, err := a.pluginStore.enqueueRunTx(ctx, tx, instance, "original_command", input)
	if err != nil {
		return err
	}
	a.pluginStore.originalCommandRuns.Store(run.ID, originalCommandBinding{object: object, command: command, input: input})
	if err = tx.Commit(); err != nil {
		a.pluginStore.originalCommandRuns.Delete(run.ID)
	}
	return err
}

func (a *App) originalExternalRunCall(ctx context.Context, run PluginRun, method string, input json.RawMessage, boundObject **originalPluginInstance) (string, json.RawMessage, error) {
	switch run.Action {
	case "service":
		var invocation struct {
			Input struct {
				OccurrenceID string `json:"occurrence_id"`
			} `json:"input"`
		}
		if json.Unmarshal(run.Input, &invocation) != nil {
			return "", nil, bad("PLUGIN_SCHEDULE_SCOPE_DENIED", "定时调用参数无效")
		}
		definition, err := a.pluginStore.pluginScheduledInvocation(ctx, run, invocation.Input.OccurrenceID)
		if err != nil {
			return "", nil, err
		}
		bound, ok := a.pluginStore.originalScheduledObjects.Load(originalServiceKey{run.InstanceID, definition.ID})
		if !ok {
			return "", nil, bad("PLUGIN_NOT_LOADED", "定时服务绑定对象不存在")
		}
		binding := bound.(originalServiceBinding)
		raw, err := json.Marshal(definition)
		if err != nil {
			return "", nil, err
		}
		if !originalObjectStillLoaded(binding.object) || binding.object.installation.Generation != run.Generation || binding.object.installation.Version != run.Version || binding.digest != tokenHash(string(raw)) {
			return "", nil, bad("PLUGIN_SCHEDULE_SCOPE_DENIED", "定时服务绑定已变化")
		}
		payload := definition.Input
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		*boundObject = binding.object
		return definition.Handler, payload, nil
	case "original_command":
		bound, ok := a.pluginStore.originalCommandRuns.LoadAndDelete(run.ID)
		if !ok {
			return "", nil, bad("LARK_PLUGIN_COMMAND_DENIED", "插件命令绑定不存在")
		}
		binding := bound.(originalCommandBinding)
		command, instance, ok := a.lookupOriginalPluginCommand(binding.command.Name)
		if !ok || instance != run.InstanceID || !reflect.DeepEqual(command, binding.command) || !originalObjectStillLoaded(binding.object) || binding.object.installation.Generation != run.Generation || binding.object.installation.Version != run.Version {
			return "", nil, bad("LARK_PLUGIN_COMMAND_DENIED", "插件命令绑定已变化")
		}
		*boundObject = binding.object
		return binding.command.Action, binding.input, nil
	default:
		return method, input, nil
	}
}
