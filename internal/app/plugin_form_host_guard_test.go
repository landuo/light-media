package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"strings"
	"testing"
)

func TestPluginFormHostGuardRejectsDriftBeforeCallback(t *testing.T) {
	for _, drift := range []string{"external-before-first", "external-between-calls", "reset", "claim"} {
		t.Run(drift, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			formCtx := withPluginFormSession(ctx, session)
			calls := 0
			host := session.wrapHost(func(context.Context, plugins.Identity, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				return json.RawMessage(`{}`), nil
			})
			if drift == "external-between-calls" {
				if _, err = host(formCtx, plugins.Identity{}, "", nil); err != nil {
					t.Fatal(err)
				}
			}
			beforeCalls := calls
			want := "PLUGIN_REVISION_CONFLICT"
			switch drift {
			case "reset":
				err = p.Reset(ctx, in.InstanceID)
				// V9 rejects the old generation before anchor comparison.
				want = "PLUGIN_LIFECYCLE_INVALID"
			case "claim":
				queued, e := p.EnqueueRun(ctx, in.InstanceID, "page", nil)
				if e != nil {
					t.Fatal(e)
				}
				claimed, e := p.ClaimRun(ctx)
				if e != nil || claimed.ID != queued.ID {
					t.Fatal("claim fixture failed", e)
				}
				want = "PLUGIN_SNAPSHOT_STALE"
			default:
				_, err = p.SaveConfig(ctx, in.InstanceID, session.durable.Revision, json.RawMessage(`{"enable":true}`))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = host(formCtx, plugins.Identity{}, "", nil); pluginErrorCode(err) != want || calls != beforeCalls {
				t.Fatal("stale request reached the business callback", calls, err)
			}
			if _, err = host(formCtx, plugins.Identity{}, "", nil); pluginErrorCode(err) != want || calls != beforeCalls || pluginErrorCode(session.validateFinal(ctx)) != want {
				t.Fatal("ignored host denial was not sticky", calls, err)
			}
		})
	}
}

func TestPluginFormHostGuardAllowsCommittedWritesAndNoop(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "cold"
		if unknown {
			name = "unknown"
		}
		t.Run(name, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			if unknown {
				lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
			}
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			formCtx := withPluginFormSession(ctx, session)
			initial := pluginFormValueCopy(session.durable)
			step := 0
			host := session.wrapHost(func(callCtx context.Context, _ plugins.Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
				if callCtx != formCtx {
					t.Fatal("host context or deadline was replaced")
				}
				view, e := p.trustedPluginFormView(callCtx, in)
				if e != nil {
					return nil, e
				}
				// The fixture has MaxOpenConns(1). These reads/writes cannot proceed
				// if pre-host validation retains its read transaction across next.
				if _, e = p.Config(callCtx, in.InstanceID); e != nil {
					return nil, e
				}
				switch step {
				case 0:
					_, e = p.NormalizeInstanceDefaults(callCtx, in, view.durable.Revision, map[string]json.RawMessage{"dashboard_widget_size": json.RawMessage(`8`)})
				case 1:
					_, e = p.NormalizeInstanceDefaults(callCtx, in, view.durable.Revision, map[string]json.RawMessage{"dashboard_widget_size": json.RawMessage(`9`)})
				case 2:
					// Mechanical full-save path only, not an integrated Merge adapter
					// or a fabricated production projection permission.
					_, e = pluginFormMechanicalSave(t, p, callCtx, in, view.durable.Revision, view.durable.Value, func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error {
						inside, e := p.trustedPluginFormView(callCtx, in)
						if e != nil {
							return e
						}
						assertPluginFormSessionValue(t, inside.durable, view.durable)
						return nil
					})
				}
				step++
				return json.RawMessage(`{}`), e
			})
			for call, delta := range []int64{1, 1, 2, 2} {
				if _, err = host(formCtx, plugins.Identity{}, "", nil); err != nil {
					t.Fatal("own committed state rejected", call, err)
				}
				if session.durable.Revision != initial.Revision+delta || session.hostFailure != nil {
					t.Fatal("no-op or committed revision lost", call)
				}
			}
			if err = session.validateFinal(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPluginFormHostGuardFailedWriteRetainsCommittedAnchor(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	formCtx := withPluginFormSession(ctx, session)
	calls := 0
	host := session.wrapHost(func(callCtx context.Context, _ plugins.Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		_, err := pluginFormMechanicalSave(t, p, callCtx, in, before.Revision, json.RawMessage(`{"replacement":true}`), func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error {
			return errors.New("synthetic-private-projector-failure")
		})
		return nil, err
	})
	if _, err = host(formCtx, plugins.Identity{}, "", nil); err == nil {
		t.Fatal("failed projection succeeded")
	}
	assertPluginFormSessionValue(t, session.durable, before)
	assertPluginFormSessionValue(t, session.anchor.durable, before)
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
	if _, err = host(formCtx, plugins.Identity{}, "", nil); pluginErrorCode(err) != "PLUGIN_FORM_FAILED" || calls != 1 {
		t.Fatal("failed write was retried", err)
	}
	if pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_FORM_FAILED" {
		t.Fatal("rollback cleared failure")
	}
}

func TestPluginFormHostGuardUnknownErrorsRemainGeneric(t *testing.T) {
	for _, failure := range []string{"canceled", "closed-db", "callback", "callback-after-drift"} {
		t.Run(failure, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			formCtx := withPluginFormSession(ctx, session)
			callCtx := formCtx
			if failure == "canceled" {
				canceled, cancel := context.WithCancel(formCtx)
				cancel()
				callCtx = canceled
			}
			if failure == "closed-db" {
				if err = p.store.DB.Close(); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			host := session.wrapHost(func(context.Context, plugins.Identity, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				if failure == "callback-after-drift" {
					// The released precheck is not atomic with business reads. An
					// unknown later callback error must not be relabeled as conflict.
					if _, e := p.SaveConfig(ctx, in.InstanceID, session.durable.Revision, json.RawMessage(`{"enable":true}`)); e != nil {
						return nil, e
					}
				}
				return nil, errors.New("PLUGIN_REVISION_CONFLICT synthetic-private-detail")
			})
			if _, err = host(callCtx, plugins.Identity{}, "", nil); err == nil {
				t.Fatal("unknown failure accepted")
			}
			wantCalls := 0
			if strings.HasPrefix(failure, "callback") {
				wantCalls = 1
			}
			if calls != wantCalls || session.hostFailure == nil || session.hostFailure.Code != "PLUGIN_FORM_FAILED" || session.hostFailure.Message != "加载插件配置表单失败" || session.hostFailure.Status != 400 {
				t.Fatal("unknown error became trusted conflict or leaked details")
			}
			if _, err = host(formCtx, plugins.Identity{}, "", nil); pluginErrorCode(err) != "PLUGIN_FORM_FAILED" || calls != wantCalls {
				t.Fatal("original unknown failure was replaced", err)
			}
			if pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_FORM_FAILED" {
				t.Fatal("final check washed the first failure")
			}
		})
	}
}
