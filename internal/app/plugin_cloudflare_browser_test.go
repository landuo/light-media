package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/web"
)

// Explicit new-data browser harness. Without its private owned marker the
// ordinary suite opens no listener or persistent directory. Official HTTP is a
// fixture replay; App/SDK/Go executable and temporary Hosts reload remain real.
func TestPluginCloudflareBrowserFixture(t *testing.T) {
	stage := os.Getenv("LIGHT_MEDIA_OWNED_CLOUDFLARE_UI_STAGE")
	if stage == "" {
		return
	}
	if !filepath.IsAbs(stage) || !strings.HasPrefix(filepath.Base(stage), "ui-fixture-stage.") {
		t.Fatal("unowned UI stage")
	}
	owner, e := os.ReadFile(filepath.Join(stage, ".owner"))
	if e != nil || len(strings.TrimSpace(string(owner))) < 20 {
		t.Fatal("owned UI marker missing")
	}
	data, e := os.MkdirTemp(stage, "owned-data-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(data)
	a, e := New(Options{DataDir: data, Web: web.Handler()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	cookie, downloads := cloudflareTestInstall(t, a)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	a.Start(context.Background())
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ready, _ := json.Marshal(map[string]string{"url": server.URL, "data_dir": data, "source_version": "1.5.1", "safe_cli_args": "-h"})
	if e = os.WriteFile(filepath.Join(stage, "ready.json"), ready, 0600); e != nil {
		t.Fatal(e)
	}
	for deadline := time.Now().Add(10 * time.Minute); time.Now().Before(deadline); {
		if _, e = os.Stat(filepath.Join(stage, "stop")); e == nil {
			config, e := a.pluginStore.Config(context.Background(), "CloudflareSpeedTest")
			if e != nil {
				t.Fatal(e)
			}
			summary, _ := json.Marshal(map[string]any{"config_revision": config.Revision, "official_fixture_downloads": downloads.Load(), "temporary_hosts": true})
			if e = os.WriteFile(filepath.Join(stage, "finished.json"), summary, 0600); e != nil {
				t.Fatal(e)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("owned UI fixture expired")
}
