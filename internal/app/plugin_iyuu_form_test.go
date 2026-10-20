package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

func TestIYUUActualHTTPPrivateTokenFormAndSave(t *testing.T) {
	for _, tc := range []struct {
		name, submitted, expected string
	}{
		{"round-trip", `""`, "owned-original-private-token"},
		{"omitted", "", "owned-original-private-token"},
		{"null", `null`, "owned-original-private-token"},
		{"replace", `"owned-replacement-private-token"`, "owned-replacement-private-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			ctx := context.Background()
			const instance = "OwnedIYUUPrivateForm"
			uninstallInstall(t, a, instance, iyuu.Manifest(), map[string]any{"enabled": false, "token": "owned-original-private-token", "sites": []any{}, "downloaders": []any{}, "onlyonce": false}, false)
			before, err := a.pluginStore.Config(ctx, instance)
			if err != nil {
				t.Fatal(err)
			}
			w := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/form", "", cookie, "")
			if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("owned-original-private-token")) {
				t.Fatal("form leaked saved token or failed", w.Code)
			}
			var form struct {
				Data struct {
					Model    map[string]any  `json:"model"`
					Conf     json.RawMessage `json:"conf"`
					Revision int64           `json:"revision"`
				} `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &form) != nil || form.Data.Revision != before.Revision || form.Data.Model["token"] != "" || !bytes.Contains(form.Data.Conf, []byte(`"type":"password"`)) || !bytes.Contains(form.Data.Conf, []byte("已保存，留空保留原凭据")) {
				t.Fatal("private token field contract missing")
			}
			afterView, err := a.pluginStore.Config(ctx, instance)
			if err != nil || afterView.Revision != before.Revision || !bytes.Equal(afterView.Value, before.Value) {
				t.Fatal("opening form changed saved configuration")
			}
			if tc.submitted == "" {
				delete(form.Data.Model, "token")
			} else if json.Unmarshal([]byte(tc.submitted), new(any)) != nil {
				t.Fatal("invalid fixture")
			} else {
				var token any
				json.Unmarshal([]byte(tc.submitted), &token)
				form.Data.Model["token"] = token
			}
			save := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance+"/config", string(mustCleanSeedJSON(map[string]any{"revision": form.Data.Revision, "config": form.Data.Model})), cookie, "")
			if save.Code != 200 || bytes.Contains(save.Body.Bytes(), []byte(tc.expected)) {
				t.Fatal("form save failed or disclosed token", save.Code)
			}
			run, err := a.pluginStore.claimRunForInstance(ctx, instance)
			if err != nil {
				t.Fatal(err)
			}
			invokeIYUUActualSDK(t, a, run)
			stored, err := a.pluginStore.Config(ctx, instance)
			if err != nil {
				t.Fatal(err)
			}
			value, err := iyuuSourceMap(stored.Value)
			if err != nil || value["token"] != tc.expected {
				t.Fatal("actual SDK changed retained/replaced token")
			}
			fresh := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/form", "", cookie, "")
			if fresh.Code != 200 || bytes.Contains(fresh.Body.Bytes(), []byte(tc.expected)) {
				t.Fatal("reopened form disclosed token", fresh.Code)
			}
			stale := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance+"/config", string(mustCleanSeedJSON(map[string]any{"revision": form.Data.Revision, "config": form.Data.Model})), cookie, "")
			if stale.Code < 400 {
				t.Fatal("stale form save was admitted")
			}
		})
	}
}

func TestIYUUTokenRetentionDoesNotPanicOnLegacyValues(t *testing.T) {
	for _, token := range []any{[]any{"legacy"}, map[string]any{"legacy": true}, false} {
		value := map[string]any{"token": token}
		if err := retainIYUUToken(value, PluginStoredValue{}); err != nil {
			t.Fatal(err)
		}
	}
}
