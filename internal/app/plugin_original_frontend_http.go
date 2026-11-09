package app

import (
	"errors"
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
	// Original plugins use query strings as cache hints on relative imports.
	// File identity comes only from the verified path/package, as in the source
	// static-file endpoint; queries never select a file or grant authentication.
	if path.Clean(r.URL.Path) != r.URL.Path || strings.ContainsAny(r.URL.Path, "\\\x00\r\n") || !pluginStorageID.MatchString(parts[0]) {
		return fail("INVALID_PLUGIN_FRONTEND_PATH", "插件界面路径无效", 400)
	}
	instance := parts[0]
	asset := strings.SplitN(parts[2], "/", 3)
	alias := len(asset) == 2 && asset[0] == "assets" && asset[1] == "remoteEntry.js"
	if !alias && (len(asset) != 3 || asset[1] != "assets" || !plugins.ValidFrontendPath(asset[2])) {
		return fail("NOT_FOUND", "插件界面资源不存在", 404)
	}
	a.pluginImplementationMu.RLock()
	if alias {
		_, installed, err := a.originalFrontendLocked(r.Context(), instance)
		a.pluginImplementationMu.RUnlock()
		if err != nil {
			a.writeError(w, r, err)
			return true
		}
		if anonymous != nil && anonymous.PackageSHA256 != installed.PackageSHA256 {
			return fail("PLUGIN_FRONTEND_STALE", "插件界面版本已变化，请重新加载", 409)
		}
		target := "/api" + frontendDescriptor(instance, installed).EntryPath
		if onHTTPSMount(r) {
			target = httpsMountPath + target
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return true
	}
	object, err := a.originalFrontendObjectLocked(r.Context(), instance)
	if err != nil {
		a.pluginImplementationMu.RUnlock()
		a.writeError(w, r, err)
		return true
	}
	resource, err := object.frontendReader.ReadAsset(a.pluginPackageRoot(), instance, asset[2], object.validateFrontendManifest, func(installed artifacts.Acquired) error {
		if !object.frontendPackageMatches(installed) {
			return &APIError{"PLUGIN_FRONTEND_INVALID", "插件界面制品校验失败", 409}
		}
		if (anonymous != nil && anonymous.PackageSHA256 != installed.PackageSHA256) || asset[0] != installed.PackageSHA256 {
			return &APIError{"PLUGIN_FRONTEND_STALE", "插件界面版本已变化，请重新加载", 409}
		}
		return nil
	})
	stillLoaded := originalObjectStillLoaded(object)
	a.pluginImplementationMu.RUnlock()
	if errors.Is(err, artifacts.ErrInstalledFrontendPackage) {
		return fail("PLUGIN_FRONTEND_INVALID", "插件界面制品校验失败", 409)
	}
	var bindingErr *APIError
	if errors.As(err, &bindingErr) {
		a.writeError(w, r, bindingErr)
		return true
	}
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
	// Authentication, exact version, package verification and the requested
	// bytes are checked before 304.
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
