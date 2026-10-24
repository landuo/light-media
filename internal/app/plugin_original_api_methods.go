package app

import (
	"context"

	"light-media/internal/plugins"
)

// getAPIs is the loaded object's get_api method. Route prefixing, the original
// default auth, and per-object error handling belong to the manager. In
// particular this method neither tests get_state nor grants host capabilities.
func (p *originalPluginInstance) getAPIs(ctx context.Context) ([]plugins.HTTPAPI, error) {
	if !p.external {
		return nil, bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
	}
	var apis []plugins.HTTPAPI
	if err := p.readSession(ctx, "get_api", &apis); err != nil {
		return nil, err
	}
	return apis, nil
}
