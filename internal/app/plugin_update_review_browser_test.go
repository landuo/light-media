//go:build linux && builtin_update_browser_fixture

package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
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

func TestPluginUpdateReviewBrowserFixture(t *testing.T) {
	dir := os.Getenv("BUILTIN_UPDATE_BROWSER_DIR")
	worker := os.Getenv("BUILTIN_UPDATE_BROWSER_WORKER")
	if !filepath.IsAbs(dir) || !filepath.IsAbs(worker) {
		t.Fatal("owned absolute paths required")
	}
	raw, e := os.ReadFile(worker)
	if e != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != os.Getenv("BUILTIN_UPDATE_BROWSER_WORKER_SHA256") {
		t.Fatal("worker digest", e)
	}
	a := newTestApp(t)
	a.web = web.Handler()
	setupTestAdmin(t, a)
	a.pluginCommand = func(id string) (plugins.Command, error) {
		return plugins.Command{Path: worker, Args: []string{"--plugin-worker", id}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	installCloneSource(t, a.pluginStore, builtin.CategoryEditorManifest())
	if _, e = a.pluginStore.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false, "content": "movie: {}"}); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"review", "sibling"} {
		in, e := a.pluginStore.Clone(ctx, "CategoryEditor", PluginCloneRequest{Suffix: suffix, Name: "Review QA " + suffix, Version: "display-preserved"})
		if e != nil {
			t.Fatal(e)
		}
		c, e := a.pluginStore.Config(ctx, in.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.pluginStore.SaveConfig(ctx, in.InstanceID, c.Revision, map[string]any{"enabled": true, "content": customJapaneseMovieCategory}); e != nil {
			t.Fatal(e)
		}
		updateOldCategory(t, a.pluginStore, in.InstanceID)
	}
	siblingBefore := lifecycleInstallation(t, a.pluginStore, "CategoryEditorsibling")
	siblingConfig, _ := a.pluginStore.Config(ctx, siblingBefore.InstanceID)
	done := make(chan error, 1)
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-tick.C:
				run, e := a.pluginStore.ClaimRun(ctx)
				if errors.Is(e, sql.ErrNoRows) {
					continue
				}
				if e != nil {
					done <- e
					return
				}
				if e = a.executePluginRun(ctx, run); e != nil {
					done <- e
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			t.Error("owned execution worker did not join")
		}
	}()
	listener, e := net.Listen("tcp", "127.0.0.1:3143")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	write := func(name string, v any) {
		raw, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("ready.json", map[string]string{"url": "http://127.0.0.1:3143", "target": "CategoryEditorreview", "sibling": "CategoryEditorsibling", "scope": "owned synthetic actual App and normal server worker"})
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(15 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatal("owned browser timeout")
		case <-ticker.C:
			if _, e = os.Stat(filepath.Join(dir, "stop")); e != nil {
				continue
			}
			target := lifecycleInstallation(t, a.pluginStore, "CategoryEditorreview")
			sibling := lifecycleInstallation(t, a.pluginStore, "CategoryEditorsibling")
			source := lifecycleInstallation(t, a.pluginStore, "CategoryEditor")
			config, e := a.pluginStore.Config(ctx, target.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			siblingAfter, _ := a.pluginStore.Config(ctx, sibling.InstanceID)
			review, e := a.pluginStore.pluginUpdateReviewRead(ctx, a.Store.DB, target)
			if e != nil {
				t.Fatal(e)
			}
			file, e := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
			if e != nil {
				t.Fatal(e)
			}
			var runs, succeeded int
			a.Store.DB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(state='succeeded'),0) FROM plugin_runs WHERE instance_id=?`, target.InstanceID).Scan(&runs, &succeeded)
			write("final-state.json", map[string]any{"target_version": target.Version, "target_generation": target.Generation, "target_config": config, "review_pending": review != nil, "target_runs": runs, "target_succeeded": succeeded, "source_version": source.Version, "source_generation": source.Generation, "sibling_version": sibling.Version, "sibling_generation": sibling.Generation, "sibling_config_unchanged": string(siblingAfter.Value) == string(siblingConfig.Value) && siblingAfter.Revision == siblingConfig.Revision, "owned_file_matches": string(file) == customJapaneseMovieCategory})
			if target.Version != "1.3" || target.Generation != 2 || review != nil || runs != 1 || succeeded != 1 || sibling.Version != siblingBefore.Version || sibling.Generation != siblingBefore.Generation || string(sibling.Manifest) != string(siblingBefore.Manifest) || string(siblingAfter.Value) != string(siblingConfig.Value) || siblingAfter.Revision != siblingConfig.Revision || source.Generation != 1 || source.Version != "1.3" || string(file) != customJapaneseMovieCategory {
				t.Fatal("review activation/isolation contract failed")
			}
			return
		}
	}
}
