package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
)

type originalPluginDataResetEvent struct {
	PluginID    string `json:"plugin_id"`
	ResetConfig bool   `json:"reset_config"`
	ResetData   bool   `json:"reset_data"`
}

// Compatibility declarations are fed into the SAME ordered generic registry.
// Explicit ChainHandlers for this event replace the old ResetHandlers field;
// ResetHandlers in turn replaces canonical Events shorthand. Never double-call
// a method because a manifest advertises more than one declaration format.
func originalResetChainCompatibilityDeclarations(manifest plugins.Manifest) []plugins.ChainHandler {
	for _, handler := range manifest.ChainHandlers {
		if handler.Event == "plugin.data.reset" {
			return nil
		}
	}
	if len(manifest.ResetHandlers) > 0 {
		declarations := make([]plugins.ChainHandler, 0, len(manifest.ResetHandlers))
		for _, handler := range manifest.ResetHandlers {
			declarations = append(declarations, plugins.ChainHandler{Event: "plugin.data.reset", Method: handler.Method, Priority: handler.Priority})
		}
		return declarations
	}
	for _, event := range manifest.Events {
		if event == "plugin.data.reset" {
			return []plugins.ChainHandler{{Event: event, Method: "event"}}
		}
	}
	return nil
}

// Only the initial data follows the source Reset schema. The shared generic
// registry/dispatcher permits arbitrary JSON mutations and preserves priority.
// The caller still ignores the chain result and resets its original route ID.
func (a *App) sendOriginalPluginDataReset(ctx context.Context, instance string) bool {
	data, _ := json.Marshal(originalPluginDataResetEvent{PluginID: instance, ResetConfig: true, ResetData: true})
	return a.sendOriginalChainEvent(ctx, &plugins.MutableEvent{Type: "plugin.data.reset", Data: data})
}

func (a *App) sendOriginalPluginDataResetChecked(ctx context.Context, instance string) error {
	data, _ := json.Marshal(originalPluginDataResetEvent{PluginID: instance, ResetConfig: true, ResetData: true})
	_, err := a.sendOriginalResetChainEventChecked(ctx, &plugins.MutableEvent{Type: "plugin.data.reset", Data: data})
	return err
}
