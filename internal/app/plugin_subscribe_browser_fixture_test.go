//go:build linux && subscribegroup_browser_fixture

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
	"os"
	"path/filepath"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/web"
)

// Owned browser acceptance only. Two existing bound instances are fixtures,
// not a claim that this test creates instances through the generic clone UI.
func TestSubscribeGroupBrowserOwnedFormFixture(t *testing.T) {
	dir, worker, want := os.Getenv("SUBSCRIBEGROUP_BROWSER_DIR"), os.Getenv("SUBSCRIBEGROUP_BROWSER_WORKER"), os.Getenv("SUBSCRIBEGROUP_BROWSER_WORKER_SHA256")
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
		if id != "SubscribeGroup" {
			return plugins.Command{}, fmt.Errorf("unexpected plugin")
		}
		return plugins.Command{Path: worker, Args: []string{"--plugin-worker", id}}, nil
	}
	a.config = Settings{AllowedRoots: []string{a.Dir}}
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	ids := []string{"SubscribeGroup", "SubscribeGroup.owned-second"}
	for i, id := range ids {
		run := uninstallInstall(t, a, id, builtin.SubscribeGroupManifest(), map[string]any{"enabled": false, "category": false, "clear": false, "clear_handle": false, "update_details": []string{}, "update_confs": fmt.Sprintf("category:国漫#include:owned-instance-%d", i+1)}, true)
		a.executePluginRun(ctx, run)
		got, e := a.pluginStore.Run(ctx, run.ID)
		if e != nil || got.State != "succeeded" {
			t.Fatal("initial real worker failed", e, got.State, got.ErrorCode)
		}
		history := []map[string]any{{"name": fmt.Sprintf("合成订阅实例%d-A", i+1), "type": "二级分类自定义配置 国漫", "content": `{"include": "owned-` + fmt.Sprint(i+1) + `"}`, "time": "2026-10-23 11:00:00"}, {"name": fmt.Sprintf("合成订阅实例%d-B", i+1), "type": "种子下载自定义配置", "content": `{"quality": "WEB-?DL|WEB-?RIP"}`, "time": "2026-10-23 12:00:00"}}
		if _, e := a.pluginStore.SaveData(ctx, id, "history", 0, history); e != nil {
			t.Fatal(e)
		}
		if _, e := a.pluginStore.SaveData(ctx, id, "history_handle", 0, []string{fmt.Sprintf("电视剧:owned-%d", i+1)}); e != nil {
			t.Fatal(e)
		}
	}
	a.web = web.Handler()
	listener, err := net.Listen("tcp", "127.0.0.1:3139")
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
	write("ready.json", map[string]any{"url": "http://127.0.0.1:3139", "worker_sha256": want, "instances": ids, "scope": "real App/web and ordinary cmd/server worker; synthetic pre-bound instances and history only; no real subscriptions or accounts"})
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
				if e != nil || json.Unmarshal(cfg.Value, &values) != nil {
					cancel()
					t.Fatal("invalid owned UI config")
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
				saves[id]++
				write("last-save-"+id+".json", map[string]any{"saves": saves[id], "instance": id, "revision": cfg.Revision, "config": values, "run_state": got.State})
			}
		}
	}
}
