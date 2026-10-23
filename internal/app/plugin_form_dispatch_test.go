package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func pluginFormDispatchFixture(t *testing.T, manifest plugins.Manifest, mode pluginFormMode, clone bool) (*PluginStore, context.Context, PluginInstallation, *pluginFormSession) {
	t.Helper()
	if mode == pluginFormPreview {
		_, p := pluginStoreFixture(t)
		p.store.DB.SetMaxOpenConns(1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		raw, _ := json.Marshal(manifest)
		in := PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}
		session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstall)
		if err != nil {
			t.Fatal(err)
		}
		return p, withPluginFormSession(ctx, session), in, session
	}
	p, ctx, in := pluginFormSessionFixture(t, manifest, json.RawMessage(`{"enable":false,"opaque":{"kept":true}}`))
	if clone {
		var err error
		in, err = p.Clone(ctx, in.InstanceID, PluginCloneRequest{Suffix: "dispatch"})
		if err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case pluginFormInstalledCold:
	case pluginFormInstalledStarted:
		stored, err := p.Config(ctx, in.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		// A clone is deliberately paused. Use the real config-save activation
		// path before claiming a run; do not forge lifecycle manifest evidence.
		_, queued, err := p.SaveConfigAndQueue(ctx, in.InstanceID, stored.Revision, stored.Value)
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := p.ClaimRun(ctx)
		if err != nil || claimed.ID != queued.ID {
			t.Fatal("could not establish a real started generation", err)
		}
		if err = finishClaimedFixtureRun(ctx, p, claimed, "succeeded", "", nil); err != nil {
			t.Fatal(err)
		}
	case pluginFormInstalledUnknown:
		lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
	default:
		t.Fatal("invalid fixture mode")
	}
	in, err := p.Installation(ctx, in.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil || session.resolution.mode != mode {
		t.Fatal("fixture did not authenticate expected mode", err)
	}
	return p, withPluginFormSession(ctx, session), in, session
}

func pluginFormDispatchCallbacks(callback pluginFormCallback) pluginFormCallbacks {
	return pluginFormCallbacks{Preview: callback, Cold: callback, Started: callback, LegacyUnknown: callback}
}

func TestPluginFormDispatchSelectsAuthenticatedModeForCompiledInstances(t *testing.T) {
	for _, manifest := range []plugins.Manifest{builtin.SystemProcessManifest(), builtin.CategoryEditorManifest()} {
		for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
			for _, clone := range []bool{false, true} {
				if mode == pluginFormPreview && clone {
					continue // An uninstalled arbitrary clone has no authenticated identity.
				}
				t.Run(manifest.PluginID+"/"+pluginFormDispatchModeName(mode)+map[bool]string{false: "/source", true: "/clone"}[clone], func(t *testing.T) {
					p, ctx, in, session := pluginFormDispatchFixture(t, manifest, mode, clone)
					called := []pluginFormMode{}
					callback := func(selected pluginFormMode) pluginFormCallback {
						return func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
							called = append(called, selected)
							if view.mode != selected || view.installation.InstanceID != in.InstanceID || view.installation.PluginID != manifest.PluginID {
								t.Fatal("callback did not receive authenticated target")
							}
							deadline, ok := callCtx.Deadline()
							if !ok || deadline.After(session.deadline) {
								t.Fatal("callback lost its bounded request budget")
							}
							// One available SQL connection proves no transaction spans the hook.
							var n int
							if err := p.store.DB.QueryRowContext(callCtx, `SELECT 1`).Scan(&n); err != nil {
								return nil, err
							}
							view.durable.Value[0], view.display.Value[0], view.installation.Manifest[0] = '!', '!', '!'
							view.mode, view.configExists = 0, false
							return json.RawMessage(`{"selected":true}`), nil
						}
					}
					before := pluginFormValueCopy(session.durable)
					callbacks := pluginFormCallbacks{Preview: callback(pluginFormPreview), Cold: callback(pluginFormInstalledCold), Started: callback(pluginFormInstalledStarted), LegacyUnknown: callback(pluginFormInstalledUnknown)}
					result, err := p.dispatchPluginForm(ctx, in, callbacks)
					if err != nil || string(result) != `{"selected":true}` || len(called) != 1 || called[0] != mode {
						t.Fatal("wrong dispatch", called, err)
					}
					assertPluginFormSessionValue(t, session.durable, before)
					if session.resolution.mode != mode || session.resolution.installation.InstanceID != in.InstanceID {
						t.Fatal("callback rewrote its mode or identity")
					}
				})
			}
		}
	}
}

