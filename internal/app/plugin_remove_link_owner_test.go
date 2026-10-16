package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// Actual SDK handler in an isolated child; the native owner remains in App.
func TestPluginRemoveLinkOwnerWorkerFixture(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--remove-link-owner-worker" {
			if plugins.Serve(context.Background(), os.Stdin, os.Stdout, removelink.Manifest(), removelink.Handle) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}
func removeLinkOwnerWorker(t *testing.T, a *App, run PluginRun) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginProcess = make(chan struct{}, 1)
	a.pluginCommand = func(string) (plugins.Command, error) {
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginRemoveLinkOwnerWorkerFixture$", "--", "--remove-link-owner-worker"}}, nil
	}
	a.executePluginRun(context.Background(), run)
	got, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || got.State != "succeeded" || string(got.Result) != "null" {
		t.Fatalf("actual SDK/owner result state=%s code=%s result=%s err=%v", got.State, got.ErrorCode, got.Result, err)
	}
	if len(a.pluginProcess) != 0 {
		t.Fatal("global worker process slot leaked")
	}
}
func removeLinkOwnerHTTP(t *testing.T, a *App, instance, operation string, body any) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(body)
	method := "POST"
	if operation == "config" {
		method = "PUT"
	}
	req := httptest.NewRequest(method, "/api/plugins/"+instance+"/"+operation, bytes.NewReader(raw))
	return a.pluginRemoveLinkLifecycleHTTP(httptest.NewRecorder(), req, instance+"/"+operation)
}
func removeLinkOwnerClaim(t *testing.T, a *App, instance string) PluginRun {
	t.Helper()
	r, err := a.pluginStore.claimRunForInstance(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func removeLinkOwnerFakeWatch(a *App) {
	a.pluginRemoveLink.watch = func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		return &cleanupEffectsIdleWatcher{events: make(chan cleanuphost.Event), errs: make(chan error)}, nil
	}
}
func removeLinkOwnerFixture(t *testing.T) (*App, transferHistoryItem, PluginRun) {
	t.Helper()
	a, item := historyRemovalFixture(t)
	removeLinkOwnerFakeWatch(a)
	cfg := map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0] + "\n" + a.Settings().Roots[0].Path, "delayed_deletion": true, "delay_seconds": 3600, "delete_history": false, "delete_torrents": false, "notify": false, "scrap": false}
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), cfg, true)
	removeLinkOwnerWorker(t, a, run)
	return a, item, run
}
func TestPluginRemoveLinkOwnerDisabledInstallResetAndSaveWithoutRoots(t *testing.T) {
	a := newTestApp(t)
	a.config = Settings{}
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": false}, true)
	removeLinkOwnerWorker(t, a, run)
	i, c, err := a.removeLinkSnapshot(context.Background(), run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	initialize, err := a.shouldInitializeRemoveLink(context.Background(), i, c)
	if err != nil || initialize {
		t.Fatal("applied idle config must not re-init", initialize, err)
	}
	if _, found, err := a.pluginStore.removeLinkDeferredOutcome(context.Background(), run, removelink.ConfigureCapability); err != nil || !found {
		t.Fatal("cold lifecycle proof", found, err)
	}
	owner := a.pluginRemoveLink.owners[run.InstanceID]
	if owner == nil || owner.h != nil {
		t.Fatal("disabled install opened an observer or fake root")
	}
	if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "reset", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	i, c, err = a.removeLinkSnapshot(context.Background(), run.InstanceID)
	if err != nil || i.Generation != 2 || i.DesiredEnabled || c.Revision != 0 {
		t.Fatal("reset state", i.Generation, c.Revision, err)
	}
	if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "config", map[string]any{"revision": 0, "config": map[string]any{"enabled": false}}); err != nil {
		t.Fatal("normal config save after reset", err)
	}
	next := removeLinkOwnerClaim(t, a, run.InstanceID)
	removeLinkOwnerWorker(t, a, next)
	if next.Generation != 2 || next.ConfigRevision != 1 {
		t.Fatal("reset save binding", next)
	}
}
func TestPluginRemoveLinkOwnerNativeGuardAndOrdinarySaveUseCurrentFlags(t *testing.T) {
	a, item, run := removeLinkOwnerFixture(t)
	ctx := context.Background()
	o := a.pluginRemoveLink.owners[run.InstanceID]
	if err := os.Remove(item.Source); err != nil {
		t.Fatal(err)
	}
	event := cleanuphost.Event{ID: "owned-native-source-gone", Kind: "deleted", Mode: "hardlink", Path: item.Source, At: time.Now()}
	if err := o.h.Observe(ctx, event); err != nil {
		t.Fatal(err)
	}
	before, err := o.j.Read(ctx)
	if err != nil || len(before.Machine.Queue) != 1 || before.Machine.TimerDue == nil {
		t.Fatal("native event did not queue", err)
	}
	// Parent authorization is intentionally broader than both siblings. Only the
	// actual source subdirectory may be granted; the library keeps its core ID.
	settings := a.Settings()
	settings.AllowedRoots = []string{filepath.Dir(settings.Roots[0].Path)}
	a.cfgMu.Lock()
	a.config = settings
	a.cfgMu.Unlock()
	c, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(c.Value, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["enabled"] = false
	if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "config", map[string]any{"revision": c.Revision, "config": cfg}); err != nil {
		t.Fatal(err)
	}
	paused, err := o.j.Read(ctx)
	if err != nil || paused.Transition.Phase != "published" || paused.Machine.TimerDue == nil || !paused.Machine.TimerDue.Equal(*before.Machine.TimerDue) {
		t.Fatal("publication changed queued deadline", err)
	}
	if _, err = os.Stat(item.Target); err != nil {
		t.Fatal("save ran old queue before SDK success", err)
	}
	next := removeLinkOwnerClaim(t, a, run.InstanceID)
	removeLinkOwnerWorker(t, a, next)
	after, err := o.j.Read(ctx)
	if err != nil || after.Binding.Revision != next.ConfigRevision || after.Transition.Phase != "applied" || len(after.Machine.Queue) != 0 {
		t.Fatal("ordinary new flags transition", err)
	}
	if _, err = os.Stat(item.Target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source Stop did not drain selected media", err)
	}
	var asset Asset
	if err = a.Store.Get(ctx, "asset", removeLinkOwnerAsset(t, a, item.Target), &asset); err != nil || asset.State != "missing_pending" {
		t.Fatal("mandatory AfterAction failed to update current index", asset.State, err)
	}
	if _, err = a.transferHistoryItem(ctx, item.ID); err != nil {
		t.Fatal("false history flag lost audit", err)
	}
}
func TestPluginRemoveLinkOwnerExpandActualDirectoryRetainsOldCapabilities(t *testing.T) {
	a, _, run := removeLinkOwnerFixture(t)
	ctx := context.Background()
	o := a.pluginRemoveLink.owners[run.InstanceID]
	before, err := o.j.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(filepath.Dir(a.Settings().Roots[0].Path), "source-extra")
	if err = os.Mkdir(extra, 0700); err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.AllowedRoots = append(settings.AllowedRoots, extra)
	a.cfgMu.Lock()
	a.config = settings
	a.cfgMu.Unlock()
	c, _ := a.pluginStore.Config(ctx, run.InstanceID)
	var cfg map[string]any
	_ = json.Unmarshal(c.Value, &cfg)
	cfg["monitor_dirs"] = extra
	if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "config", map[string]any{"revision": c.Revision, "config": cfg}); err != nil {
		t.Fatal(err)
	}
	next := removeLinkOwnerClaim(t, a, run.InstanceID)
	removeLinkOwnerWorker(t, a, next)
	after, err := o.j.Read(ctx)
	if err != nil || len(after.Roots) != len(before.Roots)+1 || !removeLinkSameRoots(before.Roots, after.Roots[:len(before.Roots)]) {
		t.Fatal("root expansion replaced old authority", err)
	}
	if o != a.pluginRemoveLink.owners[run.InstanceID] {
		t.Fatal("ordinary init replaced continuous owner")
	}
}
func TestPluginRemoveLinkOwnerPublicationUnknownAndCASRollbackStaySafe(t *testing.T) {
	for _, mode := range []string{"rollback", "committed_reply_lost"} {
		t.Run(mode, func(t *testing.T) {
			a, _, run := removeLinkOwnerFixture(t)
			ctx := context.Background()
			i, c, _ := a.removeLinkSnapshot(ctx, run.InstanceID)
			p, err := a.prepareRemoveLinkTransition(ctx, i, "init")
			if err != nil {
				t.Fatal(err)
			}
			defer p.release()
			a.pluginLifecycleMu.Lock()
			a.pluginMu.Lock()
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				a.pluginMu.Unlock()
				a.pluginLifecycleMu.Unlock()
				t.Fatal(err)
			}
			value, err := a.pluginStore.saveValueTx(ctx, tx, run.InstanceID, "config", "main", c.Revision, json.RawMessage(`{"enabled":false}`))
			if err == nil {
				var r PluginRun
				r, err = a.pluginStore.enqueueRunTx(ctx, tx, run.InstanceID, "init", nil)
				if err == nil {
					err = p.publishTx(ctx, tx, cleanuphost.Binding{InstanceID: run.InstanceID, Generation: run.Generation, Revision: value.Revision}, r.ID)
				}
			}
			if err != nil {
				tx.Rollback()
			} else if mode == "rollback" {
				err = tx.Rollback()
			} else {
				err = tx.Commit()
			}
			a.pluginMu.Unlock()
			a.pluginLifecycleMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			err = p.resolve(ctx, false)
			state, readErr := p.o.j.Read(ctx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "rollback" {
				if err != nil || state.Transition != nil {
					t.Fatal("proven rollback not restored", err)
				}
			} else {
				var api *APIError
				if !errors.As(err, &api) || api.Code != "PLUGIN_TRANSITION_UNCERTAIN" || state.Transition.Phase != "published" {
					t.Fatal("unknown commit resumed", err)
				}
			}
		})
	}
}
func TestPluginRemoveLinkOwnerRejectsUntrustedCallerAndCoreRevisionChanges(t *testing.T) {
	a, _, run := removeLinkOwnerFixture(t)
	ctx := context.Background()
	i, _ := a.pluginStore.Installation(ctx, run.InstanceID)
	if _, err := a.pluginRemoveLinkHost(ctx, i, &run, removelink.ConfigureCapability, json.RawMessage(`{}`)); err == nil {
		t.Fatal("plain context opened actual owner")
	}
	o := a.pluginRemoveLink.owners[run.InstanceID]
	state, _ := o.j.Read(ctx)
	// Core revision changes outside the coordinated public API must fail closed.
	if _, err := a.pluginStore.SaveConfig(ctx, run.InstanceID, state.Binding.Revision, map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if release, err := o.guard(ctx, state.Binding, "timer"); err == nil {
		release()
		t.Fatal("old automatic operation authorized")
	}
}
func TestPluginRemoveLinkOwnerLinuxNativeWatcherDelete(t *testing.T) {
	if runtime.GOOS != "linux" {
		return
	} // Linux-only production observer; common tests inject WatchFactory.
	a, item := historyRemovalFixture(t)
	cfg := map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0] + "\n" + a.Settings().Roots[0].Path, "delayed_deletion": false, "delete_history": false, "delete_torrents": false, "notify": false}
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), cfg, true)
	removeLinkOwnerWorker(t, a, run)
	if err := os.Remove(item.Source); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var asset Asset
		if a.Store.Get(context.Background(), "asset", removeLinkOwnerAsset(t, a, item.Target), &asset) == nil && asset.State == "missing_pending" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := a.pluginRemoveLink.owners[run.InstanceID].j.Read(context.Background())
	t.Fatalf("native observer did not finish core reconciliation: blocked=%q active=%+v", state.Blocked, state.Active)
}

