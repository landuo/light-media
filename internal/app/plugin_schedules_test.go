package app

import (
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

func pluginScheduleFixture(t *testing.T, at time.Time, trigger schedule.Spec, grace int64) (*App, PluginRun, pluginScheduleDefinition) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	a := &App{Store: s, pluginStore: p, Dir: filepath.Dir(s.lock.Name())}
	installStoredPlugin(t, p, "timer")
	_, _, err := p.SaveConfigAndQueue(context.Background(), "timer", 0, map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	definition := pluginScheduleDefinition{ID: "Timer.Check", Name: "合成定时服务", Handler: "check", Trigger: trigger, MisfireGraceMS: grace, Input: json.RawMessage(`{"reference":"owned-fixture","token":"synthetic-schedule-secret"}`)}
	if err := p.configurePluginSchedules(context.Background(), run, "services", []pluginScheduleDefinition{definition}, at); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(context.Background(), p, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	return a, run, definition
}

func pluginTimerOccurrence(t *testing.T, a *App, runID string) (string, string) {
	t.Helper()
	var id, state string
	if err := a.Store.DB.QueryRow(`SELECT id,state FROM plugin_schedule_occurrences WHERE run_id=?`, runID).Scan(&id, &state); err != nil {
		t.Fatal(err)
	}
	return id, state
}

func TestPluginSchedulesPersistDueAndBindPrivateInvocation(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "Asia/Shanghai", IntervalMS: 30000}, 0)
	for _, offset := range []time.Duration{0, 29 * time.Second} {
		if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(offset)); err != nil || count != 0 {
			t.Fatal("interval fired before first period", count, err)
		}
	}
	for index := 0; index < 2; index++ {
		count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(30*time.Second))
		if err != nil || (index == 0 && count != 1) || (index == 1 && count != 0) {
			t.Fatal("scheduled occurrence lost or duplicated", count, err)
		}
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.Action != "service" {
		t.Fatal(err, run.Action)
	}
	occurrenceID, state := pluginTimerOccurrence(t, a, run.ID)
	if state != "running" {
		t.Fatal("occurrence did not claim atomically", state)
	}
	definition, err := a.pluginStore.pluginScheduledInvocation(ctx, run, occurrenceID)
	if err != nil || definition.Handler != "check" || !strings.Contains(string(definition.Input), "owned-fixture") {
		t.Fatal("bound invocation unavailable", err)
	}
	other := run
	other.InstanceID = "another-instance"
	if _, err := a.pluginStore.pluginScheduledInvocation(ctx, other, occurrenceID); pluginErrorCode(err) != "PLUGIN_SCHEDULE_SCOPE_DENIED" {
		t.Fatal("cross-instance invocation disclosed", err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", json.RawMessage(`{"completed":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, state := pluginTimerOccurrence(t, a, run.ID); state != "succeeded" {
		t.Fatal("finish did not commit both states", state)
	}
	if _, err := a.pluginStore.pluginScheduledInvocation(ctx, run, occurrenceID); pluginErrorCode(err) != "PLUGIN_SCHEDULE_SCOPE_DENIED" {
		t.Fatal("finished invocation remained readable", err)
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(filepath.Join(a.Dir, "light-media.db") + suffix)
		if suffix == "" && err != nil {
			t.Fatal("actual fixture database could not be inspected", err)
		}
		if err == nil && strings.Contains(string(raw), "synthetic-schedule-secret") {
			t.Fatal("private scheduled input stored in plaintext")
		}
	}
}

func TestPluginSchedulesCoalesceOutageAndRespectMisfireAndBusyLimits(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000}, 1000)
	late := at.Add(10*24*time.Hour + 500*time.Millisecond)
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, late); err != nil || count != 1 {
		t.Fatal("outage did not coalesce to one latest occurrence", count, err)
	}
	var due string
	if err := a.Store.DB.QueryRow(`SELECT scheduled_at FROM plugin_schedule_occurrences`).Scan(&due); err != nil || due != schedule.FormatTime(at.Add(10*24*time.Hour)) {
		t.Fatal("coalescing chose an old due time or lost its anchor fraction", due, err)
	}
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, late.Add(time.Second)); err != nil || count != 0 {
		t.Fatal("busy service accumulated another invocation", count, err)
	}
	var skipped int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE state='skipped_busy'`).Scan(&skipped); err != nil || skipped != 1 {
		t.Fatal("busy occurrence was not recorded", skipped, err)
	}
	missedAt := time.Now().UTC().Add(-10 * time.Second)
	b, _, _ := pluginScheduleFixture(t, missedAt, schedule.Spec{Kind: "date", Timezone: "UTC", RunAt: schedule.FormatTime(missedAt.Add(3 * time.Second))}, 1000)
	if count, err := b.pluginStore.enqueueDuePluginSchedules(ctx, missedAt.Add(5*time.Second)); err != nil || count != 0 {
		t.Fatal("expired one-shot executed", count, err)
	}
	if err := b.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE state='missed'`).Scan(&skipped); err != nil || skipped != 1 {
		t.Fatal("misfire not recorded", skipped, err)
	}
}

