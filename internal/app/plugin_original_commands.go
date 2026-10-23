package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"light-media/internal/commandrouter"
	signin "light-media/internal/plugins/autosignin"
	clean "light-media/internal/plugins/cleaninvalidseed"
	cf "light-media/internal/plugins/cloudflarespeedtest"
)

// getCommands reads the already initialized native object's actual state and
// invokes the existing original command declaration, never init or get_config.
func (p *originalPluginInstance) getCommands(ctx context.Context) ([]map[string]any, error) {
	if p.external {
		var commands []map[string]any
		err := p.readSession(ctx, "get_command", &commands)
		return commands, err
	}
	var declaration any
	switch p.installation.PluginID {
	case "AutoSignIn":
		r := &p.app.pluginAutoSignIn
		r.mu.Lock()
		slot := r.owners[p.installation.InstanceID]
		r.mu.Unlock()
		if slot == nil {
			return nil, fmt.Errorf("loaded signin owner missing")
		}
		select {
		case <-slot.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case slot.gate <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-slot.gate }()
		if slot.owner == nil {
			return nil, fmt.Errorf("loaded signin owner missing")
		}
		tx, err := p.app.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		loaded, err := slot.owner.readTx(ctx, tx)
		if err != nil {
			return nil, err
		}
		var runtime signin.Object
		if err = signin.Decode(loaded.State.Runtime, &runtime); err != nil {
			return nil, err
		}
		if !signin.Truth(runtime["enabled"]) {
			return nil, nil
		}
		declaration = signin.Commands()
	case "CleanInvalidSeed":
		r := &p.app.pluginCleanInvalidSeed
		r.mu.Lock()
		owner := r.owners[p.installation.InstanceID]
		r.mu.Unlock()
		if owner == nil {
			return nil, fmt.Errorf("loaded clean seed owner missing")
		}
		if err := owner.lock(ctx); err != nil {
			return nil, err
		}
		defer owner.unlock()
		if owner.h == nil {
			return nil, fmt.Errorf("loaded clean seed owner missing")
		}
		state, err := owner.h.GetState(ctx)
		if err != nil {
			return nil, err
		}
		if !clean.Truth(state) {
			return nil, nil
		}
		declaration = clean.Commands()
	case "CloudflareSpeedTest":
		r := &p.app.pluginCloudflare
		r.mu.Lock()
		owner := r.owners[p.installation.InstanceID]
		r.mu.Unlock()
		if owner == nil {
			return nil, fmt.Errorf("loaded cloudflare owner missing")
		}
		select {
		case owner.gate <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-owner.gate }()
		if owner.machine == nil {
			return nil, fmt.Errorf("loaded cloudflare machine missing")
		}
		if !owner.machine.State() {
			return nil, nil
		}
		declaration = cf.Commands()
	default:
		return nil, nil
	}
	raw, err := json.Marshal(declaration)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	err = json.Unmarshal(raw, &rows)
	return rows, err
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
			rows, err := loaded.(*originalPluginInstance).getCommands(context.Background())
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
