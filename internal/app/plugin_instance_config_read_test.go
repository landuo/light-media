package app

import (
	"context"
	"encoding/json"
	"light-media/internal/download"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/sites"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

func instanceReadTables(t *testing.T, a *App) map[string][]string {
	t.Helper()
	rows, e := a.Store.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'plugin_%' OR name='jobs') ORDER BY name`)
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			t.Fatal(e)
		}
		names = append(names, n)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	out := map[string][]string{}
	for _, n := range names {
		rs, e := a.Store.DB.Query(`SELECT * FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"`)
		if e != nil {
			t.Fatal(e)
		}
		cols, e := rs.Columns()
		if e != nil {
			t.Fatal(e)
		}
		out[n] = []string{}
		for rs.Next() {
			v := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range v {
				ptr[i] = &v[i]
			}
			if e = rs.Scan(ptr...); e != nil {
				t.Fatal(e)
			}
			b, e := json.Marshal(v)
			if e != nil {
				t.Fatal(e)
			}
			out[n] = append(out[n], string(b))
		}
		if e = rs.Err(); e != nil {
			t.Fatal(e)
		}
		rs.Close()
		sort.Strings(out[n])
	}
	return out
}

func TestPluginInstanceConfigurationReadUsesOwnColdCloneWithoutExecution(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	var requests atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected network", 500)
	}))
	defer remote.Close()
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "capacity-owned", Kind: "nexusphp", Name: "Owned capacity", BaseURL: remote.URL}, Enabled: true}}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "capacity-qb", Kind: "qbittorrent", BaseURL: remote.URL}, Name: "Owned capacity qB"}}
	a.cfgMu.Unlock()
	if e := a.bootstrapNativeSiteIdentities(ctx); e != nil {
		t.Fatal(e)
	}
	identity, e := a.Store.nativeSiteByAppID(ctx, "capacity-owned")
	if e != nil {
		t.Fatal(e)
	}
	cfg := bf.DefaultConfig()
	cfg["enabled"] = true
	cfg["tasks"] = []any{bf.Row{"id": "capacity00000000", "name": "Owned capacity", "enabled": true, "site_id": identity.SourceID, "downloader": "Owned capacity qB"}}
	installCloneSource(t, a.pluginStore, bf.NativeManifest())
	if _, e := a.pluginStore.SaveConfig(ctx, bf.PluginID, 0, cfg); e != nil {
		t.Fatal(e)
	}
	clone, e := a.pluginStore.Clone(ctx, bf.PluginID, PluginCloneRequest{Suffix: "ReadOwned"})
	if e != nil {
		t.Fatal(e)
	}
	if clone.DesiredEnabled {
		t.Fatal("clone unexpectedly enabled")
	}
	before := instanceReadTables(t, a)
	for _, endpoint := range []string{"status", "tasks/capacity00000000"} {
		response := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone.InstanceID+"/api/"+endpoint, "", cookie, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Errorf("%s: %d %s", endpoint, response.Code, response.Body.String())
			_, diag := a.pluginStore.readPluginInstanceView(ctx, clone, func(c context.Context, v pluginFormView) (json.RawMessage, error) {
				result, inner := a.brushFlowReadInstanceConfiguration(c, v, plugins.HTTPRequest{Key: "status", Method: "GET"})
				t.Logf("read adapter error type=%T error=%v", inner, inner)
				return result, inner
			})
			t.Logf("read session error type=%T error=%v", diag, diag)
			continue
		}
		if !strings.Contains(response.Body.String(), "Owned capacity") {
			t.Errorf("missing copied configuration: %s", response.Body.String())
		}
	}
	for _, endpoint := range []string{"tasks/capacity00000000/run", "tasks/capacity00000000/check"} {
		response := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/api/"+endpoint, `{}`, cookie, "")
		if response.Code < 400 {
			t.Errorf("disabled execution/write admitted: %s %d", endpoint, response.Code)
		}
	}
	if after := instanceReadTables(t, a); !reflect.DeepEqual(before, after) {
		t.Error("configuration read or rejected execution changed plugin state/jobs")
	}
	if requests.Load() != 0 {
		t.Fatalf("read triggered network: %d", requests.Load())
	}
}
