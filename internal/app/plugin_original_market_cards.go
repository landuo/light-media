package app

import (
	"slices"

	"light-media/internal/plugins/artifacts"
)

// Source all_plugins combines imported local classes with processed market
// declarations. A catalog name alone is neither a local class nor a repository
// declaration, so it must not create an invented unavailable marketplace card.
func (a *App) originalMarketCards(locals []pluginCard, sources []artifacts.Discovery, states map[string]bool) []pluginCard {
	byID := make(map[string]pluginCard, len(locals))
	for _, local := range locals {
		byID[local.InstanceID] = local
	}
	items := []pluginCard{}
	seen := make(map[string]bool)
	for _, source := range sources {
		local, imported := byID[source.PluginID]
		card := pluginCard{SourceDisplay: source.SourceDisplay, SystemVersion: source.SystemVersion, SystemVersionCompatible: source.SystemVersionCompatible, SystemVersionMessage: source.SystemVersionMessage, Manifest: source.Current.Manifest, InstanceID: source.PluginID,
			RepositoryURL: source.RepositoryURL, RepositoryVersion: source.CurrentVersion,
			Label: source.Label, AddTime: source.AddTime, Available: source.UnavailableReason == "", UnavailableReason: source.UnavailableReason,
			Installed: imported && local.Installed, State: states[source.RepositoryURL+"\x00"+source.PluginID]}
		// _process_plugin_info checks page presence on the current object.
		card.HasPage = false
		if running, ok := a.pluginRunningInstances.Load(source.PluginID); ok {
			card.HasPage = slices.Contains(running.(*originalPluginInstance).manifest.Methods, "get_page")
		}
		if imported {
			version := local.Version
			if local.DisplayVersion != "" {
				version = local.DisplayVersion
			}
			card.HasUpdate = originalMarketVersionGreater(source, artifacts.Discovery{CurrentVersion: version})
			card.SourceInstanceID = local.SourceInstanceID
			card.UpdateBlockedReason = source.UnavailableReason
		}
		if !card.Installed || card.HasUpdate {
			items = append(items, card)
			seen[card.InstanceID] = true
		}
	}
	for _, local := range locals {
		if !local.Installed && !seen[local.InstanceID] {
			items = append(items, local)
		}
	}
	return items
}