func pluginFormDispatchModeName(mode pluginFormMode) string {
	return map[pluginFormMode]string{pluginFormPreview: "preview", pluginFormInstalledCold: "cold", pluginFormInstalledStarted: "started", pluginFormInstalledUnknown: "unknown"}[mode]
}

func TestPluginFormDispatchMissingHookNeverFallsBack(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		t.Run(pluginFormDispatchModeName(mode), func(t *testing.T) {
			p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
			calls := 0
			callbacks := pluginFormDispatchCallbacks(func(context.Context, pluginFormView) (json.RawMessage, error) { calls++; return nil, nil })
			switch mode {
			case pluginFormPreview:
				callbacks.Preview = nil
			case pluginFormInstalledCold:
				callbacks.Cold = nil
			case pluginFormInstalledStarted:
				callbacks.Started = nil
			case pluginFormInstalledUnknown:
				callbacks.LegacyUnknown = nil
			}
			if result, err := p.dispatchPluginForm(ctx, in, callbacks); result != nil || pluginErrorCode(err) != "PLUGIN_FORM_CALLBACK_UNSUPPORTED" || calls != 0 {
				t.Fatal("missing callback fell back", err)
			}
			if pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_FORM_CALLBACK_UNSUPPORTED" {
				t.Fatal("missing callback was not sticky")
			}
		})
	}
}

