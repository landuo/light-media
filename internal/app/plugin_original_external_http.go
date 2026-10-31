package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"time"
)

// Pure SDK packages have no host grants. Their registered API is a method of
// the already loaded object, not a request to enable or initialize the plugin.
func (a *App) callOriginalExternalHTTP(ctx context.Context, in PluginInstallation, registration *originalAPIRegistration, operation *originalRetainedLease, api plugins.HTTPAPI, input plugins.HTTPRequest, authority pluginAPIAuthority) (plugins.HTTPResponse, error) {
	if _, err := resolveOriginalRegisteredAPI(registration, in); err != nil {
		return plugins.HTTPResponse{}, err
	}
	object := registration.object
	if !object.external {
		return plugins.HTTPResponse{}, bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "插件调用契约不匹配")
	}
	ctx, err := object.originalRetainedParentContext(ctx, operation)
	if err != nil {
		return plugins.HTTPResponse{}, err
	}
	unlock := object.lockOriginalMethod()
	defer unlock()
	check := func() error {
		if _, err := resolveOriginalRegisteredAPI(registration, in); err != nil {
			return err
		}
		if err := a.currentOriginalAPIRegistration(registration, in, api); err != nil {
			return err
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
	result, err := object.callSession(ctx, plugins.Runner{Timeout: 30 * time.Second}, "http", request, nil)
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
