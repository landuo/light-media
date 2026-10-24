package app

import (
	"context"
	"sort"
)

// initializeOriginalPlugins is the installed-list manager init_config entry:
// stop loaded objects, then start every installed compiled class in plugin_order.
// get_state controls event/service availability, never whether init is called.
func (a *App) initializeOriginalPlugins(ctx context.Context) error {
	a.pluginOriginalRunningMu.Lock()
	running := append([]string(nil), a.pluginOriginalRunningOrder...)
	a.pluginOriginalRunningMu.Unlock()
	for _, instance := range running {
		if err := a.stopOriginalPlugin(ctx, instance); err != nil {
			return err
		}
	}
	installed, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return err
	}
	sort.SliceStable(installed, func(i, j int) bool {
		return originalCompiledPluginOrder(installed[i].PluginID) < originalCompiledPluginOrder(installed[j].PluginID)
	})
	for _, in := range installed {
		a.startOriginalPlugin(ctx, in.InstanceID)
	}
	for _, in := range installed {
		a.updateOriginalPluginJobs(ctx, in.InstanceID)
	}
	a.initOriginalPluginCommands()
	for _, in := range installed {
		a.registerOriginalPluginAPIs(in.InstanceID)
	}
	return nil
}
