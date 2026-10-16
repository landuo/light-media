package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

func signinTransitionSetup(t *testing.T) (*signinSQLFixture, PluginRun, signinjournal.State) {
	t.Helper()
	ctx := context.Background()
	f := signinSQLSetup(t)
	f.start(t)
	config := json.RawMessage(`{"enabled":true,"notify":false,"onlyonce":false,"sign_sites":[],"login_sites":[],"queue_cnt":5,"cron":"2.3/9-23"}`)
	if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "main", Kind: "config", Digest: "original-native-config-effect", Value: config, Mutation: signinjournal.Mutation{Kind: "config", Value: config}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.j.Apply(ctx, f.o.binding, signinjournal.Effect{RunID: f.run.ID, Lane: "main", Index: 1, Kind: "notify", Digest: "old-pending-notice", Value: json.RawMessage(`{"title":"old source pending notice"}`), Mutation: signinjournal.Mutation{Kind: "outbox"}}); err != nil {
		t.Fatal(err)
	}
	runtime := json.RawMessage(`{"enabled":true,"notify":false,"cron":"2.3/9-23","onlyonce":false,"queue_cnt":5,"sign_sites":[],"login_sites":[],"auto_cf":0,"clean":false,"start_time":9,"end_time":23,"schema_loaded":true,"config":{"enabled":true}}`)
	if err := f.b.Update(ctx, f.o.binding, func(s *signinjournal.State) error {
		s.Runtime = runtime
		s.History["retained-private-history"] = json.RawMessage(" {\"n\":9007199254740993123456789} ")
		s.Keys = []string{"retained-private-history"}
		s.Cache["source-cache"] = bytes.Repeat([]byte("private-cache\x00"), 20000)
		s.CacheReady["source-cache"] = true
		s.Stats["private.invalid"] = signinjournal.Statistic{Success: 2, Fail: 1, LastDate: "2026-01-02"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.j.Finish(ctx, f.o.binding, f.run.ID, runtime, json.RawMessage(`{"source":"complete"}`), "completed"); err != nil {
		t.Fatal(err)
	}
	var old signinjournal.State
	if err := f.b.View(ctx, f.o.binding, func(s *signinjournal.State) error { old = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	f.release()
	f.release = nil
	if err := f.a.pluginStore.FinishRun(ctx, f.run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	_, next, err := f.a.pluginStore.SaveConfigAndQueue(ctx, "AutoSignIn", 2, map[string]any{"enabled": true, "notify": false, "onlyonce": false, "sign_sites": []any{}, "login_sites": []any{}, "queue_cnt": 5, "cron": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, next.ID); err != nil {
		t.Fatal(err)
	}
	next, err = f.a.pluginStore.Run(ctx, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, next, old
}

func TestPluginAutoSignInTransitionArchivesOriginalScopesAndPendingOutboxWithoutPretendingDelivery(t *testing.T) {
	f, run, old := signinTransitionSetup(t)
	ctx := context.Background()
	oldOwner := f.o
	next, err := oldOwner.transitionConfig(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	f.o = next
	if next.binding.Revision != 3 || next.binding.Epoch != oldOwner.binding.Epoch {
		t.Fatal("ordinary save changed manager epoch or failed to establish a new admission")
	}
	if _, err = f.b.AcquireExecution(ctx, oldOwner.binding); !errors.Is(err, signinjournal.ErrStale) {
		t.Fatal("archived owner could revive", err)
	}
	b, _ := next.backend(&run)
	j, _ := signinjournal.NewStateJournal(b)
	var current signinjournal.State
	if err = b.View(ctx, next.binding, func(s *signinjournal.State) error { current = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(current.Runs) != 0 || len(current.Outbox) != 0 || len(current.Jobs) != 0 || current.Scope.Revision != 3 || !bytes.Equal(current.Runtime, old.Runtime) || !reflect.DeepEqual(current.History, old.History) || !reflect.DeepEqual(current.Cache, old.Cache) || !reflect.DeepEqual(current.Stats, old.Stats) {
		t.Fatal("new admission lost retained source state or imported old execution identities")
	}
	archived, receipt, err := next.readArchived(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(archived, old) || receipt.UnsettledOutbox != 1 || receipt.From != old.Scope || receipt.To != next.binding.Scope || archived.Runs[f.run.ID].Scope.Revision != 1 {
		t.Fatal("archive rewrote original source receipts/scopes", receipt)
	}
	if archived.Outbox[0].Phase != "pending" {
		t.Fatal("archiving was mislabeled event/message delivery")
	}
	var command struct {
		Config any `json:"config"`
	}
	if autosignin.Decode(run.Input, &command) != nil {
		t.Fatal("source invocation config missing")
	}
	c := signinjournal.Controller{Journal: j, Binding: next.binding, DigestKey: [32]byte{1, 2, 3}, Dependencies: signinjournal.Dependencies{Inventory: func(context.Context) (autosignin.Inventory, error) { return autosignin.Inventory{}, nil }, Timezone: "UTC", Now: func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }, RandomInt: func(a, b int) (int, error) { return a, nil }}}
	if _, err = c.Execute(ctx, run.ID, signinjournal.Command{Action: "init", Config: command.Config}); err != nil {
		t.Fatal("new admission could not execute actual source ordinary init", err)
	}
	if err = b.View(ctx, next.binding, func(s *signinjournal.State) error { current = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	var runtime autosignin.Object
	if autosignin.Decode(current.Runtime, &runtime) != nil || runtime["start_time"] != json.Number("9") || runtime["end_time"] != json.Number("23") {
		t.Fatal("ordinary configuration save reset retained source window", runtime)
	}
	cfg, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || cfg.Revision != 4 || run.ConfigRevision != 3 || next.binding.Revision != 3 {
		t.Fatal("new namespace lost monotonic public/source config receipts", cfg.Revision, err)
	}
	if archivedAgain, _, err := next.readArchived(ctx, 1); err != nil || !reflect.DeepEqual(archivedAgain, old) {
		t.Fatal("new execution mutated historical archive", err)
	}
}

func TestPluginAutoSignInTransitionLostCommitReplyUsesExactDurableReadback(t *testing.T) {
	f, run, _ := signinTransitionSetup(t)
	calls := 0
	next, err := f.o.transitionConfigCommit(context.Background(), run, func(tx *sql.Tx) error {
		calls++
		if err := tx.Commit(); err != nil {
			return err
		}
		return errors.New("owned synthetic lost SQL acknowledgment")
	})
	if err != nil || calls != 1 || next == nil {
		t.Fatal("committed transition was reported as unapplied", err, calls)
	}
	f.o = next
	if _, receipt, err := next.readArchived(context.Background(), 1); err != nil || receipt.RunID != run.ID {
		t.Fatal("exact transition evidence missing", err)
	}
	var archives int
	if err = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_archive_meta`).Scan(&archives); err != nil || archives != 1 {
		t.Fatal("transition replay duplicated archive", err, archives)
	}
}

func TestPluginAutoSignInTransitionSQLFailurePreservesOldNamespaceAndAllowsRetry(t *testing.T) {
	f, run, old := signinTransitionSetup(t)
	ctx := context.Background()
	if _, err := f.a.Store.DB.Exec(pluginAutoSignInTransitionSchema + `CREATE TRIGGER signin_archive_failure BEFORE INSERT ON plugin_signin_archive_rows BEGIN SELECT RAISE(ABORT,'owned namespace copy failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.o.transitionConfig(ctx, run); err == nil {
		t.Fatal("archive copy failure hidden")
	}
	var archived int
	if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_archive_meta`).Scan(&archived); err != nil || archived != 0 {
		t.Fatal("partial archive escaped rollback", err, archived)
	}
	tx, err := f.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := f.o.readTx(ctx, tx)
	_ = tx.Rollback()
	if err != nil || !reflect.DeepEqual(loaded.State, old) {
		t.Fatal("old source namespace changed after rollback", err)
	}
	if _, err = f.a.Store.DB.Exec(`DROP TRIGGER signin_archive_failure`); err != nil {
		t.Fatal(err)
	}
	next, err := f.o.transitionConfig(ctx, run)
	if err != nil {
		t.Fatal("known unapplied transition could not retry", err)
	}
	f.o = next
}

func TestPluginAutoSignInTransitionRequiresActualCoreDrainAndNativeRunProof(t *testing.T) {
	for _, which := range []string{"active-run", "unknown-network", "other-core-run", "timer", "event", "occurrence", "forged-input", "terminal-current", "uninstall"} {
		t.Run(which, func(t *testing.T) {
			f, run, _ := signinTransitionSetup(t)
			ctx := context.Background()
			switch which {
			case "active-run", "unknown-network":
				tx, err := f.a.Store.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				loaded, err := f.o.readTx(ctx, tx)
				if err != nil {
					t.Fatal(err)
				}
				s := loaded.State
				s.ActiveRun = f.run.ID
				s.Runs[f.run.ID].Phase = "interrupted"
				if which == "unknown-network" {
					s.Runs[f.run.ID].Lanes["site"] = []signinjournal.Entry{{Kind: "http", Digest: "original-request", Phase: "uncertain", Guard: "site-action"}}
					s.Blocks["site-action"] = f.run.ID
				}
				if err = f.o.writeTx(ctx, tx, loaded, s); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			case "other-core-run":
				if _, err := f.a.pluginStore.EnqueueRun(ctx, "AutoSignIn", "service", json.RawMessage(`{}`)); err != nil {
					t.Fatal(err)
				}
			case "timer":
				_, err := f.a.Store.DB.Exec(`INSERT INTO plugin_schedules VALUES('AutoSignIn','source-timer','source',1,2,x'01','original',1,NULL,'fixture')`)
				if err != nil {
					t.Fatal(err)
				}
			case "event":
				_, err := f.a.Store.DB.Exec(`INSERT INTO plugin_event_subscriptions VALUES('AutoSignIn','source-event',1,2,'[]',0,1,0)`)
				if err != nil {
					t.Fatal(err)
				}
			case "occurrence":
				_, err := f.a.Store.DB.Exec(`INSERT INTO plugin_schedules VALUES('AutoSignIn','source-timer','source',1,2,x'01','original',0,NULL,'fixture'); INSERT INTO plugin_schedule_occurrences VALUES('source-occurrence','AutoSignIn','source-timer',1,2,'2026-01-02','running',NULL,1000,0,'fixture')`)
				if err != nil {
					t.Fatal(err)
				}
			case "forged-input":
				run.Input = json.RawMessage(`{"action":"init","config":{}}`)
			case "terminal-current":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='succeeded' WHERE id=?`, run.ID)
			case "uninstall":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_installations SET installed=0 WHERE instance_id='AutoSignIn'`)
			}
			if _, err := f.o.transitionConfig(ctx, run); err == nil {
				t.Fatal("undrained or unauthorized config transition accepted", which)
			}
		})
	}
}

func TestPluginAutoSignInTransitionArchivedRowsAndConfigChainAreAuthenticated(t *testing.T) {
	for _, which := range []string{"row-deletion", "config-receipt", "archive-receipt", "meta-scope"} {
		t.Run(which, func(t *testing.T) {
			f, run, _ := signinTransitionSetup(t)
			next, err := f.o.transitionConfig(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			f.o = next
			query := map[string]string{"row-deletion": `DELETE FROM plugin_signin_archive_rows WHERE rowid=(SELECT MIN(rowid) FROM plugin_signin_archive_rows)`, "config-receipt": `UPDATE plugin_signin_archive_config SET sealed=x'01'`, "archive-receipt": `UPDATE plugin_signin_archive_meta SET receipt=x'01'`, "meta-scope": `UPDATE plugin_signin_archive_meta SET admission=99`}[which]
			if _, err = f.a.Store.DB.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, _, err = next.readArchived(context.Background(), 1); err == nil {
				t.Fatal("tampered historical source evidence was accepted", which)
			}
		})
	}
}

func TestPluginAutoSignInTransitionQuotaIncludesHistoricalMetadataAndConfigCiphertext(t *testing.T) {
	for _, table := range []string{"meta", "config"} {
		t.Run(table, func(t *testing.T) {
			f, run, _ := signinTransitionSetup(t)
			ctx := context.Background()
			if _, err := f.a.Store.DB.Exec(pluginAutoSignInTransitionSchema); err != nil {
				t.Fatal(err)
			}
			tx, err := f.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			current, total, err := f.o.transitionFootprintTx(ctx, tx)
			_ = tx.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			f.o.testCapacity = total + 2*current + 2*pluginValueLimit + 65536 + 4096
			if table == "meta" {
				_, err = f.a.Store.DB.Exec(`INSERT INTO plugin_signin_archive_meta VALUES('AutoSignIn',1,999,'owned-private-quota-fixture',zeroblob(131072),x'01',x'01')`)
			} else {
				_, err = f.a.Store.DB.Exec(`INSERT INTO plugin_signin_archive_config VALUES('AutoSignIn',1,999,1000,zeroblob(131072))`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.o.transitionConfig(ctx, run); !errors.Is(err, signinjournal.ErrCapacity) {
				t.Fatal("namespace admission ignored historical ciphertext table", table, err)
			}
			var count int
			if err = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_archive_meta WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("quota refusal copied a partial namespace", err, count)
			}
		})
	}
}
