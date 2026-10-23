package app

import (
	"context"
	"errors"
	"testing"
)

// The original explicit force-delete does not require the former native
// NoUnknown lifecycle admission. The operation clears logical data, not files,
// and does not retry the uncertain effect. All effects are owned TempDir data.
func TestPluginOriginalForceDataClearDoesNotReapplyUnknownEffect(t *testing.T) {
	a := newTestApp(t)
	autoTestInstall(t, a)
	ctx := context.Background()
	writes := 0
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "archive_write" {
			writes++
			return errors.New("owned lost archive acknowledgment")
		}
		return nil
	}
	if _, err := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
		t.Fatal(err)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	a.pluginAutoBackup.afterEffect = nil
	if run.State != "reconcile_pending" || writes != 1 {
		t.Fatalf("real unknown precondition %s writes=%d", run.State, writes)
	}
	before, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, "AutoBackup", run.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err = autoBackupNoUnknown(before); err == nil {
		t.Fatal("fixture did not exercise removed NoUnknown gate")
	}
	if err = a.pluginStore.deleteOriginalPluginData(ctx, "AutoBackup"); err != nil {
		t.Fatal(err)
	}
	in, err := a.pluginStore.Installation(ctx, "AutoBackup")
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, "AutoBackup", in.Generation)
	if err != nil || len(after.Runs) != 0 {
		t.Fatalf("force data clear: %v", err)
	}
	old, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || old.State != "reconcile_pending" || writes != 1 {
		t.Fatalf("clear replayed/rewrote old effect %v writes=%d", err, writes)
	}
	var n int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id='AutoBackup' AND generation=?`, in.Generation).Scan(&n); err != nil || n != 0 {
		t.Fatalf("delete_data queued work: %d %v", n, err)
	}
}