func TestPluginFormDispatchReadOnlyCommonWritesDeniedBeforeEffects(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold} {
		for _, operation := range []string{"defaults", "noop-defaults", "defaults-projector", "projection", "transition", "config", "data", "transactional-data", "queue", "enqueue", "claim", "finish", "install", "install-queue", "clone", "reset", "uninstall"} {
			t.Run(pluginFormDispatchModeName(mode)+"/"+operation, func(t *testing.T) {
				p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
				before := pluginFormValueCopy(session.durable)
				var prepareCalls, projectorCalls int
				projector := func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error {
					projectorCalls++
					return nil
				}
				_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
					var denied error
					switch operation {
					case "defaults":
						_, denied = p.NormalizeInstanceDefaults(callCtx, view.installation, view.durable.Revision, map[string]json.RawMessage{"new": json.RawMessage(`false`)})
					case "noop-defaults":
						_, denied = p.NormalizeInstanceDefaults(callCtx, view.installation, view.durable.Revision, nil)
					case "defaults-projector":
						_, denied = p.NormalizeInstanceDefaultsWithProjection(callCtx, view.installation, view.durable.Revision, map[string]json.RawMessage{"new": json.RawMessage(`false`)}, projector)
					case "projection":
						_, denied = p.ProjectInstanceConfig(callCtx, view.installation, view.durable.Revision, json.RawMessage(`{}`), projector)
					case "transition":
						_, denied = p.transitionInstanceConfig(callCtx, view.installation, view.durable.Revision, pluginConfigDefaultsPure, func(map[string]json.RawMessage) (any, bool) { prepareCalls++; return map[string]any{}, true }, projector)
					case "config":
						_, denied = p.SaveConfig(callCtx, in.InstanceID, view.durable.Revision, json.RawMessage(`{}`))
					case "data":
						_, denied = p.SaveData(callCtx, in.InstanceID, "guard", 0, json.RawMessage(`{}`))
					case "transactional-data":
						tx, e := p.store.DB.BeginTx(callCtx, nil)
						if e != nil {
							return nil, e
						}
						defer tx.Rollback()
						_, denied = p.saveValueTx(callCtx, tx, in.InstanceID, "data", "guard", 0, json.RawMessage(`{}`))
					case "queue":
						_, _, denied = p.SaveConfigAndQueue(callCtx, in.InstanceID, view.durable.Revision, json.RawMessage(`{}`))
					case "enqueue":
						_, denied = p.EnqueueRun(callCtx, in.InstanceID, "init", nil)
					case "claim":
						_, denied = p.ClaimRun(callCtx)
					case "finish":
						denied = p.FinishRun(callCtx, "not-a-run", "succeeded", "", nil)
					case "install":
						denied = p.Install(callCtx, in)
					case "install-queue":
						_, denied = p.InstallAndQueue(callCtx, in, json.RawMessage(`{}`))
					case "clone":
						_, denied = p.Clone(callCtx, in.InstanceID, PluginCloneRequest{Suffix: "forbidden"})
					case "reset":
						denied = p.Reset(callCtx, in.InstanceID)
					case "uninstall":
						denied = p.Uninstall(callCtx, in.InstanceID)
					}
					if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
						t.Fatal("read-only common API did not deny early", operation, denied)
					}
					return json.RawMessage(`{}`), nil // A callback cannot hide the attempted write.
				}))
				if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || prepareCalls != 0 || projectorCalls != 0 {
					t.Fatal("denial ran a prepare/projector or was swallowed", err)
				}
				assertPluginFormSessionValue(t, session.durable, before)
				if _, err = p.Data(context.Background(), in.InstanceID, "guard"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("read-only callback persisted KV", err)
				}
				current, e := p.pluginFormAnchor(context.Background(), session.requested, session.origin)
				if e != nil || comparePluginFormAnchors(session.anchor, current) != nil {
					t.Fatal("read-only callback changed durable config or lifecycle", e)
				}
			})
		}
	}
}

func TestPluginFormDispatchWarmNineMechanicalSavesAdvanceTrustedAnchor(t *testing.T) {
	p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, true)
	initial := pluginFormValueCopy(session.durable)
	journal, err := p.SaveData(context.Background(), in.InstanceID, "dispatch-journal", 0, json.RawMessage(`{"receipt":"original"}`))
	if err != nil {
		t.Fatal(err)
	}
	projectorCalls := 0
	var budget time.Time
	for step := 0; step < 9; step++ {
		_, err = p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
			if view.durable.Revision != initial.Revision+int64(step) {
				t.Fatal("prior committed anchor was lost")
			}
			deadline, _ := callCtx.Deadline()
			if step == 0 {
				budget = deadline
			} else if !deadline.Equal(budget) {
				t.Fatal("callback renewed the total budget")
			}
			// These real declarations allow a missing-only no-op; it must not
			// create a write, invoke a projector or reset the captured display.
			noop, e := p.NormalizeInstanceDefaults(callCtx, view.installation, view.durable.Revision, map[string]json.RawMessage{"enable": json.RawMessage(`true`)})
			if e != nil {
				return nil, e
			}
			assertPluginFormSessionValue(t, noop, view.durable)
			// Mechanical common-transition exercise, not a fabricated public
			// Form declaration or evidence of an integrated business adapter.
			written, e := pluginFormMechanicalSave(t, p, callCtx, view.installation, view.durable.Revision, view.durable.Value, func(projectCtx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
				projectorCalls++
				if projectCtx.Value(pluginFormConfigWriteKey{}) != nil {
					t.Fatal("internal config-write capability escaped to business projector")
				}
				before, e := p.trustedPluginFormView(projectCtx, in)
				if e != nil {
					return e
				}
				assertPluginFormSessionValue(t, before.durable, view.durable)
				if change.Previous.Revision != view.durable.Revision || change.Next.Revision != view.durable.Revision+1 {
					t.Fatal("projection did not receive exact preimage")
				}
				journal, e = tx.SaveData(projectCtx, "dispatch-journal", journal.Revision, json.RawMessage(`{"receipt":"original"}`))
				return e
			})
			if e != nil {
				return nil, e
			}
			committed, e := p.trustedPluginFormView(callCtx, in)
			if e != nil {
				return nil, e
			}
			assertPluginFormSessionValue(t, committed.durable, written)
			assertPluginFormSessionValue(t, committed.display, written)
			return json.RawMessage(`{}`), nil
		}})
		if err != nil {
			t.Fatal("same-session source save rejected", step, err)
		}
	}
	if projectorCalls != 9 || session.durable.Revision != initial.Revision+9 || journal.Revision != 10 {
		t.Fatal("source Save, including equal values, did not commit nine times")
	}
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal(err)
	}
	// Same-instance warm KV remains usable outside a config projector too.
	_, err = p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
		_, e := p.SaveData(callCtx, in.InstanceID, "warm-data", 0, json.RawMessage(`{"ok":true}`))
		return json.RawMessage(`{}`), e
	}})
	if err != nil {
		t.Fatal("same-instance warm KV denied", err)
	}
}

