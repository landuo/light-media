package app

import (
	"context"
	"encoding/json"
	"slices"

	"light-media/internal/plugins"
)

type originalEventHandlerIdentity struct {
	class  originalEventClass
	method string
}

type originalBroadcastHandler struct {
	ID, Instance, Method string
	object               *originalPluginInstance
}

func (p *originalPluginInstance) originalEventMethodEnabled(method string) bool {
	if !p.originalEventEnabled() {
		return false
	}
	identity, valid := originalEventClassOf(p)
	if !valid {
		return false
	}
	p.app.pluginOriginalEventsMu.Lock()
	disabled := p.app.pluginOriginalBroadcast.disabled[originalEventHandlerIdentity{identity, method}]
	p.app.pluginOriginalEventsMu.Unlock()
	return !disabled
}

func (a *App) registerOriginalBroadcastHandler(kind string, handler originalBroadcastHandler) {
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	if a.pluginOriginalBroadcast.handlers == nil {
		a.pluginOriginalBroadcast.handlers = map[string][]originalBroadcastHandler{}
	}
	if !slices.Contains(a.pluginOriginalBroadcast.broadcastOrder, kind) {
		a.pluginOriginalBroadcast.broadcastOrder = append(a.pluginOriginalBroadcast.broadcastOrder, kind)
	}
	existing := a.pluginOriginalBroadcast.handlers[kind]
	handlers := existing[:0]
	for _, old := range existing {
		if old.ID != handler.ID {
			handlers = append(handlers, old)
		}
	}
	a.pluginOriginalBroadcast.handlers[kind] = append(handlers, handler)
}

func (a *App) originalBoundChainHandler(object *originalPluginInstance, method string, priority int) originalMutableEventHandler {
	instance := object.installation.InstanceID
	return originalMutableEventHandler{ID: instance + "/" + method, Instance: instance, Method: method, object: object, Priority: priority,
		Enabled: func() bool { return object.originalEventMethodEnabled(method) },
		Invoke: func(ctx context.Context, event *plugins.MutableEvent) error {
			err := invokeOriginalMutableEvent(ctx, object, method, event)
			if err != nil {
				a.reportOriginalEventError(ctx, object, event.Type, method, err)
			}
			return err
		},
	}
}

func (a *App) originalBroadcastSnapshot(kind string) []originalBroadcastHandler {
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	return append([]originalBroadcastHandler(nil), a.pluginOriginalBroadcast.handlers[kind]...)
}

func (a *App) registerOriginalObjectBroadcastHandlers(object *originalPluginInstance) {
	declarations := append([]plugins.BroadcastHandler(nil), object.manifest.BroadcastHandlers...)
	explicit := map[string]bool{}
	for _, h := range declarations {
		explicit[h.Event] = true
	}
	for _, kind := range object.manifest.Events {
		if kind != "plugin.data.reset" && kind != "command.register" && !explicit[kind] {
			declarations = append(declarations, plugins.BroadcastHandler{Event: kind, Method: "event"})
		}
	}
	for _, h := range declarations {
		if slices.Contains(object.manifest.Methods, h.Method) {
			instance := object.installation.InstanceID
			a.registerOriginalBroadcastHandler(h.Event, originalBroadcastHandler{ID: instance + "/" + h.Method, Instance: instance, Method: h.Method, object: object})
		}
	}
}

