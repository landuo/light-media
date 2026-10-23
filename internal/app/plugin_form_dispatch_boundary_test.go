package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginFormDispatchTransactionLifecycleHelpersDenied(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormInstalledCold, pluginFormInstalledStarted} {
		for _, operation := range []string{"install", "enqueue"} {
			t.Run(pluginFormDispatchModeName(mode)+"/"+operation, func(t *testing.T) {
				p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
				var runsBefore int
				if err := p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, in.InstanceID).Scan(&runsBefore); err != nil {
					t.Fatal(err)
				}
				_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
					tx, err := p.store.DB.BeginTx(callCtx, nil)
					if err != nil {
						return nil, err
					}
					defer tx.Rollback()
					if operation == "install" {
						err = p.installTx(callCtx, tx, view.installation)
					} else {
						_, err = p.enqueueRunTx(callCtx, tx, in.InstanceID, "page", nil)
					}
					if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
						t.Fatal("common transaction helper bypassed lifecycle guard", err)
					}
					// Committing after ignored denial must still persist no effects.
					if err = tx.Commit(); err != nil {
						return nil, err
					}
					return json.RawMessage(`{}`), nil
				}))
				if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
					t.Fatal("ignored helper denial was not sticky", err)
				}
				current, err := p.pluginFormAnchor(context.Background(), session.requested, session.origin)
				if err != nil || comparePluginFormAnchors(session.anchor, current) != nil {
					t.Fatal("transaction helper changed installation/config", err)
				}
				var runsAfter int
				if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, in.InstanceID).Scan(&runsAfter); err != nil || runsAfter != runsBefore {
					t.Fatal("transaction helper queued work", runsAfter, runsBefore, err)
				}
			})
		}
	}
}

