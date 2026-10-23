//go:build linux && iyuu_browser_fixture

package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/sites"
	"light-media/web"
)

// Owned browser acceptance only. Two existing bound instances are fixtures,
// not a claim that this test creates instances through the generic clone UI.
func TestIYUUBrowserOwnedFormFixture(t *testing.T) {
	dir, worker, want := os.Getenv("IYUU_BROWSER_DIR"), os.Getenv("IYUU_BROWSER_WORKER"), os.Getenv("IYUU_BROWSER_WORKER_SHA256")
	if !filepath.IsAbs(dir) || !filepath.IsAbs(worker) || len(want) != 64 {
		t.Fatal("explicit owned paths/digest required")
	}
	raw, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != want {
		t.Fatal("worker digest mismatch")
	}
	a := newTestApp(t)
	setupTestAdmin(t, a)
	ctx := context.Background()
	a.pluginCommand = func(id string) (plugins.Command, error) {
		if id != "IYUUAutoSeed" {
			return plugins.Command{}, fmt.Errorf("unexpected plugin")
		}
		return plugins.Command{Path: worker, Args: []string{"--plugin-worker", id}}, nil
	}
	owned := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("disabled UI fixture attempted native network")
		w.WriteHeader(503)
	}))
	defer owned.Close()
	a.config = Settings{AllowedRoots: []string{a.Dir}, Downloaders: []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: owned.URL}, Name: "Owned qB"}, {Config: download.Config{ID: "owned-tr", Kind: "transmission", BaseURL: owned.URL}, Name: "Owned TR"}}, Sites: []SiteConfig{{Config: sites.Config{ID: "owned-site", Name: "Owned site", Kind: "hhanclub", BaseURL: owned.URL}, Enabled: true}}}
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	if err := a.bootstrapNativeSiteIdentities(ctx); err != nil {
		t.Fatal(err)
	}
	a.pluginIYUU.loopbackAPI = owned.URL
	ids := []string{"IYUUAutoSeed", "IYUUAutoSeed.owned-second"}
	for i, id := range ids {
		run := uninstallInstall(t, a, id, iyuu.Manifest(), map[string]any{"enabled": false, "onlyonce": false, "clearcache": false, "token": "owned-synthetic-token", "sites": []any{}, "downloaders": []any{}, "size": i + 1, "cron": "0 0 * * *", "labelsafterseed": fmt.Sprintf("instance-%d", i)}, true)
		a.executePluginRun(ctx, run)
		got, e := a.pluginStore.Run(ctx, run.ID)
		if e != nil || got.State != "succeeded" {
			t.Fatal("initial real worker failed", e, got.State, got.ErrorCode)
		}
	}
	a.web = web.Handler()
	listener, err := net.Listen("tcp", "127.0.0.1:3138")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	write := func(name string, v any) {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("ready.json", map[string]any{"url": "http://127.0.0.1:3138", "worker_sha256": want, "instances": ids, "scope": "real App/web and ordinary cmd/server worker; synthetic disabled instances"})
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	limit := time.NewTimer(20 * time.Minute)
	defer limit.Stop()
	saves := map[string]int{}
	for {
		select {
		case e := <-done:
			if !errors.Is(e, http.ErrServerClosed) {
				t.Fatal(e)
			}
			return
		case <-limit.C:
			t.Fatal("owned browser fixture deadline")
		case <-ticker.C:
			if _, e := os.Stat(filepath.Join(dir, "stop")); e == nil {
				for _, id := range ids {
					if saves[id] == 0 {
						t.Fatal("missing actual UI save", id)
					}
				}
				write("completed.json", map[string]any{"saves": saves, "owned_processes": len(a.pluginProcess)})
				return
			}
			for _, id := range ids {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				run, e := a.pluginStore.claimRunForInstance(ctx, id)
				if errors.Is(e, sql.ErrNoRows) {
					cancel()
					continue
				}
				if e != nil {
					cancel()
					t.Fatal(e)
				}
				cfg, e := a.pluginStore.Config(ctx, id)
				var values map[string]any
				if e != nil || json.Unmarshal(cfg.Value, &values) != nil || noticeTruth(values["enabled"]) || noticeTruth(values["onlyonce"]) || noticeTruth(values["clearcache"]) {
					cancel()
					t.Fatal("unsafe UI execution flags")
				}
				a.executePluginRun(ctx, run)
				cancel()
				got, e := a.pluginStore.Run(context.Background(), run.ID)
				if e != nil || got.State != "succeeded" || len(a.pluginProcess) != 0 {
					t.Fatal("UI save real worker failed", e, got.State, got.ErrorCode)
				}
				cfg, e = a.pluginStore.Config(context.Background(), id)
				if e != nil {
					t.Fatal(e)
				}
				if json.Unmarshal(cfg.Value, &values) != nil {
					t.Fatal("saved config invalid")
				}
				delete(values, "token")
				saves[id]++
				write("last-save-"+id+".json", map[string]any{"saves": saves[id], "instance": id, "revision": cfg.Revision, "config_without_token": values, "run_state": got.State})
			}
		}
	}
}