func removeLinkOwnerAsset(t *testing.T, a *App, path string) string {
	t.Helper()
	var owned managedFile
	if err := a.Store.Get(context.Background(), "managed_file", tokenHash(path), &owned); err != nil {
		t.Fatal(err)
	}
	return owned.AssetID
}

func removeLinkOwnerPersistSettings(t *testing.T, a *App) {
	t.Helper()
	sealed, err := a.Store.Seal(a.Settings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, sealed, now()); err != nil {
		t.Fatal(err)
	}
}
func TestPluginRemoveLinkOwnerRestartSkipsFreshInitAndKeepsCheckpointDeadline(t *testing.T) {
	for _, mode := range []string{"clean_close", "interrupted_checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			a, item, run := removeLinkOwnerFixture(t)
			ctx := context.Background()
			o := a.pluginRemoveLink.owners[run.InstanceID]
			removeLinkOwnerPersistSettings(t, a)
			if mode == "interrupted_checkpoint" {
				if err := os.Remove(item.Source); err != nil {
					t.Fatal(err)
				}
				if err := o.h.Observe(ctx, cleanuphost.Event{ID: "restart-queued-deletion", Kind: "deleted", Mode: "hardlink", Path: item.Source, At: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := o.j.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "interrupted_checkpoint" {
				if len(before.Machine.Queue) != 1 || before.Machine.TimerDue == nil {
					t.Fatal("fixture has no durable deadline")
				}
				if err = o.h.Suspend(ctx); err != nil {
					t.Fatal(err)
				}
			}
			// A source clean Close drains queues. The interrupted branch suspends the
			// observer only, exactly preserving the last durable checkpoint for reopen.
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(Options{DataDir: a.Dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			removeLinkOwnerFakeWatch(reopened)
			if err = reopened.restoreRemoveLinkOwners(ctx); err != nil {
				t.Fatal(err)
			}
			next := reopened.pluginRemoveLink.owners[run.InstanceID]
			if next == nil || next.h == nil {
				t.Fatal("clean native owner not restored")
			}
			after, err := next.j.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "interrupted_checkpoint" {
				if len(after.Machine.Queue) != len(before.Machine.Queue) || after.Machine.TimerDue == nil || !after.Machine.TimerDue.Equal(*before.Machine.TimerDue) {
					t.Fatal("recover changed original deadline")
				}
				if _, err = os.Stat(item.Target); err != nil {
					t.Fatal("recover blindly drained pending queue", err)
				}
			}
			var countBefore, countAfter int
			if err = reopened.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&countBefore); err != nil {
				t.Fatal(err)
			}
			if err = reopened.initializeNativePlugins(ctx); err != nil {
				t.Fatal(err)
			}
			if err = reopened.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&countAfter); err != nil {
				t.Fatal(err)
			}
			if countAfter != countBefore {
				t.Fatal("fresh startup init would prematurely drain checkpoint")
			}
		})
	}
}
func TestPluginRemoveLinkOwnerStartedLifecycleProofRepairsLostDeferredResultAfterReopen(t *testing.T) {
	for _, failure := range []string{"interrupted_running", "failed_uncertain"} {
		t.Run(failure, func(t *testing.T) {
			a, item, old := removeLinkOwnerFixture(t)
			ctx := context.Background()
			o := a.pluginRemoveLink.owners[old.InstanceID]
			removeLinkOwnerPersistSettings(t, a)
			if err := os.Remove(item.Source); err != nil {
				t.Fatal(err)
			}
			if err := o.h.Observe(ctx, cleanuphost.Event{ID: "lost-result-owned-deletion", Kind: "deleted", Mode: "hardlink", Path: item.Source, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			c, err := a.pluginStore.Config(ctx, old.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			_ = json.Unmarshal(c.Value, &config)
			config["enabled"] = false
			if _, err = removeLinkOwnerHTTP(t, a, old.InstanceID, "config", map[string]any{"revision": c.Revision, "config": config}); err != nil {
				t.Fatal(err)
			}
			run := removeLinkOwnerClaim(t, a, old.InstanceID)
			in, err := a.pluginStore.Installation(ctx, run.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginRemoveLinkOwnerWorkerFixture$", "--", "--remove-link-owner-worker"}}
			// The real worker completes its single handshake; the native operation starts
			// after it exits. The result write is intentionally lost after actual effects.
			a.pluginProcess <- struct{}{}
			result, err := (plugins.Runner{}).Run(ctx, command, removelink.Manifest(), in.InstanceID, "invoke", run.Input, d.handshake)
			<-a.pluginProcess
			if err != nil {
				t.Fatal(err)
			}
			if err = d.ready(result); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`CREATE TABLE synthetic_cleanup_phase_updates(n INTEGER); INSERT INTO synthetic_cleanup_phase_updates VALUES(0); CREATE TRIGGER synthetic_cleanup_phase_fail BEFORE UPDATE ON plugin_deferred_operations WHEN (SELECT n FROM synthetic_cleanup_phase_updates)>=1 BEGIN SELECT RAISE(ABORT,'lost final deferred reply'); END; CREATE TRIGGER synthetic_cleanup_phase_count AFTER UPDATE ON plugin_deferred_operations BEGIN UPDATE synthetic_cleanup_phase_updates SET n=n+1; END;`); err != nil {
				t.Fatal(err)
			}
			if _, err = d.execute(ctx, &run); err == nil {
				t.Fatal("injected result loss not observed")
			}
			phase, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
			if err != nil || phase.Phase != "started" {
				t.Fatal("actual persisted phase", phase.Phase, err)
			}
			state, err := o.j.Read(ctx)
			if err != nil || state.Active != nil || state.Blocked != "" || state.Transition.Phase != "applied" || state.Transition.RunID != run.ID {
				t.Fatal("host lifecycle did not actually commit", err)
			}
			if _, err = os.Stat(item.Target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("actual selected media was not deleted", err)
			}
			if _, err = a.Store.DB.Exec(`DROP TRIGGER synthetic_cleanup_phase_fail; DROP TRIGGER synthetic_cleanup_phase_count; DROP TABLE synthetic_cleanup_phase_updates;`); err != nil {
				t.Fatal(err)
			}
			replacement := []byte("new same-path file must not be replayed")
			if err = os.MkdirAll(filepath.Dir(item.Target), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(item.Target, replacement, 0600); err != nil {
				t.Fatal(err)
			}
			if failure == "failed_uncertain" {
				if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='failed',error_code='PLUGIN_DEFERRED_UNCERTAIN' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(Options{DataDir: a.Dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			recovered, err := reopened.pluginStore.Run(ctx, run.ID)
			if err != nil || recovered.State != "succeeded" || string(recovered.Result) != "null" {
				t.Fatal("old run completion not repaired from real host proof", recovered.State, recovered.ErrorCode, err)
			}
			if len(reopened.pluginRemoveLink.owners) != 0 {
				t.Fatal("result recovery executed a native owner")
			}
			got, err := os.ReadFile(item.Target)
			if err != nil || !bytes.Equal(got, replacement) {
				t.Fatal("completion recovery replayed file deletion", err)
			}
			sealedPhase, _, err := reopened.pluginStore.readDeferred(ctx, reopened.Store.DB, run)
			if err != nil || sealedPhase.Phase != "confirmed" {
				t.Fatal("completion proof not durably consumed", sealedPhase.Phase, err)
			}
		})
	}
}

func TestPluginRemoveLinkOwnerEmptyOrdinaryInitRetainsObjectButReloadUsesDefaults(t *testing.T) {
	a := newTestApp(t)
	a.config = Settings{}
	ctx := context.Background()
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": true, "notify": true}, true)
	removeLinkOwnerWorker(t, a, run)
	o := a.pluginRemoveLink.owners[run.InstanceID]
	if o.h != nil {
		t.Fatal("no-directory config unexpectedly created native watcher")
	}
	c, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "config", map[string]any{"revision": c.Revision, "config": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	ordinary := removeLinkOwnerClaim(t, a, run.InstanceID)
	removeLinkOwnerWorker(t, a, ordinary)
	state, err := o.j.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := removelink.NewConfig()
	if err = cfg.Apply(state.Config); err != nil || !removelink.Truth(cfg.Enabled) || !removelink.Truth(cfg.Notify) {
		t.Fatal("ordinary empty config reset source object", err)
	}
	if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "reload", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	reload := removeLinkOwnerClaim(t, a, run.InstanceID)
	removeLinkOwnerWorker(t, a, reload)
	state, err = o.j.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg = removelink.NewConfig()
	if err = cfg.Apply(state.Config); err != nil || removelink.Truth(cfg.Enabled) || removelink.Truth(cfg.Notify) {
		t.Fatal("explicit reload reused the old source object", err)
	}
}

func TestPluginRemoveLinkOwnerInvalidPathSaveCanBeCorrectedWithoutStaleRevision(t *testing.T) {
	a, _, run := removeLinkOwnerFixture(t)
	ctx := context.Background()
	o := a.pluginRemoveLink.owners[run.InstanceID]
	before, err := o.j.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	_ = json.Unmarshal(c.Value, &old)
	for _, kind := range []string{"missing_directory", "unsupported_storage"} {
		t.Run(kind, func(t *testing.T) {
			var config map[string]any
			_ = json.Unmarshal(c.Value, &config)
			if kind == "missing_directory" {
				config["monitor_dirs"] = filepath.Join(a.Settings().AllowedRoots[0], "not-created")
			} else {
				config["monitor_strm_deletion"] = true
				config["strm_path_mappings"] = a.Settings().Roots[0].Path + ":cloud:" + a.Settings().AllowedRoots[0]
			}
			// Use the real HTTP helper, which preflights after prepare but before SQL.
			if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "config", map[string]any{"revision": c.Revision, "config": config}); err == nil {
				t.Fatal("invalid config published")
			}
			afterConfig, readErr := a.pluginStore.Config(ctx, run.InstanceID)
			if readErr != nil || afterConfig.Revision != c.Revision || !bytes.Equal(afterConfig.Value, c.Value) {
				t.Fatal("failed config changed core revision", readErr)
			}
			after, readErr := o.j.Read(ctx)
			if readErr != nil || after.Transition != nil || after.Blocked != "" || after.Binding != before.Binding || len(after.Machine.Watches) != len(before.Machine.Watches) {
				t.Fatal("failed config blocked or stopped old owner", readErr)
			}
		})
	}
	if _, err = removeLinkOwnerHTTP(t, a, run.InstanceID, "config", map[string]any{"revision": c.Revision, "config": old}); err != nil {
		t.Fatal("corrected config cannot save", err)
	}
	removeLinkOwnerWorker(t, a, removeLinkOwnerClaim(t, a, run.InstanceID))
}

func TestPluginRemoveLinkOwnerInstalledMediaExtensionsReference(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "plugins", "remove-link-host", "installed-media-extensions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Schema     int      `json:"schema"`
		Source     string   `json:"source"`
		SHA        string   `json:"config_source_sha256"`
		Extensions []string `json:"rmt_mediaext"`
	}
	if err = json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Schema != 1 || reference.SHA != "f19111073a7ef25ecd0b7e9d53dab343d76fe64ff3ad821f4ff39a790c104033" || !reflect.DeepEqual(removeLinkMediaExtensions(), reference.Extensions) {
		t.Fatal("owner media policy differs from actual installed source", err)
	}
	copy := removeLinkMediaExtensions()
	copy[0] = ".modified"
	if !reflect.DeepEqual(removeLinkMediaExtensions(), reference.Extensions) {
		t.Fatal("caller mutated global extension policy")
	}
}
