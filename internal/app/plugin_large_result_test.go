package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/schedule"
)

func TestPluginLargeResultEncryptedReopenAndPublicList(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "one")
	_, queued, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]string{"password": "synthetic-private-config"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil || run.ID != queued.ID {
		t.Fatal("fixture run was not claimed", err)
	}
	private := "synthetic-private-result-" + strings.Repeat("r", 2<<20)
	raw, _ := json.Marshal(map[string]string{"private": private})
	if err := finishClaimedFixtureRun(ctx, p, run, "succeeded", "", raw); err != nil {
		t.Fatal("valid result above one frame could not be stored", err)
	}
	var sealed []byte
	if err := s.DB.QueryRow(`SELECT result FROM plugin_runs WHERE id=?`, run.ID).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("synthetic-private-result")) || json.Valid(sealed) {
		t.Fatal("large result was not encrypted", err)
	}
	dir := filepath.Dir(s.lock.Name())
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err = NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := p.Run(ctx, run.ID)
	if err != nil || stored.State != "succeeded" || !bytes.Equal(stored.Result, raw) {
		t.Fatal("large encrypted result failed after actual store reopen", err)
	}
	items, err := p.Runs(ctx, "one", 100)
	public, _ := json.Marshal(items)
	if err != nil || len(items) != 1 || len(items[0].Input) != 0 || len(items[0].Result) != 0 || bytes.Contains(public, []byte("synthetic-private")) {
		t.Fatal("public metadata loaded or exposed private values", err)
	}
	// A corrupt private result must not hide the execution metadata needed to
	// diagnose it. The dedicated private read still enforces AEAD integrity.
	if _, err := s.DB.Exec(`UPDATE plugin_runs SET result=? WHERE id=?`, []byte("corrupt-private-result"), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Run(ctx, run.ID); pluginErrorCode(err) != "PLUGIN_DATA_CORRUPT" {
		t.Fatal("private result integrity was not checked", err)
	}
	if items, err = p.Runs(ctx, "one", 100); err != nil || len(items) != 1 || items[0].State != "succeeded" {
		t.Fatal("corrupt result hid public execution metadata", err)
	}
}

func TestPluginLargeResultLimitsDoNotChangeOrdinaryValuesOrTerminalDecisions(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "one")
	if _, _, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]string{"value": strings.Repeat("x", (1<<20)+1)}); pluginErrorCode(err) != "INVALID_PLUGIN_VALUE" {
		t.Fatal("large result allowance expanded ordinary configuration", err)
	}
	_, _, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]bool{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runContext := executionLeaseActivate(t, p, run)
	for _, sample := range []struct {
		raw  json.RawMessage
		code string
	}{
		{json.RawMessage(`{"truncated":`), "INVALID_PLUGIN_RESULT"},
		{json.RawMessage(`"` + strings.Repeat("x", pluginDataValueLimit) + `"`), "PLUGIN_RESULT_TOO_LARGE"},
		// Encoded size matters even when the unescaped raw value is smaller.
		{json.RawMessage(`"` + strings.Repeat("<", 3<<20) + `"`), "PLUGIN_RESULT_TOO_LARGE"},
	} {
		if err := p.FinishRun(runContext, run.ID, "succeeded", "", sample.raw); pluginErrorCode(err) != sample.code {
			t.Fatal("unbounded or invalid result was accepted", sample.code, err)
		}
		current, err := p.Run(ctx, run.ID)
		if err != nil || current.State != "running" || len(current.Result) != 0 {
			t.Fatal("rejected result committed partial bytes", err)
		}
	}
	if err := p.failPluginResult(runContext, run.ID, "PLUGIN_RESULT_TOO_LARGE"); err != nil {
		t.Fatal(err)
	}
	failed, err := p.Run(ctx, run.ID)
	if err != nil || failed.State != "failed" || failed.ErrorCode != "PLUGIN_RESULT_TOO_LARGE" {
		t.Fatal("result failure was not terminal", err)
	}
	if err := p.FinishRun(runContext, run.ID, "succeeded", "", json.RawMessage(`{"late":true}`)); pluginErrorCode(err) != "PLUGIN_RUN_TERMINAL" {
		t.Fatal("late result replaced the failure", err)
	}
	if err := p.failPluginResult(runContext, run.ID, "INVALID_PLUGIN_RESULT"); err != nil {
		t.Fatal(err)
	}
	retained, err := p.Run(ctx, run.ID)
	if err != nil || retained.ErrorCode != failed.ErrorCode {
		t.Fatal("fallback overwrote another terminal decision", err)
	}
	if err := p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
}

