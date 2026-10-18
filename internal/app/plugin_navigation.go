package app

import (
	"context"
	"fmt"
	"sort"

	"light-media/internal/plugins/builtin"
)

// Navigation comes from the actual applied native owner, not a newly saved
// config row or worker-provided route. Failed items follow the original
// manager's per-plugin logging/skip behavior and cannot hide other providers.
func (a *App) pluginNavigation(ctx context.Context) (any, error) {
	installed, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, in := range installed {
		if (in.PluginID != "SeedRescue" && in.PluginID != "BrushFlowLanduo") || !in.Installed || !in.DesiredEnabled {
			continue
		}
		manifest, _, exists := builtin.Lookup(in.PluginID)
		if !exists || manifest.Version != in.Version {
			continue
		}
		var entries []map[string]any
		switch in.PluginID {
		case "SeedRescue":
			entries, err = a.seedRescueSidebar(ctx, in)
		case "BrushFlowLanduo":
			entries, err = a.brushFlowSidebar(ctx, in)
		}
		if err != nil {
			a.log("ERROR", "plugins", "plugin_sidebar_failed", fmt.Sprintf("获取插件[%s]侧栏导航出错，未展示未经确认的入口", in.InstanceID), "", "")
			continue
		}
		for _, entry := range entries {
			entry["plugin_id"] = in.PluginID
			entry["instance_id"] = in.InstanceID
			items = append(items, entry)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		for _, key := range []string{"section", "order", "instance_id", "nav_key"} {
			if key == "order" {
				x, _ := a[key].(int)
				y, _ := b[key].(int)
				if x != y {
					return x < y
				}
			} else {
				x, _ := a[key].(string)
				y, _ := b[key].(string)
				if x != y {
					return x < y
				}
			}
		}
		return false
	})
	return items, nil
}
