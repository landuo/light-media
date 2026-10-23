package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func pluginEventFixture(t *testing.T, delay time.Duration) (*App, PluginRun) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	a := &App{Store: s, pluginStore: p}
	installStoredPlugin(t, p, "consumer")
	_, _, err := p.SaveConfigAndQueue(context.Background(), "consumer", 0, map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.configureEventSubscription(context.Background(), run, "refresh", []string{"import.completed"}, delay, true); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(context.Background(), p, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	return a, run
}

func pluginEventCount(t *testing.T, a *App, table string) int {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPluginEventsDeliverOnlyCommittedMatchingFactsOnce(t *testing.T) {
	a, _ := pluginEventFixture(t, 0)
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordDomainEventTx(ctx, tx, domainFixture("import.completed", "rolled-back")); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	commitDomainFixture(t, a, domainFixture("download.accepted", "unrelated"))
	commitDomainFixture(t, a, domainFixture("import.completed", "one"))
	commitDomainFixture(t, a, domainFixture("import.completed", "two"))
	for i := 0; i < 3; i++ {
		if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if pluginEventCount(t, a, "plugin_event_deliveries") != 2 || pluginEventCount(t, a, "plugin_event_batches") != 2 {
		t.Fatal("uncommitted, mismatched or duplicate facts were delivered")
	}
	for i := 0; i < 2; i++ {
		run, err := a.pluginStore.ClaimRun(ctx)
		if err != nil || run.Action != "event" || !json.Valid(run.Input) {
			t.Fatal("matching event run missing", run.Action, err)
		}
		if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("duplicate run created", err)
	}
}

func TestPluginEventsDebounceExtendsAndEnqueuesOneDurableBatch(t *testing.T) {
	a, _ := pluginEventFixture(t, 2*time.Second)
	ctx := context.Background()
	at := time.Now()
	commitDomainFixture(t, a, domainFixture("import.completed", "one"))
	if err := a.dispatchPluginEvents(ctx, at); err != nil {
		t.Fatal(err)
	}
	commitDomainFixture(t, a, domainFixture("import.completed", "two"))
	if err := a.dispatchPluginEvents(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("batch ignored extended delay", err)
	}
	if pluginEventCount(t, a, "plugin_event_deliveries") != 2 || pluginEventCount(t, a, "plugin_event_batches") != 1 {
		t.Fatal("debounce lost items or split a pending batch")
	}
	if err := a.dispatchPluginEvents(ctx, at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.Action != "event" {
		t.Fatal("due batch not queued", err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, at.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("already queued batch repeated", err)
	}
}

func TestPluginEventsCollectionFailureRollsBackCursorAndDelivery(t *testing.T) {
	a, _ := pluginEventFixture(t, time.Second)
	ctx := context.Background()
	commitDomainFixture(t, a, domainFixture("import.completed", "retry-collection"))
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_plugin_delivery BEFORE INSERT ON plugin_event_deliveries BEGIN SELECT RAISE(ABORT,'fixture delivery failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err == nil {
		t.Fatal("delivery insert failure reported success")
	}
	var cursor int64
	if err := a.Store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions`).Scan(&cursor); err != nil || cursor != 0 || pluginEventCount(t, a, "plugin_event_batches") != 0 || pluginEventCount(t, a, "plugin_event_deliveries") != 0 {
		t.Fatal("failed collection partially committed", cursor, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_plugin_delivery`); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil || pluginEventCount(t, a, "plugin_event_deliveries") != 1 {
		t.Fatal("failed event was lost", err)
	}
}

func TestPluginEventsQueueFailureRetainsBatchAndRetriesWithoutDuplicates(t *testing.T) {
	a, _ := pluginEventFixture(t, 0)
	ctx := context.Background()
	commitDomainFixture(t, a, domainFixture("import.completed", "retry-queue"))
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_plugin_event_run BEFORE INSERT ON plugin_runs BEGIN SELECT RAISE(ABORT,'fixture queue failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err == nil || pluginEventCount(t, a, "plugin_event_deliveries") != 1 {
		t.Fatal("queue error lost durable delivery", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_plugin_event_run`); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil || pluginEventCount(t, a, "plugin_event_deliveries") != 1 || pluginEventCount(t, a, "plugin_event_batches") != 1 {
		t.Fatal("queue retry lost or duplicated batch", err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.Action != "event" {
		t.Fatal("queue failure could not be recovered", err)
	}
}

func TestPluginEventsUserEditCancelsOldDelayAndSkipsDisabledHistory(t *testing.T) {
	a, _ := pluginEventFixture(t, time.Hour)
	ctx := context.Background()
	commitDomainFixture(t, a, domainFixture("import.completed", "old-pending"))
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, "consumer", 1, map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := a.Store.DB.QueryRow(`SELECT state FROM plugin_event_batches`).Scan(&state); err != nil || state != "canceled" {
		t.Fatal("old delayed work retained authority", state, err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.configureEventSubscription(ctx, run, "refresh", []string{"import.completed"}, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	commitDomainFixture(t, a, domainFixture("import.completed", "disabled"))
	if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, "consumer", 2, map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.ClaimRun(ctx)
	if err != nil || a.pluginStore.configureEventSubscription(ctx, run, "refresh", []string{"import.completed"}, 0, true) != nil {
		t.Fatal("re-enable failed", err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now().Add(2*time.Hour)); err != nil || pluginEventCount(t, a, "plugin_event_deliveries") != 1 {
		t.Fatal("disabled history or canceled batch replayed", err)
	}
	if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("disabled-period event became executable", err)
	}
}

func TestPluginEventsInternalConfigPatchPreservesActiveConsumerAndBatch(t *testing.T) {
	a, first := pluginEventFixture(t, time.Second)
	ctx := context.Background()
	commitDomainFixture(t, a, domainFixture("import.completed", "normalized"))
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	queued, err := a.pluginStore.EnqueueRun(ctx, "consumer", "reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.ID != queued.ID || run.ConfigRevision != first.ConfigRevision {
		t.Fatal(err)
	}
	if err := a.pluginStore.patchRunConfig(ctx, &run, map[string]json.RawMessage{"onlyonce": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	event, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || event.Action != "event" || event.ConfigRevision != run.ConfigRevision {
		t.Fatal("internal patch stranded delayed work", err)
	}
}

func TestPluginEventsRestartRetainsCursorAndPendingBatch(t *testing.T) {
	a, first := pluginEventFixture(t, 10*time.Second)
	ctx := context.Background()
	at := time.Now()
	commitDomainFixture(t, a, domainFixture("import.completed", "collected-before-restart"))
	if err := a.dispatchPluginEvents(ctx, at); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(a.Store.lock.Name())
	if err := a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	p, err := NewPluginStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	a.Store, a.pluginStore = store, p
	commitDomainFixture(t, a, domainFixture("import.completed", "committed-before-reinit"))
	if _, err := p.EnqueueRun(ctx, "consumer", "reload", nil); err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil || run.ConfigRevision != first.ConfigRevision || p.configureEventSubscription(ctx, run, "refresh", []string{"import.completed"}, 10*time.Second, true) != nil {
		t.Fatal("restart could not restore unchanged consumer", err)
	}
	if err := finishClaimedFixtureRun(ctx, p, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if pluginEventCount(t, a, "plugin_event_batches") != 1 || pluginEventCount(t, a, "plugin_event_deliveries") != 2 {
		t.Fatal("restart reset cursor or lost pending items")
	}
	if err := a.dispatchPluginEvents(ctx, at.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	run, err = p.ClaimRun(ctx)
	if err != nil || run.Action != "event" {
		t.Fatal("persisted delay did not fire", err)
	}
	var batchID string
	if err := store.DB.QueryRow(`SELECT id FROM plugin_event_batches WHERE run_id=?`, run.ID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	page, err := p.pluginBatchEvents(ctx, run, batchID, 0, 1)
	if err != nil || len(page.Events) != 1 || page.Complete || page.Total != 2 {
		t.Fatal("running batch references not accessible", len(page.Events), err)
	}
	page, err = p.pluginBatchEvents(ctx, run, batchID, page.NextCursor, 1)
	if err != nil || len(page.Events) != 1 || !page.Complete {
		t.Fatal("batch pagination lost its second event", err)
	}
	wrong := run
	wrong.InstanceID = "other"
	if _, err := p.pluginBatchEvents(ctx, wrong, batchID, 0, 200); pluginErrorCode(err) != "PLUGIN_BATCH_SCOPE_DENIED" {
		t.Fatal("cross-instance batch access accepted", err)
	}
	if err := finishClaimedFixtureRun(ctx, p, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pluginBatchEvents(ctx, run, batchID, 0, 200); pluginErrorCode(err) != "PLUGIN_BATCH_SCOPE_DENIED" {
		t.Fatal("completed run retained action authority", err)
	}
}

func TestPluginEventsFirstEnableUsesCommittedHighWater(t *testing.T) {
	s, p := pluginStoreFixture(t)
	a := &App{Store: s, pluginStore: p}
	ctx := context.Background()
	commitDomainFixture(t, a, domainFixture("import.completed", "historical"))
	installStoredPlugin(t, p, "first-enable")
	if _, _, err := p.SaveConfigAndQueue(ctx, "first-enable", 0, map[string]bool{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil || p.configureEventSubscription(ctx, run, "refresh", []string{"import.completed"}, 0, true) != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, p, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil || pluginEventCount(t, a, "plugin_event_deliveries") != 0 {
		t.Fatal("first enable silently replayed old library writes", err)
	}
	if err := p.configureEventSubscription(ctx, run, "late-call", []string{"import.completed"}, 0, true); pluginErrorCode(err) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("terminal run registered new authority", err)
	}
}

func TestPluginEventsLargeDebouncedBatchStaysOneBatchAndReadsBoundedPages(t *testing.T) {
	a, _ := pluginEventFixture(t, time.Hour)
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	const total = 5010
	for i := 0; i < total; i++ {
		event := domainFixture("import.completed", fmt.Sprintf("large-%d", i))
		event.JobID = fmt.Sprintf("job_fixture_%d", i)
		if err := recordDomainEventTx(ctx, tx, event); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	for i := 0; i <= total/200; i++ {
		if err := a.dispatchPluginEvents(ctx, at); err != nil {
			t.Fatal(err)
		}
	}
	if pluginEventCount(t, a, "plugin_event_batches") != 1 || pluginEventCount(t, a, "plugin_event_deliveries") != total {
		t.Fatal("large burst was split or truncated")
	}
	if err := a.dispatchPluginEvents(ctx, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var batchID string
	if err := a.Store.DB.QueryRow(`SELECT id FROM plugin_event_batches WHERE run_id=?`, run.ID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	var after int64
	seen := map[string]bool{}
	for {
		page, err := a.pluginStore.pluginBatchEvents(ctx, run, batchID, after, 200)
		if err != nil || len(page.Events) > 200 || page.Total != total {
			t.Fatal("unbounded or invalid batch page", len(page.Events), err)
		}
		for _, event := range page.Events {
			if seen[event.EventID] {
				t.Fatal("event repeated across pages")
			}
			seen[event.EventID] = true
		}
		if page.Complete {
			break
		}
		if page.NextCursor <= after {
			t.Fatal("batch cursor did not advance")
		}
		after = page.NextCursor
	}
	if len(seen) != total {
		t.Fatal("batch pagination lost committed events", len(seen))
	}
}

func TestPluginEventsCapacityCanDrainWhileMoreFactsAreWaiting(t *testing.T) {
	a, _ := pluginEventFixture(t, 0)
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		if _, err := a.pluginStore.EnqueueRun(ctx, "consumer", "noop", nil); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		if err := recordDomainEventTx(ctx, tx, domainFixture("import.completed", fmt.Sprintf("capacity-%d", i))); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := a.dispatchPluginEvents(ctx, time.Now()); pluginErrorCode(err) != "PLUGIN_QUEUE_FULL" {
			t.Fatal("queue capacity not enforced", err)
		}
	}
	if pluginEventCount(t, a, "plugin_event_batches") != 1000 {
		t.Fatal("capacity fixture did not fill collection queue")
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.Action != "noop" {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); pluginErrorCode(err) != "PLUGIN_QUEUE_FULL" {
		t.Fatal("one free slot did not retain remaining backpressure", err)
	}
	var queued int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_event_batches WHERE run_id IS NOT NULL`).Scan(&queued); err != nil || queued != 1 {
		t.Fatal("full collection queue prevented older due work from draining", queued, err)
	}
	if pluginEventCount(t, a, "plugin_event_deliveries") != 1000 {
		t.Fatal("failed collection advanced into the waiting fact")
	}
}
