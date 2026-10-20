package app

import (
	"context"
	"encoding/json"
	"sync"

	"light-media/internal/plugins/remoteidentifiers"
)

type identifiersClass struct {
	Generation, Revision int64
	State                remoteidentifiers.State
}
type identifiersRuntime struct {
	mu      sync.Mutex
	classes map[string]identifiersClass
}

func (a *App) identifiersClassState(in PluginInstallation, revision int64, raw json.RawMessage, initialize bool) (remoteidentifiers.State, error) {
	runtime := &a.pluginIdentifiers
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.classes == nil {
		runtime.classes = map[string]identifiersClass{}
	}
	old, ok := runtime.classes[in.InstanceID]
	if !ok || old.Generation != in.Generation {
		old = identifiersClass{Generation: in.Generation, State: remoteidentifiers.Cold()}
	}
	if initialize {
		if err := old.State.Init(raw); err != nil {
			return old.State, bad("INVALID_PLUGIN_CONFIG", "共享识别词源配置无效")
		}
		old.Revision = revision
		runtime.classes[in.InstanceID] = old
		return old.State, nil
	}
	if old.Revision == 0 { // A cold service reconstructs only from its current saved config.
		old.State = remoteidentifiers.Cold()
		if err := old.State.Init(raw); err != nil {
			return old.State, bad("INVALID_PLUGIN_CONFIG", "共享识别词源配置无效")
		}
		old.Revision = revision
		runtime.classes[in.InstanceID] = old
	}
	return old.State, nil
}
func (a *App) identifiersRemember(in PluginInstallation, revision int64, state remoteidentifiers.State) {
	runtime := &a.pluginIdentifiers
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.classes == nil {
		runtime.classes = map[string]identifiersClass{}
	}
	runtime.classes[in.InstanceID] = identifiersClass{in.Generation, revision, state}
}
func (a *App) identifiersGetState(ctx context.Context, in PluginInstallation, revision int64, raw json.RawMessage) bool {
	state, err := a.identifiersClassState(in, revision, raw, false)
	return err == nil && remoteidentifiers.Truth(state.Enable)
}
