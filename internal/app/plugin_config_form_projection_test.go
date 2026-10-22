package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// No production adapter currently declares Form projection. These mechanical
// positive tests exercise the private common transaction using an explicitly
// known, real compiled pure/defaults manifest. They do not grant a Form contract
// to that manifest, override the catalog, or claim an integrated public adapter.
// The public API's closed declaration gate is tested independently below.
func pluginFormMechanicalSave(t *testing.T, p *PluginStore, ctx context.Context, snapshot PluginInstallation, revision int64, desired json.RawMessage, projector PluginDefaultsProjector) (PluginStoredValue, error) {
	t.Helper()
	manifest, _, known := builtin.Lookup(snapshot.PluginID)
	if !known || manifest.PluginID != "SystemProcess" || manifest.Version != snapshot.Version || manifest.ConfigDefaults != plugins.ConfigDefaultsPure || manifest.ConfigFormProjection != "" {
		t.Fatal("mechanical fixture must use the actual compiled SystemProcess manifest")
	}
	return p.transitionInstanceConfig(ctx, snapshot, revision, pluginConfigDefaultsPure, func(map[string]json.RawMessage) (any, bool) {
		return desired, true
	}, projector)
}

// This is a synthetic business journal, not the MergeMessageNotify adapter.
// Config and Stored intentionally diverge on init and on the final Form Save.
type pluginFormMechanicalJournal struct {
	Config   json.RawMessage `json:"config"`
	Stored   json.RawMessage `json:"stored"`
	Revision int64           `json:"revision"`
	Receipt  string          `json:"receipt"`
}

