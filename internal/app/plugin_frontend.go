package app

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/seedrescue"
)

// This is the fixed, compiled original SeedRescue federation graph. Runtime
// manifests, configuration, workers and request paths cannot register assets.
var pluginFrontendFiles = map[string]string{
	"remoteEntry.js":                            "text/javascript; charset=utf-8",
	"__federation_fn_import-JrT3xvdd.js":        "text/javascript; charset=utf-8",
	"__federation_expose_Config-CjwiiaeH.js":    "text/javascript; charset=utf-8",
	"__federation_expose_Page-CWBKd09E.js":      "text/javascript; charset=utf-8",
	"__federation_expose_Dashboard-l3LPbQrq.js": "text/javascript; charset=utf-8",
	"__federation_expose_AppPage-t5uu8UeP.js":   "text/javascript; charset=utf-8",
	"SeedRescueWorkbench-C7qM2njz.js":           "text/javascript; charset=utf-8",
	"SeedRescueWorkbench-C0MKqaDa.css":          "text/css; charset=utf-8",
}

func (a *App) pluginFrontendHTTP(w http.ResponseWriter, r *http.Request, path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "plugins" || parts[2] != "frontend" {
		return false
	}
	deny := func() bool { http.NotFound(w, r); return true }
	if len(parts) != 5 || !pluginStorageID.MatchString(parts[1]) || parts[3] != "assets" {
		return deny()
	}
	contentType, exists := pluginFrontendFiles[parts[4]]
	if !exists || !sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return deny()
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return true
	}
	in, err := a.pluginStore.Installation(r.Context(), parts[1])
	if err != nil || !in.Installed || in.PluginID != "SeedRescue" || in.Version != "1.6.3" {
		return deny()
	}
	manifest, _, registered := builtin.Lookup(in.PluginID)
	if !registered || manifest.Version != in.Version {
		return deny()
	}
	data, err := fs.ReadFile(seedrescue.FrontendAssets(), "assets/"+parts[4])
	if err != nil {
		return deny()
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	http.ServeContent(w, r, parts[4], time.Time{}, bytes.NewReader(data))
	return true
}
