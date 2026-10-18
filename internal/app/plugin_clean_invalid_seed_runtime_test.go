package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	seed "light-media/internal/plugins/cleaninvalidseed"
)

// These tests deliberately enter executePluginRun and the compiled child
// catalog. They do not call the owner adapter or settle receipts themselves.
func cleanSeedRuntimeExecute(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	a.executePluginRun(context.Background(), run)
	got, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.pluginProcess) != 0 {
		t.Fatal("worker process slot retained after native execution")
	}
	return got
}

func TestPluginCleanInvalidSeedRuntimeHTTPInstallAndOriginalOnceFlag(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	h, cookie := a.Handler(), setupTestAdmin(t, a)
	installed := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"CleanInvalidSeed"}`, cookie, "")
	if installed.Code != 200 {
		t.Fatal("compiled installation", installed.Code, installed.Body.String())
	}
	run := cleanSeedOwnerClaim(t, a, "CleanInvalidSeed")
	if got := cleanSeedRuntimeExecute(t, a, run); got.State != "succeeded" {
		t.Fatal("default init", got.State, got.ErrorCode)
	}
	form := callTest(t, h, "GET", "/api/plugins/CleanInvalidSeed/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), "onlyonce") {
		t.Fatal("original form", form.Code, form.Body.String())
	}
	config, err := a.pluginStore.Config(context.Background(), "CleanInvalidSeed")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"revision": config.Revision, "config": map[string]any{"enabled": false, "onlyonce": true}})
	saved := callTest(t, h, "PUT", "/api/plugins/CleanInvalidSeed/config", string(body), cookie, "")
	if saved.Code != 200 {
		t.Fatal("native config publication", saved.Code, saved.Body.String())
	}
	run = cleanSeedOwnerClaim(t, a, "CleanInvalidSeed")
	admission := append([]byte(nil), run.Input...)
	got := cleanSeedRuntimeExecute(t, a, run)
	if got.State != "succeeded" || got.ConfigRevision != config.Revision+1 || !bytes.Equal(got.Input, admission) {
		t.Fatal("original admission or terminal outcome changed", got.State, got.ErrorCode)
	}
	latest, err := a.pluginStore.Config(context.Background(), run.InstanceID)
	if err != nil || latest.Revision != config.Revision+2 || !bytes.Contains(latest.Value, []byte(`"onlyonce":false`)) {
		t.Fatal("one-shot flag was not atomically consumed", latest.Revision, err)
	}
	var dates int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND group_key='clean_seed' AND active=1 AND config_revision=?`, run.InstanceID, latest.Revision).Scan(&dates); err != nil || dates != 1 {
		t.Fatal("source date trigger not published", dates, err)
	}
	if err = a.initializeNativePlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state='pending'`, run.InstanceID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("startup silently created another init", pending, err)
	}
}

func TestPluginCleanInvalidSeedRuntimePreservesKnownFailureAndUnknownWrite(t *testing.T) {
	t.Run("known_source_failure", func(t *testing.T) {
		a := newTestApp(t)
		useBuiltInPluginProcess(t, a)
		run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(nil), true)
		if got := cleanSeedRuntimeExecute(t, a, run); got.State != "succeeded" {
			t.Fatal(got.State, got.ErrorCode)
		}
		if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_runtime_second_seed_notice BEFORE INSERT ON records WHEN NEW.kind='notification' AND (SELECT COUNT(*) FROM records WHERE kind='notification')>=1 BEGIN SELECT RAISE(ABORT,'owned runtime notice failure'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := a.pluginStore.enqueueCleanInvalidSeedCommand(context.Background(), run.InstanceID, map[string]any{"action": "toggle_notify_all", "channel": "Web", "user": "owned-user"}); err != nil {
			t.Fatal(err)
		}
		got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
		if got.State != "failed" || got.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" || len(got.Result) == 0 {
			t.Fatal("known failure reported as success/unknown", got.State, got.ErrorCode)
		}
		receipt, _, err := a.pluginStore.readDeferred(context.Background(), a.Store.DB, got)
		if err != nil || receipt.Phase != "terminal_failed" || receipt.ErrorCode != got.ErrorCode {
			t.Fatal("runtime lost fixed native failure receipt", receipt.Phase, err)
		}
	})
	t.Run("ambiguous_downloader_ack", func(t *testing.T) {
		a := newTestApp(t)
		useBuiltInPluginProcess(t, a)
		d := cleanSeedOwnerDaemonNew(t)
		d.settings(a)
		row := func(hash string) map[string]any {
			return map[string]any{"hash": hash, "name": "owned", "content_path": "/unused-owned-keep-data", "category": "movies", "tags": "", "state": "uploading", "size": 4, "total_size": 4, "completed": 4, "progress": 1, "ratio": 1}
		}
		d.rows, d.failWrite = []map[string]any{row(strings.Repeat("a", 40)), row(strings.Repeat("b", 40))}, true
		run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"delete_invalid_torrents": true}), true)
		if got := cleanSeedRuntimeExecute(t, a, run); got.State != "succeeded" {
			t.Fatal(got.State, got.ErrorCode)
		}
		if _, err := a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil); err != nil {
			t.Fatal(err)
		}
		got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
		if got.State != "reconcile_pending" {
			t.Fatal("uncertain write reported terminal", got.State, got.ErrorCode)
		}
		if err := a.pluginStore.recoverDeferredRuns(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := a.retryPluginDeferred(context.Background(), run.InstanceID, got.ID); err == nil {
			t.Fatal("uncertain write was made retryable")
		}
		if _, err := a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "run", nil); err != nil {
			t.Fatal(err)
		}
		blocked := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
		if blocked.State != "reconcile_pending" {
			t.Fatal("fresh invocation bypassed unknown effect", blocked.State, blocked.ErrorCode)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.deletes != 1 {
			t.Fatal("ambiguous native operation replayed", d.deletes)
		}
	})
}
