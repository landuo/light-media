package app

import (
	"context"
	"encoding/json"
)

// MoviePilot PluginManager.get_plugin_config, not _PluginBase.get_config:
// no imported class or any falsey stored value produces {}, without storing it.
func (a *App) originalManagerConfiguration(ctx context.Context, instance string) (json.RawMessage, error) {
	if _, ok := a.pluginOriginalClasses.Load(instance); !ok {
		return json.RawMessage(`{}`), nil
	}
	raw, _, err := a.readOriginalBaseConfigForInit(ctx, instance)
	if err != nil {
		return nil, err
	}
	if !originalBaseJSONTruthy(raw) {
		return json.RawMessage(`{}`), nil
	}
	var value map[string]json.RawMessage
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	delete(value, "")
	return json.Marshal(value)
}
