package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

type originalPluginDataResetEvent struct {
	PluginID    string
	ResetConfig bool
	ResetData   bool
}

type originalPluginDataResetHandler struct {
	ID       string
	Priority int
	Enabled  func() bool
	Invoke   func(context.Context, *originalPluginDataResetEvent) error
}

// The original event manager deduplicates identifiers, moves a replaced
// subscriber to the insertion tail, and stably sorts ascending priority.
func (a *App) registerOriginalPluginDataResetHandler(handler originalPluginDataResetHandler) {
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	handlers := a.pluginOriginalResetHandlers[:0]
	for _, old := range a.pluginOriginalResetHandlers {
		if old.ID != handler.ID {
			handlers = append(handlers, old)
		}
	}
	handlers = append(handlers, handler)
	sort.SliceStable(handlers, func(i, j int) bool { return handlers[i].Priority < handlers[j].Priority })
	a.pluginOriginalResetHandlers = handlers
}

// PluginDataReset is a synchronous chain, unlike PluginReload. Current 21
// compiled original classes declare no such subscriber; an empty registry
// returns false, which the original Reset endpoint deliberately ignores.
func (a *App) sendOriginalPluginDataReset(ctx context.Context, instance string) bool {
	handled := false
	raw := mustOriginalEventJSON("plugin_data_reset", json.RawMessage(fmt.Sprintf(`{"plugin_id":%q,"reset_config":true,"reset_data":true}`, instance)))
	a.pluginRunningInstances.Range(func(_, value any) bool {
		object := value.(*originalPluginInstance)
		object.configMu.RLock()
		enabled := object.eventsEnabled
		object.configMu.RUnlock()
		if !enabled {
			return true
		}
		for _, event := range object.manifest.Events {
			if event == "plugin_data_reset" {
				handled = true
				if _, err := object.callOriginalMethod(ctx, "event", raw); err != nil {
					a.log("ERROR", "plugins", "data_reset_handler_failed", "插件重置事件处理失败", "", "")
				}
				break
			}
		}
		return true
	})
	return handled
}
