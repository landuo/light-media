package app

import (
	"context"
	"encoding/json"
	"strings"

	"light-media/internal/plugins"
)

// getAPIs is the loaded object's get_api method. Route prefixing, the original
// default auth, and per-object error handling belong to the manager. In
// particular this method neither tests get_state nor grants host capabilities.
func (p *originalPluginInstance) getAPIs(ctx context.Context) ([]plugins.HTTPAPI, error) {
	if !p.external {
		return nil, bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
	}
	var raw []json.RawMessage
	if err := p.readSession(ctx, "get_api", &raw); err != nil {
		return nil, err
	}
	return decodeOriginalPluginAPIs(raw, func() {
		p.app.log("ERROR", "plugins", "register_apis_failed", "插件API字段类型无效，已跳过该接口", "", "")
	})
}

// The fixed manager supplies missing auth, while FastAPI supplies GET and
// normalizes its method set. Preserve those defaults at the Go declaration
// boundary; an anonymous source declaration is explicit route authority only.
func decodeOriginalPluginAPIs(raw []json.RawMessage, invalid func()) ([]plugins.HTTPAPI, error) {
	apis := make([]plugins.HTTPAPI, 0, len(raw))
	for _, entry := range raw {
		var fields map[string]json.RawMessage
		var path string
		if json.Unmarshal(entry, &fields) != nil || fields == nil || json.Unmarshal(fields["path"], &path) != nil {
			return nil, bad("PLUGIN_API_INVALID", "插件API声明缺少有效路径")
		}
		// auth is intentionally not a typed enum in the fixed Python interface:
		// any value except "bear" takes the API-key branch. The Go spelling
		// "anonymous" remains an explicit SDK counterpart of allow_anonymous.
		var auth string
		_ = json.Unmarshal(fields["auth"], &auth)
		delete(fields, "auth")
		typed, _ := json.Marshal(fields)
		var api plugins.HTTPAPI
		if err := json.Unmarshal(typed, &api); err != nil {
			if invalid != nil {
				invalid()
			}
			continue
		}
		if originalBaseJSONTruthy(fields["allow_anonymous"]) || auth == "anonymous" {
			api.Auth = "anonymous"
		} else if auth == "bear" {
			api.Auth = "bear"
		} else {
			api.Auth = "apikey"
		}
		if api.Methods == nil {
			api.Methods = []string{"GET"}
		}
		seen := map[string]bool{}
		methods := api.Methods[:0]
		for _, method := range api.Methods {
			method = strings.ToUpper(method)
			if !seen[method] {
				methods = append(methods, method)
				seen[method] = true
			}
		}
		api.Methods = methods
		apis = append(apis, api)
	}
	return apis, nil
}
