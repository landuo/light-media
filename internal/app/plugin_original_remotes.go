package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"net/http"
	"slices"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

// Fixed get_plugin_remotes exposes loaded Vue objects regardless of get_state.
// Keep discovery distinct from the verified Go package descriptor used to load
// an immutable module graph in this host.
func (a *App) originalPluginRemotes(ctx context.Context, pid string) (any, error) {
	result := []map[string]any{}
	for _, object := range a.originalLoadedPluginSnapshot() {
		id := object.installation.InstanceID
		if pid != "" && id != pid {
			continue
		}
		if !slices.Contains(object.manifest.Methods, "get_render_mode") {
			continue
		}
		mode, dist, _, err := object.originalViewRenderMode(ctx)
		if err != nil {
			return nil, err
		}
		if mode != "vue" {
			continue
		}
		name, err := object.originalDisplayName(ctx)
		if err != nil {
			return nil, err
		}
		parts := []string{"plugin", "file", legacyanime.LowerCase(id)}
		if dist = strings.Trim(dist, "/"); dist != "" {
			parts = append(parts, dist)
		}
		parts = append(parts, "remoteEntry.js")
		result = append(result, map[string]any{"id": id, "url": "/" + strings.Join(parts, "/"), "name": name})
	}
	return result, nil
}

// These two fixed aliases have different authentication contracts. The public
// discovery token grants metadata only; it never authorizes package resources,
// navigation, configuration, installation or plugin API calls.
func (a *App) serveOriginalPluginDiscovery(w http.ResponseWriter, r *http.Request) bool {
	name := ""
	for _, prefix := range []string{"/api/v1/plugin/", "/api/v2/plugin/"} {
		if r.URL.Path == prefix+"remotes" {
			name = "remotes"
		}
		if r.URL.Path == prefix+"sidebar_nav" {
			name = "sidebar_nav"
		}
	}
	if name == "" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		pluginHTTPError(w, &pluginHTTPFailure{405, "Method Not Allowed"})
		return true
	}
	if name == "remotes" {
		query := r.URL.Query()
		if _, ok := query["token"]; !ok {
			pluginHTTPError(w, &pluginHTTPFailure{422, []map[string]any{{"type": "missing", "loc": []string{"query", "token"}, "msg": "Field required", "input": nil}}})
			return true
		}
		if plugins.LastQuery(query, "token") != "moviepilot" {
			pluginHTTPError(w, &pluginHTTPFailure{403, "Forbidden"})
			return true
		}
	} else if _, failure := a.pluginHTTPAuthority(r, "bear", false); failure != nil {
		pluginHTTPError(w, failure)
		return true
	}
	var value any
	var err error
	if name == "remotes" {
		value, err = a.originalPluginRemotes(r.Context(), "")
	} else {
		value, err = a.originalPluginSidebarHTTPValue(r.Context(), false)
	}
	if err != nil {
		a.writeError(w, r, err)
	} else {
		pluginHTTPJSON(w, 200, value)
	}
	return true
}

// The fixed sidebar endpoint validates PluginSidebarNavItem after the manager
// getter. Keep that boundary distinct: arbitrary truthy title/icon values may
// survive aggregation, but cannot pass the HTTP string fields.
func (a *App) originalPluginSidebarHTTPValue(ctx context.Context, native bool) (any, error) {
	value, err := a.pluginNavigation(ctx)
	if err != nil {
		return nil, err
	}
	return originalPluginSidebarProjection(value.([]map[string]any), native)
}

func originalPluginSidebarProjection(rows []map[string]any, native bool) (any, error) {
	result := []map[string]any{}
	for _, row := range rows {
		copy := map[string]any{}
		for _, key := range []string{"plugin_id", "nav_key", "title", "icon", "section", "permission", "order"} {
			copy[key] = row[key]
		}
		for _, key := range []string{"title", "icon"} {
			raw, err := json.Marshal(copy[key])
			if err != nil {
				return nil, err
			}
			var text string
			if json.Unmarshal(raw, &text) != nil || string(raw) == "null" {
				return nil, &APIError{"PLUGIN_NAVIGATION_FAILED", "插件侧栏返回格式无效", 500}
			}
		}
		if native {
			copy["instance_id"] = row["instance_id"]
		}
		result = append(result, copy)
	}
	return result, nil
}
