package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/commandrouter"
	"light-media/internal/plugins"
)

// A menu declaration is not a channel provider. Keep the missing dependency
// visible when no enabled, loaded plugin actually implements register_commands.
var errOriginalCommandRegisterProviderUnavailable = errors.New("core register_commands provider unavailable")

// The decision describes only preparation of outward menu metadata. It never
// authorizes execution or proves a provider accepted/installed a command menu.
type originalCommandRegisterDecision struct {
	Cancelled      bool
	Forced         bool
	Changed        bool
	ShouldRegister bool
	Commands       map[string]json.RawMessage
}

func originalCommandMenuMetadata(commands map[string]map[string]any) (map[string]json.RawMessage, map[string]bool) {
	menu := make(map[string]json.RawMessage)
	executable := make(map[string]bool)
	// Presets already belong to the core router; this does not add executors.
	for _, command := range commandrouter.Presets() {
		executable[command.Name] = true
		menu[command.Name], _ = json.Marshal(map[string]any{"type": "preset", "description": command.Description, "category": command.Category})
	}
	for name, row := range commands {
		executable[name] = true
		if show, present := row["show"]; present && !sourceSubscriptionTruth(show) {
			continue
		}
		metadata := map[string]any{"type": "plugin", "description": row["desc"], "category": row["category"]}
		if pid, present := row["pid"]; present && sourceSubscriptionTruth(pid) {
			metadata["pid"] = pid
		}
		menu[name], _ = json.Marshal(metadata)
	}
	return menu, executable
}

// Caller holds pluginOriginalCommandsMu, just like Command's initialization
// lock. The executable map has already been rebuilt and is never edited here.
// Source command.py 179-221 sets _registered_commands before invoking
// ChainBase.register_commands. A failed provider does not roll back that snapshot.
func (a *App) prepareOriginalCommandRegistration(ctx context.Context, pid string) (originalCommandRegisterDecision, error) {
	initial, executable := originalCommandMenuMetadata(a.pluginOriginalCommands)
	data, _ := json.Marshal(map[string]any{"commands": initial, "origin": "CommandChain", "service": nil, "cancel": false, "source": "未知拦截源"})
	event := &plugins.MutableEvent{Type: "command.register", Data: data}
	dispatched := a.sendOriginalChainEvent(ctx, event)
	decision := originalCommandRegisterDecision{}
	commands := initial
	if dispatched && originalBaseJSONTruthy(event.Data) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(event.Data, &fields) != nil || fields == nil {
			return decision, errors.New("invalid command registration event data")
		}
		if originalBaseJSONTruthy(fields["cancel"]) {
			decision.Cancelled = true
			return decision, nil
		}
		var source string
		if json.Unmarshal(fields["source"], &source) == nil && pid != "" && source == pid {
			decision.Forced = true
		}
		commands = make(map[string]json.RawMessage)
		if originalBaseJSONTruthy(fields["commands"]) {
			if json.Unmarshal(fields["commands"], &commands) != nil || commands == nil {
				// Source calls len() before DictUtils: lists/strings survive and
				// project to {}, while truthy numbers/bools fail before mutation.
				rawCommands := bytes.TrimSpace(fields["commands"])
				if len(rawCommands) == 0 || (rawCommands[0] != '[' && rawCommands[0] != '"') {
					return decision, errors.New("invalid command registration menu")
				}
				commands = make(map[string]json.RawMessage)
			}
		}
	}
	filtered := make(map[string]json.RawMessage)
	for name, value := range commands {
		if executable[name] {
			filtered[name] = append(json.RawMessage(nil), value...)
		}
	}
	decision.Commands = filtered
	if len(filtered) == 0 && !decision.Forced {
		return decision, nil
	}
	before := a.pluginOriginalRegisteredCommands
	if before == nil {
		before = map[string]json.RawMessage{}
	}
	oldJSON, _ := json.Marshal(before)
	newJSON, _ := json.Marshal(filtered)
	decision.Changed = !originalBaseJSONEqual(oldJSON, newJSON)
	decision.ShouldRegister = decision.Changed || decision.Forced
	if !decision.ShouldRegister {
		return decision, nil
	}
	a.pluginOriginalRegisteredCommands = filtered
	kwargs, err := json.Marshal(map[string]any{"commands": filtered})
	if err != nil {
		return decision, err
	}
	var attempts originalModuleAttempts
	_, err = a.invokeOriginalPluginModulesObserved(ctx, "register_commands", nil, json.RawMessage(`[]`), kwargs, &attempts)
	if err != nil {
		return decision, err
	}
	if attempts.Failed > 0 {
		return decision, bad("PLUGIN_COMMAND_REGISTRATION_FAILED", "命令菜单提供方执行失败")
	}
	if attempts.Called == 0 {
		return decision, errOriginalCommandRegisterProviderUnavailable
	}
	return decision, nil
}
