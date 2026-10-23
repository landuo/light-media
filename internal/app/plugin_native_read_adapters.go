package app

import (
	bf "light-media/internal/plugins/brushflow"
	sr "light-media/internal/plugins/seedrescue"
)

// Business adapters belong to the host's compiled registration layer. Routing,
// authentication, instance loading and read-only scope remain common code.
func (a *App) compiledPluginConfigurationReaders() []pluginConfigurationHTTPRegistration {
	return []pluginConfigurationHTTPRegistration{
		{manifest: sr.NativeManifest(), capability: sr.ReadCapability, keys: []string{"status", "job"}, read: a.seedRescueReadInstanceConfiguration},
		{manifest: bf.NativeManifest(), capability: bf.ReadCapability, keys: []string{"status", "detail"}, read: a.brushFlowReadInstanceConfiguration},
	}
}

func (a *App) compiledPluginConfigurationWriters() []pluginConfigurationWriteRegistration {
	return []pluginConfigurationWriteRegistration{
		{manifest: bf.NativeManifest(), capability: bf.ConfigureCapability, keys: []string{"create", "update", "state", "delete"}},
		{manifest: bf.NativeManifest(), capability: bf.ConfigureCapability, keys: []string{"settings"}, prepare: a.brushFlowPrepareSettings, result: a.brushFlowReadSettingsResult, verifyApplied: a.brushFlowHTTPRevisionTx}}
}
