// Package builtin contains the Go implementations of migrated MoviePilot
// plugins. A catalog entry is added only when executable behavior exists.
package builtin

import (
	"light-media/internal/plugins"
	"light-media/internal/plugins/autobackup"
	"light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/removelink"
	serverstatus "light-media/internal/plugins/serverstatus"
	"light-media/internal/plugins/uninstall"
)

func Lookup(pluginID string) (plugins.Manifest, plugins.Handler, bool) {
	switch pluginID {
	case "AutoBackup":
		return autobackup.Manifest(), autobackup.Handle, true
	case "CleanInvalidSeed":
		return cleaninvalidseed.Manifest(), cleaninvalidseed.Handle, true
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
	return []plugins.Manifest{autobackup.Manifest(), CategoryEditorManifest(), CustomHostsManifest(), SystemProcessManifest(), MediaServerRefreshManifest(), RemoteIdentifiersManifest(), MergeSiteSwitchManifest(), SiteStatisticManifest(), SubscribeGroupManifest(), uninstall.Manifest(), serverstatus.Manifest(), removelink.Manifest(), cleaninvalidseed.Manifest()}
}
