package app

import (
	"context"
	"github.com/google/uuid"
	"light-media/internal/plugins"
	"slices"
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
	if !slices.Contains(a.pluginOriginalBroadcast.chainOrder, kind) {
		a.pluginOriginalBroadcast.chainOrder = append(a.pluginOriginalBroadcast.chainOrder, kind)
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
	a.registerOriginalObjectBroadcastHandlers(object)
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
		if !slices.Contains(object.manifest.Methods, method) {
			continue
		}
		a.registerOriginalChainHandler(declaration.Event, a.originalBoundChainHandler(object, method, declaration.Order()))
	}
}

func (a *App) sendOriginalChainEvent(ctx context.Context, event *plugins.MutableEvent) bool {
	return a.sendOriginalChainEventObserved(ctx, event, nil)
}

// Storage selection retains the concrete object that supplied its callable
// methods. Observation does not change callback ordering or ordinary returns.
func (a *App) sendOriginalChainEventObserved(ctx context.Context, event *plugins.MutableEvent, observe func(originalMutableEventHandler, *plugins.MutableEvent)) bool {
	data, err := originalNewEventData(event.Data)
	if err != nil {
		a.logOriginalPluginContext(ctx, "ERROR", "event_invalid", "插件事件数据编码失败")
		return false
	}
	event.Data = data
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.Priority == nil {
		priority := 10
		event.Priority = &priority
	}
	var handlers []originalMutableEventHandler
	if parallelImport(ctx) == nil || !importMutationEvent(event.Type) {
		a.pluginOriginalEventsMu.Lock()
		handlers = append(handlers, a.pluginOriginalChainHandlers[event.Type]...)
		a.pluginOriginalEventsMu.Unlock()
	}
	if observe != nil {
		for i := range handlers {
			handler := handlers[i]
			handlers[i].Invoke = func(ctx context.Context, event *plugins.MutableEvent) error {
				err := handler.Invoke(ctx, event)
				observe(handler, event)
				return err
			}
		}
	}
	return dispatchOriginalMutableEvent(ctx, handlers, event, func(error) {
		a.logOriginalPluginContext(ctx, "ERROR", "chain_handler_failed", "插件链式事件处理失败")
	})
}