// Dynamic operations are scoped to the caller's bound class and manifest
// methods. Retained registrations resolve that same class's current instance
// at dispatch; they never retain a worker session or a host-call capability.
func (a *App) originalManageEventResource(scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var control plugins.EventControl
	if err := originalResourceDecode(raw, &control); err != nil {
		return nil, err
	}
	if !control.Valid() {
		return nil, bad("PLUGIN_EVENT_INVALID", "事件管理参数无效")
	}
	object := scope.object
	identity, valid := originalEventClassOf(object)
	if !valid || control.Method != "" && !slices.Contains(object.manifest.Methods, control.Method) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "事件方法未声明")
	}
	id := object.installation.InstanceID + "/" + control.Method
	switch control.Action {
	case "add":
		if control.Kind == plugins.ChainEventKind {
			priority := 10
			if control.Priority != nil {
				priority = *control.Priority
			}
			a.registerOriginalChainHandler(control.Type, a.originalBoundChainHandler(object, control.Method, priority))
		} else {
			a.registerOriginalBroadcastHandler(control.Type, originalBroadcastHandler{ID: id, Instance: object.installation.InstanceID, Method: control.Method, object: object})
		}
	case "remove":
		a.pluginOriginalEventsMu.Lock()
		if control.Kind == plugins.ChainEventKind {
			if handlers, exists := a.pluginOriginalChainHandlers[control.Type]; exists {
				a.pluginOriginalChainHandlers[control.Type] = slices.DeleteFunc(handlers, func(h originalMutableEventHandler) bool { return h.ID == id })
			}
		} else {
			if handlers, exists := a.pluginOriginalBroadcast.handlers[control.Type]; exists {
				a.pluginOriginalBroadcast.handlers[control.Type] = slices.DeleteFunc(handlers, func(h originalBroadcastHandler) bool { return h.ID == id })
			}
		}
		a.pluginOriginalEventsMu.Unlock()
	case "enable_handler", "disable_handler":
		a.pluginOriginalEventsMu.Lock()
		if a.pluginOriginalBroadcast.disabled == nil {
			a.pluginOriginalBroadcast.disabled = map[originalEventHandlerIdentity]bool{}
		}
		key := originalEventHandlerIdentity{identity, control.Method}
		if control.Action == "disable_handler" {
			a.pluginOriginalBroadcast.disabled[key] = true
		} else {
			delete(a.pluginOriginalBroadcast.disabled, key)
		}
		a.pluginOriginalEventsMu.Unlock()
	case "enable_class", "disable_class":
		object.configMu.Lock()
		object.eventsEnabled = control.Action == "enable_class"
		object.configMu.Unlock()
	case "visualize":
		return json.Marshal(a.originalVisualizeEventHandlers())
	}
	return json.RawMessage(`null`), nil
}

func (a *App) originalVisualizeEventHandlers() []plugins.EventHandlerInfo {
	// Snapshot under the registry lock; checking current objects and class flags
	// happens afterwards to avoid holding it across callbacks/configuration locks.
	a.pluginOriginalEventsMu.Lock()
	broadcasts := map[string][]originalBroadcastHandler{}
	chains := map[string][]originalMutableEventHandler{}
	for kind, handlers := range a.pluginOriginalBroadcast.handlers {
		broadcasts[kind] = append([]originalBroadcastHandler(nil), handlers...)
	}
	for kind, handlers := range a.pluginOriginalChainHandlers {
		chains[kind] = append([]originalMutableEventHandler(nil), handlers...)
	}
	broadcastOrder := append([]string(nil), a.pluginOriginalBroadcast.broadcastOrder...)
	chainOrder := append([]string(nil), a.pluginOriginalBroadcast.chainOrder...)
	a.pluginOriginalEventsMu.Unlock()
	result := originalCoreEventHandlerInventory()
	for _, kind := range broadcastOrder {
		for _, h := range broadcasts[kind] {
			status := "disabled"
			if h.object.originalEventMethodEnabled(h.Method) {
				status = "enabled"
			}
			result = append(result, plugins.EventHandlerInfo{EventType: kind, HandlerIdentifier: "go.plugins." + h.Instance + "." + h.Method, Status: status})
		}
	}
	for _, kind := range chainOrder {
		for _, h := range chains[kind] {
			status := "disabled"
			if h.Enabled == nil || h.Enabled() {
				status = "enabled"
			}
			id := h.ID
			if h.Method != "" {
				id = "go.plugins." + h.Instance + "." + h.Method
			}
			priority := h.Priority
			result = append(result, plugins.EventHandlerInfo{EventType: kind, HandlerIdentifier: id, Status: status, Priority: &priority})
		}
	}
	return result
}
