package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iyuu "light-media/internal/plugins/iyuuautoseed"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

func iyuuLifecycleFixture(t *testing.T) (*App, PluginInstallation) {
	t.Helper()
	a := newTestApp(t)
	uninstallInstall(t, a, "OwnedIYUULifecycle", iyuu.Manifest(), map[string]any{"enabled": false, "token": "synthetic-token"}, false)
	in, err := a.pluginStore.Installation(context.Background(), "OwnedIYUULifecycle")
	if err != nil {
		t.Fatal(err)
	}
	return a, in
}
func iyuuLifecyclePrepared(t *testing.T, a *App, in PluginInstallation, operation string) *pluginIYUUPrepared {
	t.Helper()
	p, err := a.prepareIYUUTransition(context.Background(), in, operation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.release)
	return p
}
func iyuuLifecycleOwner(t *testing.T, a *App, instance string) *pluginIYUUOwner {
	t.Helper()
	a.pluginIYUU.mu.Lock()
	defer a.pluginIYUU.mu.Unlock()
	return a.pluginIYUU.owners[instance]
}

// Apply exactly the Root generation/installation changes. The production bulk
// adapter owns its other uninstall changes and calls publishTx in that same TX.
func iyuuLifecycleChangeTx(t *testing.T, p *pluginIYUUPrepared, tx *sql.Tx) {
	t.Helper()
	parent, err := p.a.pluginStore.iyuuTransitionParentTx(context.Background(), tx, p.from.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	installed := p.operation == "reset"
	result, err := tx.Exec(`UPDATE plugin_installations SET installed=?,desired_enabled=0,generation=generation+1 WHERE instance_id=? AND plugin_id='IYUUAutoSeed' AND version='2.19' AND installed=1 AND generation=?`, installed, p.from.InstanceID, p.from.Generation)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatalf("Root generation update %d %v", count, err)
	}
	if installed {
		if _, err = tx.Exec(`DELETE FROM plugin_values WHERE instance_id=?`, p.from.InstanceID); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.a.pluginStore.iyuuGenesisTx(context.Background(), tx, p.from.InstanceID, p.operation, parent); err != nil {
		t.Fatal(err)
	}
}
func iyuuLifecyclePublish(t *testing.T, p *pluginIYUUPrepared, commit bool) {
	t.Helper()
	tx, err := p.a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	iyuuLifecycleChangeTx(t, p, tx)
	if err = p.publishTx(context.Background(), tx, p.target(), ""); err != nil {
		t.Fatal(err)
	}
	if err = p.publishTx(context.Background(), tx, p.target(), ""); err != nil {
		t.Fatal("same-TX publication replay", err)
	}
	if commit {
		err = tx.Commit()
	} else {
		err = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestIYUULifecycleRetirementSharesRootTransaction(t *testing.T) {
	for _, operation := range []string{"uninstall", "reset"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-commit-%v", operation, commit), func(t *testing.T) {
				a, in := iyuuLifecycleFixture(t)
				if _, err := a.pluginStore.SaveData(context.Background(), in.InstanceID, "history", 0, []string{"synthetic-history"}); err != nil {
					t.Fatal(err)
				}
				p := iyuuLifecyclePrepared(t, a, in, operation)
				if p.from != (cleanuphost.Binding{InstanceID: in.InstanceID, Generation: in.Generation, Revision: 1}) {
					t.Fatal(p.from)
				}
				if err := p.drain(context.Background()); err != nil {
					t.Fatal(err)
				}
				iyuuLifecyclePublish(t, p, commit)
				if err := p.resolve(context.Background(), commit); err != nil {
					t.Fatal(err)
				}
				got, err := a.pluginStore.Installation(context.Background(), in.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					if got.Generation != in.Generation+1 || got.Installed != (operation == "reset") || got.DesiredEnabled {
						t.Fatal("Root target scope", got)
					}
					if owner := iyuuLifecycleOwner(t, a, in.InstanceID); owner != nil {
						t.Fatal("old owner was not retired")
					}
					if err = p.resolve(context.Background(), true); err != nil {
						t.Fatal("resolution replay", err)
					}
				} else {
					if got.Generation != in.Generation || !got.Installed || iyuuLifecycleOwner(t, a, in.InstanceID) != p.owner {
						t.Fatal("rollback retired original owner", got)
					}
					var count int
					if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_iyuu_retirements WHERE instance_id=?`, in.InstanceID).Scan(&count); err != nil || count != 0 {
						t.Fatalf("partial retirement fact %d %v", count, err)
					}
				}
				if !commit || operation == "uninstall" {
					data, err := a.pluginStore.Data(context.Background(), in.InstanceID, "history")
					if err != nil || string(data.Value) != `["synthetic-history"]` {
						t.Fatalf("uninstall/rollback deleted history %s %v", data.Value, err)
					}
				}
				p.release()
				if len(p.owner.gate) != 0 {
					t.Fatal("retired owner gate remained held")
				}
			})
		}
	}
}

func TestIYUULifecycleWaitDoesNotHoldGlobalLocksAndCancellationKeepsGate(t *testing.T) {
	a, in := iyuuLifecycleFixture(t)
	owner, err := a.iyuuOwner(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			owner.unlock()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan error, 1)
	go func() {
		p, err := a.prepareIYUUTransition(ctx, in, "uninstall")
		if p != nil {
			p.release()
		}
		waiting <- err
	}()
	select {
	case err = <-waiting:
		t.Fatalf("preparation passed occupied owner gate: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	globals := make(chan struct{})
	go func() {
		a.pluginLifecycleMu.Lock()
		a.pluginMu.Lock()
		a.pluginMu.Unlock()
		a.pluginLifecycleMu.Unlock()
		close(globals)
	}()
	select {
	case <-globals:
	case <-time.After(time.Second):
		t.Fatal("preparation held global locks while waiting for owner")
	}
	cancel()
	select {
	case err = <-waiting:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("owner wait did not honor cancellation")
	}
	if len(owner.gate) != 1 {
		t.Fatal("canceled waiter released another execution's gate")
	}
	owner.unlock()
	locked = false
	p := iyuuLifecyclePrepared(t, a, in, "uninstall")
	if err = p.resolve(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestIYUULifecycleReleaseExactlyOnce(t *testing.T) {
	a, in := iyuuLifecycleFixture(t)
	p := iyuuLifecyclePrepared(t, a, in, "uninstall")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.release() }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("duplicate release blocked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.owner.lock(ctx); err != nil {
		t.Fatal("owner gate leaked", err)
	}
	defer p.owner.unlock()
	p.release()
	if len(p.owner.gate) != 1 {
		t.Fatal("repeat release unlocked a new holder")
	}
	if err := p.drain(context.Background()); err == nil {
		t.Fatal("released preparation regained permission")
	}
}

func TestIYUULifecycleRejectsStaleAndUnsupportedScope(t *testing.T) {
	t.Run("unsupported-operation", func(t *testing.T) {
		a, in := iyuuLifecycleFixture(t)
		for _, operation := range []string{"init", "reload", "stop", ""} {
			if p, err := a.prepareIYUUTransition(context.Background(), in, operation); err == nil || p != nil {
				t.Fatalf("accepted %q", operation)
			}
		}
	})
	for _, change := range []string{"plugin", "version", "generation", "uninstalled"} {
		t.Run(change, func(t *testing.T) {
			a, in := iyuuLifecycleFixture(t)
			forged := in
			switch change {
			case "plugin":
				forged.PluginID = "RemoveLink"
			case "version":
				forged.Version = "2.18"
			case "generation":
				forged.Generation++
			case "uninstalled":
				forged.Installed = false
			}
			if p, err := a.prepareIYUUTransition(context.Background(), forged, "uninstall"); err == nil || p != nil {
				t.Fatal("accepted stale installation")
			}
		})
	}
	t.Run("config-changed-after-prepare", func(t *testing.T) {
		a, in := iyuuLifecycleFixture(t)
		p := iyuuLifecyclePrepared(t, a, in, "uninstall")
		if _, err := a.pluginStore.SaveConfig(context.Background(), in.InstanceID, 1, map[string]any{"enabled": false, "changed": true}); err != nil {
			t.Fatal(err)
		}
		if err := p.drain(context.Background()); err == nil {
			t.Fatal("accepted changed config revision")
		}
		if err := p.resolve(context.Background(), false); err == nil {
			t.Fatal("claimed original abort after foreign config change")
		}
	})
	t.Run("generation-changed-while-waiting", func(t *testing.T) {
		a, in := iyuuLifecycleFixture(t)
		owner, err := a.iyuuOwner(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if err = owner.lock(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			p, err := a.prepareIYUUTransition(ctx, in, "uninstall")
			if p != nil {
				p.release()
			}
			done <- err
		}()
		if _, err = a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, in.InstanceID); err != nil {
			owner.unlock()
			t.Fatal(err)
		}
		owner.unlock()
		if err = <-done; err == nil {
			t.Fatal("accepted stale generation after waiting")
		}
		if len(owner.gate) != 0 {
			t.Fatal("failed preparation leaked acquired gate")
		}
	})
}

// Unit admission uses the actual Root run/deferred storage producers; the
// separate SDK tests exercise worker authentication and the full source body.
func iyuuJournalUnitAdmission(t *testing.T, a *App, in PluginInstallation) (context.Context, iyuu.RunIdentity) {
	t.Helper()
	ctx := context.Background()
	queued, e := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.claimRunForInstance(ctx, queued.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	method, payload, e := iyuuDeferredSpec(run)
	if e != nil {
		t.Fatal(e)
	}
	d := pluginDeferredRecord{Schema: 1, Plugin: in.PluginID, Instance: in.InstanceID, Version: in.Version, Run: run.ID, Action: run.Action, Method: method, Generation: run.Generation, Revision: run.ConfigRevision, InputSHA: deferredInputSHA(run.Input), Raw: payload, Ack: json.RawMessage(`{}`), Phase: "started"}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = a.pluginStore.writeDeferred(ctx, tx, d, nil); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	ctx = context.WithValue(ctx, iyuuAdmissionKey{}, iyuuAdmissionFromRun(run, method))
	if e = a.pluginStore.iyuuAcceptRun(ctx, run); e != nil {
		t.Fatal(e)
	}
	return ctx, iyuu.RunIdentity{Instance: run.InstanceID, Generation: uint64(run.Generation), Revision: uint64(d.Revision), Fingerprint: d.InputSHA, Run: run.ID}
}

func TestIYUULifecycleAuthenticatedUnknownBlocksRetirement(t *testing.T) {
	for _, deletedIntent := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted-intent-%v", deletedIntent), func(t *testing.T) {
			a, in := iyuuLifecycleFixture(t)
			ctx := context.Background()
			owner, err := a.iyuuOwner(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
			}))
			defer server.Close()
			ctx, identity := iyuuJournalUnitAdmission(t, a, in)
			jr, err := owner.journal.Begin(ctx, identity, nil, func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			transport, err := iyuu.NewAPITransport(iyuu.TrustedAPIBinding{Token: "synthetic"}, iyuu.APITransportOptions{Guard: func(ctx context.Context) error { return ctx.Err() }, LoopbackOrigin: server.URL, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()
			durable, err := iyuu.NewDurableAPI(ctx, jr, transport)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = durable.Call(ctx, iyuu.APIRequest{Operation: iyuu.ReportExisting, Params: map[string]any{"sid_list": []any{1}}}); !errors.Is(err, iyuu.ErrUnconfirmed) {
				t.Fatal("failed to create real unknown receipt", err)
			}
			if calls.Load() != 1 {
				t.Fatal("HTTP fixture mutation repeated", calls.Load())
			}
			if deletedIntent {
				receipt := jr.ReceiptID()
				runKey := receipt[:strings.LastIndex(receipt, "/")]
				if _, err = a.Store.DB.Exec(`DELETE FROM iyuu_steps WHERE run=?`, runKey); err != nil {
					t.Fatal(err)
				}
			}
			p := iyuuLifecyclePrepared(t, a, in, "uninstall")
			err = p.drain(ctx)
			if deletedIntent {
				if !errors.Is(err, iyuu.ErrJournalIntegrity) {
					t.Fatal("missing mutation was treated as safe", err)
				}
			} else if !errors.Is(err, iyuu.ErrUnconfirmed) {
				t.Fatal("older-run unknown barrier lost", err)
			}
			if err = p.resolve(ctx, false); err != nil {
				t.Fatal("safe abort could not retain original scope", err)
			}
			got, err := a.pluginStore.Installation(ctx, in.InstanceID)
			if err != nil || !got.Installed || got.Generation != in.Generation || iyuuLifecycleOwner(t, a, in.InstanceID) != owner {
				t.Fatal("blocked drain changed Root scope", got, err)
			}
			if calls.Load() != 1 {
				t.Fatal("drain replayed external IO")
			}
		})
	}
}

func TestIYUULifecyclePublicationNeedsDrainedExactRootTarget(t *testing.T) {
	for _, invalid := range []string{"not-drained", "wrong-instance", "wrong-generation", "wrong-revision", "run-id", "still-installed", "still-enabled", "config-changed"} {
		t.Run(invalid, func(t *testing.T) {
			a, in := iyuuLifecycleFixture(t)
			p := iyuuLifecyclePrepared(t, a, in, "uninstall")
			if invalid != "not-drained" {
				if err := p.drain(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := a.Store.DB.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			iyuuLifecycleChangeTx(t, p, tx)
			to, runID := p.target(), ""
			switch invalid {
			case "wrong-instance":
				to.InstanceID = "other"
			case "wrong-generation":
				to.Generation++
			case "wrong-revision":
				to.Revision = 1
			case "run-id":
				runID = "forged-target-run"
			case "still-installed":
				_, err = tx.Exec(`UPDATE plugin_installations SET installed=1 WHERE instance_id=?`, in.InstanceID)
			case "still-enabled":
				_, err = tx.Exec(`UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id=?`, in.InstanceID)
			case "config-changed":
				_, err = tx.Exec(`UPDATE plugin_values SET revision=revision+1 WHERE instance_id=? AND kind='config'`, in.InstanceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = p.publishTx(context.Background(), tx, to, runID); err == nil {
				t.Fatal("accepted invalid retirement publication")
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = p.resolve(context.Background(), false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIYUULifecyclePublicationUsesExistingTransactionUnderGlobalLocks(t *testing.T) {
	a, in := iyuuLifecycleFixture(t)
	p := iyuuLifecyclePrepared(t, a, in, "uninstall")
	if err := p.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.pluginLifecycleMu.Lock()
	a.pluginMu.Lock()
	locked := true
	defer func() {
		if locked {
			a.pluginMu.Unlock()
			a.pluginLifecycleMu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	iyuuLifecycleChangeTx(t, p, tx)
	done := make(chan error, 1)
	go func() { done <- p.publishTx(ctx, tx, p.target(), "") }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		_ = tx.Rollback()
		t.Fatal("publication waited for a global mutex or DB-pool connection")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a.pluginMu.Unlock()
	a.pluginLifecycleMu.Unlock()
	locked = false
	if err = p.resolve(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}

func TestIYUULifecycleResolutionRequiresSealedCommittedFact(t *testing.T) {
	for _, damage := range []string{"missing", "ciphertext", "token", "target", "claim-abort"} {
		t.Run(damage, func(t *testing.T) {
			a, in := iyuuLifecycleFixture(t)
			p := iyuuLifecyclePrepared(t, a, in, "uninstall")
			if err := p.drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			iyuuLifecyclePublish(t, p, true)
			var err error
			switch damage {
			case "missing":
				_, err = a.Store.DB.Exec(`DELETE FROM plugin_iyuu_retirements WHERE instance_id=?`, in.InstanceID)
			case "ciphertext":
				_, err = a.Store.DB.Exec(`UPDATE plugin_iyuu_retirements SET sealed=X'00' WHERE instance_id=?`, in.InstanceID)
			case "token":
				_, err = a.Store.DB.Exec(`UPDATE plugin_iyuu_retirements SET token='forged' WHERE instance_id=?`, in.InstanceID)
			case "target":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, in.InstanceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = p.resolve(context.Background(), damage != "claim-abort"); err == nil {
				t.Fatal("accepted unconfirmed/changed retirement")
			}
			if iyuuLifecycleOwner(t, a, in.InstanceID) != p.owner {
				t.Fatal("evicted owner without committed proof")
			}
		})
	}
	t.Run("claim-commit-before-publish", func(t *testing.T) {
		a, in := iyuuLifecycleFixture(t)
		p := iyuuLifecyclePrepared(t, a, in, "uninstall")
		if err := p.drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := p.resolve(context.Background(), true); err == nil {
			t.Fatal("retired without publication")
		}
		if iyuuLifecycleOwner(t, a, in.InstanceID) != p.owner {
			t.Fatal("evicted original owner")
		}
	})
	t.Run("do-not-evict-new-owner", func(t *testing.T) {
		a, in := iyuuLifecycleFixture(t)
		p := iyuuLifecyclePrepared(t, a, in, "uninstall")
		if err := p.drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		iyuuLifecyclePublish(t, p, true)
		newOwner := &pluginIYUUOwner{gate: make(chan struct{}, 1), generation: in.Generation + 1, journal: p.owner.journal}
		a.pluginIYUU.mu.Lock()
		a.pluginIYUU.owners[in.InstanceID] = newOwner
		a.pluginIYUU.mu.Unlock()
		if err := p.resolve(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		if iyuuLifecycleOwner(t, a, in.InstanceID) != newOwner {
			t.Fatal("old resolution evicted new owner")
		}
	})
}

func TestIYUUActualOwnerNamespaceLossCannotRetireUnknown(t *testing.T) {
	a, in := iyuuLifecycleFixture(t)
	ctx := context.Background()
	owner, e := a.iyuuOwner(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		c, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		c.Close()
	}))
	defer server.Close()
	ctx, identity := iyuuJournalUnitAdmission(t, a, in)
	jr, e := owner.journal.Begin(ctx, identity, nil, func(ctx context.Context) error { return ctx.Err() })
	if e != nil {
		t.Fatal(e)
	}
	transport, e := iyuu.NewAPITransport(iyuu.TrustedAPIBinding{Token: "synthetic"}, iyuu.APITransportOptions{Guard: func(ctx context.Context) error { return ctx.Err() }, LoopbackOrigin: server.URL, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	defer transport.Close()
	durable, e := iyuu.NewDurableAPI(ctx, jr, transport)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = durable.Call(ctx, iyuu.APIRequest{Operation: iyuu.ReportExisting, Params: map[string]any{"sid_list": []any{1}}}); !errors.Is(e, iyuu.ErrUnconfirmed) {
		t.Fatal("expected real owned HTTP unknown", e)
	}
	// These are only the isolated test App's IYUU tables. Root installation,
	// config, runs and the native owner key are preserved. This is not a full DB rollback.
	for _, table := range []string{"iyuu_steps", "iyuu_runs", "iyuu_values", "iyuu_run_manifests", "iyuu_owner_manifests", "iyuu_registry"} {
		if _, e = a.Store.DB.Exec("DELETE FROM " + table); e != nil {
			t.Fatal(e)
		}
	}
	a.pluginIYUU.mu.Lock()
	delete(a.pluginIYUU.owners, in.InstanceID)
	a.pluginIYUU.mu.Unlock()
	p, e := a.prepareIYUUTransition(ctx, in, "uninstall")
	if p != nil {
		defer p.release()
	}
	if e == nil {
		e = p.drain(ctx)
	}
	if !errors.Is(e, iyuu.ErrJournalIntegrity) {
		t.Fatalf("ordinary missing journal namespace must fail integrity before retirement; got %v", e)
	}
	if calls.Load() != 1 {
		t.Fatalf("unknown effect repeated %d", calls.Load())
	}
	current, e := a.pluginStore.Installation(ctx, in.InstanceID)
	if e != nil || !current.Installed || current.Generation != in.Generation {
		t.Fatal("Root installation changed", current, e)
	}
}
