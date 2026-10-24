package app

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
)

// originalViewRead calls only the loaded standard object. Unadapted native
// methods are unavailable; there is no old host fallback.
// No installed flag, guessed default, process start, or init is involved.
func (p *originalPluginInstance) originalViewRead(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, bool, error) {
	if p == nil || !p.external {
		return nil, false, bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
	}
	if !slices.Contains(p.manifest.Methods, method) {
		return nil, true, bad("PLUGIN_VIEW_METHOD_UNAVAILABLE", "插件未声明此视图方法")
	}
	result, err := p.callOriginalMethod(ctx, method, input)
	return result.Result, true, err
}
func (p *originalPluginInstance) originalViewRenderMode(ctx context.Context) (mode, dist string, handled bool, err error) {
	raw, handled, err := p.originalViewRead(ctx, "get_render_mode", nil)
	if err != nil || !handled {
		return "", "", handled, err
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) != 2 || json.Unmarshal(parts[0], &mode) != nil || json.Unmarshal(parts[1], &dist) != nil {
		return "", "", true, bad("PLUGIN_VIEW_FAILED", "插件渲染模式返回格式无效")
	}
	return mode, dist, true, nil
}

func (p *originalPluginInstance) originalViewDashboard(ctx context.Context, key string, userAgent *string) (json.RawMessage, bool, error) {
	input, err := json.Marshal(map[string]any{"key": key, "user_agent": userAgent})
	if err != nil {
		return nil, p != nil && p.external, err
	}
	return p.originalViewRead(ctx, "get_dashboard", input)
}

// The existing Base session callback must call observeOriginalViewHostFailure
// on every returned error. This context carries no permission or snapshot.
type originalViewHostObservationKey struct{}
type originalViewHostObservation struct {
	mu  sync.Mutex
	err error
}

func (o *originalViewHostObservation) capture(err error) {
	if err == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err == nil {
		o.err = err
	}
}
func (o *originalViewHostObservation) failure() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}
func observeOriginalViewHostFailure(ctx context.Context, err error) {
	if o, ok := ctx.Value(originalViewHostObservationKey{}).(*originalViewHostObservation); ok && o != nil {
		o.capture(err)
	}
}

// Preserve the underlying typed code without allowing a host error to be
// mistaken for the original Form/Page catchable business-method failure.
type originalViewHostDenied struct{ cause error }

func (e *originalViewHostDenied) Error() string { return e.cause.Error() }
func (e *originalViewHostDenied) Unwrap() error { return e.cause }
