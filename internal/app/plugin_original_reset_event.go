package app

import (
	"context"
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
	a.pluginOriginalEventsMu.Lock()
	handlers := append([]originalPluginDataResetHandler(nil), a.pluginOriginalResetHandlers...)
	a.pluginOriginalEventsMu.Unlock()
	enabled := handlers[:0]
	for _, handler := range handlers {
		if handler.Enabled == nil || handler.Enabled() {
			enabled = append(enabled, handler)
		}
	}
	if len(enabled) == 0 {
		return false
	}
	event := &originalPluginDataResetEvent{PluginID: instance, ResetConfig: true, ResetData: true}
	for _, handler := range enabled {
		if handler.Enabled != nil && !handler.Enabled() {
			continue
		}
		func() {
			defer func() {
				if recover() != nil {
					a.log("ERROR", "plugins", "data_reset_handler_failed", "插件重置事件处理失败", "", "")
				}
			}()
			if handler.Invoke != nil {
				if err := handler.Invoke(ctx, event); err != nil {
					a.log("ERROR", "plugins", "data_reset_handler_failed", "插件重置事件处理失败", "", "")
				}
			}
		}()
	}
	return true
}