func TestPluginFormDispatchWarmCannotBypassConfigProjection(t *testing.T) {
	for _, operation := range []string{"config", "queue", "transactional-config", "undeclared-public-projection"} {
		t.Run(operation, func(t *testing.T) {
			p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
			before := pluginFormValueCopy(session.durable)
			want := "PLUGIN_CAPABILITY_DENIED"
			if operation == "undeclared-public-projection" {
				want = "PLUGIN_FORM_PROJECTION_UNSUPPORTED"
			}
			projectorCalls := 0
			_, err := p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
				var e error
				switch operation {
				case "config":
					_, e = p.SaveConfig(callCtx, in.InstanceID, view.durable.Revision, view.durable.Value)
				case "queue":
					_, _, e = p.SaveConfigAndQueue(callCtx, in.InstanceID, view.durable.Revision, view.durable.Value)
				case "transactional-config":
					tx, txErr := p.store.DB.BeginTx(callCtx, nil)
					if txErr != nil {
						return nil, txErr
					}
					defer tx.Rollback()
					_, e = p.saveValueTx(callCtx, tx, in.InstanceID, "config", "main", view.durable.Revision, view.durable.Value)
				case "undeclared-public-projection":
					_, e = p.ProjectInstanceConfig(callCtx, in, view.durable.Revision, view.durable.Value, func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error {
						projectorCalls++
						return nil
					})
				}
				return nil, e
			}})
			if pluginErrorCode(err) != want || projectorCalls != 0 {
				t.Fatal("warm bypass accepted", err)
			}
			assertPluginFormSessionValue(t, session.durable, before)
			current, err := p.pluginFormAnchor(context.Background(), session.requested, session.origin)
			if err != nil || comparePluginFormAnchors(session.anchor, current) != nil {
				t.Fatal("failed bypass changed durable state", err)
			}
		})
	}
}

