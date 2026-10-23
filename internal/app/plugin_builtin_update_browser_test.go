//go:build linux && builtin_update_browser_fixture

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/web"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Owned synthetic database only. Actual authenticated App handlers and built UI.
func TestPluginBuiltinUpdateBrowserFixture(t *testing.T) {
	dir := os.Getenv("BUILTIN_UPDATE_BROWSER_DIR")
	if !filepath.IsAbs(dir) {
		t.Fatal("owned absolute evidence directory required")
	}
	worker := os.Getenv("BUILTIN_UPDATE_BROWSER_WORKER")
	if !filepath.IsAbs(worker) {
		t.Fatal("actual compiled server worker required")
	}
	workerRaw, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(workerRaw)) != os.Getenv("BUILTIN_UPDATE_BROWSER_WORKER_SHA256") {
		t.Fatal("actual server worker digest changed")
	}
	a := newTestApp(t)
	a.pluginCommand = func(id string) (plugins.Command, error) {
		return plugins.Command{Path: worker, Args: []string{"--plugin-worker", id}}, nil
	}
	a.web = web.Handler()
	setupTestAdmin(t, a)
	ctx := context.Background()
	installCloneSource(t, a.pluginStore, builtin.CategoryEditorManifest())
	if _, err := a.pluginStore.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false, "content": "movie: {}"}); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"safe", "blocked"} {
		in, err := a.pluginStore.Clone(ctx, "CategoryEditor", PluginCloneRequest{Suffix: suffix, Name: "Update QA " + suffix, Version: "display-preserved"})
		if err != nil {
			t.Fatal(err)
		}
		if suffix == "blocked" {
			c, e := a.pluginStore.Config(ctx, in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = a.pluginStore.SaveConfig(ctx, in.InstanceID, c.Revision, map[string]any{"enabled": true, "content": "movie: {}"}); e != nil {
				t.Fatal(e)
			}
		}
		updateOldCategory(t, a.pluginStore, in.InstanceID)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:3143")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(ln)
	write := func(name string, v any) {
		raw, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("ready.json", map[string]string{"url": "http://127.0.0.1:3143", "source": "actual App Handler, compiled UI, owned synthetic legacy installations"})
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(15 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatal("owned browser fixture timeout")
		case <-ticker.C:
			if _, e := os.Stat(filepath.Join(dir, "stop")); e == nil {
				safe := lifecycleInstallation(t, a.pluginStore, "CategoryEditorsafe")
				blocked := lifecycleInstallation(t, a.pluginStore, "CategoryEditorblocked")
				source := lifecycleInstallation(t, a.pluginStore, "CategoryEditor")
				write("final-state.json", map[string]any{"safe": safe.Version, "blocked": blocked.Version, "source": source.Version, "safe_generation": safe.Generation, "blocked_generation": blocked.Generation})
				if safe.Version != "1.3" || blocked.Version != "1.2" || source.Version != "1.3" {
					t.Fatal("UI migration/isolation not established")
				}
				return
			}
		}
	}
}
