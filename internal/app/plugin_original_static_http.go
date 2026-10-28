package app

import (
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

// Source plugin/file is the code package's ordinary static-file endpoint, not
// the versioned frontend graph. Keep it independent from a live worker: primary
// uninstall retains code in the original, whereas clone uninstall removes it.
func (a *App) serveOriginalPluginStaticFile(w http.ResponseWriter, r *http.Request) bool {
	var suffix string
	matched := false
	for _, prefix := range []string{"/api/v1/plugin/file/", "/api/v2/plugin/file/"} {
		if strings.HasPrefix(r.URL.Path, prefix) {
			suffix = strings.TrimPrefix(r.URL.Path, prefix)
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	parts := strings.SplitN(suffix, "/", 2)
	if len(parts) != 2 {
		pluginHTTPError(w, &pluginHTTPFailure{404, "Not Found"})
		return true
	}
	pluginID, relative := parts[0], strings.TrimLeft(parts[1], "/")
	// Resolve only a directory spelling here. Authentication still precedes
	// filesystem result/status disclosure, as the fixed dependency does.
	codeID := a.originalStaticCodeID(pluginID)
	if !a.authenticated(r) {
		descriptor, err := a.originalAuthFrontend(r.Context(), codeID)
		allowed := false
		if err == nil && descriptor != nil {
			if object, ok := a.pluginRunningInstances.Load(codeID); ok {
				frontend := object.(*originalPluginInstance).manifest.Frontend
				allowed = frontend != nil && (relative == path.Join(frontend.Root, frontend.Entry) || strings.HasPrefix(relative, strings.TrimSuffix(frontend.Root, "/")+"/"))
			}
		}
		if !allowed {
			pluginHTTPError(w, &pluginHTTPFailure{403, "token校验不通过"})
			return true
		}
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		pluginHTTPError(w, &pluginHTTPFailure{405, "Method Not Allowed"})
		return true
	}
	if strings.Contains(pluginID, "..") || strings.Contains(relative, "..") {
		pluginHTTPError(w, &pluginHTTPFailure{403, "Forbidden"})
		return true
	}
	if strings.ContainsAny(relative, "\x00\\") {
		pluginHTTPError(w, &pluginHTTPFailure{400, "Invalid path"})
		return true
	}
	relative = path.Clean(relative)
	if codeID == "" {
		pluginHTTPError(w, &pluginHTTPFailure{404, "Not Found"})
		return true
	}
	a.pluginImplementationMu.RLock()
	resource, err := artifacts.OpenPackageResource(a.pluginPackageRoot(), codeID, relative, validatePluginArtifactContract)
	a.pluginImplementationMu.RUnlock()
	if err != nil {
		status, detail := 404, "Not Found"
		if errors.Is(err, os.ErrPermission) {
			status, detail = 403, "Forbidden"
		}
		pluginHTTPError(w, &pluginHTTPFailure{status, detail})
		return true
	}
	defer resource.Reader.Close()
	kind := mime.TypeByExtension(strings.ToLower(path.Ext(resource.Name)))
	switch strings.ToLower(path.Ext(resource.Name)) {
	case ".js", ".mjs":
		kind = "application/javascript"
	case ".css":
		if kind == "" {
			kind = "text/css"
		}
	}
	if kind == "" {
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", kind)
	// Fixed StreamingResponse encodes header values as Latin1. Preserve its
	// real500 for non-Latin1 filenames; the separate pinned Go frontend ABI
	// has no inline disposition header and is intentionally a different wire.
	disposition := make([]byte, 0, len(resource.Name)+17)
	for _, r := range "inline; filename=" + path.Base(resource.Name) {
		if r > 255 {
			a.log("ERROR", "plugins", "static_stream_failed", "插件静态文件响应头无法编码", "", "")
			pluginHTTPError(w, &pluginHTTPFailure{500, "Internal Server Error"})
			return true
		}
		disposition = append(disposition, byte(r))
	}
	w.Header().Set("Content-Disposition", string(disposition))
	// The fixed StreamingResponse has no Range/conditional-file implementation;
	// preserve its complete 200 stream rather than silently adding ServeContent.
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	buffer := make([]byte, 8<<10)
	for {
		n, readErr := resource.Reader.Read(buffer)
		if n > 0 {
			if _, err := w.Write(buffer[:n]); err != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
	return true
}

func (a *App) originalStaticCodeID(id string) string {
	if !pluginStorageID.MatchString(id) {
		return ""
	}
	entries, err := os.ReadDir(a.pluginPackageRoot())
	if err != nil {
		return ""
	}
	candidate := ""
	for _, entry := range entries {
		if !entry.IsDir() || !pluginStorageID.MatchString(entry.Name()) || plugins.OriginalPluginLower(entry.Name()) != plugins.OriginalPluginLower(id) {
			continue
		}
		if entry.Name() == id {
			return id
		}
		if candidate != "" {
			return ""
		}
		candidate = entry.Name()
	}
	return candidate
}
