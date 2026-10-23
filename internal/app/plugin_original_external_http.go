package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"reflect"
	"time"
)

// Pure SDK packages have no host grants. Their registered API is a method of
// the already loaded object, not a request to enable or initialize the plugin.
func (a *App) callOriginalExternalHTTP(ctx context.Context, in PluginInstallation, api plugins.HTTPAPI, input plugins.HTTPRequest, authority pluginAPIAuthority) (plugins.HTTPResponse, error) {
	value, ok := a.pluginRunningInstances.Load(in.InstanceID)
	if !ok {
		return plugins.HTTPResponse{}, bad("PLUGIN_NOT_LOADED", "插件实例尚未加载")
	}
	object := value.(*originalPluginInstance)
	if !object.external || len(object.manifest.Capabilities) != 0 {
		return plugins.HTTPResponse{}, bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "插件调用契约不匹配")
	}
	object.methodMu.Lock()
	defer object.methodMu.Unlock()
	check := func() error {
		if current, ok := a.pluginRunningInstances.Load(in.InstanceID); !ok || current != object {
			return bad("PLUGIN_SNAPSHOT_STALE", "插件调用对象已经被替换")
		}
		if object.installation.PluginID != in.PluginID || object.installation.Version != in.Version || object.installation.Generation != in.Generation {
			return bad("PLUGIN_SNAPSHOT_STALE", "插件实例已变化")
		}
		routes, ok := a.pluginRegisteredAPIs.Load(in.InstanceID)
		matched := false
		if ok {
			for _, route := range routes.([]plugins.HTTPAPI) {
				if reflect.DeepEqual(route, api) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return bad("PLUGIN_API_SCOPE_DENIED", "插件路由已变化")
		}
		return a.validatePluginReadAuthority(ctx, authority)
	}
	if err := check(); err != nil {
		return plugins.HTTPResponse{}, err
	}
	select {
	case a.pluginProcess <- struct{}{}:
	case <-ctx.Done():
		return plugins.HTTPResponse{}, ctx.Err()
	}
	defer func() { <-a.pluginProcess }()
	if err := check(); err != nil {
		return plugins.HTTPResponse{}, err
	}
	request, err := json.Marshal(input)
	if err != nil {
		return plugins.HTTPResponse{}, err
	}
	payload, err := json.Marshal(plugins.Invocation{Action: "api", Config: json.RawMessage("null"), Input: request})
	if err != nil {
		return plugins.HTTPResponse{}, err
	}
	result, err := object.callSession(ctx, plugins.Runner{Timeout: 30 * time.Second}, "invoke", payload, nil)
	if err != nil {
		return plugins.HTTPResponse{}, err
	}
	if err = check(); err != nil {
		return plugins.HTTPResponse{}, err
	}
	var response plugins.HTTPResponse
	if !pluginStrictJSON(result.Result, &response) {
		return response, bad("PLUGIN_API_RESPONSE_INVALID", "插件响应无效")
	}
	if response.Validate() != nil {
		return response, bad("PLUGIN_API_RESPONSE_INVALID", "插件响应无效")
	}
	return response, nil
}
