package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Fixed manager get_plugin_sidebar_nav: enabled loaded Vue instances only.
// No native ID list, installed/config predicate, or implicit initialization.
func (a *App) pluginNavigation(ctx context.Context) (any, error) {
	objects := map[string]*originalPluginInstance{}
	a.pluginRunningInstances.Range(func(k, v any) bool { objects[k.(string)] = v.(*originalPluginInstance); return true })
	items := []map[string]any{}
	for id, p := range objects {
		enabled, err := p.getState(ctx)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		if !slices.Contains(p.manifest.Methods, "get_sidebar_nav") || !slices.Contains(p.manifest.Methods, "get_render_mode") {
			continue
		}
		mode, _, _, err := p.originalViewRenderMode(ctx)
		if err != nil {
			return nil, err
		}
		if mode != "vue" {
			continue
		}
		raw, _, err := p.originalViewRead(ctx, "get_sidebar_nav", nil)
		var entries []any
		if err != nil || json.Unmarshal(raw, &entries) != nil {
			a.log("ERROR", "plugins", "plugin_sidebar_failed", "插件侧栏方法读取失败", "", "")
			continue
		}
		for _, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok || len(entry) == 0 {
				continue
			}
			key := entry["nav_key"]
			if !sourceSubscriptionTruth(key) {
				key = entry["key"]
			}
			if !sourceSubscriptionTruth(key) {
				key = "main"
			}
			nav := strings.TrimSpace(fmt.Sprint(key))
			if nav == "" || strings.ContainsAny(nav, "/?# ") {
				continue
			}
			title := entry["title"]
			if !sourceSubscriptionTruth(title) {
				title = p.manifest.Name
			}
			icon := entry["icon"]
			if !sourceSubscriptionTruth(icon) {
				icon = "mdi-puzzle"
			}
			section := "system"
			if sourceSubscriptionTruth(entry["section"]) {
				section = strings.ToLower(fmt.Sprint(entry["section"]))
			}
			if !slices.Contains([]string{"start", "discovery", "subscribe", "organize", "system"}, section) {
				section = "system"
			}
			var permission any
			if v := entry["permission"]; v != nil && slices.Contains([]string{"subscribe", "discovery", "search", "manage", "admin"}, fmt.Sprint(v)) {
				permission = fmt.Sprint(v)
			}
			order := 0
			switch v := entry["order"].(type) {
			case float64:
				order = int(v)
			case string:
				order, _ = strconv.Atoi(v)
			case bool:
				if v {
					order = 1
				}
			}
			items = append(items, map[string]any{"plugin_id": id, "instance_id": id, "nav_key": nav, "title": title, "icon": icon, "section": section, "permission": permission, "order": order})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		for _, k := range []string{"section", "order", "plugin_id", "nav_key"} {
			if k == "order" {
				x, y := items[i][k].(int), items[j][k].(int)
				if x != y {
					return x < y
				}
			} else {
				x, y := items[i][k].(string), items[j][k].(string)
				if x != y {
					return x < y
				}
			}
		}
		return false
	})
	return items, nil
}
