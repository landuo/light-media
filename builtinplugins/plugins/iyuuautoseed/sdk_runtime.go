package iyuuautoseed

import (
	"context"
	"encoding/json"

	sdk "light-media/builtinplugins/internal/sdk"
)

type runtimeHost struct {
	sdkInstanceHost
	sourceDownloaderReads
	*sdkSiteResolver
	cache TorrentCache
}

func newRuntimeHost(caller sdk.HostCaller, cache TorrentCache, skipRaw json.RawMessage) runtimeHost {
	return runtimeHost{
		sdkInstanceHost:       sdkInstanceHost{caller: caller, skipRaw: skipRaw},
		sourceDownloaderReads: sourceDownloaderReads{client: sdk.Downloaders{Host: caller}},
		sdkSiteResolver:       newSDKSiteResolver(sdk.Sites{Host: caller}),
		cache:                 cache,
	}
}

func (h runtimeHost) Torrent(ctx context.Context, site Site, address string) (TorrentContent, error) {
	ref, err := h.Reference(site)
	if err != nil {
		return TorrentContent{}, err
	}
	return downloadSourceTorrent(ctx, h.sites, ref, site, h.cache, address)
}

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "IYUUAutoSeed", Name: "IYUU自动辅种",
		Description: "基于IYUU官方Api实现自动辅种。", Icon: "IYUU.png", Version: "1.0.0",
		Author: "landuo", AuthorURL: "https://github.com/landuo", PluginOrder: 17,
		Runtime: "go-executable", Entry: "iyuu-auto-seed", HostAPIMin: 1, HostAPIMax: 1,
		ManagedPublicServices: true, ManagedPrivateScheduler: true,
		Methods: []string{
			"init", "get_state", "get_form", "get_render_mode", "get_service",
			"get_page", "get_api", "get_command", "auto_seed", "site_deleted",
			"private_scheduler", "pass_continue", "close", "stop_service", "stop",
		},
		Capabilities: []string{
			sdk.BaseGetConfig, sdk.BaseUpdateConfig, sdk.BaseGetDataPath, sdk.BaseGetData, sdk.BaseSaveData,
			sdk.SitesReadCapability, sdk.SitesSourceCustomConfigCapability, sdk.SitesSourceIndexerCapability,
			sdk.SitesSourceCredentialsCapability, sdk.SitesSourceRequestCapability, sdk.SitesSourceCheckCapability,
			sdk.DownloaderConfigurationsReadCapability, sdk.DownloaderSourceConnectionCapability,
			sdk.DownloaderSourceCompletedCapability, sdk.DownloaderSourceTasksCapability,
			sdk.DownloaderSourceAddCapability, sdk.DownloaderSourceFindByTagCapability, sdk.DownloaderSourceControlCapability,
			sdk.BackgroundSubmitCapability, sdk.PostMessageCapability, sdk.LogWriteCapability, sdk.NetworkProxyReadCapability,
		},
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "site.deleted", Method: "site_deleted"}},
		RuntimeSupport:    &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}
