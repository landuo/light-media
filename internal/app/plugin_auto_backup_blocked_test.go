package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPluginAutoBackupBlockedRetriesHaveTerminalZeroEffectProof(t *testing.T) {
	a := newTestApp(t)
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	cookie := autoTestInstall(t, a)
	instant := time.Date(2026, 10, 18, 15, 0, 0, 0, time.UTC)
	a.pluginAutoBackup.now = func() time.Time { return instant }
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "archive_write" {
			return errors.New("owned original archive acknowledgement lost")
		}
		return nil
	}
	if _, err := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
		t.Fatal(err)
	}
	old := autoTestExecute(t, a, autoTestClaim(t, a))
	if old.State != "reconcile_pending" || len(autoTestFiles(t, a)) != 1 {
		t.Fatal("unknown original archive fixture failed")
	}
	a.pluginAutoBackup.afterEffect = nil
	state, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, old.InstanceID, old.Generation)
	if err != nil {
		t.Fatal(err)
	}
	oldAction := mustCleanSeedJSON(state.Runs[old.ID])
	var oldDeferred []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, old.ID).Scan(&oldDeferred); err != nil {
		t.Fatal(err)
	}
	failed := false
	retries := []PluginRun{}
	for n := 0; n < 3; n++ {
		if n == 2 {
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
		if _, err = a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
			t.Fatal(err)
		}
		run := autoTestExecute(t, a, autoTestClaim(t, a))
		retries = append(retries, run)
		state, _, err = a.pluginStore.autoBackupRead(ctx, a.Store.DB, old.InstanceID, old.Generation)
		if err != nil {
			t.Fatal(err)
		}
		action := state.Runs[run.ID]
		d, _, e := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("owned retry=%d cold=%t state=%s code=%s owner_phase=%s effects=%d deferred=%s archives=%d", n, n == 2, run.State, run.ErrorCode, action.Phase, len(action.Effects), d.Phase, len(autoTestFiles(t, a)))
		if run.State != "failed" || run.ErrorCode != "AUTO_BACKUP_BLOCKED_BY_UNKNOWN" || action.Phase != "failed" || action.ErrorCode != run.ErrorCode || len(action.Effects) != 0 || d.Phase != "terminal_failed" || d.ErrorCode != run.ErrorCode || !bytes.Equal(action.Result, d.Result) {
			t.Errorf("zero-effect rejected retry became an extra unknown: %s/%s, owner=%s, deferred=%s", run.State, run.ErrorCode, action.Phase, d.Phase)
			failed = true
		}
		var sealed []byte
		if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, old.ID).Scan(&sealed); err != nil || !bytes.Equal(oldDeferred, sealed) || !bytes.Equal(oldAction, mustCleanSeedJSON(state.Runs[old.ID])) || len(autoTestFiles(t, a)) != 1 {
			t.Fatal("retry changed original unknown proof or executed an effect", err)
		}
	}
	if failed {
		return // Preserve all three real before-fix SDK outcomes in this failure log.
	}
	installation, _ := a.pluginStore.Installation(ctx, "AutoBackup")
	for _, run := range retries {
		diag, e := a.pluginStore.autoBackupDiagnostic(ctx, installation, run)
		if e != nil || diag == nil || diag.AbandonAllowed || !strings.Contains(diag.Message, "此次执行未开始") {
			t.Fatal("zero-effect blocked retry lacks bounded non-abandon diagnosis", e)
		}
	}
	body, _ := json.Marshal(map[string]any{"run_id": old.ID, "confirm_abandon": true})
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/abandon-unknown", string(body), cookie, ""); w.Code != 200 {
		t.Fatal("original unknown cannot be explicitly resolved", w.Code, w.Body.String())
	}
	if _, err = a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
		t.Fatal(err)
	}
	if next := autoTestExecute(t, a, autoTestClaim(t, a)); next.State != "succeeded" || len(autoTestFiles(t, a)) != 2 {
		t.Fatal("zero-effect retry still locks new explicit backup", next.State, next.ErrorCode)
	}
	for _, run := range retries {
		got, e := a.pluginStore.Run(ctx, run.ID)
		if e != nil || got.State != "failed" || got.ErrorCode != "AUTO_BACKUP_BLOCKED_BY_UNKNOWN" || !bytes.Equal(got.Input, run.Input) {
			t.Fatal("blocked retry identity or terminal result changed", e)
		}
	}
}