func TestPluginFormProjectionMechanicalReplacementSequenceAndIsolation(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	if _, err := p.SaveConfig(ctx, m.PluginID, 0, json.RawMessage(`{"enabled":false,"enable":false,"":17}`)); err != nil {
		t.Fatal(err)
	}
	source, sourceSealed := pluginDefaultsStoredConfig(t, p, m.PluginID)
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "form"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	journal := pluginFormMechanicalJournal{Stored: before.Value, Revision: before.Revision, Receipt: "immutable-original-init-receipt"}
	if _, err = p.SaveData(ctx, clone.InstanceID, "form-journal", 0, journal); err != nil {
		t.Fatal(err)
	}
	// Synthetic source sequence: first channel drops init-only flags and the
	// empty suffix key; later channels retain prior channels. Final strategy
	// affects Stored only. Names/counts do not claim business integration.
	fields := map[string]any{}
	revision := before.Revision
	calls := 0
	var configBeforeStrategy json.RawMessage
	for step := 0; step < 11; step++ {
		storedOnly := step >= 9
		if step < 9 {
			fields["channel_"+string(rune('a'+step))] = map[string]any{"enabled": false, "value": step}
		} else if step == 9 {
			fields["strategy"] = "synthetic-strategy"
		}
		desired, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if step == 9 {
			configBeforeStrategy = append(json.RawMessage(nil), journal.Config...)
		}
		// Step 10 is byte-equal: an actual source Save must still call the
		// projector and advance config plus journal revisions atomically.
		want := append(json.RawMessage(nil), desired...)
		caller := clone
		caller.DesiredEnabled, caller.Installed = true, false
		caller.Manifest = json.RawMessage(`{"caller":"not-authority"}`)
		value, err := pluginFormMechanicalSave(t, p, ctx, caller, revision, desired, func(ctx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
			calls++
			if _, ok := tx.(interface{ Commit() error }); ok {
				t.Fatal("raw commit escaped")
			}
			if _, ok := tx.(interface{ Rollback() error }); ok {
				t.Fatal("raw rollback escaped")
			}
			if change.Installation.InstanceID != clone.InstanceID || change.Installation.DesiredEnabled || !change.Installation.Installed || change.Installation.Generation != clone.Generation || change.Installation.PluginID != m.PluginID || change.Installation.Version != m.Version {
				t.Fatal("callback did not receive database-current instance")
			}
			var compiled plugins.Manifest
			if json.Unmarshal(change.Installation.Manifest, &compiled) != nil || compiled.PluginID != m.PluginID || compiled.Version != m.Version || compiled.ConfigDefaults != plugins.ConfigDefaultsPure || compiled.ConfigFormProjection != "" {
				t.Fatal("caller manifest reached projector")
			}
			state, e := tx.Data(ctx, "form-journal")
			if e != nil {
				return e
			}
			if json.Unmarshal(state.Value, &journal) != nil || journal.Revision != change.Previous.Revision || !bytes.Equal(journal.Stored, change.Previous.Value) || journal.Receipt != "immutable-original-init-receipt" {
				return errors.New("synthetic journal previous-image mismatch")
			}
			var headRevision int64
			var sealed []byte
			if e = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, clone.InstanceID).Scan(&headRevision, &sealed); e != nil {
				return e
			}
			head, e := p.unseal("config", clone.InstanceID, "main", sealed)
			if e != nil || headRevision != change.Next.Revision || !bytes.Equal(head, change.Next.Value) || change.Next.Revision != change.Previous.Revision+1 {
				t.Fatal("callback was not after the config write", e)
			}
			if !storedOnly {
				journal.Config = append(json.RawMessage(nil), change.Next.Value...)
			}
			journal.Stored, journal.Revision = append(json.RawMessage(nil), change.Next.Value...), change.Next.Revision
			if _, e = tx.SaveData(ctx, "form-journal", state.Revision, journal); e != nil {
				return e
			}
			// No aliases into caller, result, stored config/state, or later
			// propagation may be exposed to the projector.
			desired[0], change.Previous.Value[0], change.Next.Value[0], change.Installation.Manifest[0] = '!', '!', '!', '!'
			return nil
		})
		if err != nil || value.Revision != revision+1 || !bytes.Equal(value.Value, want) {
			t.Fatal("mechanical source Save failed", step, value, err)
		}
		revision = value.Revision
		if step == 0 {
			var got map[string]json.RawMessage
			if json.Unmarshal(value.Value, &got) != nil {
				t.Fatal("bad replacement")
			}
			for _, removed := range []string{"enabled", "enable", ""} {
				if _, exists := got[removed]; exists {
					t.Fatal("full replacement retained deleted field", removed)
				}
			}
		}
		value.Value[0] = '!'
		persisted, e := p.Config(ctx, clone.InstanceID)
		if e != nil || !bytes.Equal(persisted.Value, want) || persisted.Revision != revision {
			t.Fatal("returned value aliases persistence", e)
		}
	}
	if calls != 11 || !bytes.Equal(journal.Config, configBeforeStrategy) || bytes.Equal(journal.Config, journal.Stored) {
		t.Fatal("Config/Stored source distinction lost", calls)
	}
	state, err := p.Data(ctx, clone.InstanceID, "form-journal")
	if err != nil || state.Revision != 12 || json.Unmarshal(state.Value, &journal) != nil || journal.Receipt != "immutable-original-init-receipt" {
		t.Fatal("journal or original receipt lost", err)
	}
	assertPluginDefaultsConfigUnchanged(t, p, m.PluginID, source, sourceSealed)
	if _, err = p.Data(ctx, m.PluginID, "form-journal"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("journal crossed instances", err)
	}
	current, err := p.Installation(ctx, clone.InstanceID)
	if err != nil || current.DesiredEnabled || current.Generation != clone.Generation {
		t.Fatal("form transition changed lifecycle", err)
	}
	for _, table := range []string{"plugin_runs", "plugin_schedules", "plugin_event_subscriptions", "plugin_run_origins"} {
		var n int
		if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("form created work or authority", table, n, err)
		}
	}
}

