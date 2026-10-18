package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestPluginAutoBackupMissingOwnerRowMustBlockUnknownLifecycleAndNewBackup(t *testing.T) {
	for _, cold := range []bool{false, true} {
		for _, operation := range []string{"config", "reset", "reload", "backup"} {
			label := "warm/" + operation
			if cold {
				label = "cold/" + operation
			}
			t.Run(label, func(t *testing.T) {
				ctx := context.Background()
				a := newTestApp(t)
				defer func() {
					if a != nil {
						_ = a.Close()
					}
				}()
				cookie := autoTestInstall(t, a)
				settings := a.Settings()
				settings.APIToken = "owned-evidence-loss-only"
				if err := a.saveSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
				instant := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
				a.pluginAutoBackup.now = func() time.Time { return instant }
				a.pluginAutoBackup.afterEffect = func(kind string) error {
					if kind == "archive_write" {
						return errors.New("owned archive acknowledgment lost")
					}
					return nil
				}
				if _, err := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
					t.Fatal(err)
				}
				lost := autoTestExecute(t, a, autoTestClaim(t, a))
				if lost.State != "reconcile_pending" || len(autoTestFiles(t, a)) != 1 {
					t.Fatal("fixture did not produce unknown archive")
				}
				var sealed []byte
				if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, lost.ID).Scan(&sealed); err != nil {
					t.Fatal(err)
				}
				owner, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, lost.InstanceID, lost.Generation)
				if err != nil || owner.Runs[lost.ID].Phase != "started" {
					t.Fatal("fixture missing started owner action", err)
				}
				if _, err = a.Store.DB.Exec(`DELETE FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, lost.InstanceID, lost.Generation); err != nil {
					t.Fatal(err)
				}
				a.pluginAutoBackup.afterEffect = nil
				if cold {
					dir := a.Dir
					if err = a.Close(); err != nil {
						t.Fatal(err)
					}
					a, err = New(Options{DataDir: dir})
					if err != nil {
						t.Fatal(err)
					}
					useBuiltInPluginProcess(t, a)
				}
				instant = instant.Add(time.Minute)
				a.pluginAutoBackup.now = func() time.Time { return instant }
				before, _ := a.pluginStore.Config(ctx, "AutoBackup")
				method, path, body := "POST", "/api/plugins/AutoBackup/"+operation, ""
				if operation == "config" {
					method = "PUT"
					raw, _ := json.Marshal(map[string]any{"revision": before.Revision, "config": map[string]any{"enabled": false, "cnt": 10}})
					body = string(raw)
				}
				var wcode int
				success := false
				newRunState := ""
				if operation == "backup" {
					workerCtx, cancel := context.WithCancel(ctx)
					done := make(chan struct{})
					go func() { defer close(done); a.pluginWorker(workerCtx) }()
					w := callTest(t, a.Handler(), http.MethodGet, "/api/v1/plugin/AutoBackup/backup?apikey="+settings.APIToken, "", nil, "")
					cancel()
					<-done
					wcode = w.Code
					var tuple []any
					if json.Unmarshal(w.Body.Bytes(), &tuple) == nil && len(tuple) > 0 {
						success, _ = tuple[0].(bool)
					}
				} else {
					w := callTest(t, a.Handler(), method, path, body, cookie, "")
					wcode = w.Code
					success = w.Code >= 200 && w.Code < 300
					if success && operation != "reset" {
						next, err := a.pluginStore.claimRunForInstance(ctx, "AutoBackup")
						if err != nil {
							t.Fatal(err)
						}
						newRunState = autoTestExecute(t, a, next).State
					}
				}
				var afterSeal []byte
				if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, lost.ID).Scan(&afterSeal); err != nil || !bytes.Equal(sealed, afterSeal) {
					t.Fatal("independent old deferred evidence mutated", err)
				}
				old, err := a.pluginStore.Run(ctx, lost.ID)
				if err != nil || !bytes.Equal(old.Input, lost.Input) {
					t.Fatal("old admission input lost", err)
				}
				currentInstall, _ := a.pluginStore.Installation(ctx, "AutoBackup")
				files := len(autoTestFiles(t, a))
				t.Logf("owned evidence: cold=%t operation=%s HTTP=%d business_success=%t followup_state=%s archive_count=%d old_run_state=%s old_deferred_unchanged=true generation_before=%d generation_after=%d", cold, operation, wcode, success, newRunState, files, old.State, lost.Generation, currentInstall.Generation)
				if success || files != 1 {
					t.Errorf("missing owner journal was treated as fresh initialization: operation=%s HTTP=%d business_success=%t archives=%d", operation, wcode, success, files)
				}
			})
		}
	}
}

func TestPluginAutoBackupMissingConfirmedOwnerRowMustNotBecomeEmptyOwner(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := autoTestInstall(t, a)
	confirmed := autoTestRun(t, a)
	var deferredBefore []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, confirmed.ID).Scan(&deferredBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`DELETE FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, confirmed.InstanceID, confirmed.Generation); err != nil {
		t.Fatal(err)
	}
	state, blob, readErr := a.pluginStore.autoBackupRead(ctx, a.Store.DB, confirmed.InstanceID, confirmed.Generation)
	in, _ := a.pluginStore.Installation(ctx, confirmed.InstanceID)
	config, _ := a.pluginStore.Config(ctx, confirmed.InstanceID)
	initialize, initErr := a.shouldInitializeAutoBackup(ctx, in, config)
	w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/reload", "", cookie, "")
	nextState := ""
	if w.Code == 200 {
		next, err := a.pluginStore.claimRunForInstance(ctx, "AutoBackup")
		if err != nil {
			t.Fatal(err)
		}
		nextState = autoTestExecute(t, a, next).State
	}
	var deferredAfter []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, confirmed.ID).Scan(&deferredAfter); err != nil || !bytes.Equal(deferredBefore, deferredAfter) {
		t.Fatal("old confirmation changed", err)
	}
	old, err := a.pluginStore.Run(ctx, confirmed.ID)
	if err != nil || old.State != "succeeded" || !bytes.Equal(old.Input, confirmed.Input) {
		t.Fatal("known admission changed", err)
	}
	t.Logf("owned confirmed evidence: read_error=%t empty_blob=%t empty_actions=%t applied_revision=%d initialize=%t initialize_error=%t HTTP_reload=%d followup_state=%s deferred_confirmation_unchanged=true", readErr != nil, blob == nil, len(state.Runs) == 0, state.AppliedRevision, initialize, initErr != nil, w.Code, nextState)
	if readErr == nil || initErr == nil || w.Code == 200 {
		t.Error("missing previously committed owner journal silently became empty initial owner")
	}
}
