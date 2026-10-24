package app

import (
	"context"
	"encoding/json"
	"slices"
)

// Fixed manager get_plugin_dashboard_meta snapshots loaded instances. Each
// object's state and metadata failures are caught independently; no install,
// configuration, queue or implicit initialization is involved.
func (a *App) originalPluginDashboardMetadata(ctx context.Context) []map[string]any {
	type loadedDashboard struct {
		id     string
		object *originalPluginInstance
	}
	objects := []loadedDashboard{}
	a.pluginOriginalRunningMu.Lock()
	for _, id := range a.pluginOriginalRunningOrder {
		if value, ok := a.pluginRunningInstances.Load(id); ok {
			objects = append(objects, loadedDashboard{id, value.(*originalPluginInstance)})
		}
	}
	a.pluginOriginalRunningMu.Unlock()
	result := []map[string]any{}
	for _, loaded := range objects {
		id, object := loaded.id, loaded.object
		if !slices.Contains(object.manifest.Methods, "get_dashboard") {
			continue
		}
		enabled, err := object.getState(ctx)
		if err != nil {
			a.log("ERROR", "plugins", "plugin_dashboard_meta_failed", "插件仪表盘元数据读取失败", "", "")
			continue
		}
		if !enabled {
			continue
		}
		if !slices.Contains(object.manifest.Methods, "get_dashboard_meta") {
			// The original clone changes its copied class plugin_name. The
			// verified instance declaration carries that display identity here;
			// the executable manifest remains the independently pinned code identity.
			metadata, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, id)
			if err != nil {
				a.log("ERROR", "plugins", "plugin_dashboard_meta_failed", "插件仪表盘元数据读取失败", "", "")
				continue
			}
			name := object.manifest.Name
			if metadata.SourceInstanceID != "" {
				name = metadata.Name
			}
			result = append(result, map[string]any{"id": id, "name": name, "key": ""})
			continue
		}
		raw, _, err := object.originalViewRead(ctx, "get_dashboard_meta", nil)
		if err != nil {
			a.log("ERROR", "plugins", "plugin_dashboard_meta_failed", "插件仪表盘元数据读取失败", "", "")
			continue
		}
		if !originalViewJSONTruth(raw) {
			continue
		}
		var values []any
		if json.Unmarshal(raw, &values) != nil {
			a.log("ERROR", "plugins", "plugin_dashboard_meta_failed", "插件仪表盘元数据读取失败", "", "")
			continue
		}
		entries := []map[string]any{}
		valid := true
		for _, value := range values {
			if !sourceSubscriptionTruth(value) {
				continue
			}
			item, ok := value.(map[string]any)
			if !ok {
				valid = false
				break
			}
			entries = append(entries, map[string]any{"id": id, "name": item["name"], "key": item["key"]})
		}
		// Python builds the entire list comprehension before extending the result.
		if !valid {
			a.log("ERROR", "plugins", "plugin_dashboard_meta_failed", "插件仪表盘元数据读取失败", "", "")
			continue
		}
		result = append(result, entries...)
	}
	return result
}