func TestPluginSchedulesQueueAndFinishFailuresAreAtomic(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000}, -1)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_schedule_receipt BEFORE INSERT ON plugin_schedule_occurrences BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(time.Second)); err == nil || count != 0 {
		t.Fatal("failed late receipt consumed due time", count, err)
	}
	var due string
	if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules`).Scan(&due); err != nil || due != schedule.FormatTime(at.Add(time.Second)) {
		t.Fatal("failed receipt advanced schedule", due, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER fail_schedule_receipt`); err != nil {
		t.Fatal(err)
	}
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(time.Second)); err != nil || count != 1 {
		t.Fatal("retry did not queue exactly one occurrence", count, err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runContext := executionLeaseActivate(t, a.pluginStore, run)
	t.Cleanup(run.executionLease.runCleanup) // SQL failure deliberately leaves the attempt unresolved.
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_schedule_finish BEFORE UPDATE OF state ON plugin_schedule_occurrences WHEN NEW.state='succeeded' BEGIN SELECT RAISE(ABORT,'injected finish failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.FinishRun(runContext, run.ID, "succeeded", "", nil); err == nil || !strings.Contains(err.Error(), "injected finish failure") {
		t.Fatal("finish did not reach the injected SQL failure", err)
	}
	if current, err := a.pluginStore.Run(ctx, run.ID); err != nil || current.State != "running" {
		t.Fatal("run committed without its occurrence", current.State, err)
	}
	if _, state := pluginTimerOccurrence(t, a, run.ID); state != "running" {
		t.Fatal("occurrence committed despite rollback", state)
	}
}

func TestPluginSchedulesDateDoesNotReplayOnSameConfigRestart(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, definition := pluginScheduleFixture(t, at, schedule.Spec{Kind: "date", Timezone: "UTC", RunAt: schedule.FormatTime(at.Add(3 * time.Second))}, -1)
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(3*time.Second)); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.EnqueueRun(ctx, "timer", "init", nil); err != nil {
		t.Fatal(err)
	}
	init, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.configurePluginSchedules(ctx, init, "services", []pluginScheduleDefinition{definition}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, init, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(2*time.Hour)); err != nil || count != 0 {
		t.Fatal("same config replayed a completed one-shot", count, err)
	}
}

func TestPluginSchedulesManualDoesNotMovePeriodAndCannotOverlap(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 30000}, 1000)
	run, err := a.pluginStore.enqueueManualPluginService(ctx, "timer", "Timer.Check", at.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.enqueueManualPluginService(ctx, "timer", "Timer.Check", at); pluginErrorCode(err) != "PLUGIN_SERVICE_BUSY" {
		t.Fatal("manual overlapping invocation accepted", err)
	}
	claimed, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || claimed.ID != run.ID {
		t.Fatal("manual invocation incorrectly applied misfire grace", claimed.ID, err)
	}
	var next string
	if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules`).Scan(&next); err != nil || next != schedule.FormatTime(at.Add(30*time.Second)) {
		t.Fatal("manual run moved recurring schedule", next, err)
	}
}

func TestPluginSchedulesWaitingRunExpiresAndConfigEditCancelsOldSnapshot(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(-10 * time.Second)
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "date", Timezone: "UTC", RunAt: schedule.FormatTime(at.Add(3 * time.Second))}, 1000)
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(3*time.Second)); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired queued invocation was executed", err)
	}
	var state, errorCode string
	if err := a.Store.DB.QueryRow(`SELECT r.state,r.error_code FROM plugin_runs r JOIN plugin_schedule_occurrences o ON o.run_id=r.id`).Scan(&state, &errorCode); err != nil || state != "canceled" || errorCode != "PLUGIN_SCHEDULE_MISFIRE" {
		t.Fatal("queued expiry was not recorded", state, errorCode, err)
	}
	b, _, _ := pluginScheduleFixture(t, time.Now(), schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000}, -1)
	manual, err := b.pluginStore.enqueueManualPluginService(ctx, "timer", "Timer.Check", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.pluginStore.SaveConfigAndQueue(ctx, "timer", 1, map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	claimed, err := b.pluginStore.ClaimRun(ctx)
	if err != nil || claimed.Action != "init" {
		t.Fatal("replacement config unavailable", claimed.Action, err)
	}
	if old, err := b.pluginStore.Run(ctx, manual.ID); err != nil || old.State != "canceled" {
		t.Fatal("old schedule snapshot survived user edit", old.State, err)
	}
}

func TestPluginSchedulesInternalFlagPatchPreservesQueuedWorkAndAnchor(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, definition := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 30000}, -1)
	if _, err := a.pluginStore.EnqueueRun(ctx, "timer", "reload", nil); err != nil {
		t.Fatal(err)
	}
	reload, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.enqueueManualPluginService(ctx, "timer", "Timer.Check", at); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.patchRunConfig(ctx, &reload, map[string]json.RawMessage{"onlyonce": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.configurePluginSchedules(ctx, reload, "services", []pluginScheduleDefinition{definition}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, reload, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.Action != "service" || run.ConfigRevision != 2 {
		t.Fatal("internal normalization canceled accepted work", run.Action, run.ConfigRevision, err)
	}
	occurrenceID, _ := pluginTimerOccurrence(t, a, run.ID)
	if _, err := a.pluginStore.pluginScheduledInvocation(ctx, run, occurrenceID); err != nil {
		t.Fatal(err)
	}
	var next string
	if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules`).Scan(&next); err != nil || next != schedule.FormatTime(at.Add(30*time.Second)) {
		t.Fatal("internal normalization reset interval anchor", next, err)
	}
}

