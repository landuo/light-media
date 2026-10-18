package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	sr "light-media/internal/plugins/seedrescue"
)

func TestSeedRescueActualSDKLegacyRSSListProjectionAndRetain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		private bool
	}{
		{"legacy-list", []any{"https://owned.example/rss?passkey=owned-private-list-a", "https://owned.example/rss?passkey=owned-private-list-b"}, true},
		{"empty-list", []any{}, false},
		{"null", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			cfg := sr.ParseConfig(sr.Object{}).Current()
			cfg["rss_urls"] = tc.value
			in, run := rescueInstallation(t, a, cfg)
			rescueFinish(t, a, run)
			w := callTest(t, a.Handler(), "GET", "/api/plugins/SeedRescue/form", "", cookie, "")
			if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("owned-private-list-")) {
				t.Fatal("actual SDK form disclosed a private legacy RSS list", w.Code)
			}
			var form struct {
				Data struct {
					Model    sr.Object `json:"model"`
					Revision int64     `json:"revision"`
				} `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &form) != nil {
				t.Fatal("invalid actual form response")
			}
			if tc.private && form.Data.Model["rss_urls"] != seedRescuePrivateRSS {
				t.Fatal("missing private RSS marker")
			}
			save := callTest(t, a.Handler(), "PUT", "/api/plugins/SeedRescue/config", string(mustCleanSeedJSON(map[string]any{"revision": form.Data.Revision, "config": form.Data.Model})), cookie, "")
			if save.Code != 200 {
				t.Fatal("original Vue save failed", save.Code)
			}
			next, err := a.pluginStore.ClaimRun(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			rescueFinish(t, a, next)
			stored, err := a.pluginStore.Config(context.Background(), in.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			var after sr.Object
			if json.Unmarshal(stored.Value, &after) != nil || !bytes.Equal(mustCleanSeedJSON(after["rss_urls"]), mustCleanSeedJSON(tc.value)) {
				t.Fatal("form save changed original RSS list/null shape or contents")
			}
		})
	}
}
