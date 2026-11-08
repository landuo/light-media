package app

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"testing"
)

func TestBrushFlowInstalledAMD64NavigationAndDashboard(t *testing.T) {
	bundle := os.Getenv("SOURCE_BRUSH_FLOW_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the exact owned Linux amd64 BrushFlow package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	a.Start(t.Context())
	installBrushCandidate(t, a, cookie, bundle)
	handler := a.Handler()
	get := func(path string, value any) {
		t.Helper()
		response := callTest(t, handler, http.MethodGet, path, "", cookie, "")
		var body struct{ Data json.RawMessage }
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || json.Unmarshal(body.Data, value) != nil {
			t.Fatalf("actual view route %s failed: %d %s", path, response.Code, response.Body.String())
		}
	}
	var nav []struct {
		PluginID string `json:"plugin_id"`
		NavKey   string `json:"nav_key"`
		Section  string `json:"section"`
		Order    int    `json:"order"`
	}
	var meta []struct{ ID, Key string }
	get("/api/plugins/navigation", &nav)
	get("/api/plugins/dashboard/meta", &meta)
	if len(nav) != 0 || len(meta) != 0 {
		t.Fatal("disabled BrushFlow appeared in source-gated navigation or dashboard")
	}
	const base = "/api/plugins/" + brushPackageFixtureID
	saved := callTest(t, handler, http.MethodPost, base+"/api/settings", `{"enabled":true,"show_sidebar_nav":true}`, cookie, "")
	var result struct{ Success bool }
	if saved.Code != http.StatusOK || json.Unmarshal(saved.Body.Bytes(), &result) != nil || !result.Success {
		t.Fatal("BrushFlow enable did not take effect", saved.Body.String())
	}
	get("/api/plugins/navigation", &nav)
	get("/api/plugins/dashboard/meta", &meta)
	if len(nav) != 1 || nav[0].PluginID != brushPackageFixtureID || nav[0].NavKey != "main" || nav[0].Section != "organize" || nav[0].Order != 45 ||
		len(meta) != 1 || meta[0].ID != brushPackageFixtureID || meta[0].Key != "" {
		t.Fatalf("source navigation or default dashboard registration mismatch: %v %v", nav, meta)
	}
	var dashboard struct {
		RenderMode string `json:"render_mode"`
		Cols       struct{ Cols, SM, MD int }
		Attrs      struct {
			Title, Subtitle string
			Refresh         int
			Border          bool
		}
		Elements json.RawMessage
	}
	get(base+"/dashboard", &dashboard)
	if dashboard.RenderMode != "vue" || dashboard.Cols.Cols != 12 || dashboard.Cols.SM != 6 || dashboard.Cols.MD != 6 ||
		dashboard.Attrs.Title != "站点刷流" || dashboard.Attrs.Subtitle != "多任务运行概览" || dashboard.Attrs.Refresh != 30 || !dashboard.Attrs.Border || string(dashboard.Elements) != "null" {
		t.Fatalf("source dashboard tuple changed: %+v", dashboard)
	}
	t.Log("final ZIP installed; enabled source navigation and Vue dashboard matched and disabled declarations stayed hidden")
}
