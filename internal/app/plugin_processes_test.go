package app

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
)

func TestPluginProcessCPUSamplingDetectsPIDReuse(t *testing.T) {
	at := time.Now()
	previous := pluginCPUSample{created: 100, seconds: 4, at: at}
	current := pluginCPUSample{created: 100, seconds: 5.25, at: at.Add(time.Second)}
	if value := processSampleCPU(previous, current); value != 125 {
		t.Fatal("multi-core CPU value changed", value)
	}
	current.created = 101
	if processSampleCPU(previous, current) != 0 || processSampleCPU(pluginCPUSample{}, current) != 0 {
		t.Fatal("reused PID or first sample inherited CPU")
	}
}

func TestPluginProcessSnapshotIncludesActualProcessWithoutCommandOrEnvironment(t *testing.T) {
	var collector pluginProcessSnapshotter
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	items, err := collector.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.PID == int32(os.Getpid()) {
			found = item.Name != "" && item.Memory > 0 && item.CreateTime > 0 && item.CPU == 0
		}
	}
	if !found {
		t.Fatal("real process and memory not found")
	}
	raw, _ := json.Marshal(items)
	for _, forbidden := range []string{"cmdline", "environ", "exe", "password", "username"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatal("unrequested process details exposed", forbidden)
		}
	}
}

func TestPluginSystemProcessActualWorkerPageConfigAndDashboard(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	h := a.Handler()
	cookie := setupTestAdmin(t, a)
	if result := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"SystemProcess"}`, cookie, ""); result.Code != 200 {
		t.Fatal("install failed", result.Code, result.Body.String())
	}
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	for _, endpoint := range []string{"page", "dashboard"} {
		result := callTest(t, h, "GET", "/api/plugins/SystemProcess/"+endpoint, "", nil, "")
		if result.Code != 401 {
			t.Fatal("view allowed unauthenticated process read", result.Code)
		}
	}
	page := callTest(t, h, "GET", "/api/plugins/SystemProcess/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "进程ID") || !strings.Contains(page.Body.String(), `"text":`+strconv.Itoa(os.Getpid())) || !strings.Contains(page.Body.String(), "VTable") {
		t.Fatal("actual process not rendered by child", page.Code, page.Body.String())
	}
	meta := callTest(t, h, "GET", "/api/plugins/dashboard/meta", "", cookie, "")
	if meta.Code != 200 || strings.Contains(meta.Body.String(), "SystemProcess") {
		t.Fatal("disabled plugin appeared in dashboard", meta.Code)
	}
	config := callTest(t, h, "PUT", "/api/plugins/SystemProcess/config", `{"revision":1,"config":{"enable":true,"dashboard_widget_size":6,"dashboard_widget_refresh":"7"}}`, cookie, "")
	if config.Code != 200 {
		t.Fatal("config save failed", config.Code, config.Body.String())
	}
	run, err = a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	meta = callTest(t, h, "GET", "/api/plugins/dashboard/meta", "", cookie, "")
	if !strings.Contains(meta.Body.String(), `"key":"default"`) || !strings.Contains(meta.Body.String(), "SystemProcess") {
		t.Fatal("enabled dashboard metadata missing", meta.Body.String())
	}
	dashboard := callTest(t, h, "GET", "/api/plugins/SystemProcess/dashboard?key=default", "", cookie, "")
	if dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), `"md":6`) || !strings.Contains(dashboard.Body.String(), `"refresh":7`) || !strings.Contains(dashboard.Body.String(), "242px") {
		t.Fatal("original dashboard configuration missing", dashboard.Code, dashboard.Body.String())
	}
	if w := callTest(t, h, "GET", "/api/plugins/SystemProcess/dashboard?key=other", "", cookie, ""); w.Code != 404 {
		t.Fatal("unknown widget key accepted", w.Code)
	}
	if err := a.pluginStore.Uninstall(context.Background(), builtin.SystemProcessManifest().PluginID); err != nil {
		t.Fatal(err)
	}
	if w := callTest(t, h, "GET", "/api/plugins/SystemProcess/page", "", cookie, ""); w.Code != 404 {
		t.Fatal("uninstalled plugin remained readable", w.Code)
	}
}
