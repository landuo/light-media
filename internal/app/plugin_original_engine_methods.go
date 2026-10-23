package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	signin "light-media/internal/plugins/autosignin"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

// These helpers run only after native host admission, with the real owner gate
// held. A new original object must never adopt an earlier owner's live members.
func originalAutoSignInEngineForCall(ctx context.Context, owner *pluginAutoSignInOwner, in PluginInstallation, action, zone string) (*signin.Engine, error) {
	if originalInitializationMatches(ctx, in.InstanceID, in.PluginID, in.Version, in.Generation, action) {
		engine, _ := originalNativeStateBeforeInit(ctx).(*signin.Engine)
		if engine == nil {
			var err error
			engine, err = signin.NewAtZone(signin.Inventory{}, time.Now, zone)
			if err != nil {
				return nil, err
			}
		}
		clearOriginalAbsentEngine(ctx)
		owner.originalEngine = engine
		captureOriginalNativeState(ctx, engine)
	}
	return owner.originalEngine, nil
}
func originalCleanSeedMachineForCall(ctx context.Context, owner *pluginCleanInvalidSeedOwner, in PluginInstallation, action string) *seed.Machine {
	if originalInitializationMatches(ctx, in.InstanceID, in.PluginID, in.Version, in.Generation, action) {
		machine, _ := originalNativeStateBeforeInit(ctx).(*seed.Machine)
		if machine == nil {
			machine = seed.New(nil)
		}
		clearOriginalAbsentEngine(ctx)
		owner.originalMachine = machine
		captureOriginalNativeState(ctx, machine)
	}
	return owner.originalMachine
}

func originalEngineState(ctx context.Context, p *originalPluginInstance) (bool, bool, error) {
	switch p.installation.PluginID {
	case "AutoSignIn":
		engine, release, err := originalLoadedSignInEngine(ctx, p)
		if err != nil {
			return false, true, err
		}
		defer release()
		return signin.Truth(engine.Enabled), true, nil
	case "CleanInvalidSeed":
		machine, release, err := originalLoadedCleanSeedMachine(ctx, p)
		if err != nil {
			return false, true, err
		}
		defer release()
		return seed.Truth(machine.GetState()), true, nil
	default:
		return false, false, nil
	}
}
func originalEngineServices(ctx context.Context, p *originalPluginInstance) ([]pluginScheduleDefinition, bool, error) {
	switch p.installation.PluginID {
	case "AutoSignIn":
		engine, release, err := originalLoadedSignInEngine(ctx, p)
		if err != nil {
			return nil, true, err
		}
		defer release()
		// Services reads Now for randomized daily jobs. The admitted controller has
		// detached its journal clock; a getter must never touch an expired run lane.
		services, err := engine.Services(nil, engine.ScheduleTimezone)
		if err != nil {
			return nil, true, err
		}
		definitions := make([]pluginScheduleDefinition, 0, len(services))
		for _, service := range services {
			raw, err := json.Marshal(service)
			if err != nil {
				return nil, true, err
			}
			definition, err := autoSignInServiceDefinition(raw, engine.ScheduleTimezone)
			if err != nil {
				return nil, true, err
			}
			definitions = append(definitions, definition)
		}
		return definitions, true, nil
	case "CleanInvalidSeed":
		machine, release, err := originalLoadedCleanSeedMachine(ctx, p)
		if err != nil {
			return nil, true, err
		}
		defer release()
		trigger, err := machine.ServiceTrigger()
		if err != nil || trigger == nil {
			return nil, true, err
		}
		return []pluginScheduleDefinition{{ID: "CleanInvalidSeed", Name: "清理无效做种", Handler: "clean_invalid_seed", Trigger: *trigger, MisfireGraceMS: 1000}}, true, nil
	default:
		return nil, false, nil
	}
}
func originalEngineCommands(ctx context.Context, p *originalPluginInstance) ([]map[string]any, bool, error) {
	if p.installation.PluginID == "AutoSignIn" || p.installation.PluginID == "CleanInvalidSeed" {
		p.methodMu.Lock()
		defer p.methodMu.Unlock()
	}
	var declaration any
	switch p.installation.PluginID {
	case "AutoSignIn":
		_, release, err := originalLoadedSignInEngine(ctx, p)
		if err != nil {
			return nil, true, err
		}
		defer release()
		declaration = signin.Commands()
	case "CleanInvalidSeed":
		_, release, err := originalLoadedCleanSeedMachine(ctx, p)
		if err != nil {
			return nil, true, err
		}
		defer release()
		// This is the original static declaration. State filtering belongs to the
		// common manager, not this method.
		var rows []map[string]any
		err = json.Unmarshal(seed.Commands(), &rows)
		return rows, true, err
	default:
		return nil, false, nil
	}
	raw, err := json.Marshal(declaration)
	if err != nil {
		return nil, true, err
	}
	var rows []map[string]any
	err = json.Unmarshal(raw, &rows)
	return rows, true, err
}
func originalLoadedSignInEngine(ctx context.Context, p *originalPluginInstance) (*signin.Engine, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	p.configMu.RLock()
	expected, _ := p.nativeState.(*signin.Engine)
	absent := expected != nil && p.originalAbsentEngine == expected
	p.configMu.RUnlock()
	if absent {
		return expected, func() {}, nil
	}
	runtime := &p.app.pluginAutoSignIn
	runtime.mu.Lock()
	slot := runtime.owners[p.installation.InstanceID]
	runtime.mu.Unlock()
	if slot == nil || expected == nil {
		return nil, nil, fmt.Errorf("loaded signin engine missing")
	}
	select {
	case <-slot.ready:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	select {
	case slot.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	release := func() { <-slot.gate }
	if slot.err != nil || slot.owner == nil || slot.owner.binding.Generation != uint64(p.installation.Generation) || slot.owner.originalEngine != expected {
		release()
		return nil, nil, fmt.Errorf("loaded signin engine binding changed")
	}
	return expected, release, nil
}
func originalLoadedCleanSeedMachine(ctx context.Context, p *originalPluginInstance) (*seed.Machine, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	p.configMu.RLock()
	expected, _ := p.nativeState.(*seed.Machine)
	absent := expected != nil && p.originalAbsentEngine == expected
	p.configMu.RUnlock()
	if absent {
		return expected, func() {}, nil
	}
	runtime := &p.app.pluginCleanInvalidSeed
	runtime.mu.Lock()
	owner := runtime.owners[p.installation.InstanceID]
	runtime.mu.Unlock()
	if owner == nil || expected == nil {
		return nil, nil, fmt.Errorf("loaded clean seed machine missing")
	}
	if err := owner.lock(ctx); err != nil {
		return nil, nil, err
	}
	if owner.generation != p.installation.Generation || owner.originalMachine != expected {
		owner.unlock()
		return nil, nil, fmt.Errorf("loaded clean seed machine binding changed")
	}
	return expected, owner.unlock, nil
}
