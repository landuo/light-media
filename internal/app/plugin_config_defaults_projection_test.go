package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// The private mechanical-path fixtures intentionally use actual compiled pure
// implementations. No production manifest is relaxed or forged to claim an
// owned projector is already integrated. The public declaration gate is tested
// separately; a real transactional consumer requires its own integration gates.
func TestPluginNormalizeProjectionAtomicScopedSequence(t *testing.T) {
	for _, m := range []plugins.Manifest{builtin.SystemProcessManifest(), builtin.CategoryEditorManifest()} {
		t.Run(m.PluginID, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			installCloneSource(t, p, m)
			if _, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": false}); err != nil {
				t.Fatal(err)
			}
			clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "projection"})
			if err != nil {
				t.Fatal(err)
			}
			source, err := p.Config(ctx, m.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			callerManifest := append([]byte(nil), clone.Manifest...)
			calls := 0
			projector := func(ctx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
				calls++
				if _, ok := tx.(interface{ Commit() error }); ok {
					t.Fatal("raw transaction escaped")
				}
				if _, ok := tx.(interface{ Rollback() error }); ok {
					t.Fatal("rollback authority escaped")
				}
				if change.Installation.InstanceID != clone.InstanceID || change.Installation.DesiredEnabled || !change.Installation.Installed || change.Installation.Generation != clone.Generation {
					t.Fatal("wrong current identity", change.Installation)
				}
				if change.Next.Revision != change.Previous.Revision+1 || !json.Valid(change.Next.Value) {
					t.Fatal("invalid transition")
				}
				previous, e := tx.Data(ctx, "projection-state")
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
				_, e = tx.SaveData(ctx, "projection-state", previous.Revision, map[string]any{"revision": change.Next.Revision, "config": change.Next.Value})
				if e != nil {
					return e
				}
				change.Previous.Value[0] = '!'
				change.Next.Value[0] = '!'
				change.Installation.Manifest[0] = '!'
				return nil
			}
			revision := int64(1)
			for _, key := range []string{"channel_default", "channel_strategy"} {
				// Stale, non-authoritative caller display/state fields must not reach hook.
				snapshot := clone
				snapshot.DesiredEnabled = true
				snapshot.Installed = false
				v, e := p.normalizeInstanceDefaults(ctx, snapshot, revision, map[string]json.RawMessage{key: json.RawMessage(`false`)}, plugins.ConfigDefaultsPure, projector)
				if e != nil || v.Revision != revision+1 || !json.Valid(v.Value) {
					t.Fatal(v, e)
				}
				revision = v.Revision
			}
			if calls != 2 || !bytes.Equal(callerManifest, clone.Manifest) {
				t.Fatal("hook count/caller mutation", calls)
			}
			current, e := p.Config(ctx, clone.InstanceID)
			if e != nil || current.Revision != 3 || !json.Valid(current.Value) {
				t.Fatal(current, e)
			}
			after, e := p.Config(ctx, m.PluginID)
			if e != nil || !bytes.Equal(after.Value, source.Value) || after.Revision != source.Revision {
				t.Fatal("source config changed", e)
			}
			if _, e = p.Data(ctx, m.PluginID, "projection-state"); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("state namespace leaked", e)
			}
			state, e := p.Data(ctx, clone.InstanceID, "projection-state")
			if e != nil || state.Revision != 2 || !json.Valid(state.Value) {
				t.Fatal(state, e)
			}
			installation, e := p.Installation(ctx, clone.InstanceID)
			if e != nil || installation.DesiredEnabled || installation.Generation != clone.Generation {
				t.Fatal("projection changed lifecycle", e)
			}
			var n int
			if e = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, clone.InstanceID).Scan(&n); e != nil || n != 0 {
				t.Fatal("projection queued work", n, e)
			}
		})
	}
}

