package app

import (
	"context"
	"sort"
)

// initializeOriginalPlugins is the installed-list manager init_config entry:
// stop loaded objects, then start every installed class in its actual manifest plugin_order.
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
	// Resolve actual installed executable metadata before sorting; neither an ID
	// allowlist nor an unauthenticated UI/config value defines class order.
	orders := make(map[string]int, len(installed))
	loadable := installed[:0]
	for _, in := range installed {
		implementation, err := a.resolvePluginImplementation(ctx, in)
		if err != nil {
			a.log("ERROR", "plugins", "load_failed", "插件实现读取失败，继续加载其他插件", "", "")
			continue
		}
		orders[in.InstanceID] = implementation.Manifest.PluginOrder
		loadable = append(loadable, in)
	}
	installed = loadable
	sort.SliceStable(installed, func(i, j int) bool {
		return orders[installed[i].InstanceID] < orders[installed[j].InstanceID]
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
