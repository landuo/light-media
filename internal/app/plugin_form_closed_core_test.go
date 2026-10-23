package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"reflect"
	"strings"
	"testing"
	"time"
)

var pluginClosedCoreOperations = []string{
	"migration-begin", "migration-idempotent", "migration-running", "migration-failed", "migration-terminal", "import-new", "import-idempotent", "constructor-noop",
	"result-running", "result-terminal", "run-patch", "run-patch-tx", "run-replace-tx", "event-due-empty", "event-due-pending", "admission-noop", "admission-duplicate", "origin-noop", "deferred-handshake", "deferred-execute",
}

func pluginClosedCoreFixture(t *testing.T, name string) pluginCommonFormMutation {
	t.Helper()
	ctx := context.Background()
	f := pluginCommonFormMutation{changesRows: true}
	switch {
	case strings.HasPrefix(name, "migration-") || strings.HasPrefix(name, "import-") || name == "constructor-noop":
		_, p := pluginStoreFixture(t)
		f.store = p
		if name == "constructor-noop" {
			f.changesRows = false
			f.call = func(c context.Context) error { _, e := NewPluginStore(c, p.store); return e }
			break
		}
		if name == "migration-begin" || name == "migration-idempotent" {
			if name == "migration-idempotent" {
				if _, e := p.BeginMigration(ctx, strings.Repeat("a", 64), "OldSyntheticPlugin", "converted"); e != nil {
					t.Fatal(e)
				}
				f.changesRows = false
			}
			f.call = func(c context.Context) error {
				_, e := p.BeginMigration(c, strings.Repeat("a", 64), "OldSyntheticPlugin", "converted")
				return e
			}
			break
		}
		in := pluginImportFixture(t, p, "a", "converted")
		if name == "import-idempotent" || name == "migration-terminal" {
			if _, e := p.Import(ctx, in); e != nil {
				t.Fatal(e)
			}
			f.changesRows = false
		}
		if strings.HasPrefix(name, "import-") {
			f.call = func(c context.Context) error { _, e := p.Import(c, in); return e }
			break
		}
		calls := 0
		state, code := "running", ""
		if name == "migration-failed" {
			state, code = "failed", "OWNED_SOURCE_FAILED"
		}
		f.call = func(c context.Context) error {
			return p.SetMigrationState(c, in.RunID, state, code, pluginCommonFormMarshaler{calls: &calls})
		}
		f.verify = func(denied bool) {
			want := 1
			if denied {
				want = 0
			}
			if calls != want {
				t.Errorf("migration Marshal calls=%d, want %d", calls, want)
			}
		}
		if name == "migration-terminal" {
			f.skipPositive = true
		}
	case strings.HasPrefix(name, "result-") || strings.HasPrefix(name, "run-"):
		a := eventAdmissionHost(t)
		p := a.pluginStore
		f.store = p
		run := eventAdmissionQueue(t, a, "admission", "reload", nil)
		if name == "result-terminal" {
			if e := p.FinishRun(ctx, run.ID, "succeeded", "", nil); e != nil {
				t.Fatal(e)
			}
			f.changesRows = false
		}
		if strings.HasPrefix(name, "result-") {
			f.call = func(c context.Context) error { return p.failPluginResult(c, run.ID, "INVALID_PLUGIN_RESULT") }
			break
		}
		before := run
		patch := map[string]json.RawMessage{"form_guard": json.RawMessage(`true`)}
		f.call = func(c context.Context) error {
			if name == "run-patch" {
				return p.patchRunConfig(c, &run, patch)
			}
			return pluginCommonFormCommittedTx(t, p, c, func(tx *sql.Tx) error {
				if name == "run-patch-tx" {
					return p.patchRunConfigTx(c, tx, &run, patch)
				}
				return p.replaceRunConfigTx(c, tx, &run, json.RawMessage(`{"enabled":true,"form_guard":true}`))
			})
		}
		f.verify = func(denied bool) {
			if denied && !reflect.DeepEqual(run, before) {
				t.Error("denied mutation changed caller run")
			}
		}
	case strings.HasPrefix(name, "event-due-"):
		op := "event-collect-noop"
		if name == "event-due-pending" {
			op = "event-collect-matching"
		}
		f = pluginCommonFormMutationFixture(t, op)
		if e := f.call(ctx); e != nil {
			t.Fatal("prepare accepted event batch", e)
		}
		p := f.store
		f.verify = nil
		f.changesRows = name == "event-due-pending"
		f.call = func(c context.Context) error { return p.enqueueDuePluginEventsFor(c, time.Now().Add(time.Hour), "") }
	case name == "admission-noop" || name == "admission-duplicate" || name == "origin-noop":
		a := eventAdmissionHost(t)
		p := a.pluginStore
		f.store = p
		f.changesRows = false
		run := eventAdmissionQueue(t, a, "admission", "reload", nil)
		config, e := p.Config(ctx, run.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		in, e := p.Installation(ctx, run.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		if name == "admission-duplicate" {
			f.skipPositive = true
		}
		f.call = func(c context.Context) error {
			return pluginCommonFormCommittedTx(t, p, c, func(tx *sql.Tx) error {
				if name == "origin-noop" {
					return p.recordRunOriginTx(c, tx, in.PluginID, run, config.Value)
				}
				if name == "admission-noop" {
					empty := PluginRun{Action: "invoke"}
					return p.recordEventAdmissionTx(c, tx, in.PluginID, empty, nil, nil)
				}
				return p.recordEventAdmissionTx(c, tx, in.PluginID, run, config.Value, nil)
			})
		}
	case name == "deferred-handshake" || name == "deferred-execute":
		a, in, run := deferredFixture(t)
		p := a.pluginStore
		f.store = p
		f.changesRows = false
		d, e := a.preparePluginDeferred(ctx, in, run)
		if e != nil || d == nil {
			t.Fatal("prepare real deferred record", e)
		}
		called, poisoned := d.called, d.poisoned
		f.call = func(c context.Context) error {
			if name == "deferred-handshake" {
				_, e := d.handshake(c, deferredIdentity(in), d.record.Method, d.record.Raw)
				return e
			}
			_, e := d.execute(c, &run)
			return e
		}
		f.verify = func(denied bool) {
			if denied && (d.called != called || d.poisoned != poisoned) {
				t.Error("Form entered handshake mutable preparation")
			}
		}
		// Executing the uninstall business operation is deliberately not part of
		// these Form boundary fixtures; existing deferred acceptance tests own it.
		if name == "deferred-execute" {
			f.skipPositive = true
		}
	default:
		t.Fatal("unknown closed common operation", name)
	}
	return f
}
func TestPluginFormClosedCoreMutationsDenyBeforeDurableEffects(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, foreign := range []bool{false, true} {
			for _, name := range pluginClosedCoreOperations {
				destination := "same-store-other-instance"
				if foreign {
					destination = "foreign-store"
				}
				t.Run(pluginFormDispatchModeName(mode)+"/"+destination+"/"+name, func(t *testing.T) {
					f := pluginClosedCoreFixture(t, name)
					source := f.store
					if foreign {
						_, source = pluginStoreFixture(t)
					}
					ctx, in, session := pluginCommonFormAttach(t, source, mode)
					beforeSource := pluginCommonFormRows(t, source)
					beforeTarget := beforeSource
					if source != f.store {
						beforeTarget = pluginCommonFormRows(t, f.store)
					}
					result, err := source.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(c context.Context, _ pluginFormView) (json.RawMessage, error) {
						if denied := f.call(c); pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
							t.Errorf("common API did not immediately deny: %v", denied)
						}
						return json.RawMessage(`{"swallowed":true}`), nil
					}))
					if result != nil || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
						t.Errorf("common denial was not sticky: %s %v", result, err)
					}
					pluginCommonFormRowsUnchanged(t, source, beforeSource)
					if source != f.store {
						pluginCommonFormRowsUnchanged(t, f.store, beforeTarget)
					}
					if f.verify != nil {
						f.verify(true)
					}
				})
			}
		}
	}
}
func TestPluginFormClosedCoreScopeFreeContractsRemain(t *testing.T) {
	for _, name := range pluginClosedCoreOperations {
		// Negative source contracts and business effects have dedicated tests; do
		// not count them here as ordinary positive fixtures.
		if name == "migration-terminal" || name == "admission-duplicate" || name == "deferred-execute" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			f := pluginClosedCoreFixture(t, name)
			before := pluginCommonFormRows(t, f.store)
			if e := f.call(context.Background()); e != nil {
				t.Fatal(e)
			}
			after := pluginCommonFormRows(t, f.store)
			if f.changesRows == reflect.DeepEqual(before, after) {
				t.Errorf("scope-free row change=%v want %v", !reflect.DeepEqual(before, after), f.changesRows)
			}
			if f.verify != nil {
				f.verify(false)
			}
		})
	}
}
func TestPluginFormClosedRuntimeCannotFinishViaDetachedContext(t *testing.T) {
	setup := func(t *testing.T) (*App, PluginRun, *int) {
		a := newTestApp(t)
		p := a.pluginStore
		installCloneSource(t, p, builtin.CategoryEditorManifest())
		if _, e := p.SaveConfig(context.Background(), "CategoryEditor", 0, json.RawMessage(`{"enabled":false,"content":""}`)); e != nil {
			t.Fatal(e)
		}
		run := pluginCommonFormRunningTarget(t, p, "CategoryEditor")
		calls := new(int)
		a.pluginCommand = func(string) (plugins.Command, error) {
			(*calls)++
			return plugins.Command{}, bad("OWNED_COMMAND_STOP", "owned preparation stop")
		}
		return a, run, calls
	}
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		t.Run(pluginFormDispatchModeName(mode), func(t *testing.T) {
			a, run, calls := setup(t)
			ctx, in, session := pluginCommonFormAttach(t, a.pluginStore, mode)
			before := pluginCommonFormRows(t, a.pluginStore)
			result, err := a.pluginStore.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(c context.Context, _ pluginFormView) (json.RawMessage, error) {
				a.executePluginRun(c, run)
				return json.RawMessage(`{"swallowed":true}`), nil
			}))
			if result != nil || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
				t.Errorf("runtime denial not sticky: %s %v", result, err)
			}
			if *calls != 0 || len(a.pluginProcess) != 0 {
				t.Error("Form entered worker preparation or left process slot held")
			}
			pluginCommonFormRowsUnchanged(t, a.pluginStore, before)
		})
	}
	t.Run("scope-free-preparation-failure-still-finishes", func(t *testing.T) {
		a, run, calls := setup(t)
		a.executePluginRun(context.Background(), run)
		var state, code string
		if e := a.Store.DB.QueryRow(`SELECT state,error_code FROM plugin_runs WHERE id=?`, run.ID).Scan(&state, &code); e != nil {
			t.Fatal(e)
		}
		if *calls != 1 || state != "failed" || code != "OWNED_COMMAND_STOP" {
			t.Fatalf("ordinary completion changed: %d %s %s", *calls, state, code)
		}
	})
}