func TestPluginFormDispatchRejectsCrossScopeAndNestedSessions(t *testing.T) {
	for _, operation := range []string{"other-instance-data", "other-store-data", "other-store-dispatch", "other-instance-dispatch", "nested-same", "nested-instance", "nested-store", "rebound-session"} {
		t.Run(operation, func(t *testing.T) {
			p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
			deadline, hasDeadline := ctx.Deadline()
			phaseStart := deadline.Add(-5 * time.Second)
			statsStart := p.store.DB.Stats()
			callbackEntered := false
			phase := func(name string) {
				stats := p.store.DB.Stats()
				t.Logf("owned phase=%s request_deadline_present=%t elapsed_from_original_ctx=%s remaining=%s callback_entered=%t primary_wait_count_delta=%d primary_wait_duration_delta=%s", name, hasDeadline, time.Since(phaseStart), time.Until(deadline), callbackEntered, stats.WaitCount-statsStart.WaitCount, stats.WaitDuration-statsStart.WaitDuration)
			}
			phase("fixture-complete")
			_, other := pluginStoreFixture(t)
			phase("other-store-created")
			installCloneSource(t, other, builtin.SystemProcessManifest())
			phase("other-install-complete")
			otherIn, err := other.Installation(context.Background(), in.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			otherSession, err := other.beginPluginFormSession(context.Background(), otherIn, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			phase("other-session-complete")
			clone, err := p.Clone(context.Background(), in.InstanceID, PluginCloneRequest{Suffix: "other"})
			if err != nil {
				t.Fatal(err)
			}
			phase("clone-complete")
			// The second database, its installation/session and the source clone
			// are fixture preparation, not part of a new Form request.
			requestCtx, requestCancel := context.WithTimeout(context.Background(), 5*time.Second)
			t.Cleanup(requestCancel)
			requestSession, requestErr := p.beginPluginFormSession(requestCtx, in, pluginFormOriginInstalled)
			if requestErr != nil || requestSession.resolution.mode != pluginFormInstalledStarted {
				t.Fatal("fresh request did not authenticate started fixture", requestErr)
			}
			ctx = withPluginFormSession(requestCtx, requestSession)
			deadline, hasDeadline = requestCtx.Deadline()
			phaseStart = deadline.Add(-5 * time.Second)
			statsStart = p.store.DB.Stats()
			phase("fresh-request-ready")
			nestedCalls := 0
			nested := pluginFormDispatchCallbacks(func(context.Context, pluginFormView) (json.RawMessage, error) {
				nestedCalls++
				return json.RawMessage(`{}`), nil
			})
			phase("before-dispatch")
			_, err = p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
				callbackEntered = true
				phase("callback-entered")
				var denied error
				switch operation {
				case "other-instance-data":
					_, denied = p.SaveData(callCtx, clone.InstanceID, "guard", 0, json.RawMessage(`{}`))
				case "other-store-data":
					_, denied = other.SaveData(callCtx, otherIn.InstanceID, "guard", 0, json.RawMessage(`{}`))
				case "other-store-dispatch":
					_, denied = other.dispatchPluginForm(callCtx, otherIn, nested)
				case "other-instance-dispatch":
					_, denied = p.dispatchPluginForm(callCtx, clone, nested)
				case "nested-same":
					_, denied = p.beginPluginFormSession(callCtx, in, pluginFormOriginInstalled)
				case "nested-instance":
					_, denied = p.beginPluginFormSession(callCtx, clone, pluginFormOriginInstalled)
				case "nested-store":
					_, denied = other.beginPluginFormSession(callCtx, otherIn, pluginFormOriginInstalled)
				case "rebound-session":
					_, denied = other.dispatchPluginForm(withPluginFormSession(callCtx, otherSession), otherIn, nested)
				}
				if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
					t.Fatal("nested/cross scope not denied", denied)
				}
				return json.RawMessage(`{}`), nil
			}})
			phase("dispatch-return")
			if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || nestedCalls != 0 || !callbackEntered {
				t.Fatal("cross-scope attempt was hidden", err)
			}
			for _, target := range []struct {
				store *PluginStore
				id    string
			}{{p, clone.InstanceID}, {other, otherIn.InstanceID}} {
				if _, err := target.store.Data(context.Background(), target.id, "guard"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("cross-scope KV changed", err)
				}
			}
		})
	}
}

