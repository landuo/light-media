package app

import (
	"context"
	"encoding/json"
	"sort"

	"light-media/internal/plugins"
)

type originalPluginDataResetEvent struct {
	PluginID    string `json:"plugin_id"`
	ResetConfig bool   `json:"reset_config"`
	ResetData   bool   `json:"reset_data"`
}

type originalPluginDataResetHandler struct {
	ID       string
	Instance string
	Priority int
	Enabled  func() bool
	Invoke   func(context.Context, *originalPluginDataResetEvent) error
}

// Like the original chain dictionary: replace an identifier by removing it
// first, append at the insertion tail, then stably sort ascending priority.
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

func (a *App) unregisterOriginalObjectResetHandlers(instance string) {
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	handlers := a.pluginOriginalResetHandlers[:0]
	for _, old := range a.pluginOriginalResetHandlers {
		if old.Instance != instance {
			handlers = append(handlers, old)
		}
	}
	a.pluginOriginalResetHandlers = handlers
}

func (a *App) registerOriginalObjectResetHandlers(object *originalPluginInstance) {
	instance := object.installation.InstanceID
	a.unregisterOriginalObjectResetHandlers(instance)
	declarations := object.manifest.ResetHandlers
	if len(declarations) == 0 {
		for _, event := range object.manifest.Events {
			if event == "plugin_data_reset" {
				declarations = []plugins.PluginDataResetHandler{{Method: "event"}}
				break
			}
		}
	}
	for _, declaration := range declarations {
		method := declaration.Method
		a.registerOriginalPluginDataResetHandler(originalPluginDataResetHandler{
			ID: instance + "/" + method, Instance: instance, Priority: declaration.Order(),
			Enabled: func() bool {
				if !originalObjectStillLoaded(object) {
					return false
				}
				object.configMu.RLock()
				defer object.configMu.RUnlock()
				return object.eventsEnabled
			},
			Invoke: func(ctx context.Context, event *originalPluginDataResetEvent) error {
				data, err := json.Marshal(event)
				if err != nil {
					return err
				}
				_, err = object.callOriginalMethod(ctx, method, mustOriginalEventJSON("plugin_data_reset", data))
				return err
			},
		})
	}
}

// PluginDataReset is a synchronous chain. Snapshot enabled subscribers in
// registration/priority order, release the registry lock before actual IPC,
// and continue after a failed subscriber, as in the original safe invocation.
func (a *App) sendOriginalPluginDataReset(ctx context.Context, instance string) bool {
	a.pluginOriginalEventsMu.Lock()
	handlers := append([]originalPluginDataResetHandler(nil), a.pluginOriginalResetHandlers...)
	a.pluginOriginalEventsMu.Unlock()
	enabled := handlers[:0]
	for _, handler := range handlers {
		if handler.Enabled == nil || handler.Enabled() {
			enabled = append(enabled, handler)
		}
	}
	event := &originalPluginDataResetEvent{PluginID: instance, ResetConfig: true, ResetData: true}
	for _, handler := range enabled {
		if err := handler.Invoke(ctx, event); err != nil {
			a.log("ERROR", "plugins", "data_reset_handler_failed", "插件重置事件处理失败", "", "")
		}
	}
	return len(enabled) > 0
}
