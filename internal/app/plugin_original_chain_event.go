package app

import (
	"context"
	"light-media/internal/plugins"
	"sort"
)

// Registrations follow the original per-event ordered dictionary: replacing
// an identity removes it, inserts at the tail, then stably sorts by priority.
// Instance identity keeps cloned classes independent of their source object.
func (a *App) registerOriginalChainHandler(kind string, handler originalMutableEventHandler) {
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	if a.pluginOriginalChainHandlers == nil {
		a.pluginOriginalChainHandlers = make(map[string][]originalMutableEventHandler)
	}
	handlers := a.pluginOriginalChainHandlers[kind][:0]
	for _, old := range a.pluginOriginalChainHandlers[kind] {
		if old.ID != handler.ID {
			handlers = append(handlers, old)
		}
	}
	handlers = append(handlers, handler)
	sort.SliceStable(handlers, func(i, j int) bool { return handlers[i].Priority < handlers[j].Priority })
	a.pluginOriginalChainHandlers[kind] = handlers
}

func (a *App) unregisterOriginalObjectChainHandlers(instance string) {
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	for kind, existing := range a.pluginOriginalChainHandlers {
		handlers := existing[:0]
		for _, old := range existing {
			if old.Instance != instance {
				handlers = append(handlers, old)
			}
		}
		if len(handlers) == 0 {
			delete(a.pluginOriginalChainHandlers, kind)
		} else {
			a.pluginOriginalChainHandlers[kind] = handlers
		}
	}
}

func (a *App) registerOriginalObjectChainHandlers(object *originalPluginInstance) {
	instance := object.installation.InstanceID
	a.unregisterOriginalObjectChainHandlers(instance)
	declarations := append([]plugins.ChainHandler(nil), object.manifest.ChainHandlers...)
	// Explicit canonical chain declarations take precedence over the published
	// ResetHandlers compatibility declaration, then canonical Events shorthand.
	declarations = append(declarations, originalResetChainCompatibilityDeclarations(object.manifest)...)
	explicitCommand := false
	for _, h := range declarations {
		if h.Event == "command.register" {
			explicitCommand = true
		}
	}
	if !explicitCommand {
		for _, kind := range object.manifest.Events {
			if kind == "command.register" {
				declarations = append(declarations, plugins.ChainHandler{Event: kind, Method: "event"})
				break
			}
		}
	}
	for _, declaration := range declarations {
		method := declaration.Method
		a.registerOriginalChainHandler(declaration.Event, originalMutableEventHandler{
			ID: instance + "/" + method, Instance: instance, Priority: declaration.Order(),
			Enabled: func() bool {
				if !originalObjectStillLoaded(object) {
					return false
				}
				object.configMu.RLock()
				defer object.configMu.RUnlock()
				return object.eventsEnabled
			},
			Invoke: func(ctx context.Context, event *plugins.MutableEvent) error {
				return invokeOriginalMutableEvent(ctx, object, method, event)
			},
		})
	}
}

func (a *App) sendOriginalChainEvent(ctx context.Context, event *plugins.MutableEvent) bool {
	a.pluginOriginalEventsMu.Lock()
	handlers := append([]originalMutableEventHandler(nil), a.pluginOriginalChainHandlers[event.Type]...)
	a.pluginOriginalEventsMu.Unlock()
	return dispatchOriginalMutableEvent(ctx, handlers, event, func(error) {
		a.log("ERROR", "plugins", "chain_handler_failed", "插件链式事件处理失败", "", "")
	})
}
