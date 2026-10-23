// Package builtin contains the Go implementations of migrated MoviePilot
// plugins. A catalog entry is added only when executable behavior exists.
package builtin

import (
	"light-media/internal/plugins"
	"light-media/internal/plugins/autobackup"
	"light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/cloudflarespeedtest"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/plugins/removelink"
	"light-media/internal/plugins/scrapefileclean"
	"light-media/internal/plugins/seedrescue"
	serverstatus "light-media/internal/plugins/serverstatus"
	transfer "light-media/internal/plugins/torrenttransfer"
	"light-media/internal/plugins/uninstall"
)

func Lookup(pluginID string) (plugins.Manifest, plugins.Handler, bool) {
	switch pluginID {
	case "AutoSignIn":
		return autosignin.Manifest(), autosignin.Handle, true
	case "TorrentTransfer", "TorrentTransfermusic":
		return transfer.Manifest(pluginID), transfer.Handle, true
	case "CloudflareSpeedTest":
		return cloudflarespeedtest.NativeManifest(), cloudflarespeedtest.NativeHandle, true
	case "BrushFlowLanduo":
		return brushflow.NativeManifest(), brushflow.NativeHandle, true
	case "SeedRescue":
		return seedrescue.NativeManifest(), seedrescue.NativeHandle, true
	case "AutoBackup":
		return autobackup.Manifest(), autobackup.Handle, true
	case "IYUUAutoSeed":
		return iyuu.Manifest(), iyuu.Handle, true
	case "CleanInvalidSeed":
		return cleaninvalidseed.Manifest(), cleaninvalidseed.Handle, true
	case "ScrapeFileClean":
		return scrapefileclean.Manifest(), scrapefileclean.Handle, true
	case "RemoveLink":
		return removelink.Manifest(), removelink.Handle, true
	case "CategoryEditor":
		return CategoryEditorManifest(), HandleCategoryEditor, true
	case "CustomHosts":
		return CustomHostsManifest(), HandleCustomHosts, true
	case "SystemProcess":
		return SystemProcessManifest(), HandleSystemProcess, true
	case "SiteStatistic":
		return SiteStatisticManifest(), HandleSiteStatistic, true
	case "SubscribeGroup":
		return SubscribeGroupManifest(), HandleSubscribeGroup, true
	case "MergeSiteSwitch":
		return MergeSiteSwitchManifest(), HandleMergeSiteSwitch, true
	case "RemoteIdentifiers":
		return RemoteIdentifiersManifest(), HandleRemoteIdentifiers, true
	case "MediaServerRefresh":
		return MediaServerRefreshManifest(), HandleMediaServerRefresh, true
	case "PluginUnInstall":
		return uninstall.Manifest(), uninstall.Handle, true
	case "MPServerStatus":
		return serverstatus.Manifest(), serverstatus.Handle, true
	default:
		return plugins.Manifest{}, nil, false
	}
}

func Catalog() []plugins.Manifest {
	return []plugins.Manifest{autosignin.Manifest(), scrapefileclean.Manifest(), brushflow.NativeManifest(), transfer.Manifest("TorrentTransfer"), transfer.Manifest("TorrentTransfermusic"), seedrescue.NativeManifest(), cloudflarespeedtest.NativeManifest(), iyuu.Manifest(), autobackup.Manifest(), CategoryEditorManifest(), CustomHostsManifest(), SystemProcessManifest(), MediaServerRefreshManifest(), RemoteIdentifiersManifest(), MergeSiteSwitchManifest(), SiteStatisticManifest(), SubscribeGroupManifest(), uninstall.Manifest(), serverstatus.Manifest(), removelink.Manifest(), cleaninvalidseed.Manifest()}
}