func TestPluginFormDispatchScopeFreeTransactionLifecycleHelpersRemainLegal(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	manifest := builtin.SystemProcessManifest()
	raw, _ := json.Marshal(manifest)
	in, err := validatePluginInstallation(PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = p.installTx(ctx, tx, in); err != nil {
		t.Fatal("scope-free install helper denied", err)
	}
	if _, err = p.saveValueTx(ctx, tx, in.InstanceID, "config", "main", 0, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	queued, err := p.enqueueRunTx(ctx, tx, in.InstanceID, "page", nil)
	if err != nil {
		t.Fatal("scope-free enqueue helper denied", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stored, err := p.Run(ctx, queued.ID)
	if err != nil || stored.State != "pending" || stored.InstanceID != in.InstanceID {
		t.Fatal("scope-free helper effects were lost", err)
	}
}

type pluginFormDispatchMarshalProbe struct{ calls *int }

func (p pluginFormDispatchMarshalProbe) MarshalJSON() ([]byte, error) {
	*p.calls++
	return []byte(`{"probe":true}`), nil
}

func TestPluginFormDispatchTransactionWrapperChecksScopeBeforeSerialization(t *testing.T) {
	for _, target := range []string{"cold", "foreign-instance", "foreign-store"} {
		for _, input := range []string{"marshaler", "invalid-value"} {
			t.Run(target+"/"+input, func(t *testing.T) {
				mode := pluginFormInstalledStarted
				if target == "cold" {
					mode = pluginFormInstalledCold
				}
				p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
				boundStore, boundInstance := p, in.InstanceID
				if target == "foreign-instance" {
					clone, err := p.Clone(context.Background(), in.InstanceID, PluginCloneRequest{Suffix: "foreign"})
					if err != nil {
						t.Fatal(err)
					}
					boundInstance = clone.InstanceID
				}
				if target == "foreign-store" {
					_, boundStore = pluginStoreFixture(t)
					installCloneSource(t, boundStore, builtin.SystemProcessManifest())
				}
				marshalCalls := 0
				var value any = pluginFormDispatchMarshalProbe{calls: &marshalCalls}
				if input == "invalid-value" {
					value = make(chan int)
				}
				_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
					tx, err := boundStore.store.DB.BeginTx(callCtx, nil)
					if err != nil {
						return nil, err
					}
					defer tx.Rollback()
					wrapper := pluginProjectionWrapperForTest(t, callCtx, tx, boundStore, boundInstance)
					_, err = wrapper.SaveData(callCtx, "marshal-guard", 0, value)
					if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || marshalCalls != 0 {
						t.Fatal("serialization ran before scope denial", marshalCalls, err)
					}
					if err = tx.Commit(); err != nil {
						return nil, err
					}
					return json.RawMessage(`{}`), nil
				}))
				if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || marshalCalls != 0 {
					t.Fatal("serialization denial was not sticky", err)
				}
				if _, err = boundStore.Data(context.Background(), boundInstance, "marshal-guard"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("denied wrapper persisted KV", err)
				}
			})
		}
	}
}

func TestPluginFormDispatchTransactionDataHelpersRecheckAnchor(t *testing.T) {
	for _, operation := range []string{"bare-helper", "transaction-wrapper"} {
		for _, drift := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/unchanged", true: "/drift"}[drift], func(t *testing.T) {
				p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
				_, err := p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
					if drift {
						if _, e := p.SaveConfig(context.Background(), in.InstanceID, view.durable.Revision, json.RawMessage(`{"external":true}`)); e != nil {
							t.Fatal(e)
						}
					}
					tx, err := p.store.DB.BeginTx(callCtx, nil)
					if err != nil {
						return nil, err
					}
					defer tx.Rollback()
					if operation == "bare-helper" {
						_, err = p.saveValueTx(callCtx, tx, in.InstanceID, "data", "tx-data", 0, json.RawMessage(`{}`))
					} else {
						_, err = (pluginProjectionWrapperForTest(t, callCtx, tx, p, in.InstanceID)).SaveData(callCtx, "tx-data", 0, json.RawMessage(`{}`))
					}
					if drift && pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
						t.Fatal("unadmitted transaction lacked anchor check", err)
					}
					if !drift && err != nil {
						t.Fatal("unchanged warm transaction denied", err)
					}
					// Explicitly commit after denied write: final validation is not
					// being relied on to undo a previously persisted data effect.
					if err = tx.Commit(); err != nil {
						return nil, err
					}
					return json.RawMessage(`{}`), nil
				}})
				if drift && pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
					t.Fatal("drift denial was hidden", err)
				}
				if !drift && err != nil {
					t.Fatal(err)
				}
				stored, err := p.Data(context.Background(), in.InstanceID, "tx-data")
				if drift && !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("stale helper committed data before final rejection", err)
				}
				if !drift && (err != nil || stored.Revision != 1) {
					t.Fatal("warm data helper did not commit", err)
				}
				if !drift {
					if err = session.validateFinal(ctx); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestPluginFormDispatchInitialFailuresAreSticky(t *testing.T) {
	for _, failure := range []string{"identity", "other-store", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
			firstCtx, firstStore, requested := ctx, p, in
			want := "PLUGIN_CAPABILITY_DENIED"
			switch failure {
			case "identity":
				requested.InstanceID += "wrong"
			case "other-store":
				_, firstStore = pluginStoreFixture(t)
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				firstCtx, want = canceled, "PLUGIN_FORM_FAILED"
			}
			calls := 0
			callbacks := pluginFormDispatchCallbacks(func(context.Context, pluginFormView) (json.RawMessage, error) {
				calls++
				return json.RawMessage(`{}`), nil
			})
			_, err := firstStore.dispatchPluginForm(firstCtx, requested, callbacks)
			if failure == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation identity lost", err)
				}
			} else if pluginErrorCode(err) != want {
				t.Fatal("entry validation did not fail", err)
			}
			_, err = p.dispatchPluginForm(ctx, in, callbacks)
			if pluginErrorCode(err) != want || calls != 0 || pluginErrorCode(session.validateFinal(ctx)) != want {
				t.Fatal("clean retry hid initial dispatcher error", err, calls)
			}
		})
	}
}

func TestPluginFormDispatchPanicIsRedactedAndSticky(t *testing.T) {
	for _, payload := range []string{"text", "typed-error", "nil"} {
		t.Run(payload, func(t *testing.T) {
			p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
			before := pluginFormValueCopy(session.durable)
			callbacks := pluginFormCallbacks{Started: func(context.Context, pluginFormView) (json.RawMessage, error) {
				switch payload {
				case "typed-error":
					panic(bad("PLUGIN_REVISION_CONFLICT", "private-panic-payload"))
				case "nil":
					panic(nil)
				default:
					panic("private-panic-payload")
				}
			}}
			var result json.RawMessage
			var err error
			escaped := false
			func() {
				defer func() {
					if recover() != nil {
						escaped = true
					}
				}()
				result, err = p.dispatchPluginForm(ctx, in, callbacks)
			}()
			if escaped || result != nil || pluginErrorCode(err) != "PLUGIN_FORM_FAILED" || strings.Contains(err.Error(), "private-panic") {
				t.Fatal("panic escaped or was misclassified", escaped, err)
			}
			calls := 0
			_, err = p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(context.Context, pluginFormView) (json.RawMessage, error) {
				calls++
				return json.RawMessage(`{}`), nil
			}})
			if pluginErrorCode(err) != "PLUGIN_FORM_FAILED" || calls != 0 {
				t.Fatal("panic could be hidden by recovery and retry", err)
			}
			assertPluginFormSessionValue(t, session.durable, before)
		})
	}
}

