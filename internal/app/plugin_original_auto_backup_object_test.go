package app

import (
	"context"
	"encoding/json"
	ab "light-media/internal/plugins/autobackup"
	"testing"
	"time"
)

// Fixed clock, no App.Start, scheduler pump, backup action, or remote DAV.
func originalBackupObjectFixture(t *testing.T, enabled, once bool) (*App, *originalPluginInstance, *ab.Plugin, string) {
	t.Helper()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	a.pluginAutoBackup.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	installCloneSource(t, a.pluginStore, ab.Manifest())
	brother, err := a.pluginStore.Clone(ctx, "AutoBackup", PluginCloneRequest{Suffix: "OwnedSibling", Name: "owned sibling"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{brother.InstanceID, "AutoBackup"} {
		revision := int64(0)
		if c, e := a.pluginStore.Config(ctx, id); e == nil {
			revision = c.Revision
		}
		_, err = a.pluginStore.SaveConfig(ctx, id, revision, map[string]any{"enabled": enabled, "onlyonce": once, "cron": "0 0 * * *", "webdav_enabled": false, "back_path": ""})
		if err != nil {
			t.Fatal(err)
		}
		if a.startOriginalPlugin(ctx, id) == nil {
			t.Fatalf("actual init failed: %s", id)
		}
	}
	value, ok := a.pluginRunningInstances.Load("AutoBackup")
	if !ok {
		t.Fatal("missing actual object")
	}
	object := value.(*originalPluginInstance)
	object.configMu.RLock()
	machine, ok := object.nativeState.(*ab.Plugin)
	object.configMu.RUnlock()
	if !ok || machine == nil {
		t.Fatal("init did not capture actual AutoBackup machine")
	}
	return a, object, machine, brother.InstanceID
}
func originalBackupActive(t *testing.T, a *App, id, group string) int {
	t.Helper()
	var n int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND group_key=? AND active=1`, id, group).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func originalBackupNoExecution(t *testing.T, a *App) {
	t.Helper()
	var n int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE action<>'init'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("backup or other action executed/queued: %d %v", n, err)
	}
}
func TestPluginOriginalAutoBackupPrivateOnceStopKeepsSibling(t *testing.T) {
	a, _, machine, sibling := originalBackupObjectFixture(t, false, true)
	ctx := context.Background()
	if machine.GetState() != false {
		t.Fatal("disabled business state lost")
	}
	stored, err := a.pluginStore.Config(ctx, "AutoBackup")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if json.Unmarshal(stored.Value, &cfg) != nil || cfg["onlyonce"] != false {
		t.Fatal("actual init did not persist consumed once flag")
	}
	if n := originalBackupActive(t, a, "AutoBackup", "auto_backup"); n != 1 {
		t.Fatalf("private once=%d", n)
	}
	if n := originalBackupActive(t, a, sibling, "auto_backup"); n != 1 {
		t.Fatalf("sibling once=%d", n)
	}
	machine.Stop(ctx)
	if n := originalBackupActive(t, a, "AutoBackup", "auto_backup"); n != 0 {
		t.Errorf("Stop left private DB job eligible: %d", n)
	}
	if n := originalBackupActive(t, a, sibling, "auto_backup"); n != 1 {
		t.Errorf("Stop touched sibling: %d", n)
	}
	originalBackupNoExecution(t, a)
}
func TestPluginOriginalAutoBackupPublicRegisterAndPrivateStopSeparate(t *testing.T) {
	a, _, _, sibling := originalBackupObjectFixture(t, true, true)
	ctx := context.Background()
	if n := originalBackupActive(t, a, "AutoBackup", "original_services"); n != 0 {
		t.Fatal("init registered central jobs early")
	}
	a.updateOriginalPluginJobs(ctx, "AutoBackup")
	a.updateOriginalPluginJobs(ctx, sibling)
	if n := originalBackupActive(t, a, "AutoBackup", "original_services"); n != 1 {
		t.Fatalf("central get_service not registered: %d", n)
	}
	if err := a.stopOriginalPlugin(ctx, "AutoBackup"); err != nil {
		t.Fatal(err)
	}
	if n := originalBackupActive(t, a, "AutoBackup", "auto_backup"); n != 0 {
		t.Errorf("manager stop did not stop actual private scheduler: %d", n)
	}
	for _, id := range []string{"AutoBackup", sibling} {
		if n := originalBackupActive(t, a, id, "original_services"); n != 1 {
			t.Errorf("private stop removed central job for %s: %d", id, n)
		}
	}
	if n := originalBackupActive(t, a, sibling, "auto_backup"); n != 1 {
		t.Errorf("sibling private job changed: %d", n)
	}
	originalBackupNoExecution(t, a)
}
func TestPluginOriginalAutoBackupDirectReinitReusesObjectAndConsumesOnce(t *testing.T) {
	a, object, machine, sibling := originalBackupObjectFixture(t, false, true)
	if err := object.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	object.configMu.RLock()
	after := object.nativeState
	object.configMu.RUnlock()
	if after != machine {
		t.Fatal("direct init reconstructed machine instead of reusing actual object")
	}
	if n := originalBackupActive(t, a, "AutoBackup", "auto_backup"); n != 0 {
		t.Errorf("second init retained consumed private once: %d", n)
	}
	if n := originalBackupActive(t, a, sibling, "auto_backup"); n != 1 {
		t.Errorf("second init touched sibling: %d", n)
	}
	var total int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id='AutoBackup' AND action='init' AND state='succeeded'`).Scan(&total); err != nil || total != 2 {
		t.Fatalf("exact actual init count: %d %v", total, err)
	}
	originalBackupNoExecution(t, a)
}

// Faults apply only to owned private schedule mutations. The current defs-only
// Clear never reaches them; passing the no-panic half alone is not evidence of
// its error/log path. The retry must actually remove persistent eligibility.
func TestPluginOriginalAutoBackupStopOutsideInvocationSQLFailureRetry(t *testing.T) {
	a, object, machine, sibling := originalBackupObjectFixture(t, false, true)
	ctx := context.Background()
	owner, err := a.autoBackupOwner(ctx, object.installation)
	if err != nil {
		t.Fatal(err)
	}
	if owner.active != nil {
		t.Fatal("fixture still inside native invocation")
	}
	for _, sql := range []string{
		`CREATE TRIGGER owned_backup_stop_update BEFORE UPDATE ON plugin_schedules WHEN OLD.instance_id='AutoBackup' AND OLD.group_key='auto_backup' BEGIN SELECT RAISE(FAIL,'owned_stop_failure'); END`,
		`CREATE TRIGGER owned_backup_stop_delete BEFORE DELETE ON plugin_schedules WHEN OLD.instance_id='AutoBackup' AND OLD.group_key='auto_backup' BEGIN SELECT RAISE(FAIL,'owned_stop_failure'); END`,
	} {
		if _, err = a.Store.DB.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("Stop outside active invocation panicked: %v", p)
			}
		}()
		machine.Stop(ctx)
	}()
	if n := originalBackupActive(t, a, "AutoBackup", "auto_backup"); n != 1 {
		t.Errorf("failed clear changed schedule: %d", n)
	}
	for _, name := range []string{"owned_backup_stop_update", "owned_backup_stop_delete"} {
		if _, err = a.Store.DB.Exec(`DROP TRIGGER ` + name); err != nil {
			t.Fatal(err)
		}
	}
	machine.Stop(ctx)
	if n := originalBackupActive(t, a, "AutoBackup", "auto_backup"); n != 0 {
		t.Errorf("retry did not clear private eligibility: %d", n)
	}
	if n := originalBackupActive(t, a, sibling, "auto_backup"); n != 1 {
		t.Errorf("failed/retried stop touched sibling: %d", n)
	}
	originalBackupNoExecution(t, a)
}
