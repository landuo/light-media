package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPluginDashboardPreferencesEncryptedAuthProfilesAndCold(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	cookie := setupTestAdmin(t, a)
	endpoint := "/api/plugins/dashboard/settings/desktop"
	if w := callTest(t, h, "GET", endpoint, "", nil, ""); w.Code != 401 {
		t.Fatal("anonymous layout read allowed", w.Code)
	}
	if w := callTest(t, h, "GET", endpoint, "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"value":null`) {
		t.Fatal("missing settings must remain absent", w.Code)
	}
	raw := `{"items":{"SystemProcess":{"x":2,"y":3,"w":6,"h":20}},"enabled":{"SystemProcess":true},"updatedAt":42}`
	if w := callTest(t, h, "POST", endpoint, raw, cookie, ""); w.Code != 200 {
		t.Fatal("save failed", w.Code)
	}
	if w := callTest(t, h, "POST", endpoint, `{}`, cookie, "https://owned-other.invalid"); w.Code != 403 {
		t.Fatal("cross-origin preference write", w.Code)
	}
	if w := callTest(t, h, "GET", "/api/plugins/dashboard/settings/mobile", "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"value":null`) {
		t.Fatal("desktop leaked into mobile", w.Code)
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='plugin_dashboard_settings' AND id='desktop'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "SystemProcess") {
		t.Fatal("preferences not encrypted")
	}
	// A cold provider reads the same ciphertext without creating a run or plugin
	// configuration row. This proves the durable layer independently of JS cache.
	p, err := NewPluginStore(context.Background(), a.Store)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := p.unseal("plugin_dashboard_settings", "admin", "desktop", sealed)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	json.Unmarshal(plain, &got)
	json.Unmarshal([]byte(raw), &want)
	if string(plain) != raw {
		t.Fatal("layout changed at rest")
	}
	for _, key := range []string{"tablet", "mobile", "order"} {
		if _, err = p.unseal("plugin_dashboard_settings", "admin", key, sealed); err == nil {
			t.Fatal("ciphertext not bound to profile", key)
		}
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("layout change queued business work", count, err)
	}
}
func TestPluginSystemProcessOriginalConfigAndResetThroughHTTP(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	h := a.Handler()
	cookie := setupTestAdmin(t, a)
	if w := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"SystemProcess"}`, cookie, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	raw := `{"revision":1,"config":{"enable":"false","dashboard_widget_size":"6","dashboard_widget_refresh":"0","unknown":{"retained":true}}}`
	if w := callTest(t, h, "PUT", "/api/plugins/SystemProcess/config", raw, cookie, ""); w.Code != 200 {
		t.Fatal("source-valid raw values rejected", w.Code, w.Body.String())
	}
	run, err = a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	form := callTest(t, h, "GET", "/api/plugins/SystemProcess/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), `"enable":"false"`) || !strings.Contains(form.Body.String(), `"unknown":{"retained":true}`) {
		t.Fatal("source form/config projection changed", form.Code, form.Body.String())
	}
	meta := callTest(t, h, "GET", "/api/plugins/dashboard/meta", "", cookie, "")
	if meta.Code != 200 || !strings.Contains(meta.Body.String(), "SystemProcess") {
		t.Fatal("nonempty string must be source truthy")
	}
	if w := callTest(t, h, "POST", "/api/plugins/SystemProcess/reset", `{}`, cookie, ""); w.Code != 200 {
		t.Fatal("reset failed", w.Code)
	}
	meta = callTest(t, h, "GET", "/api/plugins/dashboard/meta", "", cookie, "")
	if meta.Code != 200 || strings.Contains(meta.Body.String(), "SystemProcess") {
		t.Fatal("reset dashboard remained active")
	}
	if w := callTest(t, h, "POST", "/api/plugins/SystemProcess/uninstall", `{}`, cookie, ""); w.Code != 200 {
		t.Fatal("uninstall failed", w.Code)
	}
	if w := callTest(t, h, "GET", "/api/plugins/SystemProcess/page", "", cookie, ""); w.Code != 404 {
		t.Fatal("uninstalled process page remained exposed", w.Code)
	}
}
