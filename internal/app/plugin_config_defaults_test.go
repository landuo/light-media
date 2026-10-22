package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/schedule"
)

func TestPluginNormalizeDefaultsPreservesValuesAndInactiveClones(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	original, e := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enable": false, "zero": 0, "empty": "", "null": nil, "opaque": map[string]any{"x": false}})
	if e != nil {
		t.Fatal(e)
	}
	clone, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "copy"})
	if e != nil {
		t.Fatal(e)
	}
	patch := map[string]json.RawMessage{"enable": json.RawMessage(`true`), "zero": json.RawMessage(`99`), "empty": json.RawMessage(`"override"`), "null": json.RawMessage(`"not null"`), "dashboard_widget_size": json.RawMessage(`8`)}
	normalized, e := p.NormalizeInstanceDefaults(ctx, clone, 1, patch)
	if e != nil || normalized.Revision != 2 {
		t.Fatal(normalized, e)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(normalized.Value, &fields) != nil || string(fields["enable"]) != "false" || string(fields["zero"]) != "0" || string(fields["empty"]) != `""` || string(fields["null"]) != "null" || string(fields["dashboard_widget_size"]) != "8" {
		t.Fatal(string(normalized.Value))
	}
	again, e := p.NormalizeInstanceDefaults(ctx, clone, normalized.Revision, patch)
	if e != nil || again.Revision != normalized.Revision || again.UpdatedAt != normalized.UpdatedAt || !bytes.Equal(again.Value, normalized.Value) {
		t.Fatal("non-idempotent default write", again, e)
	}
	in, e := p.Installation(ctx, clone.InstanceID)
	if e != nil || in.DesiredEnabled || in.Generation != clone.Generation {
		t.Fatal("form normalized clone was activated", in, e)
	}
	source, e := p.Config(ctx, m.PluginID)
	if e != nil || source.Revision != original.Revision || !bytes.Equal(source.Value, original.Value) {
		t.Fatal("normalization crossed instances", e)
	}
	for _, table := range []string{"plugin_runs", "plugin_schedules", "plugin_event_subscriptions"} {
		var n int
		if e = s.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE instance_id=?`, clone.InstanceID).Scan(&n); e != nil || n != 0 {
			t.Fatal("form normalization scheduled work", table, n, e)
		}
	}
}

func TestPluginNormalizeDefaultsRejectsBusyStaleAndConcurrentWrites(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	_, _, e := p.SaveConfigAndQueue(ctx, m.PluginID, 0, map[string]any{"enable": false})
	if e != nil {
		t.Fatal(e)
	}
	run, e := p.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	in, e := p.Installation(ctx, m.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	defaults := map[string]json.RawMessage{"dashboard_widget_size": json.RawMessage(`8`)}
	if _, e = p.NormalizeInstanceDefaults(ctx, in, 1, defaults); pluginErrorCode(e) != "PLUGIN_BUSY" {
		t.Fatal("form borrowed live run authority", e)
	}
	v, e := p.Config(ctx, m.PluginID)
	if e != nil || v.Revision != 1 {
		t.Fatal(v, e)
	}
	if e = p.FinishRun(ctx, run.ID, "succeeded", "", nil); e != nil {
		t.Fatal(e)
	}
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.NormalizeInstanceDefaults(ctx, in, 1, defaults)
			if err == nil {
				success.Add(1)
			} else if pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("CAS admitted multiple writes", success.Load())
	}
	if e = p.Reset(ctx, m.PluginID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.NormalizeInstanceDefaults(ctx, in, 0, defaults); pluginErrorCode(e) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("stale form resurrected reset config", e)
	}
	if _, e = p.Config(ctx, m.PluginID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("reset config recreated", e)
	}
}

// This installs an actual pure/v1 implementation, but does not execute it.
// Callers explicitly register synthetic host event/schedule fixtures to exercise
// shared storage propagation, not nonexistent SystemProcess business services.
func pluginDefaultsHostFixture(t *testing.T) (*App, PluginRun) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	a := &App{Store: s, pluginStore: p}
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	if _, _, err := p.SaveConfigAndQueue(ctx, m.PluginID, 0, map[string]any{"enable": true}); err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return a, run
}

func TestPluginNormalizeDefaultsPropagatesPendingSnapshotsAndSyntheticEventCursor(t *testing.T) {
	a, first := pluginDefaultsHostFixture(t)
	ctx := context.Background()
	p := a.pluginStore
	if err := p.configureEventSubscription(ctx, first, "synthetic_defaults", []string{"import.completed"}, time.Second, true); err != nil {
		t.Fatal(err)
	}
	if err := p.FinishRun(ctx, first.ID, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	commitDomainFixture(t, a, domainFixture("import.completed", "form-defaults"))
	if e := a.dispatchPluginEvents(ctx, time.Now()); e != nil {
		t.Fatal(e)
	}
	pending, e := p.EnqueueRun(ctx, first.InstanceID, "reload", nil)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := p.Installation(ctx, first.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	var cursor int64
	if e = a.Store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id=?`, first.InstanceID).Scan(&cursor); e != nil {
		t.Fatal(e)
	}
	value, e := p.NormalizeInstanceDefaults(ctx, snapshot, first.ConfigRevision, map[string]json.RawMessage{"new_default": json.RawMessage(`false`)})
	if e != nil {
		t.Fatal(e)
	}
	reloaded, e := p.Run(ctx, pending.ID)
	if e != nil || reloaded.ConfigRevision != value.Revision || reloaded.State != "pending" {
		t.Fatal(reloaded, e)
	}
	var invocation struct{ Config map[string]any }
	if json.Unmarshal(reloaded.Input, &invocation) != nil || invocation.Config["new_default"] != false {
		t.Fatal("pending encrypted config was not propagated")
	}
	var afterCursor, afterRevision int64
	var active bool
	if e = a.Store.DB.QueryRow(`SELECT cursor,config_revision,active FROM plugin_event_subscriptions WHERE instance_id=?`, first.InstanceID).Scan(&afterCursor, &afterRevision, &active); e != nil || cursor != afterCursor || afterRevision != value.Revision || !active {
		t.Fatal("default form reset event boundary", afterCursor, afterRevision, active, e)
	}
	if e := a.dispatchPluginEvents(ctx, time.Now().Add(2*time.Second)); e != nil {
		t.Fatal(e)
	}
	var batches int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_event_batches WHERE instance_id=? AND config_revision=?`, first.InstanceID, value.Revision).Scan(&batches); e != nil || batches != 1 {
		t.Fatal("default form lost delayed events", batches, e)
	}
}

func TestPluginNormalizeDefaultsRollbackKeepsPendingAndConfig(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	original, run, e := p.SaveConfigAndQueue(ctx, m.PluginID, 0, map[string]any{"enable": false})
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := p.Installation(ctx, m.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`CREATE TRIGGER synthetic_normalize_fail BEFORE UPDATE ON plugin_runs BEGIN SELECT RAISE(ABORT,'synthetic normalize propagation failure'); END;`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.NormalizeInstanceDefaults(ctx, snapshot, original.Revision, map[string]json.RawMessage{"dashboard_widget_size": json.RawMessage(`8`)}); e == nil {
		t.Fatal("propagation failure reported success")
	}
	value, e := p.Config(ctx, m.PluginID)
	if e != nil || value.Revision != original.Revision || !bytes.Equal(value.Value, original.Value) {
		t.Fatal("normalization partially committed", value, e)
	}
	after, e := p.Run(ctx, run.ID)
	if e != nil || after.ConfigRevision != run.ConfigRevision || !bytes.Equal(after.Input, run.Input) {
		t.Fatal("pending run changed on rollback", after, e)
	}
}

// Direct helper invocation, the real worker's form, and explicit user-save init
// are separate steps. This does not establish production form integration of
// NormalizeInstanceDefaults; the form endpoint itself does not call the helper.
func TestPluginNormalizeDefaultsRealHelperThenFormAndExplicitInit(t *testing.T) {
	for _, id := range []string{"SystemProcess", "CategoryEditor"} {
		t.Run(id, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			ctx := context.Background()
			if id == "CategoryEditor" {
				// Use an existing valid synthetic policy, not arbitrary YAML. Both
				// missing-default save and disabled explicit init must leave it alone.
				if e := a.writePluginCategory(categoryReloadInitial); e != nil {
					t.Fatal(e)
				}
			}
			m, _, _ := builtin.Lookup(id)
			installCloneSource(t, a.pluginStore, m)
			clone, e := a.pluginStore.Clone(ctx, id, PluginCloneRequest{Suffix: "defaults"})
			if e != nil {
				t.Fatal(e)
			}
			defaults := map[string]json.RawMessage{"enable": json.RawMessage(`false`), "dashboard_widget_size": json.RawMessage(`8`)}
			if id == "CategoryEditor" {
				defaults = map[string]json.RawMessage{"enabled": json.RawMessage(`false`), "content": json.RawMessage(`""`)}
			}
			value, e := a.pluginStore.NormalizeInstanceDefaults(ctx, clone, 1, defaults)
			if e != nil {
				t.Fatal(e)
			}
			if w := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone.InstanceID+"/form", "", cookie, ""); w.Code != 200 {
				t.Fatal("real normalized form", w.Body.String())
			}
			afterForm, e := a.pluginStore.Config(ctx, clone.InstanceID)
			if e != nil || afterForm.Revision != value.Revision || !bytes.Equal(afterForm.Value, value.Value) {
				t.Fatal("form unexpectedly normalized configuration itself", afterForm, e)
			}
			if _, e = a.pluginStore.ClaimRun(ctx); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("form autoactivated normalized clone", e)
			}
			body, e := json.Marshal(map[string]any{"revision": value.Revision, "config": value.Value})
			if e != nil {
				t.Fatal(e)
			}
			if w := callTest(t, a.Handler(), "PUT", "/api/plugins/"+clone.InstanceID+"/config", string(body), cookie, ""); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			run, e := a.pluginStore.ClaimRun(ctx)
			if e != nil || run.InstanceID != clone.InstanceID {
				t.Fatal(run, e)
			}
			a.executePluginRun(ctx, run)
			run, e = a.pluginStore.Run(ctx, run.ID)
			if e != nil || run.State != "succeeded" {
				t.Fatal("real normalized explicit init", run, e)
			}
			if id == "CategoryEditor" {
				policy, e := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
				if e != nil || string(policy) != categoryReloadInitial {
					t.Fatal("disabled defaults/form/init changed the shared category policy", e)
				}
			}
		})
	}
}

func TestPluginNormalizeDefaultsKeepsSyntheticScheduleAnchorAndOccurrence(t *testing.T) {
	at := time.Now().UTC().Add(time.Hour)
	a, first := pluginDefaultsHostFixture(t)
	p := a.pluginStore
	ctx := context.Background()
	definition := pluginScheduleDefinition{ID: "SyntheticDefaults.Check", Name: "合成默认值事务调度", Handler: "check", Trigger: schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000}, MisfireGraceMS: -1}
	if err := p.configurePluginSchedules(ctx, first, "synthetic_defaults", []pluginScheduleDefinition{definition}, at); err != nil {
		t.Fatal(err)
	}
	if err := p.FinishRun(ctx, first.ID, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if n, e := p.enqueueDuePluginSchedules(ctx, at.Add(time.Second)); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	snapshot, e := p.Installation(ctx, first.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	var beforeNext string
	if e = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=?`, first.InstanceID).Scan(&beforeNext); e != nil {
		t.Fatal(e)
	}
	value, e := p.NormalizeInstanceDefaults(ctx, snapshot, first.ConfigRevision, map[string]json.RawMessage{"form_default": json.RawMessage(`"default"`)})
	if e != nil {
		t.Fatal(e)
	}
	var next string
	var revision int64
	if e = a.Store.DB.QueryRow(`SELECT next_due,config_revision FROM plugin_schedules WHERE instance_id=?`, first.InstanceID).Scan(&next, &revision); e != nil || next != beforeNext || revision != value.Revision {
		t.Fatal("schedule anchor changed", next, beforeNext, revision, e)
	}
	var n int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=? AND config_revision=?`, first.InstanceID, value.Revision).Scan(&n); e != nil || n != 1 {
		t.Fatal("occurrence disconnected", n, e)
	}
	run, e := p.ClaimRun(ctx)
	if e != nil || run.ConfigRevision != value.Revision {
		t.Fatal("normalized timer no longer claimable", run, e)
	}
}

func TestPluginNormalizeDefaultsMissingAndInvalidSnapshots(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	in, e := p.Installation(ctx, m.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	value, e := p.NormalizeInstanceDefaults(ctx, in, 0, nil)
	if e != nil || value.Revision != 0 {
		t.Fatal("empty defaults created config", value, e)
	}
	if _, e = p.Config(ctx, m.PluginID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("no-op created row", e)
	}
	if _, e = p.NormalizeInstanceDefaults(ctx, in, 0, map[string]json.RawMessage{"x": json.RawMessage(`bad`)}); pluginErrorCode(e) != "INVALID_PLUGIN_VALUE" {
		t.Fatal(e)
	}
	if _, e = p.NormalizeInstanceDefaults(ctx, in, 0, map[string]json.RawMessage{"": json.RawMessage(`1`)}); pluginErrorCode(e) != "INVALID_PLUGIN_VALUE" {
		t.Fatal(e)
	}
	stale := in
	stale.Version = "not-current"
	if _, e = p.NormalizeInstanceDefaults(ctx, stale, 0, map[string]json.RawMessage{"x": json.RawMessage(`1`)}); pluginErrorCode(e) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal(e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = p.NormalizeInstanceDefaults(canceled, in, 0, map[string]json.RawMessage{"x": json.RawMessage(`1`)}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	value, e = p.NormalizeInstanceDefaults(ctx, in, 0, map[string]json.RawMessage{"enable": json.RawMessage(`false`)})
	if e != nil || value.Revision != 1 {
		t.Fatal(value, e)
	}
	var pending int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&pending); e != nil || pending != 0 {
		t.Fatal(pending, e)
	}
	if e = p.Uninstall(ctx, m.PluginID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.NormalizeInstanceDefaults(ctx, in, value.Revision, map[string]json.RawMessage{"x": json.RawMessage(`1`)}); pluginErrorCode(e) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("uninstall form reactivated tombstone", e)
	}
}

func pluginDefaultsStoredConfig(t *testing.T, p *PluginStore, instance string) (PluginStoredValue, []byte) {
	t.Helper()
	value, err := p.Config(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := p.store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instance).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return value, sealed
}

func assertPluginDefaultsConfigUnchanged(t *testing.T, p *PluginStore, instance string, before PluginStoredValue, beforeSealed []byte) {
	t.Helper()
	after, sealed := pluginDefaultsStoredConfig(t, p, instance)
	if after.Revision != before.Revision || after.UpdatedAt != before.UpdatedAt || !bytes.Equal(after.Value, before.Value) || !bytes.Equal(sealed, beforeSealed) {
		t.Fatal("default normalization changed configuration bytes or revision")
	}
}

func TestPluginNormalizeDefaultsRejectsUndeclaredCompiledCatalogOwners(t *testing.T) {
	for _, m := range builtin.Catalog() {
		if m.ConfigDefaults == plugins.ConfigDefaultsPure {
			if m.PluginID != "SystemProcess" && m.PluginID != "CategoryEditor" {
				t.Fatalf("unreviewed compiled default-write declaration: %s", m.PluginID)
			}
			continue
		}
		t.Run(m.PluginID, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			installCloneSource(t, p, m)
			// Clone supplies a safe empty staged configuration through the real
			// installation/owner hooks, without initializing any business owner.
			clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "defaults"})
			if err != nil {
				t.Fatal(err)
			}
			before, sealed := pluginDefaultsStoredConfig(t, p, clone.InstanceID)
			if string(before.Value) != `{}` {
				t.Fatal("unsupported-owner fixture is not empty and staged")
			}
			if _, err = p.NormalizeInstanceDefaults(ctx, clone, before.Revision, map[string]json.RawMessage{"synthetic_missing": json.RawMessage(`false`)}); pluginErrorCode(err) != "PLUGIN_DEFAULTS_UNSUPPORTED" {
				t.Fatal("undeclared owner accepted a default write", err)
			}
			noop, err := p.NormalizeInstanceDefaults(ctx, clone, before.Revision, nil)
			if err != nil || noop.Revision != before.Revision || noop.UpdatedAt != before.UpdatedAt || !bytes.Equal(noop.Value, before.Value) {
				t.Fatal("undeclared owner lost read-only no-op access", err)
			}
			assertPluginDefaultsConfigUnchanged(t, p, clone.InstanceID, before, sealed)
			installation, err := p.Installation(ctx, clone.InstanceID)
			if err != nil || installation.DesiredEnabled || installation.Generation != clone.Generation {
				t.Fatal("rejected defaults changed staging or generation", err)
			}
			var count int
			if err = s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, clone.InstanceID).Scan(&count); err != nil || count != 0 {
				t.Fatal("rejected defaults created work", count, err)
			}
		})
	}
}

func TestPluginNormalizeDefaultsStoredManifestCannotGrantPureContract(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m, _, known := builtin.Lookup("AutoSignIn")
	if !known || m.ConfigDefaults != "" {
		t.Fatal("expected an undeclared compiled native owner")
	}
	m.ConfigDefaults = plugins.ConfigDefaultsPure // Forged persisted metadata, not compiled authority.
	installCloneSource(t, p, m)
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "forged"})
	if err != nil {
		t.Fatal(err)
	}
	var stored plugins.Manifest
	if json.Unmarshal(clone.Manifest, &stored) != nil || stored.ConfigDefaults != plugins.ConfigDefaultsPure {
		t.Fatal("fixture did not persist the forged declaration")
	}
	before, sealed := pluginDefaultsStoredConfig(t, p, clone.InstanceID)
	if _, err = p.NormalizeInstanceDefaults(ctx, clone, before.Revision, map[string]json.RawMessage{"synthetic_missing": json.RawMessage(`false`)}); pluginErrorCode(err) != "PLUGIN_DEFAULTS_UNSUPPORTED" {
		t.Fatal("stored declaration granted default-write authority", err)
	}
	assertPluginDefaultsConfigUnchanged(t, p, clone.InstanceID, before, sealed)
}

func TestPluginNormalizeDefaultsRequiresKnownExactCompiledVersion(t *testing.T) {
	for _, tc := range []struct{ name, plugin, version string }{
		{"unknown_implementation", "SyntheticPureDefaults", "1.1"},
		{"old_version", "SystemProcess", "1.0"},
		{"wrong_version", "SystemProcess", "99.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			m.PluginID, m.Version = tc.plugin, tc.version
			installCloneSource(t, p, m)
			if _, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enable": false}); err != nil {
				t.Fatal(err)
			}
			in, err := p.Installation(ctx, m.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			if _, err = p.NormalizeInstanceDefaults(ctx, in, before.Revision, map[string]json.RawMessage{"dashboard_widget_size": json.RawMessage(`8`)}); pluginErrorCode(err) != "PLUGIN_DEFAULTS_UNSUPPORTED" {
				t.Fatal("unknown/mismatched compiled implementation accepted defaults", err)
			}
			noop, err := p.NormalizeInstanceDefaults(ctx, in, before.Revision, map[string]json.RawMessage{"enable": json.RawMessage(`true`)})
			if err != nil || noop.Revision != before.Revision || !bytes.Equal(noop.Value, before.Value) {
				t.Fatal("unsupported version lost non-overwriting no-op access", err)
			}
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
		})
	}
}

func TestPluginNormalizeDefaultsPreservesPendingPreparedDeferredReceipt(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	_, pending, err := p.SaveConfigAndQueue(ctx, m.PluginID, 0, map[string]any{"enable": false})
	if err != nil {
		t.Fatal(err)
	}
	in, err := p.Installation(ctx, m.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	// SystemProcess has no business deferred call. This deliberately synthetic
	// immutable receipt exists only in the temporary test DB to defend against
	// future declarations or retained prepared records, without running effects.
	record := pluginDeferredRecord{Schema: 1, Plugin: in.PluginID, Instance: in.InstanceID, Version: in.Version, Run: pending.ID, Action: pending.Action, Method: "synthetic.prepared", Generation: pending.Generation, Revision: pending.ConfigRevision, InputSHA: deferredInputSHA(pending.Input), Raw: json.RawMessage(`{}`), Ack: json.RawMessage(`{"accepted":true}`), Phase: "prepared"}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = p.writeDeferred(ctx, tx, record, nil); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before, configSealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	beforeRecord, receiptSealed, err := p.readDeferred(ctx, s.DB, pending)
	if err != nil || beforeRecord.Phase != "prepared" || !deferredSameRun(beforeRecord, in, pending) {
		t.Fatal("prepared receipt was not bound to the pending snapshot", err)
	}
	var runSealed []byte
	if err = s.DB.QueryRow(`SELECT input FROM plugin_runs WHERE id=?`, pending.ID).Scan(&runSealed); err != nil {
		t.Fatal(err)
	}
	if _, err = p.NormalizeInstanceDefaults(ctx, in, before.Revision, map[string]json.RawMessage{"dashboard_widget_size": json.RawMessage(`8`)}); pluginErrorCode(err) != "PLUGIN_BUSY" {
		t.Fatal("normalization rewrote a prepared snapshot", err)
	}
	noop, err := p.NormalizeInstanceDefaults(ctx, in, before.Revision, map[string]json.RawMessage{"enable": json.RawMessage(`true`)})
	if err != nil || noop.Revision != before.Revision || !bytes.Equal(noop.Value, before.Value) {
		t.Fatal("prepared receipt incorrectly blocked a read-only no-op", err)
	}
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, configSealed)
	after, err := p.Run(ctx, pending.ID)
	if err != nil || after.State != "pending" || after.ConfigRevision != pending.ConfigRevision || after.UpdatedAt != pending.UpdatedAt || !bytes.Equal(after.Input, pending.Input) {
		t.Fatal("pending prepared run changed", err)
	}
	var afterRunSealed []byte
	if err = s.DB.QueryRow(`SELECT input FROM plugin_runs WHERE id=?`, pending.ID).Scan(&afterRunSealed); err != nil || !bytes.Equal(afterRunSealed, runSealed) {
		t.Fatal("pending encrypted input changed", err)
	}
	afterRecord, afterReceiptSealed, err := p.readDeferred(ctx, s.DB, after)
	if err != nil || afterRecord.Phase != "prepared" || !deferredSameRun(afterRecord, in, after) || !bytes.Equal(afterReceiptSealed, receiptSealed) {
		t.Fatal("prepared receipt was rebound or invalidated", err)
	}
}
