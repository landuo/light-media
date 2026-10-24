package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
)

// Registries own ordering, identity and lifecycle. The shared dispatcher owns
// only enabled-snapshot selection and synchronous mutation/exception behavior.
type originalMutableEventHandler struct {
	ID       string
	Instance string
	Priority int
	Enabled  func() bool
	Invoke   func(context.Context, *plugins.MutableEvent) error
}

func invokeOriginalMutableEvent(ctx context.Context, object *originalPluginInstance, method string, event *plugins.MutableEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := object.callOriginalMethod(ctx, method, raw)
	if err != nil {
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
		return errors.New("event handler failed")
	}
	return nil
}

// dispatchOriginalMutableEvent preserves the supplied registry order. Callers
// take their registry snapshot under its own lock before entering. All enabled
// decisions are collected before any invocation, and failures never stop the
// remaining selected handlers. A valid shared event mutation is not rolled back.
func dispatchOriginalMutableEvent(ctx context.Context, handlers []originalMutableEventHandler, event *plugins.MutableEvent, onError func(error)) bool {
	enabled := make([]originalMutableEventHandler, 0, len(handlers))
	for _, handler := range handlers {
		if handler.Enabled == nil || handler.Enabled() {
			enabled = append(enabled, handler)
		}
	}
	for _, handler := range enabled {
		if err := handler.Invoke(ctx, event); err != nil && onError != nil {
			onError(err)
		}
	}
	return len(enabled) > 0
}
