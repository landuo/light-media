package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
)

func (a *App) originalEventHasHandler(query plugins.EventQuery) bool {
	if query.Kind == plugins.ChainEventKind {
		a.pluginOriginalEventsMu.Lock()
		handlers := append([]originalMutableEventHandler(nil), a.pluginOriginalChainHandlers[query.Type]...)
		a.pluginOriginalEventsMu.Unlock()
		for _, handler := range handlers {
			if handler.Enabled == nil || handler.Enabled() {
				return true
			}
		}
		return false
	}
	if originalCoreBroadcastHandler(query.Type) != "" {
		return true
	}
	for _, handler := range a.originalBroadcastSnapshot(query.Type) {
		if handler.object.originalEventMethodEnabled(handler.Method) {
			return true
		}
	}
	return false
}

func (a *App) originalEventResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if scope.object == nil || scope.object.app != a {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "事件调用需要当前插件对象")
	}
	if method == plugins.EventManageCapability {
		return a.originalManageEventResource(scope, raw)
	}
	if method == plugins.EventCheckCapability {
		var query plugins.EventQuery
		if err := originalResourceDecode(raw, &query); err != nil {
			return nil, err
		}
		if !query.Valid() {
			return nil, bad("PLUGIN_EVENT_INVALID", "事件类型无效")
		}
		return json.Marshal(a.originalEventHasHandler(query))
	}
	var input plugins.EventSendRequest
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if !(plugins.EventQuery{Kind: input.Kind, Type: input.Type}).Valid() {
		return nil, bad("PLUGIN_EVENT_INVALID", "事件类型无效")
	}
	event, err := originalNewEvent(input.Type, input.Data, input.Priority)
	if err != nil {
		return nil, bad("PLUGIN_EVENT_INVALID", "事件数据无效")
	}
	if input.Kind == plugins.BroadcastEventKind {
		if !a.enqueueOriginalBroadcast(ctx, event) {
			return nil, bad("APP_STOPPING", "服务正在停止")
		}
		return json.RawMessage(`null`), nil
	}
	// Synchronous chains can re-enter the originating object. A serial worker
	// cannot process that callback while waiting for its host-call response.
	scope.object.sessionMu.Lock()
	concurrent := scope.object.session != nil && scope.object.session.SupportsConcurrent()
	scope.object.sessionMu.Unlock()
	if !concurrent {
		return nil, bad("PLUGIN_CONCURRENT_REQUIRED", "同步事件需要协商并发worker")
	}
	if !a.admitOriginalBackground() {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	if input.Type == "plugin.data.reset" {
		called, err := a.sendOriginalResetChainEventChecked(ctx, &event)
		if err != nil {
			return nil, err
		}
		if !called {
			return json.RawMessage(`null`), nil
		}
		return json.Marshal(event)
	}
	if !a.sendOriginalChainEvent(ctx, &event) {
		return json.RawMessage(`null`), nil
	}
	return json.Marshal(event)
}
