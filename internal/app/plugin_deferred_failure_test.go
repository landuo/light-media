package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

func cleanDeferredFixture(t *testing.T) (*App, PluginInstallation, PluginRun, *pluginDeferredCall) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	manifest, _ := json.Marshal(cleaninvalidseed.Manifest())
	run, err := p.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: "CleanInvalidSeed", PluginID: "CleanInvalidSeed", Version: "2.1", ManifestSchemaVersion: 1, Manifest: manifest}, map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = p.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	in, err := p.Installation(context.Background(), run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Store: s, pluginStore: p}
	d, err := a.preparePluginDeferred(context.Background(), in, run)
	if err != nil || d == nil {
		t.Fatal("prepare terminal handshake", err)
	}
	return a, in, run, d
}

func TestPluginDeferredTerminalFailureRecoveryPreservesFailureAndRejectsRetry(t *testing.T) {
	a, in, run, d := cleanDeferredFixture(t)
	ctx := context.Background()
	if _, err := d.handshake(ctx, deferredIdentity(in), cleaninvalidseed.ConfigureCapability, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.changePhase(ctx, "prepared", "started", nil); err != nil {
		t.Fatal(err)
	}
	want := json.RawMessage(`{"accepted_notices":1,"detail":"synthetic-private-prefix"}`)
	if err := d.changeOutcome(ctx, "started", "terminal_failed", want, "CLEAN_SEED_SOURCE_FAILED"); err != nil {
		t.Fatal(err)
	}
	r, sealed, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if err != nil || r.Phase != "terminal_failed" || r.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" || !bytes.Equal(r.Result, want) || bytes.Contains(sealed, []byte("synthetic-private-prefix")) {
		t.Fatal("known failed receipt not preserved privately", err)
	}
	// A later reset/uninstall cannot turn a known old source failure into a
	// successful run or grant permission to replay the operation.
	if _, err = a.Store.DB.Exec(`UPDATE plugin_installations SET installed=0,generation=generation+1 WHERE instance_id=?`, run.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER synthetic_deferred_failure_block BEFORE INSERT ON plugin_run_startup_blocks BEGIN SELECT RAISE(ABORT,'synthetic marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.recoverDeferredRuns(ctx); err == nil {
		t.Fatal("startup block failure was ignored")
	}
	before, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || before.State != "reconcile_pending" {
		t.Fatal("terminal failure was published without its startup replay block", err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER synthetic_deferred_failure_block`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = a.pluginStore.recoverDeferredRuns(ctx); err != nil {
			t.Fatal(err)
		}
		got, e := a.pluginStore.Run(ctx, run.ID)
		if e != nil || got.State != "failed" || got.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" || !bytes.Equal(got.Result, want) {
			t.Fatal("failure became success/unknown or lost result", got.State, got.ErrorCode, e)
		}
	}
	if _, err = a.retryPluginDeferred(ctx, run.InstanceID, run.ID); err == nil {
		t.Fatal("known failed action was eligible for retry")
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_startup_blocks WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("confirmed failed startup could replay after recovery", count, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&count); err != nil || count != 1 {
		t.Fatal("recovery enqueued a replacement execution", count, err)
	}
}

func TestPluginDeferredFailureReceiptRejectsForeignPluginAndUntrustedErrorCode(t *testing.T) {
	a, _, run, d := cleanDeferredFixture(t)
	ctx := context.Background()
	if err := d.changePhase(ctx, "prepared", "started", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.changeOutcome(ctx, "started", "terminal_failed", json.RawMessage(`{}`), "password=synthetic-secret"); err == nil {
		t.Fatal("arbitrary error text became a durable public code")
	}
	r, sealed, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if err != nil || r.Phase != "started" || r.ErrorCode != "" {
		t.Fatal("refused outcome partially committed", err)
	}
	// Even a trusted local accidental write must not widen failure receipts to
	// unrelated plugins. Bypass writeDeferred only to exercise strict readback.
	r.Phase, r.ErrorCode, r.Result, r.Plugin = "terminal_failed", "CLEAN_SEED_HOST_DENIED", json.RawMessage(`{}`), "PluginUnInstall"
	raw, _ := json.Marshal(r)
	forged, err := a.pluginStore.seal("run_result", run.InstanceID, "deferred:v1:"+run.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_deferred_operations SET sealed=? WHERE run_id=? AND sealed=?`, forged, run.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.readDeferred(ctx, a.Store.DB, run); err == nil {
		t.Fatal("foreign plugin's forged terminal failure accepted")
	}
}

func TestPluginDeferredCleanSpecificationMatchesNativeSDKWithoutWorkerAuthority(t *testing.T) {
	in := PluginInstallation{PluginID: "CleanInvalidSeed", InstanceID: "owned-clean", Version: "2.1"}
	for _, action := range []string{"init", "reload", "run", "event", "service", "once", "command"} {
		t.Run(action, func(t *testing.T) {
			inv := plugins.Invocation{Action: action, Config: json.RawMessage(`{"arbitrary_worker_path":"/never-authorized"}`), Input: json.RawMessage(`{"event_id":"owned-event"}`)}
			raw, _ := json.Marshal(inv)
			run := PluginRun{Action: action, Input: raw}
			method, want, err := pluginDeferredSpec(in, run)
			if err != nil || method == "" {
				t.Fatal(err)
			}
			calls := 0
			_, err = cleaninvalidseed.Handle(context.Background(), "invoke", raw, func(_ context.Context, got string, payload json.RawMessage) (json.RawMessage, error) {
				calls++
				if got != method || !deferredPayloadEqual(payload, want) || bytes.Contains(payload, []byte("never-authorized")) {
					t.Fatal("native SDK terminal payload differs", got, string(payload))
				}
				return json.RawMessage(`{"deferred":"owned"}`), nil
			})
			if err != nil || calls != 1 {
				t.Fatal("native terminal sequence", calls, err)
			}
		})
	}
	for _, payload := range []string{`{}`, `{"event_id":""}`, `{"event_id":"one","path":"/foreign"}`, `{"event_id":"one"} {}`} {
		inv, _ := json.Marshal(plugins.Invocation{Action: "command", Input: json.RawMessage(payload)})
		// Malformed RawMessage cannot be marshaled; a missing invocation is
		// still refused instead of gaining a default cleanup capability.
		if _, _, err := pluginDeferredSpec(in, PluginRun{Action: "command", Input: inv}); err == nil {
			t.Fatal("unbound event accepted", payload)
		}
	}
}

func TestPluginDeferredCleanPrivateProofCannotHideUnknownEffects(t *testing.T) {
	a, _, run, d := cleanDeferredFixture(t)
	ctx := context.Background()
	inv, err := cleanSeedInvocation(run)
	if err != nil {
		t.Fatal(err)
	}
	j, err := newPluginCleanInvalidSeedJournal(ctx, a.pluginStore, run.InstanceID, run.Generation)
	if err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"prefix":"synthetic-private-known-failure"}`)
	if err = j.Update(ctx, func(_ *sql.Tx, s *seedhost.State) error {
		s.Binding = inv.Binding
		s.Runs[run.ID] = seedhost.Run{Invocation: inv, Effective: inv.Binding, Phase: "failed", ErrorCode: "SOURCE_FAILED", Result: result}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = d.changePhase(ctx, "prepared", "started", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"started", "unknown", "fs_confirmed", "confirmed"} {
		if err = j.Update(ctx, func(_ *sql.Tx, s *seedhost.State) error {
			s.Effects["owned-effect"] = seedhost.Effect{ID: "owned-effect", RunID: run.ID, Binding: inv.Binding, Authority: inv.Binding, Kind: "native_write", Phase: phase}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		// Merely setting phase=confirmed is also insufficient without the
		// source-specific Core receipt. Unknown facts are never erased here.
		if _, _, found, e := a.pluginStore.cleanInvalidSeedDeferredOutcome(ctx, run, cleaninvalidseed.ConfigureCapability); e != nil || found {
			t.Fatal("failure concealed an unresolved/unproved effect", phase, found, e)
		}
		if err = a.pluginStore.recoverDeferredRuns(ctx); err != nil {
			t.Fatal(err)
		}
		got, e := a.pluginStore.Run(ctx, run.ID)
		if e != nil || got.State != "reconcile_pending" {
			t.Fatal("unproved operation became terminal", phase, got.State, e)
		}
	}
	// Independent fixture: a source failure before any effect is a known
	// failure. This edit is native test setup, never a recovery/worker API.
	if err = j.Update(ctx, func(_ *sql.Tx, s *seedhost.State) error {
		delete(s.Effects, "owned-effect")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, code, found, e := a.pluginStore.cleanInvalidSeedDeferredOutcome(ctx, run, cleaninvalidseed.ConfigureCapability); e != nil || !found || code != "CLEAN_SEED_SOURCE_FAILED" || !bytes.Equal(got, result) {
		t.Fatal("source's known pre-effect failure not proved", code, found, e)
	}
	if err = a.pluginStore.recoverDeferredRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || got.State != "failed" || got.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" || !bytes.Equal(got.Result, result) {
		t.Fatal("source failure lost at deferred recovery", got.State, err)
	}
}
