package app

import (
	"context"
	"encoding/json"
	"fmt"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// These compiled implementations are native Go objects. External packages keep
// their SDK process. Neither path retains a previous invocation's host authority.
type originalBuiltinHostKey struct{}
type originalBuiltinHostState struct {
	object   originalBuiltinObject
	instance *originalPluginInstance
}

type originalBuiltinObject interface {
	Handle(context.Context, string, json.RawMessage, plugins.HostCaller) (json.RawMessage, error)
}

func newOriginalBuiltinObject(id string) originalBuiltinObject {
	switch id {
	case "CategoryEditor":
		return builtin.NewOriginalCategoryEditor()
	case "MediaServerRefresh":
		return builtin.NewOriginalMediaServerRefresh()
	case "SiteStatistic":
		return builtin.NewOriginalSiteStatistic()
	case "MergeSiteSwitch":
		return builtin.NewOriginalMergeSiteSwitch()
	default:
		return nil
	}
}
func originalBuiltinConfiguration(object originalBuiltinObject) any {
	switch object := object.(type) {
	case *builtin.OriginalCategoryEditor:
		return map[string]any{"enabled": object.State()}
	case *builtin.OriginalMediaServerRefresh:
		return object.Configuration()
	case *builtin.OriginalSiteStatistic:
		return object.Configuration()
	case *builtin.OriginalMergeSiteSwitch:
		return object.Configuration()
	default:
		return nil
	}
}
func (p *originalPluginInstance) callOriginalBuiltin(ctx context.Context, method string, raw json.RawMessage, host plugins.HostHandler) (plugins.RunResult, bool, error) {
	var object originalBuiltinObject
	originalInit := originalInitializationMatches(ctx, p.installation.InstanceID, p.installation.PluginID, p.installation.Version, p.installation.Generation, "init")
	if originalInit {
		object, _ = originalNativeStateBeforeInit(ctx).(originalBuiltinObject)
		if object == nil {
			object = newOriginalBuiltinObject(p.installation.PluginID)
		}
	} else {
		p.methodMu.Lock()
		defer p.methodMu.Unlock()
		p.configMu.RLock()
		object, _ = p.nativeState.(originalBuiltinObject)
		p.configMu.RUnlock()
	}
	if object == nil {
		return plugins.RunResult{}, false, nil
	}
	if originalInit {
		captureOriginalNativeState(ctx, object)
		defer func() { captureOriginalAppliedConfiguration(ctx, originalBuiltinConfiguration(object)) }()
	}
	identity := plugins.Identity{PluginID: p.installation.PluginID, InstanceID: p.installation.InstanceID, Version: p.installation.Version}
	result, err := object.Handle(ctx, method, raw, func(callCtx context.Context, name string, payload json.RawMessage) (json.RawMessage, error) {
		if host == nil {
			return nil, fmt.Errorf("host call denied outside admitted invocation")
		}
		return host(context.WithValue(callCtx, originalBuiltinHostKey{}, &originalBuiltinHostState{object: object, instance: p}), identity, name, payload)
	})
	return plugins.RunResult{Result: result}, true, err
}

// Read the actual native member object during its currently admitted host call.
// This carries no authority: pluginHostCall still authenticates the run first.
func originalBuiltinHostConfiguration(ctx context.Context, a *App, instance, plugin string) (any, bool) {
	state, ok := ctx.Value(originalBuiltinHostKey{}).(*originalBuiltinHostState)
	if !ok || state == nil || state.instance == nil || state.instance.app != a || state.instance.installation.InstanceID != instance || state.instance.installation.PluginID != plugin {
		return nil, false
	}
	return originalBuiltinConfiguration(state.object), true
}
