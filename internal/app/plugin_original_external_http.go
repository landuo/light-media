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
	if !object.external {
		return plugins.HTTPResponse{}, bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "插件调用契约不匹配")
	}
	unlock := object.lockOriginalMethod()
	defer unlock()
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
	release, err := object.acquireOriginalProcessSlot(ctx)
	if err != nil {
		return plugins.HTTPResponse{}, err
	}
	defer release()
	if err := check(); err != nil {
		return plugins.HTTPResponse{}, err
	}
	request, err := json.Marshal(input)
	if err != nil {
		return plugins.HTTPResponse{}, err
	}
	callCtx := ctx
	retained := false
	finish := func() {}
	if object.manifest.ManagedPublicServices {
		callCtx, finish, err = object.beginOriginalBoundEntry(ctx, originalSourceAPI)
		if err != nil {
			return plugins.HTTPResponse{}, err
		}
		retained = true
	}
	if retained && object.installation.PluginID == "AutoBackup" && api.Key == "api_backup" && api.Path == "/backup" &&
		api.Auth == "apikey" && authority.Kind == "key" && input.Method == "GET" && input.CredentialMatches["apikey"] {
		callCtx = context.WithValue(callCtx, originalLocalBackupAPIKey{}, object)
	}
	if retained && object.installation.PluginID == "CloudflareSpeedTest" && api.Key == "cloudflare_speedtest" &&
		api.Path == "/cloudflare_speedtest" && api.Auth == "apikey" && authority.Kind == "key" &&
		input.Method == "GET" && input.CredentialMatches["apikey"] {
		callCtx = context.WithValue(callCtx, originalCloudflareAPIKey{}, object)
	}
	if object.installation.PluginID == "LarkMessager" && api.Key == "webhook" && api.Path == "/webhook" &&
		api.Auth == "anonymous" && authority.Kind == "anonymous" && api.Body == "raw" {
		callCtx = context.WithValue(callCtx, originalLarkWebhookKey{}, originalLarkWebhookGrant{object: object, verified: a.originalLarkVerifiedWebhook(ctx, object, input)})
	}
	if retained && object.installation.PluginID == "SeedRescue" && api.Auth == "bear" &&
		(authority.Kind == "session" || authority.Kind == "key") {
		if grant := originalSeedRescueHTTPGrantFor(object, api, input); grant != nil {
			callCtx = context.WithValue(callCtx, originalSeedRescueHTTPKey{}, grant)
		}
	}
	invoke := func() (plugins.HTTPResponse, error) {
		defer finish()
		result, err := object.callSession(callCtx, plugins.Runner{Timeout: 30 * time.Second}, "http", request, nil)
		if err != nil {
			return plugins.HTTPResponse{}, err
		}
		// Source route dependencies and the method authenticate on entry.
		// Once admitted, later route removal or key rotation does not retract
		// the completed result. New requests and every ordinary resource guard
		// still use their existing current authority checks.
		if !retained {
			if err = check(); err != nil {
				return plugins.HTTPResponse{}, err
			}
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
	if !retained {
		return invoke()
	}
	// The admitted source method belongs to its exact object, independently
	// of the HTTP waiter. Uvicorn can cancel that waiter without stopping its
	// synchronous AnyIO function. The physical call owns finish and App.wg.
	// beginOriginalBoundEntry required the negotiated concurrent client, so
	// this path's legacy method/process unlock functions are both no-ops.
	type completedAPI struct {
		response plugins.HTTPResponse
		err      error
	}
	completed := make(chan completedAPI, 1)
	go func() {
		defer func() {
			if recover() != nil {
				completed <- completedAPI{err: bad("PLUGIN_API_EXECUTION_FAILED", "插件 API 调用异常")}
			}
		}()
		response, err := invoke()
		completed <- completedAPI{response: response, err: err}
	}()
	select {
	case result := <-completed:
		return result.response, result.err
	case <-ctx.Done():
		return plugins.HTTPResponse{}, bad("PLUGIN_API_REQUEST_CANCELLED", "插件 API 请求已结束")
	}
}