func TestPluginSchedulesReopenRetainsNextDueAndDoesNotReplayUncertainRun(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Hour)
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 30000}, -1)
	if _, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	current, err := reopened.pluginStore.Run(ctx, run.ID)
	if err != nil || current.State != "reconcile_pending" {
		t.Fatal("unknown business was blindly replayed after restart", current.State, err)
	}
	if _, state := pluginTimerOccurrence(t, reopened, run.ID); state != "reconcile_pending" {
		t.Fatal("occurrence did not preserve uncertain state", state)
	}
	var next string
	if err := reopened.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules`).Scan(&next); err != nil || next != schedule.FormatTime(at.Add(time.Minute)) {
		t.Fatal("restart lost next occurrence", next, err)
	}
	if count, err := reopened.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(time.Minute)); err != nil || count != 0 {
		t.Fatal("new occurrence bypassed unresolved previous execution", count, err)
	}
	if _, err := reopened.pluginStore.enqueueManualPluginService(ctx, "timer", "Timer.Check", at.Add(time.Minute)); pluginErrorCode(err) != "PLUGIN_SERVICE_BUSY" {
		t.Fatal("manual retry bypassed unresolved operation", err)
	}
}

func TestPluginSchedulesGroupReplacementAndLifecycleStopNewTriggers(t *testing.T) {
	for _, action := range []string{"replace_group", "uninstall", "reset"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			at := time.Now().UTC().Add(time.Hour)
			a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000}, -1)
			if action == "replace_group" {
				if _, err := a.pluginStore.EnqueueRun(ctx, "timer", "reload", nil); err != nil {
					t.Fatal(err)
				}
				run, err := a.pluginStore.ClaimRun(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.pluginStore.configurePluginSchedules(ctx, run, "services", nil, at); err != nil {
					t.Fatal(err)
				}
			} else if action == "uninstall" {
				if err := a.pluginStore.Uninstall(ctx, "timer"); err != nil {
					t.Fatal(err)
				}
			} else if err := a.pluginStore.Reset(ctx, "timer"); err != nil {
				t.Fatal(err)
			}
			if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(time.Hour)); err != nil || count != 0 {
				t.Fatal("removed service kept scheduling", count, err)
			}
			if _, err := a.pluginStore.enqueueManualPluginService(ctx, "timer", "Timer.Check", at.Add(time.Hour)); pluginErrorCode(err) != "PLUGIN_SERVICE_NOT_FOUND" {
				t.Fatal("removed service remained manually runnable", err)
			}
		})
	}
}

func TestPluginSchedulesCoalescingCalendarGapsFoldsAndCenturies(t *testing.T) {
	for _, tc := range []struct {
		cron, zone, first, at, last string
	}{
		{"* * * * *", "UTC", "1970-01-01T00:00:00Z", "2999-10-16T00:03:30Z", "2999-10-16T00:03:00Z"},
		{"0 0 29 feb mon", "UTC", "2044-02-29T00:00:00Z", "2073-01-01T00:00:00Z", "2072-02-29T00:00:00Z"},
		{"30 1 * * *", "America/New_York", "2026-10-30T05:30:00Z", "2026-11-01T06:40:00Z", "2026-11-01T06:30:00Z"},
		{"30 2 * * *", "America/New_York", "2026-03-01T07:30:00Z", "2026-03-08T07:35:00Z", "2026-03-08T07:30:00Z"},
	} {
		t.Run(tc.cron+tc.zone, func(t *testing.T) {
			parse := func(s string) time.Time {
				value, err := time.Parse(time.RFC3339Nano, s)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			trigger, err := schedule.New(schedule.Spec{Kind: "cron", Timezone: tc.zone, Cron: tc.cron}, parse(tc.first))
			if err != nil {
				t.Fatal(err)
			}
			latest, next := latestPluginScheduleDue(trigger, parse(tc.first), parse(tc.at))
			if !latest.Equal(parse(tc.last)) || (!next.IsZero() && !next.After(parse(tc.at))) {
				t.Fatal("calendar coalescing selected the wrong occurrence", latest, next)
			}
		})
	}
}
