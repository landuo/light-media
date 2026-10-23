package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"
)

func originalDispatchFixture(t *testing.T) (*App, *originalPluginInstance, *originalPluginInstance, func(string, bool)) {
	t.Helper()
	binary := os.Getenv("OWNED_MODULE_FIXTURE_BINARY_V4")
	if binary == "" {
		t.Fatal("requires actual OWNED_MODULE_FIXTURE_BINARY_V4")
	}
	t.Setenv("OWNED_MODULE_FIXTURE_BINARY", binary)
	a, cookie, source := originalArtifactCloneFixture(t)
	save := func(id string, enabled bool) {
		t.Helper()
		body := `{"enabled":false,"cron":"* * * * *"}`
		if enabled {
			body = `{"enabled":true,"cron":"* * * * *"}`
		}
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, body, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	save("OwnedModuleFixture", true)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedModuleFixture/clone", `{"suffix":"Dispatch"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	save("OwnedModuleFixturedispatch", true)
	v, ok := a.pluginRunningInstances.Load("OwnedModuleFixturedispatch")
	if !ok {
		t.Fatal("actual clone absent")
	}
	return a, source, v.(*originalPluginInstance), save
}
func originalDispatchDue(t *testing.T, a *App) time.Time {
	t.Helper()
	var raw string
	if err := a.Store.DB.QueryRow(`SELECT max(next_due) FROM plugin_schedules WHERE active=1 AND group_key='original_services'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	due, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal(err)
	}
	return due
}
func originalDispatchExecute(t *testing.T, a *App) PluginRun {
	t.Helper()
	ctx := context.Background()
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.executePluginRun(ctx, run); err != nil {
		t.Errorf("actual execution error: %v", err)
	}
	done, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.State != "succeeded" {
		t.Errorf("actual run %s state=%s code=%s", run.ID, done.State, done.ErrorCode)
	}
	return done
}
func TestPluginOriginalArtifactDueServiceCallsLoadedHandler(t *testing.T) {
	a, source, clone, save := originalDispatchFixture(t)
	ctx := context.Background()
	due := originalDispatchDue(t, a)
	if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, due); err != nil || n != 2 {
		t.Fatalf("actual due admission=%d %v", n, err)
	}
	for i := 0; i < 2; i++ {
		run := originalDispatchExecute(t, a)
		if run.Action != "service" {
			t.Errorf("wrong queued action: %s", run.Action)
		}
	}
	for _, p := range []*originalPluginInstance{source, clone} {
		got := originalArtifactSnapshot(t, p)
		if got["tick_count"] != float64(1) {
			t.Errorf("Handler not invoked on %s: %+v", p.installation.InstanceID, got)
			continue
		}
		inputs, ok := got["tick_inputs"].([]any)
		if !ok || len(inputs) != 1 {
			t.Errorf("missing real handler input: %+v", got)
			continue
		}
		input, ok := inputs[0].(map[string]any)
		if !ok || input["owned_origin"] != "service" {
			t.Errorf("definition.Input lost: %+v", inputs)
		}
	}
	save(source.installation.InstanceID, false)
	if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, originalDispatchDue(t, a)); err != nil || n != 1 {
		t.Fatalf("disabled source should leave only clone due: %d %v", n, err)
	}
	run := originalDispatchExecute(t, a)
	if run.InstanceID != clone.installation.InstanceID {
		t.Error("disabled source ran")
	}
	if got := originalArtifactSnapshot(t, source); got["tick_count"] != float64(1) {
		t.Errorf("source executed after disable: %+v", got)
	}
	if got := originalArtifactSnapshot(t, clone); got["tick_count"] != float64(2) {
		t.Errorf("clone service did not run independently: %+v", got)
	}
	// Original core.stop does not remove the central job. In the Go process
	// transport a stopped binding cannot execute or silently restart; this is
	// not a claim that Python's scheduler checks the manager's running map.
	if err := a.stopOriginalPlugin(ctx, clone.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	save(source.installation.InstanceID, true)
	readStoppedSchedule := func() (string, int) {
		t.Helper()
		var nextDue string
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT next_due FROM plugin_schedules WHERE instance_id=? AND group_key='original_services' AND active=1`, clone.installation.InstanceID).Scan(&nextDue); err != nil {
			t.Fatal(err)
		}
		var occurrences int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=?`, clone.installation.InstanceID).Scan(&occurrences); err != nil {
			t.Fatal(err)
		}
		return nextDue, occurrences
	}
	stoppedDue, stoppedOccurrences := readStoppedSchedule()
	if _, ok := a.pluginRunningInstances.Load(clone.installation.InstanceID); ok {
		t.Fatal("source save revived stopped clone")
	}
	if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, originalDispatchDue(t, a)); err != nil || n != 1 {
		t.Fatalf("stopped binding blocked live sibling or queued itself: count=%d error=%v", n, err)
	}
	run = originalDispatchExecute(t, a)
	if run.InstanceID != source.installation.InstanceID || run.Action != "service" {
		t.Errorf("expected only live source service, got instance=%s action=%s", run.InstanceID, run.Action)
	}
	if got := originalArtifactSnapshot(t, source); got["tick_count"] != float64(2) {
		t.Errorf("live source did not execute its handler independently: %+v", got)
	}
	if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unexpected extra pending run after live sibling: %v", err)
	}
	if nextDue, occurrences := readStoppedSchedule(); nextDue != stoppedDue || occurrences != stoppedOccurrences {
		t.Errorf("stopped binding consumed due/occurrence: due %s -> %s, occurrences %d -> %d", stoppedDue, nextDue, stoppedOccurrences, occurrences)
	}
	if _, ok := a.pluginRunningInstances.Load(clone.installation.InstanceID); ok {
		t.Fatal("service silently reloaded stopped clone")
	}

}
func TestPluginOriginalArtifactRegisteredCommandCallsLoadedInstance(t *testing.T) {
	a, source, clone, save := originalDispatchFixture(t)
	ctx := context.Background()
	command, id, ok := a.lookupOriginalPluginCommand("/owned_module")
	if !ok || id != clone.installation.InstanceID {
		t.Fatalf("last registered instance command: %s %v", id, ok)
	}
	if err := a.dispatchLarkPluginCommandInstance(ctx, command, id, "owned-user"); err != nil {
		t.Fatalf("actual registered command dispatch: %v", err)
	}
	run := originalDispatchExecute(t, a)
	if run.InstanceID != id {
		t.Fatal("command ran wrong instance")
	}
	if got := originalArtifactSnapshot(t, clone); got["tick_count"] != float64(1) {
		t.Errorf("command function not invoked: %+v", got)
	}
	if got := originalArtifactSnapshot(t, source); got["tick_count"] != float64(0) {
		t.Errorf("command leaked into source: %+v", got)
	}
	save(clone.installation.InstanceID, false)
	save(source.installation.InstanceID, false)
	if _, _, ok = a.lookupOriginalPluginCommand("/owned_module"); ok {
		t.Fatal("disabled instances retained command qualification")
	}
}
