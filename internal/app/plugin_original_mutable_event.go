package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
)

// Only the public reset chain opts into the original secondary .name failure.
// Existing bool dispatch wrappers and every other event retain their policy.
type originalCheckedResetKey struct{}
type originalResetAbort struct{ error }
type originalMutableCallbackFailure struct{ name plugins.MutableEventErrorName }

func (originalMutableCallbackFailure) Error() string { return "event handler failed" }
func originalCheckedReset(ctx context.Context) bool {
	value, _ := ctx.Value(originalCheckedResetKey{}).(bool)
	return value
}

// Registries own ordering, identity and lifecycle. The shared dispatcher owns
// only enabled-snapshot selection and synchronous mutation/exception behavior.
type originalMutableEventHandler struct {
	ID       string
	Instance string
	Method   string
	object   *originalPluginInstance
	Priority int
	Enabled  func() bool
	Invoke   func(context.Context, *plugins.MutableEvent) error
}

func invokeOriginalMutableEvent(ctx context.Context, object *originalPluginInstance, method string, event *plugins.MutableEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, invoked, err := object.callOriginalEventMethod(ctx, method, raw)
	if err != nil || !invoked {
		return err
	}
	next, failed, present, err := plugins.DecodeMutableEventSnapshot(result.Result)
	if err != nil {
		return err
	}
	if !present {
		return nil
	} // Ordinary returns never replace the shared event.
	*event = next // Commit mutation before reporting callback failure.
	if failed {
		if originalCheckedReset(ctx) {
			name, err := plugins.DecodeMutableEventErrorName(result.Result)
			if err != nil {
				return originalResetAbort{bad("PLUGIN_EVENT_SNAPSHOT_INVALID", "插件重置事件失败快照无效")}
			}
			return originalMutableCallbackFailure{name: name}
		}
		return errors.New("event handler failed")
	}
	return nil
}

// dispatchOriginalMutableEvent preserves the supplied registry order. Callers
// take their registry snapshot under its own lock before entering. All enabled
// decisions are collected before any invocation. Like __safe_invoke_handler,
// each selected handler is checked again when its turn arrives; disabling a
// later subscriber during an earlier callback prevents that later invocation.
// Failures never stop remaining handlers or roll back a valid event mutation.
func dispatchOriginalMutableEvent(ctx context.Context, handlers []originalMutableEventHandler, event *plugins.MutableEvent, onError func(error)) bool {
	called, _ := dispatchOriginalMutableEventResult(ctx, handlers, event, onError)
	return called
}

func dispatchOriginalMutableEventResult(ctx context.Context, handlers []originalMutableEventHandler, event *plugins.MutableEvent, onError func(error)) (bool, error) {
	enabled := make([]originalMutableEventHandler, 0, len(handlers))
	for _, handler := range handlers {
		if handler.Enabled == nil || handler.Enabled() {
			enabled = append(enabled, handler)
		}
	}
	for _, handler := range enabled {
		if handler.Enabled != nil && !handler.Enabled() {
			continue
		}
		if err := handler.Invoke(ctx, event); err != nil {
			var abort originalResetAbort
			if originalCheckedReset(ctx) && errors.As(err, &abort) {
				return len(enabled) > 0, abort.error
			}
			if onError != nil {
				onError(err)
			}
		}
	}
	return len(enabled) > 0, nil
}