func TestPluginFormDispatchBusinessRestorationFailureNeverInitializes(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, damaged := range []bool{false, true} {
			t.Run(pluginFormDispatchModeName(mode)+map[bool]string{false: "/missing", true: "/damaged"}[damaged], func(t *testing.T) {
				p, ctx, in, session := pluginFormDispatchFixture(t, builtin.CategoryEditorManifest(), mode, true)
				before := pluginFormValueCopy(session.durable)
				if damaged {
					if _, err := p.SaveData(context.Background(), in.InstanceID, "mechanical-owner", 0, json.RawMessage(`{"damaged":true}`)); err != nil {
						t.Fatal(err)
					}
				}
				calls := 0
				// This is a mechanical strict-restoration hook, not CategoryEditor
				// business journal validation. The dispatcher never repairs it.
				callback := func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
					calls++
					_, err := p.Data(callCtx, in.InstanceID, "mechanical-owner")
					if damaged && err != nil {
						t.Fatal(err)
					}
					if !damaged && !errors.Is(err, sql.ErrNoRows) {
						t.Fatal("fixture expected missing owner", err)
					}
					return nil, errors.New("private mechanical owner-state integrity failure")
				}
				result, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(callback))
				if result != nil || pluginErrorCode(err) != "PLUGIN_FORM_FAILED" || strings.Contains(err.Error(), "private") || calls != 1 {
					t.Fatal("restoration failure leaked or fell back", err)
				}
				assertPluginFormSessionValue(t, session.durable, before)
				stored, err := p.Data(context.Background(), in.InstanceID, "mechanical-owner")
				if damaged && (err != nil || stored.Revision != 1 || string(stored.Value) != `{"damaged":true}`) {
					t.Fatal("damaged owner was overwritten", err)
				}
				if !damaged && !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("missing owner was fabricated", err)
				}
			})
		}
	}
}

func TestPluginFormDispatchDriftChecksSurroundBusinessCallback(t *testing.T) {
	for _, drift := range []string{"before-config", "before-mode", "before-reset", "after-config", "before-data-save"} {
		t.Run(drift, func(t *testing.T) {
			mode := pluginFormInstalledStarted
			if drift == "before-mode" {
				mode = pluginFormInstalledCold
			}
			p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
			before := pluginFormValueCopy(session.durable)
			edit := func() {
				if _, err := p.SaveConfig(context.Background(), in.InstanceID, before.Revision, json.RawMessage(`{"external":true}`)); err != nil {
					t.Fatal(err)
				}
			}
			want := "PLUGIN_REVISION_CONFLICT"
			switch drift {
			case "before-config":
				edit()
			case "before-mode":
				if _, err := p.EnqueueRun(context.Background(), in.InstanceID, "page", nil); err != nil {
					t.Fatal(err)
				}
				if _, err := p.ClaimRun(context.Background()); err != nil {
					t.Fatal(err)
				}
				want = "PLUGIN_SNAPSHOT_STALE"
			case "before-reset":
				if err := p.Reset(context.Background(), in.InstanceID); err != nil {
					t.Fatal(err)
				}
				want = "PLUGIN_LIFECYCLE_INVALID"
			}
			calls := 0
			_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
				calls++
				if drift == "after-config" || drift == "before-data-save" {
					edit()
				}
				if drift == "before-data-save" {
					_, err := p.SaveData(callCtx, in.InstanceID, "drift-guard", 0, json.RawMessage(`{}`))
					if pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
						t.Fatal("KV write lacked same-TX anchor check", err)
					}
				}
				return json.RawMessage(`{}`), nil
			}))
			wantCalls := 0
			if drift == "after-config" || drift == "before-data-save" {
				wantCalls = 1
			}
			if pluginErrorCode(err) != want || calls != wantCalls {
				t.Fatal("drift admitted business work or response", err, calls)
			}
			assertPluginFormSessionValue(t, session.durable, before)
			if _, err := p.Data(context.Background(), in.InstanceID, "drift-guard"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("stale callback wrote data", err)
			}
		})
	}
}

