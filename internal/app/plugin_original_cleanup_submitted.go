package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// Cleanup continuations stay owned by the real SDK cleanup root. They never
// reserve ordinary submitted-object lifetime or create another task registry.
func (a *App) originalCleanupSubmitResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	p := scope.object
	if p == nil || p.app != a || scope.phase != "stop" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "清理续跑未绑定实际停止调用")
	}
	marker, ok := ctx.Value(originalBaseStopKey{}).(*originalBaseStopCall)
	if !ok || marker == nil || marker.object != p || marker.client == nil {
		return nil, bad("PLUGIN_CALL_CLOSED", "清理续跑未绑定实际客户端")
	}
	var in struct {
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := originalResourceDecode(raw, &in); err != nil {
		return nil, err
	}
	if !slices.Contains(p.manifest.Methods, in.Method) || len(in.Payload) == 0 || !json.Valid(in.Payload) || strings.HasPrefix(in.Method, "transport.") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "清理续跑方法未声明或参数无效")
	}
	switch in.Method {
	case "init", "handshake", "close", "stop_service", "stop":
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "清理续跑不得重入生命周期")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	rootCtx, err := marker.client.CleanupContext(ctx)
	if err != nil {
		return nil, err
	}
	callback, revoke := p.originalBaseSessionHost(rootCtx, "stop", nil)
	pending, err := marker.client.StartCleanupContinuationWithAdmission(ctx, in.Method, in.Payload, callback)
	if err != nil {
		revoke()
		return nil, err
	}
	// The SDK root joins actual replies/callback drain and propagates child
	// errors. This tail only revokes the fresh App closure; it owns no App work,
	// storage, ordinary lifetime lease, or detached execution after root close.
	go func() { _, _ = pending.Wait(); revoke() }()
	return json.RawMessage(`{"accepted":true}`), nil
}
