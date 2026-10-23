package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func nativeSubscribeRetryFixture(t *testing.T) (*App, PluginRun, string) {
	t.Helper()
	a := subscriptionTestApp(t)
	subscribeGroupConfigure(t, a, subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫#include:Jade")))
	_, run, batch := subscribeGroupCreate(t, a)
	return a, run, batch
}

func TestPluginNativeRetryClassificationKeepsLiveAttemptUnreplayable(t *testing.T) {
	a, run, _ := nativeSubscribeRetryFixture(t)
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	head, err := a.pluginStore.runJournal.loadHead(ctx, tx, run.InstanceID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := a.pluginStore.runJournal.classifyNativeRecoveryTx(ctx, tx, head)
	if err != nil || evidence.Class != pluginNativeNoEffectsRetry {
		t.Fatal("exact transactional event not classified", evidence, err)
	}
	proven, err := a.pluginStore.runJournal.nativeRetryProofTx(ctx, tx, head, true)
	if err != nil || proven {
		t.Fatal("live boot minted a retry", proven, err)
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempt_retries`).Scan(&count); err != nil || count != 0 {
		t.Fatal("classification wrote proof", count, err)
	}
}

func TestPluginNativeRetryRejectsChangedScopeAndSnapshots(t *testing.T) {
	for _, mode := range []string{"handler", "batch_count", "delivery", "domain", "snapshot", "config", "receipt-other-scope", "action"} {
		t.Run(mode, func(t *testing.T) {
			a, run, batch := nativeSubscribeRetryFixture(t)
			ctx := context.Background()
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			head, err := a.pluginStore.runJournal.loadHead(ctx, tx, run.InstanceID, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, e := tx.ExecContext(ctx, q, args...); e != nil {
					t.Fatal(e)
				}
			}
			switch mode {
			case "handler":
				exec(`UPDATE plugin_event_batches SET handler_key='unknown' WHERE id=?`, batch)
			case "batch_count":
				exec(`UPDATE plugin_event_batches SET item_count=2 WHERE id=?`, batch)
			case "delivery":
				exec(`DELETE FROM plugin_event_deliveries WHERE batch_id=?`, batch)
			case "domain":
				exec(`UPDATE domain_events SET payload='{}' WHERE event_id IN (SELECT event_id FROM plugin_event_deliveries WHERE batch_id=?)`, batch)
			case "snapshot":
				exec(`UPDATE records SET payload=X'00' WHERE kind='subscription_added'`)
			case "config":
				exec(`UPDATE plugin_values SET sealed=X'00' WHERE instance_id=? AND kind='config'`, run.InstanceID)
			case "receipt-other-scope":
				exec(`INSERT INTO plugin_subscribe_group_operations(run_id,instance_id,generation,config_revision,result) VALUES(?,?,?,?,?)`, run.ID, run.InstanceID, run.Generation+1, run.ConfigRevision, []byte{0})
			case "action":
				exec(`UPDATE plugin_runs SET action='init' WHERE id=?`, run.ID)
			}
			evidence, e := a.pluginStore.runJournal.classifyNativeRecoveryTx(ctx, tx, head)
			if e == nil && evidence.Class == pluginNativeNoEffectsRetry {
				t.Fatal("invalid evidence granted retry", mode)
			}
			var count int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempt_retries`).Scan(&count); err != nil || count != 0 {
				t.Fatal("readonly classifier changed retry proof", count, err)
			}
		})
	}
}

func TestPluginNativeRetryColdProofRechecksBeforeClaim(t *testing.T) {
	for _, mode := range []string{"unchanged", "proof-corrupt", "proof-delete", "snapshot-corrupt"} {
		t.Run(mode, func(t *testing.T) {
			a, run, _ := nativeSubscribeRetryFixture(t)
			ctx := context.Background()
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			current, err := cold.pluginStore.Run(ctx, run.ID)
			if err != nil || current.State != "pending" {
				t.Fatal("exact cold retry absent", current.State, err)
			}
			switch mode {
			case "proof-corrupt":
				_, err = cold.Store.DB.Exec(`UPDATE plugin_run_attempt_retries SET sealed=X'00'`)
			case "proof-delete":
				_, err = cold.Store.DB.Exec(`DELETE FROM plugin_run_attempt_retries`)
			case "snapshot-corrupt":
				_, err = cold.Store.DB.Exec(`UPDATE records SET payload=X'00' WHERE kind='subscription_added'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := cold.pluginStore.claimRunForInstance(ctx, run.InstanceID)
			if mode == "unchanged" {
				if err != nil || next.ID != run.ID || next.executionLease == nil || next.executionLease.claim.nonce == run.executionLease.claim.nonce {
					t.Fatal("cold retry lacks new exact attempt", err)
				}
			} else if err == nil || err == sql.ErrNoRows {
				t.Fatal("missing/corrupt proof did not reject exact claim", mode, err)
			}
		})
	}
}

func TestPluginNativeSubscribeInitRollbackCanRetryExactColdRun(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	run := subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫#include:Jade"))
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER native_init_receipt_fault BEFORE INSERT ON plugin_subscribe_group_operations BEGIN SELECT RAISE(ABORT,'native owned init receipt rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.configureSubscribeGroup(ctx, &run); err == nil || !strings.Contains(err.Error(), "native owned init receipt rollback") {
		t.Fatal("did not reach original receipt write", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER native_init_receipt_fault`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	next, err := cold.pluginStore.claimRunForInstance(ctx, run.InstanceID)
	if err != nil || next.ID != run.ID {
		t.Fatal("init rollback lost exact retry", err)
	}
	claimed := next
	result, err := cold.configureSubscribeGroup(ctx, &next)
	if err != nil {
		t.Fatal(err)
	}
	if err = finishClaimedFixtureRun(ctx, cold.pluginStore, claimed, "succeeded", "", result); err != nil {
		t.Fatal(err)
	}
}
