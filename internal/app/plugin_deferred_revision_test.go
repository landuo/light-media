package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins/uninstall"
)

func deferredRevisionFixture(t *testing.T) (*App, *pluginDeferredCall, PluginRun, context.Context) {
	t.Helper()
	a, in, run := deferredFixture(t)
	d, err := a.preparePluginDeferred(context.Background(), in, run)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	if err = d.changePhase(context.Background(), "prepared", "started", nil); err != nil {
		t.Fatal(err)
	}
	return a, d, run, context.WithValue(context.Background(), pluginDeferredExecutionKey{}, d)
}

func TestPluginDeferredRevisionContinuesImmutableReceipt(t *testing.T) {
	a, d, run, ctx := deferredRevisionFixture(t)
	p := a.pluginStore
	original, sealed, err := p.readDeferred(ctx, a.Store.DB, run)
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"first_test_once", "second_test_once", "third_test_once"} {
		if err = a.requireDeferredExecution(ctx, &run, uninstall.ApplyCapability); err != nil {
			t.Fatal("before patch", i, err)
		}
		if err = p.patchRunConfig(ctx, &run, map[string]json.RawMessage{key: json.RawMessage(`false`)}); err != nil {
			t.Fatal(err)
		}
		if err = a.requireDeferredExecution(ctx, &run, uninstall.ApplyCapability); err != nil {
			t.Fatal("after patch", i, err)
		}
		receipt, after, e := p.readDeferred(ctx, a.Store.DB, run)
		if e != nil || !bytes.Equal(sealed, after) || receipt.Revision != original.Revision || receipt.InputSHA != original.InputSHA {
			t.Fatal("receipt mutated", e)
		}
	}
	if run.ConfigRevision != original.Revision+3 || d.run.ConfigRevision != original.Revision {
		t.Fatal("run revision ownership")
	}
	if err = d.changePhase(ctx, "started", "confirmed", json.RawMessage(`{"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	if err = a.requireDeferredExecution(ctx, &run, uninstall.ApplyCapability); err == nil {
		t.Fatal("confirmed operation reused")
	}
	deferredStillInstalled(t, a, true)
}

func TestPluginDeferredRevisionRejectsStaleProofAndIdentity(t *testing.T) {
	for _, mode := range []string{"missing-edge", "corrupt-edge", "stale-caller", "old-config", "rollback-revision", "wrong-instance", "wrong-generation", "wrong-version", "wrong-action", "changed-input", "wrong-method", "wrong-context", "disabled", "reset", "phase-prepared", "forged-anchor"} {
		t.Run(mode, func(t *testing.T) {
			a, d, run, ctx := deferredRevisionFixture(t)
			p := a.pluginStore
			old := run
			var oldSealed []byte
			if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, run.InstanceID).Scan(&oldSealed); err != nil {
				t.Fatal(err)
			}
			if err := p.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
				t.Fatal(err)
			}
			method := uninstall.ApplyCapability
			var err error
			switch mode {
			case "missing-edge":
				_, err = a.Store.DB.Exec(`DELETE FROM plugin_run_config_transitions WHERE run_id=?`, run.ID)
			case "corrupt-edge":
				_, err = a.Store.DB.Exec(`UPDATE plugin_run_config_transitions SET sealed=x'00' WHERE run_id=?`, run.ID)
			case "stale-caller":
				run = old
			case "old-config":
				_, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, oldSealed, run.InstanceID)
			case "rollback-revision":
				_, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=?,revision=? WHERE instance_id=? AND kind='config' AND item_key='main'`, oldSealed, old.ConfigRevision, run.InstanceID)
				if err == nil {
					_, err = a.Store.DB.Exec(`UPDATE plugin_runs SET config_revision=? WHERE id=?`, old.ConfigRevision, run.ID)
				}
				run = old
			case "wrong-instance":
				run.InstanceID = "CategoryEditor"
			case "wrong-generation":
				run.Generation++
			case "wrong-version":
				run.Version = "9.9.9"
			case "wrong-action":
				run.Action = "reload"
			case "changed-input":
				run.Input = json.RawMessage(`{}`)
			case "wrong-method":
				method = uninstall.OptionsCapability
			case "wrong-context":
				ctx = context.Background()
			case "forged-anchor":
				d.run = run
			case "disabled":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id=?`, run.InstanceID)
			case "reset":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, run.InstanceID)
			case "phase-prepared":
				r, sealed, e := p.readDeferred(ctx, a.Store.DB, d.run)
				if e != nil {
					t.Fatal(e)
				}
				r.Phase = "prepared"
				tx, e := a.Store.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				err = p.writeDeferred(ctx, tx, r, sealed)
				if err == nil {
					err = tx.Commit()
				} else {
					tx.Rollback()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = a.requireDeferredExecution(ctx, &run, method); err == nil {
				t.Fatal("invalid continuation accepted")
			}
			deferredStillInstalled(t, a, true)
		})
	}
}

func TestPluginDeferredRevisionPreparedCannotAdoptChain(t *testing.T) {
	a, in, run := deferredFixture(t)
	ctx := context.Background()
	d, err := a.preparePluginDeferred(ctx, in, run)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	if err = d.changePhase(ctx, "prepared", "started", nil); err == nil {
		t.Fatal("old prepared operation adopted new revision")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = a.pluginStore.validateDeferredRunTx(ctx, tx, d.record, run); err == nil {
		t.Fatal("prepared proof authorized new snapshot")
	}
}

func TestPluginDeferredRevisionAnchorsFirstConfigBytes(t *testing.T) {
	a, _, run, ctx := deferredRevisionFixture(t)
	replacement, err := a.pluginStore.seal("config", run.InstanceID, "main", []byte(`{"plugin_ids":[],"test_once":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, replacement, run.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	if err = a.requireDeferredExecution(ctx, &run, uninstall.ApplyCapability); err == nil {
		t.Fatal("replayed configuration adopted as original receipt anchor")
	}
	deferredStillInstalled(t, a, true)
}

func TestPluginDeferredRevisionLegacyReceiptStaysExact(t *testing.T) {
	a, d, run, ctx := deferredRevisionFixture(t)
	r, sealed, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if err != nil {
		t.Fatal(err)
	}
	r.ConfigSHA = ""
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.writeDeferred(ctx, tx, r, sealed); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	d.record.ConfigSHA = ""
	if err = a.requireDeferredExecution(ctx, &run, uninstall.ApplyCapability); err != nil {
		t.Fatal("legacy exact snapshot rejected", err)
	}
	if err = a.pluginStore.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	if err = a.requireDeferredExecution(ctx, &run, uninstall.ApplyCapability); err == nil {
		t.Fatal("legacy receipt gained unanchored continuation")
	}
	deferredStillInstalled(t, a, true)
}
