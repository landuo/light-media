package app

import (
	"context"
	"fmt"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// getAPIs is the loaded object's get_api method. Route prefixing, the original
// default auth, and per-object error handling belong to the manager. In
// particular this method neither tests get_state nor grants host capabilities.
func (p *originalPluginInstance) getAPIs(ctx context.Context) ([]plugins.HTTPAPI, error) {
	if p.external {
		var apis []plugins.HTTPAPI
		if err := p.readSession(ctx, "get_api", &apis); err != nil {
			return nil, err
		}
		return apis, nil
	}

	// These original get_api bodies are unconditional pass/return []; they are
	// not inferred from an empty manifest or an unrecognized plugin identifier.
	switch p.installation.PluginID {
	case "TorrentTransfer", "TorrentTransfermusic", "IYUUAutoSeed",
		"CleanInvalidSeed", "ScrapeFileClean", "RemoveLink", "CategoryEditor",
		"CustomHosts", "SystemProcess", "SubscribeGroup", "MergeSiteSwitch",
		"RemoteIdentifiers", "MediaServerRefresh", "PluginUnInstall", "MPServerStatus":
		return nil, nil
	case "AutoSignIn", "AutoBackup", "SiteStatistic", "CloudflareSpeedTest",
		"BrushFlowLanduo", "SeedRescue":
		// These six originals return constant route lists. The compiled native
		// catalog is their reviewed Go declaration, including transport parameter
		// bindings. Obtain fresh slices, not mutable loaded manifest data. Keep
		// this explicit list: a future builtin requires its own get_api evidence.
		declaration, _, ok := builtin.Lookup(p.installation.PluginID)
		if !ok {
			return nil, fmt.Errorf("original get_api declaration missing for %s", p.installation.PluginID)
		}
		apis := declaration.APIs
		switch p.installation.PluginID {
		case "AutoSignIn", "AutoBackup", "SiteStatistic", "CloudflareSpeedTest":
			// Original declarations omit auth, unlike some transport manifests.
			// In particular AutoSignIn does NOT declare allow_anonymous. The
			// manager supplies apikey as in MoviePilot's get_plugin_apis.
			for i := range apis {
				apis[i].Auth = ""
			}
		}
		return apis, nil
	default:
		return nil, fmt.Errorf("original get_api method not implemented for %s", p.installation.PluginID)
	}
}
