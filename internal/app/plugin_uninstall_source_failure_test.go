package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"light-media/internal/plugins/builtin"
	seed "light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/uninstall"
)

// The source writes UserInstalledPlugins before its own reset. A failure of
// that later, separate DB commit cannot reinstall a package already removed.
func TestPluginUninstallSourceOwnResetFailureKeepsEarlierRemoval(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "CategoryEditor", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"CategoryEditor"}}, true)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_own_reset_failure BEFORE UPDATE ON plugin_values WHEN NEW.instance_id='PluginUnInstall' BEGIN SELECT RAISE(ABORT,'owned own-config commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyPluginUninstall(ctx, &run); err == nil {
		t.Fatal("source reset commit failure reported success")
	}
	target, err := a.pluginStore.Installation(ctx, "CategoryEditor")
	if err != nil || target.Installed || target.Generation != 2 {
		t.Fatalf("source removal was incorrectly rolled back after later own-config failure: installed=%v generation=%d err=%v", target.Installed, target.Generation, err)
	}
	config, err := a.pluginStore.Config(ctx, "PluginUnInstall")
	if err != nil || config.Revision != 1 {
		t.Fatal("failed source own reset changed durable config", config.Revision, err)
	}
}

func TestPluginUninstallSourceLaterOwnerPreparationCannotPreemptEarlierRemoval(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "a-simple", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	clean := uninstallInstall(t, a, "z-clean", seed.Manifest(), map[string]any{"enabled": false}, true)
	cleanSeedOwnerInvoke(t, a, clean)
	if _, err := a.Store.DB.Exec(`INSERT INTO plugin_schedules VALUES('z-clean','owned','clean_seed',1,1,x'00','owned',1,NULL,?)`, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_later_owner_failure BEFORE UPDATE OF updated_at ON plugin_schedules WHEN NEW.instance_id='z-clean' BEGIN SELECT RAISE(ABORT,'owned later owner preparation failure'); END`); err != nil {
		t.Fatal(err)
	}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"z-clean", "a-simple"}}, true)
	if _, err := a.applyPluginUninstall(ctx, &run); pluginErrorCode(err) != uninstallSourceFailed {
		t.Fatal("later native owner failure not retained", err)
	}
	first, _ := a.pluginStore.Installation(ctx, "a-simple")
	second, _ := a.pluginStore.Installation(ctx, "z-clean")
	if first.Installed || first.Generation != 2 || !second.Installed || second.Generation != 1 {
		t.Fatal("all-target preflight changed source failure order", first.Installed, second.Installed)
	}
	if owner := a.pluginCleanInvalidSeed.owners["z-clean"]; owner != nil && len(owner.gate) != 0 {
		t.Fatal("failed later owner retained reservation")
	}
}

func TestPluginUninstallSourceInterruptedOrMissingProofCannotReplayReplacement(t *testing.T) {
	for _, missingList := range []bool{false, true} {
		t.Run(map[bool]string{false: "started", true: "list_evidence_missing"}[missingList], func(t *testing.T) {
			dir := t.TempDir()
			a, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			open := true
			t.Cleanup(func() {
				if open {
					_ = a.Close()
				}
			})
			ctx := context.Background()
			uninstallInstall(t, a, "Alpha", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Alpha"}}, true)
			if _, err := a.applyPluginUninstall(ctx, &run); err != nil {
				t.Fatal(err)
			}
			r, previous, err := a.pluginStore.readUninstallSource(ctx, a.Store.DB, run)
			if err != nil {
				t.Fatal(err)
			}
			r.Phase = "started"
			r.Result = nil
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.pluginStore.writeUninstallSourceTx(ctx, tx, r, previous); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if missingList {
				if _, err = a.Store.DB.Exec(`DELETE FROM plugin_uninstall_source_lists WHERE run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			manifest, _ := json.Marshal(builtin.CategoryEditorManifest())
			if err := a.pluginStore.Install(ctx, PluginInstallation{InstanceID: "Alpha", PluginID: "CategoryEditor", Version: builtin.CategoryEditorManifest().Version, ManifestSchemaVersion: 1, Manifest: manifest, DesiredEnabled: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			err = a.pluginStore.recoverUninstallRuns(ctx)
			if missingList {
				if err != nil {
					t.Fatal("one plugin's missing private proof prevented App recovery", err)
				}
				saved, _ := a.pluginStore.Run(ctx, run.ID)
				if saved.State != "reconcile_pending" || saved.ErrorCode != "PLUGIN_UNINSTALL_EVIDENCE_INVALID" {
					t.Fatal("missing evidence was accepted", saved.State, saved.ErrorCode)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			open = false
			a, err = New(Options{DataDir: dir})
			if err != nil {
				t.Fatal("one plugin's incomplete proof blocked cold App", err)
			}
			t.Cleanup(func() { _ = a.Close() })
			if response := callTest(t, a.Handler(), "GET", "/healthz", "", nil, ""); response.Code != http.StatusOK {
				t.Fatal("unrelated App HTTP stopped", response.Code)
			}
			if err = a.initializeNativePlugins(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = a.applyPluginUninstall(ctx, &run); err == nil {
				t.Fatal("source started operation replayed")
			}
			in, _ := a.pluginStore.Installation(ctx, "Alpha")
			if !in.Installed || in.Generation != 3 {
				t.Fatal("old partial operation affected replacement", in.Installed, in.Generation)
			}
			if _, err = a.retryPluginDeferred(ctx, "PluginUnInstall", run.ID); err == nil {
				t.Fatal("unknown source operation offered direct retry")
			}
		})
	}
}

func TestPluginUninstallSourceCommitProofRequiresExactExpectedOrPreviousCiphertext(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "Alpha", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Alpha"}}, true)
	if _, err := a.applyPluginUninstall(ctx, &run); err != nil {
		t.Fatal(err)
	}
	_, sealed, err := a.pluginStore.readUninstallSource(ctx, a.Store.DB, run)
	if err != nil {
		t.Fatal(err)
	}
	if committed, err := a.pluginStore.uninstallCommitProof(run.ID, sealed, []byte("previous-owned")); err != nil || !committed {
		t.Fatal(committed, err)
	}
	if committed, err := a.pluginStore.uninstallCommitProof(run.ID, []byte("expected-owned"), sealed); err != nil || committed {
		t.Fatal(committed, err)
	}
	if _, err := a.pluginStore.uninstallCommitProof(run.ID, []byte("other-owned"), []byte("previous-owned")); !errors.Is(err, errUninstallSourceUnknown) {
		t.Fatal("unrelated journal was treated as a source outcome", err)
	}
}

func TestPluginUninstallSourceInstalledCommitFailureKeepsCacheAndEarlierPackages(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"Alpha", "Beta"} {
		uninstallInstall(t, a, id, builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Beta", "Alpha"}}, true)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_installed_failure BEFORE UPDATE ON plugin_uninstall_source_lists BEGIN SELECT RAISE(ABORT,'owned installed-list commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyPluginUninstall(ctx, &run); pluginErrorCode(err) != uninstallSourceFailed {
		t.Fatal(err)
	}
	for _, id := range []string{"Alpha", "Beta"} {
		in, _ := a.pluginStore.Installation(ctx, id)
		if in.Installed || in.Generation != 2 {
			t.Fatal("earlier package rolled back", id, in.Generation)
		}
	}
	r, _, err := a.pluginStore.readUninstallSource(ctx, a.Store.DB, run)
	if err != nil || r.Phase != "failed" || r.Stage != "save_installed" || !reflect.DeepEqual(r.InstalledCache, []string{"PluginUnInstall"}) || !reflect.DeepEqual(r.InstalledDurable, []string{"Alpha", "Beta", "PluginUnInstall"}) {
		t.Fatal("source cache-before-commit ordering", r.Stage, r.Phase, err)
	}
	config, _ := a.pluginStore.Config(ctx, "PluginUnInstall")
	if !bytes.Contains(config.Value, []byte(`"Beta","Alpha"`)) {
		t.Fatal("own reset ran after failed installed-list commit")
	}
}

func TestPluginUninstallSourceSchedulerCatchAndNotificationFailureKeepEarlierPackages(t *testing.T) {
	for _, noticeFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "caught", true: "notification_escapes"}[noticeFails], func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			for _, id := range []string{"Alpha", "Beta"} {
				uninstallInstall(t, a, id, builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
			}
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"Alpha", "Beta"}}, true)
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_scheduler_failure BEFORE UPDATE ON plugin_schedules WHEN NEW.instance_id='Beta' BEGIN SELECT RAISE(ABORT,'owned scheduler backend failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`INSERT INTO plugin_schedules VALUES('Beta','owned','owned',1,1,x'00','owned',1,NULL,?)`, now()); err != nil {
				t.Fatal(err)
			}
			if noticeFails {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_notice_failure BEFORE INSERT ON records WHEN NEW.kind='notification' BEGIN SELECT RAISE(ABORT,'owned scheduler message failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err := a.applyPluginUninstall(ctx, &run)
			if noticeFails {
				if pluginErrorCode(err) != uninstallSourceFailed {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal("scheduler catch escaped", err)
			}
			alpha, _ := a.pluginStore.Installation(ctx, "Alpha")
			beta, _ := a.pluginStore.Installation(ctx, "Beta")
			if alpha.Installed || beta.Installed != noticeFails {
				t.Fatal("source sequential target boundary", alpha.Installed, beta.Installed)
			}
			var count int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 1
			if noticeFails {
				want = 0
			}
			if count != want {
				t.Fatal("source system-message failure boundary", count)
			}
		})
	}
}

