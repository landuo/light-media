package app

import (
	"net/http"
	"path"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Run after authentication but before ServeMux path cleaning and JSON routing.
// A true return means the response has been written and must not be wrapped.
func (a *App) serveOriginalPluginFrontend(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/api/plugins/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, prefix), "/", 3)
	if len(parts) != 3 || parts[1] != "frontend" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	fail := func(code, message string, status int) bool {
		a.writeError(w, r, &APIError{code, message, status})
		return true
	}
	// Authentication-provider declarations grant only this verified package's
	// frontend directory. Management APIs remain behind the normal session gate.
	var anonymous *originalFrontendDescriptor
	if !a.authenticated(r) {
		var err error
		anonymous, err = a.originalAuthFrontend(r.Context(), parts[0])
		if err != nil {
			return fail("UNAUTHORIZED", "请先登录", 401)
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		return fail("METHOD_NOT_ALLOWED", "插件静态资源仅支持读取", 405)
	}
	if path.Clean(r.URL.Path) != r.URL.Path || strings.ContainsAny(r.URL.Path, "\\\x00\r\n") || r.URL.RawQuery != "" || !pluginStorageID.MatchString(parts[0]) {
		return fail("INVALID_PLUGIN_FRONTEND_PATH", "插件界面路径无效", 400)
	}
	instance := parts[0]
	asset := strings.SplitN(parts[2], "/", 3)
	alias := len(asset) == 2 && asset[0] == "assets" && asset[1] == "remoteEntry.js"
	if !alias && (len(asset) != 3 || asset[1] != "assets" || !plugins.ValidFrontendPath(asset[2])) {
		return fail("NOT_FOUND", "插件界面资源不存在", 404)
	}
	a.pluginImplementationMu.RLock()
	object, installed, err := a.originalFrontendLocked(r.Context(), instance)
	if err != nil {
		a.pluginImplementationMu.RUnlock()
		a.writeError(w, r, err)
		return true
	}
	if anonymous != nil && anonymous.PackageSHA256 != installed.PackageSHA256 {
		a.pluginImplementationMu.RUnlock()
		return fail("PLUGIN_FRONTEND_STALE", "插件界面版本已变化，请重新加载", 409)
	}
	if alias {
		descriptor := frontendDescriptor(instance, installed)
		a.pluginImplementationMu.RUnlock()
		target := "/api" + descriptor.EntryPath
		if onHTTPSMount(r) {
			target = httpsMountPath + target
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return true
	}
	if asset[0] != installed.PackageSHA256 {
		a.pluginImplementationMu.RUnlock()
		return fail("PLUGIN_FRONTEND_STALE", "插件界面版本已变化，请重新加载", 409)
	}
	resource, err := artifacts.ReadFrontendAsset(a.pluginPackageRoot(), instance, asset[0], asset[2], func(m plugins.Manifest, contract string) error {
		if m.PluginID != installed.Manifest.PluginID || m.Version != installed.Manifest.Version || contract != installed.HostContract {
			return bad("PLUGIN_IMPLEMENTATION_MISMATCH", "插件制品已变化")
		}
		return validatePluginArtifactContract(m, contract)
	})
	stillLoaded := originalObjectStillLoaded(object)
	a.pluginImplementationMu.RUnlock()
	if !stillLoaded {
		return fail("PLUGIN_FRONTEND_STALE", "插件界面对象已变化，请重新加载", 409)
	}
	if err != nil {
		return fail("PLUGIN_FRONTEND_INVALID", "插件界面资源不存在或校验失败", 404)
	}
	etag := `"` + resource.SHA256 + `"`
	w.Header().Set("Content-Type", resource.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	// Authentication, exact version and complete bytes are checked before 304.
	if frontendETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(resource.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(resource.Data)
	}
	return true
}

func frontendETagMatches(value, etag string) bool {
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
