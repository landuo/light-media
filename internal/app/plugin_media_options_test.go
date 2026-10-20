package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestPluginMediaRefreshNativeFormUsesSourceEnabledConfigsWithoutLosingSelection(t *testing.T) {
	f := newRefreshFixture(t, 0)
	a := f.app
	ctx := context.Background()
	initial, e := a.pluginStore.Config(ctx, "MediaServerRefresh")
	if e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name    string
		enabled bool
		host    string
		options int
	}{{"configured_enabled", true, a.Settings().FNOS.Host, 1}, {"configured_disabled", false, a.Settings().FNOS.Host, 0}, {"configured_enabled_without_host", true, "", 1}, {"absent_or_disabled_without_host", false, "", 0}, {"reenabled", true, a.Settings().FNOS.Host, 1}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a.cfgMu.Lock()
			a.config.FNOS.Enabled = c.enabled
			a.config.FNOS.Host = c.host
			a.cfgMu.Unlock()
			w := callTest(t, a.Handler(), "GET", "/api/plugins/MediaServerRefresh/form", "", f.cookie, "")
			if w.Code != 200 {
				t.Fatal("actual form request failed", w.Code)
			}
			var response struct {
				Data struct {
					Conf     []map[string]any           `json:"conf"`
					Model    map[string]json.RawMessage `json:"model"`
					Revision int64                      `json:"revision"`
				} `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatal("invalid actual form")
			}
			var found []any
			var visit func([]map[string]any)
			visit = func(nodes []map[string]any) {
				for _, n := range nodes {
					if n["component"] == "VSelect" {
						p, _ := n["props"].(map[string]any)
						if p["model"] == "mediaservers" {
							found, _ = p["items"].([]any)
						}
					}
					if children, ok := n["content"].([]any); ok {
						list := []map[string]any{}
						for _, child := range children {
							if value, ok := child.(map[string]any); ok {
								list = append(list, value)
							}
						}
						visit(list)
					}
				}
			}
			visit(response.Data.Conf)
			if found == nil || len(found) != c.options {
				t.Fatalf("source enabled-config list mismatch: got %d, want %d", len(found), c.options)
			}
			if c.options == 1 {
				choice, ok := found[0].(map[string]any)
				if !ok || choice["title"] != "飞牛影视" || choice["value"] != "fnos" {
					t.Fatal("native reference projection changed")
				}
			}
			if !bytes.Equal(response.Data.Model["mediaservers"], []byte(`["fnos"]`)) || !bytes.Equal(response.Data.Model["enabled"], []byte(`true`)) || response.Data.Revision != initial.Revision {
				t.Fatal("unavailable core service cleared the selected plugin reference")
			}
			stored, e := a.pluginStore.Config(ctx, "MediaServerRefresh")
			if e != nil || stored.Revision != initial.Revision || !bytes.Equal(stored.Value, initial.Value) {
				t.Fatal("reading dynamic form mutated plugin config")
			}
			if f.scans.Load() != 0 || f.logins.Load() != 0 {
				t.Fatal("form read invoked media business IO")
			}
		})
	}
}