func TestPluginLargeResultExactBoundaryAndRunIdentity(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	for _, id := range []string{"one", "two"} {
		installStoredPlugin(t, p, id)
		if _, _, err := p.SaveConfigAndQueue(ctx, id, 0, map[string]bool{"enabled": true}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`"` + strings.Repeat("b", pluginDataValueLimit-2) + `"`)
	if err := finishClaimedFixtureRun(ctx, p, first, "succeeded", "", raw); err != nil {
		t.Fatal("exact encoded result limit was rejected", err)
	}
	current, err := p.Run(ctx, first.ID)
	if err != nil || !bytes.Equal(current.Result, raw) {
		t.Fatal("exact encoded result was truncated or unreadable", err)
	}
	if _, err := s.DB.Exec(`UPDATE plugin_runs SET result=(SELECT result FROM plugin_runs WHERE id=?) WHERE id=?`, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Run(ctx, second.ID); pluginErrorCode(err) != "PLUGIN_DATA_CORRUPT" {
		t.Fatal("large result ciphertext could be moved to another real run", err)
	}
}

func TestPluginResultSaveFailureStopsActualWorkerReplayAfterSideEffect(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	if response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"CategoryEditor"}`, cookie, ""); response.Code != 200 {
		t.Fatal("fixture install failed", response.Code)
	}
	initial, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, initial)
	request, _ := json.Marshal(map[string]any{"revision": 1, "config": map[string]any{"enabled": true, "content": customJapaneseMovieCategory}})
	if response := callTest(t, a.Handler(), "PUT", "/api/plugins/CategoryEditor/config", string(request), cookie, ""); response.Code != 200 {
		t.Fatal("fixture configuration failed", response.Code)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER synthetic_result_write_failure BEFORE UPDATE OF result ON plugin_runs BEGIN SELECT RAISE(ABORT,'synthetic result column failure'); END;`); err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	file, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(file) != customJapaneseMovieCategory {
		t.Fatal("test did not cross the real host side-effect boundary", err)
	}
	failed, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || failed.State != "failed" || failed.ErrorCode != "PLUGIN_RESULT_SAVE_FAILED" || len(failed.Result) != 0 {
		t.Fatal("result-save failure left the actual worker claim running", failed.State, failed.ErrorCode, err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restart automatically replayed a worker whose result could not be saved", err)
	}
	if current, err := reopened.pluginStore.Run(ctx, run.ID); err != nil || current.State != "failed" || current.ErrorCode != failed.ErrorCode {
		t.Fatal("restart hid the persisted result failure", err)
	}
}

func TestPluginResultFailureCommitsScheduleStateAtomically(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000}, 0)
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(time.Second)); err != nil || count != 1 {
		t.Fatal("fixture occurrence not enqueued", count, err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runContext := executionLeaseActivate(t, a.pluginStore, run)
	t.Cleanup(run.executionLease.runCleanup)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER synthetic_occurrence_result_failure BEFORE UPDATE OF state ON plugin_schedule_occurrences BEGIN SELECT RAISE(ABORT,'synthetic occurrence update failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.failPluginResult(runContext, run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err == nil || !strings.Contains(err.Error(), "synthetic occurrence update failure") {
		t.Fatal("fallback did not reach injected occurrence SQL failure", err)
	}
	current, err := a.pluginStore.Run(ctx, run.ID)
	_, occurrence := pluginTimerOccurrence(t, a, run.ID)
	if err != nil || current.State != "running" || occurrence != "running" {
		t.Fatal("late failure committed only one terminal state", current.State, occurrence, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER synthetic_occurrence_result_failure`); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.failPluginResult(runContext, run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err != nil {
		t.Fatal(err)
	}
	current, err = a.pluginStore.Run(ctx, run.ID)
	_, occurrence = pluginTimerOccurrence(t, a, run.ID)
	if err != nil || current.State != "failed" || occurrence != "failed" {
		t.Fatal("result failure did not finish execution and occurrence together", current.State, occurrence, err)
	}
	if err := a.pluginStore.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
}

func TestPluginResultFailureAlsoBlocksFreshStartupInitialization(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	if response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SystemProcess"}`, cookie, ""); response.Code != 200 {
		t.Fatal("fixture install failed", response.Code)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.Action != "init" {
		t.Fatal("fixture init not claimed", err)
	}
	runContext := executionLeaseActivate(t, a.pluginStore, run)
	t.Cleanup(run.executionLease.runCleanup)
	if err := a.pluginStore.failPluginResult(runContext, run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// This is the real startup initializer used by Start, beyond recovery of
	// the previous run's state. SystemProcess has no CategoryEditor flag guard.
	if err := reopened.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("startup queued a fresh invocation for the uncertain completed init", err)
	}
	config, err := reopened.pluginStore.Config(ctx, "SystemProcess")
	if err != nil {
		t.Fatal(err)
	}
	// Explicitly saving a new revision remains available; a result failure is
	// not a permanent installation lock or a reason to discard saved settings.
	_, next, err := reopened.pluginStore.SaveConfigAndQueue(ctx, "SystemProcess", config.Revision, config.Value)
	if err != nil || next.ConfigRevision <= run.ConfigRevision {
		t.Fatal("explicit later configuration could not initialize", err)
	}
	claimed, err := reopened.pluginStore.ClaimRun(ctx)
	if err != nil || claimed.ID != next.ID {
		t.Fatal("later explicit configuration was blocked by the old failure", err)
	}
}