func TestPluginFormProjectionPublicRequiresDedicatedCompiledContract(t *testing.T) {
	for _, m := range builtin.Catalog() {
		t.Run(m.PluginID, func(t *testing.T) {
			if m.ConfigFormProjection != "" {
				t.Fatal("a production form contract requires its own reviewed adapter/integration evidence")
			}
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			// Persisted declarations cannot enable Form projection, even when
			// both fields claim transactional support.
			m.ConfigDefaults, m.ConfigFormProjection = plugins.ConfigDefaultsTransactional, plugins.ConfigFormProjectionTransactional
			installCloneSource(t, p, m)
			clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "closed"})
			if err != nil {
				t.Fatal(err)
			}
			before, sealed := pluginDefaultsStoredConfig(t, p, clone.InstanceID)
			calls := 0
			hook := func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error { calls++; return nil }
			for _, desired := range []json.RawMessage{json.RawMessage(`{"new":false}`), before.Value} {
				if _, err = p.ProjectInstanceConfig(ctx, clone, before.Revision, desired, hook); pluginErrorCode(err) != "PLUGIN_FORM_PROJECTION_UNSUPPORTED" {
					t.Fatal("stored/defaults declaration or equal input authorized public Form Save", err)
				}
			}
			if calls != 0 {
				t.Fatal("undeclared callback was invoked")
			}
			assertPluginDefaultsConfigUnchanged(t, p, clone.InstanceID, before, sealed)
		})
	}
}

func TestPluginFormProjectionPublicValidationAndExactSnapshot(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	if _, err := p.SaveConfig(ctx, m.PluginID, 0, json.RawMessage(`{"enable":false}`)); err != nil {
		t.Fatal(err)
	}
	in, err := p.Installation(ctx, m.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	calls := 0
	hook := func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error { calls++; return nil }
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		code string
	}{
		{"invalid", json.RawMessage(`{"incomplete":`), "INVALID_PLUGIN_VALUE"},
		{"empty", json.RawMessage{}, "INVALID_PLUGIN_VALUE"},
		{"nil", nil, "INVALID_PLUGIN_CONFIG"},
		{"null", json.RawMessage(`null`), "INVALID_PLUGIN_CONFIG"},
		{"array", json.RawMessage(`[]`), "INVALID_PLUGIN_CONFIG"},
		{"boolean", json.RawMessage(`true`), "INVALID_PLUGIN_CONFIG"},
		{"string", json.RawMessage(`"text"`), "INVALID_PLUGIN_CONFIG"},
		{"number", json.RawMessage(`17`), "INVALID_PLUGIN_CONFIG"},
		{"oversized", json.RawMessage(`{"huge":"` + strings.Repeat("x", pluginValueLimit) + `"}`), "INVALID_PLUGIN_VALUE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := p.ProjectInstanceConfig(ctx, in, before.Revision, tc.raw, hook); pluginErrorCode(e) != tc.code {
				t.Fatal("bad input error", e)
			}
		})
	}
	if _, err = p.ProjectInstanceConfig(ctx, in, before.Revision, before.Value, nil); pluginErrorCode(err) != "PLUGIN_FORM_PROJECTION_UNSUPPORTED" {
		t.Fatal("nil trusted callback accepted", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*PluginInstallation, *int64)
		code string
	}{
		{"negative-revision", func(_ *PluginInstallation, r *int64) { *r = -1 }, "INVALID_PLUGIN_VALUE"},
		{"stale-revision", func(_ *PluginInstallation, r *int64) { *r++ }, "PLUGIN_REVISION_CONFLICT"},
		{"invalid-instance", func(i *PluginInstallation, _ *int64) { i.InstanceID = "../escape" }, "INVALID_PLUGIN_VALUE"},
		{"invalid-generation", func(i *PluginInstallation, _ *int64) { i.Generation = 0 }, "INVALID_PLUGIN_VALUE"},
		{"stale-generation", func(i *PluginInstallation, _ *int64) { i.Generation++ }, "PLUGIN_SNAPSHOT_STALE"},
		{"wrong-plugin", func(i *PluginInstallation, _ *int64) { i.PluginID = "CategoryEditor" }, "PLUGIN_SNAPSHOT_STALE"},
		{"wrong-version", func(i *PluginInstallation, _ *int64) { i.Version = "99.0" }, "PLUGIN_SNAPSHOT_STALE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, revision := in, before.Revision
			tc.edit(&snapshot, &revision)
			if _, e := p.ProjectInstanceConfig(ctx, snapshot, revision, before.Value, hook); pluginErrorCode(e) != tc.code {
				t.Fatal("snapshot gate lost", e)
			}
		})
	}
	if calls != 0 {
		t.Fatal("rejected input reached callback")
	}
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
	if err = p.Reset(ctx, in.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProjectInstanceConfig(ctx, in, 0, json.RawMessage(`{}`), hook); pluginErrorCode(err) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("stale Form resurrected reset config", err)
	}
}