func TestPluginFormDispatchAdmittedProjectorDataProofStaysPrivate(t *testing.T) {
	p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
	before := pluginFormValueCopy(session.durable)
	marshalCalls := 0
	_, err := p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
		_, err := pluginFormMechanicalSave(t, p, callCtx, in, view.durable.Revision, json.RawMessage(`{"replacement":true}`), func(projectCtx context.Context, transaction PluginDefaultsTransaction, change PluginDefaultsChange) error {
			if projectCtx.Value(pluginFormDataWriteKey{}) != nil || projectCtx.Value(pluginFormConfigWriteKey{}) != nil {
				t.Fatal("transaction capability exposed in projector context")
			}
			if change.Next.Revision != before.Revision+1 || string(change.Next.Value) != `{"replacement":true}` {
				t.Fatal("fixture did not change config before journal write")
			}
			_, err := transaction.SaveData(projectCtx, "admitted-journal", 0, pluginFormDispatchMarshalProbe{calls: &marshalCalls})
			return err
		})
		return json.RawMessage(`{}`), err
	}})
	if err != nil || marshalCalls != 1 {
		t.Fatal("admitted same-TX journal projection denied", marshalCalls, err)
	}
	if session.durable.Revision != before.Revision+1 || string(session.durable.Value) != `{"replacement":true}` {
		t.Fatal("config anchor failed to advance")
	}
	stored, err := p.Data(context.Background(), in.InstanceID, "admitted-journal")
	if err != nil || stored.Revision != 1 || string(stored.Value) != `{"probe":true}` {
		t.Fatal("journal did not commit with new config", err)
	}
}

func TestPluginFormDispatchDataProofRejectsWrongBindingAndConfig(t *testing.T) {
	for _, mismatch := range []string{"store", "transaction", "instance", "session", "config"} {
		t.Run(mismatch, func(t *testing.T) {
			p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
			_, other := pluginStoreFixture(t)
			_, err := p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
				tx, err := p.store.DB.BeginTx(callCtx, nil)
				if err != nil {
					return nil, err
				}
				defer tx.Rollback()
				// Mechanical forged-token controls verify exact binding, not a
				// public way for callbacks to acquire an admission capability.
				permit := pluginFormDataWrite{store: p, tx: tx, instance: in.InstanceID, session: session}
				switch mismatch {
				case "store":
					permit.store = other
				case "transaction":
					permit.tx = nil
				case "instance":
					permit.instance += "other"
				case "session":
					permit.session = &pluginFormSession{}
				}
				writeCtx := context.WithValue(callCtx, pluginFormDataWriteKey{}, permit)
				if mismatch == "config" {
					_, err = p.saveValueTx(writeCtx, tx, in.InstanceID, "config", "main", view.durable.Revision, json.RawMessage(`{"bypass":true}`))
				} else {
					_, err = p.saveValueTx(writeCtx, tx, in.InstanceID, "data", "proof-guard", 0, json.RawMessage(`{}`))
				}
				if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
					t.Fatal("mismatched/data-only proof granted write", err)
				}
				if err = tx.Commit(); err != nil {
					return nil, err
				}
				return json.RawMessage(`{}`), nil
			}})
			if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
				t.Fatal("proof denial lost", err)
			}
			if _, err = p.Data(context.Background(), in.InstanceID, "proof-guard"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("wrong proof wrote KV", err)
			}
			current, err := p.pluginFormAnchor(context.Background(), session.requested, session.origin)
			if err != nil || comparePluginFormAnchors(session.anchor, current) != nil {
				t.Fatal("data proof rewrote config", err)
			}
		})
	}
}

func TestPluginFormDispatchScopeFreeTransactionWrapperStillSerializes(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{}`))
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	calls := 0
	_, err = (pluginProjectionWrapperForTest(t, ctx, tx, p, in.InstanceID)).SaveData(ctx, "scope-free-wrapper", 0, pluginFormDispatchMarshalProbe{calls: &calls})
	if err != nil || calls != 1 {
		t.Fatal("scope-free wrapper serialization changed", calls, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stored, err := p.Data(ctx, in.InstanceID, "scope-free-wrapper")
	if err != nil || stored.Revision != 1 {
		t.Fatal("scope-free wrapper failed to persist", err)
	}
}

func TestPluginFormDispatchNestedBeginLatchesOwningScope(t *testing.T) {
	p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
	other, otherCtx, otherIn, otherSession := pluginFormDispatchFixture(t, builtin.CategoryEditorManifest(), pluginFormInstalledCold, false)
	_, err := p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Started: func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
		_, denied := other.beginPluginFormSession(withPluginFormSession(callCtx, otherSession), otherIn, pluginFormOriginInstalled)
		if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
			t.Fatal("rebound session admitted nested begin", denied)
		}
		return json.RawMessage(`{}`), nil
	}})
	if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("nested begin failed to latch owning callback scope", err)
	}
	if err = otherSession.validateFinal(otherCtx); err != nil {
		t.Fatal("owning denial poisoned unrelated foreign session", err)
	}
}