func TestPluginFormDispatchCancellationAndResultOwnership(t *testing.T) {
	for _, control := range []string{"canceled-before", "expired-before", "canceled-during", "shorter-deadline", "result-copy"} {
		t.Run(control, func(t *testing.T) {
			p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
			callCtx := ctx
			cancel := func() {}
			var wantedDeadline time.Time
			switch control {
			case "canceled-before", "canceled-during":
				callCtx, cancel = context.WithCancel(ctx)
				if control == "canceled-before" {
					cancel()
				}
			case "expired-before":
				callCtx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			case "shorter-deadline":
				wantedDeadline = time.Now().Add(time.Second)
				callCtx, cancel = context.WithDeadline(ctx, wantedDeadline)
			}
			defer cancel()
			calls := 0
			output := json.RawMessage(`{"copied":true}`)
			result, err := p.dispatchPluginForm(callCtx, in, pluginFormCallbacks{Started: func(bounded context.Context, view pluginFormView) (json.RawMessage, error) {
				calls++
				if control == "canceled-during" {
					cancel()
				}
				if control == "shorter-deadline" {
					deadline, ok := bounded.Deadline()
					if !ok || !deadline.Equal(wantedDeadline) || deadline.After(session.deadline) {
						t.Fatal("earlier caller deadline was extended")
					}
				}
				return output, nil
			}})
			switch control {
			case "canceled-before", "canceled-during":
				if !errors.Is(err, context.Canceled) || result != nil {
					t.Fatal("cancellation was lost", err)
				}
			case "expired-before":
				if !errors.Is(err, context.DeadlineExceeded) || result != nil {
					t.Fatal("expired request was admitted", err)
				}
			default:
				if err != nil || !bytes.Equal(result, output) {
					t.Fatal(err)
				}
				output[0] = '!'
				if !json.Valid(result) {
					t.Fatal("callback retained alias of returned JSON")
				}
			}
			wantCalls := 1
			if control == "canceled-before" || control == "expired-before" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("canceled callback was invoked")
			}
		})
	}
}

func TestPluginFormDispatchScopeFreeColdDefaultsRemainCompatible(t *testing.T) {
	p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledCold, false)
	before := pluginFormValueCopy(session.durable)
	value, err := p.NormalizeInstanceDefaults(ctx, in, before.Revision, map[string]json.RawMessage{"dispatch_default": json.RawMessage(`false`)})
	if err != nil || value.Revision != before.Revision+1 {
		t.Fatal("dispatcher broadened read-only to all cold sessions", err)
	}
	assertPluginFormSessionValue(t, session.durable, value)
	noop, err := p.NormalizeInstanceDefaults(ctx, in, value.Revision, map[string]json.RawMessage{"dispatch_default": json.RawMessage(`true`)})
	if err != nil {
		t.Fatal(err)
	}
	assertPluginFormSessionValue(t, noop, value)
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPluginFormDispatchIgnoredProjectorDenialRollsBack(t *testing.T) {
	p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	_, err := p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
		_, e := pluginFormMechanicalSave(t, p, callCtx, in, view.durable.Revision, json.RawMessage(`{"replacement":true}`), func(projectCtx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
			if _, e := tx.SaveData(projectCtx, "rollback-journal", 0, json.RawMessage(`{}`)); e != nil {
				return e
			}
			_, denied := p.SaveConfig(projectCtx, in.InstanceID, change.Next.Revision, json.RawMessage(`{"bypass":true}`))
			if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
				t.Fatal("projector inherited a config bypass", denied)
			}
			return nil // Deliberately ignore denial; the transaction must still fail.
		})
		return nil, e
	}})
	if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("ignored projector denial committed", err)
	}
	assertPluginFormSessionValue(t, session.durable, before)
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
	if _, err = p.Data(context.Background(), in.InstanceID, "rollback-journal"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed projector KV committed", err)
	}
}