func TestPluginFormProjectionMechanicalEmptyReplacementAndMissingConfig(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	in, err := p.Installation(ctx, m.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	revision := int64(0)
	for _, raw := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"enable":false,"":true}`), json.RawMessage(`{}`), json.RawMessage(`{}`)} {
		value, e := pluginFormMechanicalSave(t, p, ctx, in, revision, raw, func(_ context.Context, _ PluginDefaultsTransaction, c PluginDefaultsChange) error {
			calls++
			if c.Previous.Revision != revision || c.Next.Revision != revision+1 || (revision == 0 && string(c.Previous.Value) != `{}`) {
				t.Fatal("missing config transition mismatch")
			}
			return nil
		})
		if e != nil || value.Revision != revision+1 || !bytes.Equal(value.Value, raw) {
			t.Fatal(value, e)
		}
		revision = value.Revision
	}
	if calls != 4 {
		t.Fatal("empty/equal source save was elided", calls)
	}
}

func TestPluginFormProjectionMechanicalPendingOriginChain(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	// This supplies the private marker only as a synthetic storage fixture.
	// It is not a claim that an HTTP Form route or production owner is wired.
	originContext := context.WithValue(ctx, pluginConfigSaveRequestKey{}, m.PluginID)
	before, original, err := p.SaveConfigAndQueue(originContext, m.PluginID, 0, json.RawMessage(`{"enable":false,"enabled":false,"":true}`))
	if err != nil {
		t.Fatal(err)
	}
	in, err := p.Installation(ctx, m.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	var originalReceipt []byte
	if err = p.store.DB.QueryRow(`SELECT sealed FROM plugin_run_origins WHERE run_id=?`, original.ID).Scan(&originalReceipt); err != nil {
		t.Fatal(err)
	}
	calls := 0
	revision := before.Revision
	for _, raw := range []json.RawMessage{json.RawMessage(`{"channel":false}`), json.RawMessage(`{"channel":false,"strategy":"private-form-value"}`), json.RawMessage(`{"channel":false,"strategy":"private-form-value"}`)} {
		value, e := pluginFormMechanicalSave(t, p, ctx, in, revision, raw, func(ctx context.Context, tx PluginDefaultsTransaction, c PluginDefaultsChange) error {
			calls++
			// The callback sees the unchanged pending head (Previous), not a
			// synthetic receipt rebound to the already-written Next config.
			pending, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, original.ID))
			if e != nil {
				return e
			}
			sha, e := p.verifyRunConfigChainTx(ctx, tx, m.PluginID, pending, original.ConfigRevision, deferredInputSHA(original.Input), deferredInputSHA(before.Value))
			if e != nil || pending.ConfigRevision != c.Previous.Revision || sha != deferredInputSHA(c.Previous.Value) {
				t.Fatal("callback lost original previous-image proof", e)
			}
			state, e := tx.Data(ctx, "pending-form-journal")
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if _, e = tx.SaveData(ctx, "pending-form-journal", state.Revision, map[string]any{"revision": c.Next.Revision, "stored": c.Next.Value}); e != nil {
				return e
			}
			c.Previous.Value[0], c.Next.Value[0] = '!', '!'
			return nil
		})
		if e != nil || value.Revision != revision+1 {
			t.Fatal(value, e)
		}
		revision = value.Revision
		pending, e := p.Run(ctx, original.ID)
		if e != nil || pending.ConfigRevision != revision || pending.State != "pending" || pending.Action != original.Action || pending.Generation != original.Generation {
			t.Fatal("pending run was not propagated", e)
		}
		var input map[string]json.RawMessage
		if json.Unmarshal(pending.Input, &input) != nil || !bytes.Equal(input["config"], value.Value) {
			t.Fatal("pending config did not track full replacement")
		}
		var receipt, runSealed, configSealed, stateSealed []byte
		for _, query := range []struct {
			sql string
			arg string
			out *[]byte
		}{
			{`SELECT sealed FROM plugin_run_origins WHERE run_id=?`, original.ID, &receipt},
			{`SELECT input FROM plugin_runs WHERE id=?`, original.ID, &runSealed},
			{`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID, &configSealed},
			{`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='pending-form-journal'`, in.InstanceID, &stateSealed},
		} {
			if e = p.store.DB.QueryRow(query.sql, query.arg).Scan(query.out); e != nil {
				t.Fatal(e)
			}
		}
		if !bytes.Equal(receipt, originalReceipt) {
			t.Fatal("original init authority was resealed or replaced")
		}
		for _, sealed := range [][]byte{runSealed, configSealed, stateSealed} {
			if bytes.Contains(sealed, []byte("private-form-value")) {
				t.Fatal("config/state/input lost encryption")
			}
		}
		value.Value[0] = '!'
	}
	run, err := p.ClaimRun(ctx)
	if err != nil || run.ID != original.ID {
		t.Fatal(run, err)
	}
	valid, err := p.ConfigSaveOrigin(ctx, run)
	if err != nil || !valid {
		t.Fatal("full replacements lost original authority chain", valid, err)
	}
	var origins, edges, runs int
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_origins`).Scan(&origins); err != nil {
		t.Fatal(err)
	}
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || origins != 1 || edges != 3 || runs != 1 || calls != 3 {
		t.Fatal("form fabricated origin/run or lost equal-config proof edge", origins, edges, runs, calls, err)
	}
}

func TestPluginFormProjectionMechanicalRollback(t *testing.T) {
	for _, stage := range []string{"callback", "proof", "late-propagation"} {
		t.Run(stage, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			originContext := context.WithValue(ctx, pluginConfigSaveRequestKey{}, m.PluginID)
			before, run, err := p.SaveConfigAndQueue(originContext, m.PluginID, 0, json.RawMessage(`{"enable":false,"drop":true}`))
			if err != nil {
				t.Fatal(err)
			}
			in, err := p.Installation(ctx, m.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			_, configSealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			state, err := p.SaveData(ctx, in.InstanceID, "rollback-journal", 0, map[string]any{"original": true})
			if err != nil {
				t.Fatal(err)
			}
			var stateSealed, runSealed, originSealed []byte
			for _, query := range []struct {
				sql string
				arg string
				out *[]byte
			}{
				{`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='rollback-journal'`, in.InstanceID, &stateSealed},
				{`SELECT input FROM plugin_runs WHERE id=?`, run.ID, &runSealed},
				{`SELECT sealed FROM plugin_run_origins WHERE run_id=?`, run.ID, &originSealed},
			} {
				if err = p.store.DB.QueryRow(query.sql, query.arg).Scan(query.out); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "proof" {
				if _, err = p.store.DB.Exec(`CREATE TRIGGER reject_form_proof BEFORE INSERT ON plugin_run_config_transitions BEGIN SELECT RAISE(ABORT,'synthetic form proof rejection'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "late-propagation" {
				// This is the last shared propagation table. The pending run and
				// its authenticated edge have already been updated when it fails.
				if _, err = p.store.DB.Exec(`INSERT INTO plugin_event_batches(id,instance_id,handler_key,generation,config_revision,due_at,created_at) VALUES('form-rollback-batch',?,'synthetic',?,?,0,?)`, in.InstanceID, in.Generation, before.Revision, now()); err != nil {
					t.Fatal(err)
				}
				if _, err = p.store.DB.Exec(`CREATE TRIGGER reject_form_late_propagation BEFORE UPDATE ON plugin_event_batches BEGIN SELECT RAISE(ABORT,'synthetic form late propagation rejection'); END`); err != nil {
					t.Fatal(err)
				}
			}
			sentinel := errors.New("synthetic form journal rejected")
			calls := 0
			_, err = pluginFormMechanicalSave(t, p, ctx, in, before.Revision, json.RawMessage(`{"replacement":true}`), func(ctx context.Context, tx PluginDefaultsTransaction, c PluginDefaultsChange) error {
				calls++
				if _, e := tx.SaveData(ctx, "rollback-journal", state.Revision, map[string]any{"revision": c.Next.Revision}); e != nil {
					return e
				}
				if stage == "callback" {
					return sentinel
				}
				return nil
			})
			if err == nil || calls != 1 || (stage == "callback" && !errors.Is(err, sentinel)) {
				t.Fatal("rollback path did not fail", calls, err)
			}
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, configSealed)
			after, err := p.Data(ctx, in.InstanceID, "rollback-journal")
			if err != nil || after.Revision != state.Revision || after.UpdatedAt != state.UpdatedAt || !bytes.Equal(after.Value, state.Value) {
				t.Fatal("business state partially committed", err)
			}
			pending, err := p.Run(ctx, run.ID)
			if err != nil || pending.ConfigRevision != run.ConfigRevision || pending.UpdatedAt != run.UpdatedAt || !bytes.Equal(pending.Input, run.Input) {
				t.Fatal("pending run partially committed", err)
			}
			for _, query := range []struct {
				sql  string
				arg  string
				want []byte
			}{
				{`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='rollback-journal'`, in.InstanceID, stateSealed},
				{`SELECT input FROM plugin_runs WHERE id=?`, run.ID, runSealed},
				{`SELECT sealed FROM plugin_run_origins WHERE run_id=?`, run.ID, originSealed},
			} {
				var got []byte
				if err = p.store.DB.QueryRow(query.sql, query.arg).Scan(&got); err != nil || !bytes.Equal(got, query.want) {
					t.Fatal("encrypted rollback preimage changed", err)
				}
			}
			var edges int
			if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions`).Scan(&edges); err != nil || edges != 0 {
				t.Fatal("proof edge committed on failure", edges, err)
			}
			if stage == "late-propagation" {
				var revision int64
				if err = p.store.DB.QueryRow(`SELECT config_revision FROM plugin_event_batches WHERE id='form-rollback-batch'`).Scan(&revision); err != nil || revision != before.Revision {
					t.Fatal("late propagation did not roll back", revision, err)
				}
			}
		})
	}
}

func TestPluginFormProjectionMechanicalBusyAndImmutableDeferred(t *testing.T) {
	for _, state := range []string{"running", "reconcile_pending", "pending-prepared"} {
		t.Run(state, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			before, pending, err := p.SaveConfigAndQueue(ctx, m.PluginID, 0, json.RawMessage(`{"enable":false}`))
			if err != nil {
				t.Fatal(err)
			}
			in, err := p.Installation(ctx, m.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			var receipt []byte
			if state == "pending-prepared" {
				record := pluginDeferredRecord{Schema: 1, Plugin: in.PluginID, Instance: in.InstanceID, Version: in.Version, Run: pending.ID, Action: pending.Action, Method: "synthetic.form-prepared", Generation: pending.Generation, Revision: pending.ConfigRevision, InputSHA: deferredInputSHA(pending.Input), Raw: json.RawMessage(`{}`), Ack: json.RawMessage(`{"accepted":true}`), Phase: "prepared"}
				tx, e := p.store.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if e = p.writeDeferred(ctx, tx, record, nil); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
				if _, receipt, e = p.readDeferred(ctx, p.store.DB, pending); e != nil {
					t.Fatal(e)
				}
			} else {
				if _, err = p.ClaimRun(ctx); err != nil {
					t.Fatal(err)
				}
				if state == "reconcile_pending" {
					if _, err = p.store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, pending.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			var inputSealed []byte
			if err = p.store.DB.QueryRow(`SELECT input FROM plugin_runs WHERE id=?`, pending.ID).Scan(&inputSealed); err != nil {
				t.Fatal(err)
			}
			calls := 0
			hook := func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error { calls++; return nil }
			for _, desired := range []json.RawMessage{json.RawMessage(`{}`), before.Value} {
				if _, err = pluginFormMechanicalSave(t, p, ctx, in, before.Revision, desired, hook); pluginErrorCode(err) != "PLUGIN_BUSY" {
					t.Fatal("busy/equal Save rewrote immutable snapshot", err)
				}
			}
			if calls != 0 {
				t.Fatal("busy callback ran")
			}
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
			var afterInput []byte
			if err = p.store.DB.QueryRow(`SELECT input FROM plugin_runs WHERE id=?`, pending.ID).Scan(&afterInput); err != nil || !bytes.Equal(inputSealed, afterInput) {
				t.Fatal("busy run input changed", err)
			}
			if receipt != nil {
				_, afterReceipt, e := p.readDeferred(ctx, p.store.DB, pending)
				if e != nil || !bytes.Equal(afterReceipt, receipt) {
					t.Fatal("immutable prepared receipt changed", e)
				}
			}
			// The legacy API still treats a truly missing-only no-op as read
			// only, even when a full source Save would be blocked as busy.
			noop, err := p.NormalizeInstanceDefaults(ctx, in, before.Revision, map[string]json.RawMessage{"enable": json.RawMessage(`true`)})
			if err != nil || noop.Revision != before.Revision || !bytes.Equal(noop.Value, before.Value) {
				t.Fatal("missing-only no-op semantics changed", err)
			}
		})
	}
}

func TestPluginFormProjectionAuthenticatedMetadataAndExactCompiledVersion(t *testing.T) {
	t.Run("corrupt-clone-metadata", func(t *testing.T) {
		_, p := pluginStoreFixture(t)
		ctx := context.Background()
		m := builtin.SystemProcessManifest()
		installCloneSource(t, p, m)
		clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "corrupt"})
		if err != nil {
			t.Fatal(err)
		}
		before, sealed := pluginDefaultsStoredConfig(t, p, clone.InstanceID)
		if _, err = p.store.DB.Exec(`UPDATE plugin_instance_metadata SET sealed=x'00' WHERE instance_id=?`, clone.InstanceID); err != nil {
			t.Fatal(err)
		}
		calls := 0
		hook := func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error { calls++; return nil }
		if _, err = p.ProjectInstanceConfig(ctx, clone, before.Revision, before.Value, hook); err == nil || pluginErrorCode(err) == "PLUGIN_FORM_PROJECTION_UNSUPPORTED" {
			t.Fatal("public path did not authenticate clone metadata first", err)
		}
		if _, err = pluginFormMechanicalSave(t, p, ctx, clone, before.Revision, before.Value, hook); err == nil || calls != 0 {
			t.Fatal("mechanical transaction bypassed authenticated metadata", calls, err)
		}
		assertPluginDefaultsConfigUnchanged(t, p, clone.InstanceID, before, sealed)
	})
	for _, tc := range []struct{ name, plugin, version string }{
		{"unknown", "SyntheticFormProjection", "1.0"},
		{"old-version", "SystemProcess", "1.0"},
		{"wrong-version", "SystemProcess", "99.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			m.PluginID, m.Version, m.ConfigFormProjection = tc.plugin, tc.version, plugins.ConfigFormProjectionTransactional
			installCloneSource(t, p, m)
			if _, err := p.SaveConfig(ctx, m.PluginID, 0, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			in, err := p.Installation(ctx, m.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			calls := 0
			if _, err = p.ProjectInstanceConfig(ctx, in, before.Revision, json.RawMessage(`{"replace":true}`), func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error { calls++; return nil }); pluginErrorCode(err) != "PLUGIN_FORM_PROJECTION_UNSUPPORTED" || calls != 0 {
				t.Fatal("unknown or nonexact compiled implementation accepted", calls, err)
			}
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
		})
	}
}
