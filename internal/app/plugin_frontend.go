package app

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/seedrescue"
)

// These are the fixed, compiled original plugin federation graphs. Runtime
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

var brushFlowFrontendFiles = map[string]string{
	"remoteEntry.js":                             "text/javascript; charset=utf-8",
	"__federation_fn_import-JrT3xvdd.js":         "text/javascript; charset=utf-8",
	"__federation_expose_Config-CEnA3JSm.js":     "text/javascript; charset=utf-8",
	"__federation_expose_Page-DWtJDoX0.js":       "text/javascript; charset=utf-8",
	"__federation_expose_Dashboard-DeCNUr-1.js":  "text/javascript; charset=utf-8",
	"__federation_expose_Dashboard-BF4A7g-y.css": "text/css; charset=utf-8",
	"__federation_expose_AppPage-CM8LIrza.js":    "text/javascript; charset=utf-8",
	"_plugin-vue_export-helper-L2psG6M3.js":      "text/javascript; charset=utf-8",
	"BrushFlowWorkbench-Bm62dbu_.js":             "text/javascript; charset=utf-8",
	"BrushFlowWorkbench-Cm4ufV31.css":            "text/css; charset=utf-8",
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
	if !sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return deny()
	}
	in, err := a.pluginStore.Installation(r.Context(), parts[1])
	if err != nil || !in.Installed {
		return deny()
	}
	var assets fs.FS
	var files map[string]string
	var version string
	switch in.PluginID {
	case "SeedRescue":
		assets, files, version = seedrescue.FrontendAssets(), pluginFrontendFiles, "1.6.3"
	case "BrushFlowLanduo":
		assets, files, version = brushflow.FrontendAssets(), brushFlowFrontendFiles, "5.2.15"
	default:
		return deny()
	}
	contentType, exists := files[parts[4]]
	if !exists || in.Version != version {
		return deny()
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return true
	}
	manifest, _, registered := builtin.Lookup(in.PluginID)
	if !registered || manifest.Version != in.Version {
		return deny()
	}
	data, err := fs.ReadFile(assets, "assets/"+parts[4])
	if err != nil {
		return deny()
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	http.ServeContent(w, r, parts[4], time.Time{}, bytes.NewReader(data))
	return true
}
