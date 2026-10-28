package app

import (
	"net/http"
	"strings"

	"light-media/internal/plugins"
)

// MoviePilot's *2 scheduler endpoints authenticate the integration token in
// the query string, independently of an administrator's cookie or bearer token.
// Reuse the application's stored API token and constant-time comparison.
func (a *App) serveOriginalSchedulerTokenAPI(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if strings.HasPrefix(path, "v1/") || strings.HasPrefix(path, "v2/") {
		path = path[3:]
	}
	mapped := ""
	switch path {
	case "dashboard/schedule2":
		mapped = "dashboard/schedule"
	case "system/runscheduler2":
		mapped = "system/runscheduler"
	default:
		candidate := strings.Replace(path, "dashboard/schedule2/", "dashboard/schedule/", 1)
		if candidate != path {
			if _, ok := originalServiceProgressPath(candidate); ok {
				mapped = candidate
			}
		}
	}
	if mapped == "" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		a.writeError(w, r, &APIError{"METHOD_NOT_ALLOWED", "请求方法不支持", http.StatusMethodNotAllowed})
		return true
	}
	if !apiKeyEqual(plugins.LastQuery(r.URL.Query(), "token"), a.Settings().APIToken) {
		a.writeError(w, r, &APIError{"UNAUTHORIZED", "token 校验不通过", http.StatusUnauthorized})
		return true
	}
	return a.pluginServicesHTTP(w, r, mapped)
}
