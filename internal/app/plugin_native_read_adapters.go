package app

import bf "light-media/internal/plugins/brushflow"

// Business adapters belong to the host's compiled registration layer. Routing,
// authentication, instance loading and read-only scope remain common code.
func (a *App) compiledPluginConfigurationReaders() []pluginConfigurationHTTPRegistration {
	return []pluginConfigurationHTTPRegistration{
		{manifest: bf.NativeManifest(), capability: bf.ReadCapability, keys: []string{"status", "detail"}, read: a.brushFlowReadInstanceConfiguration},
	}
}