func TestPluginNormalizeProjectionRollbackOnCallbackOrPropagation(t *testing.T) {
	for _, stage := range []string{"callback", "propagation"} {
		t.Run(stage, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			before, run, e := p.SaveConfigAndQueue(ctx, m.PluginID, 0, map[string]any{"enable": false})
			if e != nil {
				t.Fatal(e)
			}
			in, e := p.Installation(ctx, m.PluginID)
			if e != nil {
				t.Fatal(e)
			}
			_, sealedBefore := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			stateBefore, e := p.SaveData(ctx, in.InstanceID, "projection-state", 0, map[string]any{"original": true})
			if e != nil {
				t.Fatal(e)
			}
			if stage == "propagation" {
				_, e = p.store.DB.Exec(`CREATE TRIGGER reject_projection_propagation BEFORE UPDATE OF config_revision ON plugin_runs BEGIN SELECT RAISE(ABORT,'owned projection propagation rejection'); END`)
				if e != nil {
					t.Fatal(e)
				}
			}
			sentinel := errors.New("owned projector rejected state")
			calls := 0
			_, e = p.normalizeInstanceDefaults(ctx, in, before.Revision, map[string]json.RawMessage{"extra": json.RawMessage(`7`)}, plugins.ConfigDefaultsPure, func(ctx context.Context, tx PluginDefaultsTransaction, c PluginDefaultsChange) error {
				calls++
				if _, err := tx.SaveData(ctx, "projection-state", stateBefore.Revision, map[string]any{"revision": c.Next.Revision}); err != nil {
					return err
				}
				if stage == "callback" {
					return sentinel
				}
				return nil
			})
			if e == nil || calls != 1 || (stage == "callback" && !errors.Is(e, sentinel)) {
				t.Fatal("missing rollback failure", calls, e)
			}
			after, e := p.Config(ctx, in.InstanceID)
			if e != nil || after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) {
				t.Fatal("config not rolled back", e)
			}
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealedBefore)
			stateAfter, e := p.Data(ctx, in.InstanceID, "projection-state")
			if e != nil || stateAfter.Revision != stateBefore.Revision || !bytes.Equal(stateAfter.Value, stateBefore.Value) {
				t.Fatal("state not rolled back", e)
			}
			saved, e := p.Run(ctx, run.ID)
			if e != nil || saved.ConfigRevision != run.ConfigRevision || !bytes.Equal(saved.Input, run.Input) {
				t.Fatal("pending run not rolled back", e)
			}
			var edges int
			if e = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=?`, run.ID).Scan(&edges); e != nil || edges != 0 {
				t.Fatal("proof committed on failure", edges, e)
			}
		})
	}
}

func TestPluginNormalizeProjectionPublicDeclarationAndNoop(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	before, e := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enable": false, "present": nil})
	if e != nil {
		t.Fatal(e)
	}
	in, e := p.Installation(ctx, m.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	hook := func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error { calls++; return nil }
	patch := map[string]json.RawMessage{"missing": json.RawMessage(`1`)}
	if _, e = p.NormalizeInstanceDefaultsWithProjection(ctx, in, before.Revision, patch, hook); pluginErrorCode(e) != "PLUGIN_DEFAULTS_UNSUPPORTED" {
		t.Fatal("pure manifest authorized transactional writes", e)
	}
	if _, e = p.NormalizeInstanceDefaultsWithProjection(ctx, in, before.Revision, patch, nil); pluginErrorCode(e) != "PLUGIN_DEFAULTS_UNSUPPORTED" {
		t.Fatal("nil hook accepted", e)
	}
	if _, e = p.NormalizeInstanceDefaultsWithProjection(ctx, in, before.Revision+1, patch, hook); pluginErrorCode(e) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("CAS lost", e)
	}
	// A public no-op remains read-only, including explicit null, without claiming
	// transactional authority or calling a business state validator/repair hook.
	v, e := p.NormalizeInstanceDefaultsWithProjection(ctx, in, before.Revision, map[string]json.RawMessage{"present": json.RawMessage(`2`)}, hook)
	if e != nil || v.Revision != before.Revision || calls != 0 || !bytes.Equal(v.Value, before.Value) {
		t.Fatal("noop invoked hook or wrote", v, calls, e)
	}
	_, _, e = p.SaveConfigAndQueue(ctx, in.InstanceID, before.Revision, map[string]any{"enable": false})
	if e != nil {
		t.Fatal(e)
	}
	run, e := p.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.normalizeInstanceDefaults(ctx, in, run.ConfigRevision, patch, plugins.ConfigDefaultsPure, hook); pluginErrorCode(e) != "PLUGIN_BUSY" || calls != 0 {
		t.Fatal("busy hook invoked", calls, e)
	}
}

func TestPluginNormalizeProjectionStoredManifestCannotGrantContract(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m, _, ok := builtin.Lookup("AutoSignIn")
	if !ok {
		t.Fatal("missing implementation")
	}
	m.ConfigDefaults = plugins.ConfigDefaultsTransactional
	installCloneSource(t, p, m)
	clone, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "projected"})
	if e != nil {
		t.Fatal(e)
	}
	before, sealed := pluginDefaultsStoredConfig(t, p, clone.InstanceID)
	calls := 0
	_, e = p.NormalizeInstanceDefaultsWithProjection(ctx, clone, before.Revision, map[string]json.RawMessage{"missing": json.RawMessage(`false`)}, func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error { calls++; return nil })
	if pluginErrorCode(e) != "PLUGIN_DEFAULTS_UNSUPPORTED" || calls != 0 {
		t.Fatal("stored declaration authorized hook", calls, e)
	}
	assertPluginDefaultsConfigUnchanged(t, p, clone.InstanceID, before, sealed)
}