func TestPluginUninstallSourceHTTPWorkerFailureWarmCacheAndActualColdReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	open := true
	t.Cleanup(func() {
		if open {
			_ = a.Close()
		}
	})
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	for _, plugin := range []string{"CategoryEditor", "PluginUnInstall"} {
		response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"`+plugin+`"}`, cookie, "")
		if response.Code != http.StatusOK {
			t.Fatal(response.Code, response.Body.String())
		}
		run, e := a.pluginStore.claimRunForInstance(ctx, plugin)
		if e != nil {
			t.Fatal(e)
		}
		a.executePluginRun(ctx, run)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER source_reset_failure BEFORE UPDATE ON plugin_values WHEN NEW.instance_id='PluginUnInstall' AND NEW.revision=3 BEGIN SELECT RAISE(ABORT,'owned source reset commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/PluginUnInstall/config", `{"revision":1,"config":{"plugin_ids":["CategoryEditor"],"clear_config":true,"clear_data":true}}`, cookie, "")
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	run, err := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	saved, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || saved.State != "failed" || saved.ErrorCode != uninstallSourceFailed {
		t.Fatal(saved.State, saved.ErrorCode, err)
	}
	warm, _ := a.pluginStore.Config(ctx, "PluginUnInstall")
	durable, _ := a.pluginStore.readValue(ctx, "PluginUnInstall", "config", "main")
	var warmValue, durableValue struct {
		PluginIDs []string `json:"plugin_ids"`
	}
	if json.Unmarshal(warm.Value, &warmValue) != nil || json.Unmarshal(durable.Value, &durableValue) != nil || len(warmValue.PluginIDs) != 0 || !reflect.DeepEqual(durableValue.PluginIDs, []string{"CategoryEditor"}) || warm.Revision != 2 || durable.Revision != 2 {
		t.Fatal("SystemConfig process cache did not survive failed DB commit")
	}
	var runs int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	open = false
	b, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	useBuiltInPluginProcess(t, b)
	cold, err := b.pluginStore.Config(ctx, "PluginUnInstall")
	var coldValue struct {
		PluginIDs []string `json:"plugin_ids"`
	}
	if err != nil || json.Unmarshal(cold.Value, &coldValue) != nil || !reflect.DeepEqual(coldValue.PluginIDs, durableValue.PluginIDs) {
		t.Fatal("cold startup did not discard process-only source cache", err)
	}
	if err = b.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	var coldRuns int
	_ = b.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&coldRuns)
	target, _ := b.pluginStore.Installation(ctx, "CategoryEditor")
	if coldRuns != runs || target.Installed || target.Generation != 2 {
		t.Fatal("cold startup replayed source partial failure", runs, coldRuns, target.Generation)
	}
	saved, _ = b.pluginStore.Run(ctx, run.ID)
	if saved.State != "failed" || saved.ErrorCode != uninstallSourceFailed {
		t.Fatal("cold lost known source outcome", saved.State, saved.ErrorCode)
	}
}

// Manager.__stop_plugin catches close/stop_service errors. This synthetic
// lifecycle error is distinct from cancellation or a host ownership rejection.
func TestPluginUninstallSourceStopExceptionDoesNotRetainTarget(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	uninstallInstall(t, a, "MediaServerRefresh", builtin.MediaServerRefreshManifest(), map[string]any{"enabled": true}, false)
	if _, err := a.Store.DB.Exec(`INSERT INTO plugin_event_subscriptions VALUES('MediaServerRefresh','refresh',1,1,'["import.completed"]',0,1,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER source_stop_exception BEFORE UPDATE OF active ON plugin_event_subscriptions WHEN NEW.instance_id='MediaServerRefresh' AND NEW.active=0 AND EXISTS(SELECT 1 FROM plugin_installations WHERE instance_id=NEW.instance_id AND installed=1) BEGIN SELECT RAISE(ABORT,'owned stop_service exception'); END`); err != nil {
		t.Fatal(err)
	}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"MediaServerRefresh"}}, true)
	if _, err := a.applyPluginUninstall(ctx, &run); err != nil {
		t.Fatal("source caught stop_service exception was surfaced as uninstall failure", err)
	}
	target, err := a.pluginStore.Installation(ctx, "MediaServerRefresh")
	if err != nil || target.Installed {
		t.Fatal("source stop exception retained package", target.Installed, err)
	}
}
