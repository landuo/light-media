package app

import (
	"light-media/internal/plugins/builtin"
	"net/http"
	"strings"
)

// Sole plugin management router. Legacy review/retry/recovery and native
// per-plugin lifecycle routers are intentionally not reachable here.
func (a *App) originalManagerHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	if originalPluginInstallRoute(path, r.Method) {
		return a.originalPluginInstallHTTP(w, r, path)
	}
	if originalPluginUninstallRoute(path, r.Method) {
		return a.originalPluginUninstallHTTP(r, path)
	}
	if path == "markets" {
		return a.pluginMarketsHTTP(w, r)
	}
	if r.Method != http.MethodGet {
		instance := strings.Split(path, "/")[0]
		plugin := instance
		if in, err := a.pluginStore.Installation(r.Context(), instance); err == nil {
			plugin = in.PluginID
		}
		if _, _, native := builtin.Lookup(plugin); native {
			return nil, bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
		}
	}
	if originalPluginConfigurationRoute(path, r.Method) {
		return a.originalPluginConfigurationHTTP(w, r, path)
	}
	if path == "" && r.Method == http.MethodGet {
		return a.pluginCards(r.Context(), false)
	}
	if path == "market" && r.Method == http.MethodGet {
		return a.pluginCards(r.Context(), true)
	}
	if path == "navigation" && r.Method == http.MethodGet {
		return a.pluginNavigation(r.Context())
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost {
		return a.pluginCloneHTTP(w, r, parts[0])
	}
	if len(parts) == 2 && r.Method == http.MethodGet && (parts[1] == "form" || parts[1] == "page" || parts[1] == "dashboard") {
		value, ok := a.pluginRunningInstances.Load(parts[0])
		if !ok {
			return nil, &APIError{"PLUGIN_NOT_LOADED", "插件未加载或尚未适配", 404}
		}
		object := value.(*originalPluginInstance)
		ua := r.UserAgent()
		return a.originalExternalViewHTTP(r.Context(), object, parts[1], r.URL.Query().Get("key"), &ua)
	}
	return nil, &APIError{"NOT_FOUND", "插件接口不存在", 404}
}
