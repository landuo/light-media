package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/schedule"
)

// All tests in this file use production symbols already present in the frozen
// preimage. The entire file can therefore be overlaid tests-only for the red
// run. These are common-boundary fixtures, not business-owner integrations.
func pluginFormPropagationRows(t *testing.T, p *PluginStore) []byte {
	t.Helper()
	state := map[string][]string{}
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs", "plugin_run_origins", "plugin_run_config_transitions", "plugin_event_admissions", "plugin_event_subscriptions", "plugin_event_batches", "plugin_event_deliveries", "plugin_schedules", "plugin_schedule_occurrences", "plugin_api_calls"} {
		rows, err := p.store.DB.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		state[table] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			raw, err := json.Marshal(values) // Includes every ciphertext byte.
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			state[table] = append(state[table], string(raw))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(state[table])
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Register synthetic COMMON host declarations through the actual registration
// APIs and a genuinely claimed run. No business origin, journal, receipt, or
// signed transition is fabricated. SystemProcess/CategoryEditor do not claim
// these as real business services or public API routes.
func pluginFormPropagationPending(t *testing.T, p *PluginStore, instance string, rich bool) PluginRun {
	t.Helper()
	ctx := context.Background()
	if rich {
		queued, err := p.EnqueueRun(ctx, instance, "reload", nil)
		if err != nil {
			t.Fatal(err)
		}
		running, err := p.claimRunForInstance(ctx, instance)
		if err != nil || running.ID != queued.ID || running.State != "running" {
			t.Fatal("genuine registration run not claimed", err)
		}
		if err = p.configureEventSubscription(ctx, running, "form_propagation", []string{"import.completed"}, time.Hour, true); err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Add(time.Hour)
		definition := pluginScheduleDefinition{ID: "Form.Propagation", Name: "Synthetic common propagation", Handler: "form_propagation", Trigger: schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 30000}, MisfireGraceMS: -1}
		if err = p.configurePluginSchedules(ctx, running, "form_propagation", []pluginScheduleDefinition{definition}, at); err != nil {
			t.Fatal(err)
		}
		if err = p.FinishRun(ctx, running.ID, "succeeded", "", nil); err != nil {
			t.Fatal(err)
		}
		a := &App{Store: p.store, pluginStore: p}
		commitDomainFixture(t, a, domainFixture("import.completed", "form-propagation"))
		subscriptions, err := p.activeEventSubscriptions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		collected := false
		for _, subscription := range subscriptions {
			if subscription.InstanceID == instance && subscription.Handler == "form_propagation" {
				if err = p.collectPluginEvents(ctx, subscription, at); err != nil {
					t.Fatal(err)
				}
				collected = true
			}
		}
		if !collected {
			t.Fatal("registered event subscription missing")
		}
		if count, err := p.enqueueDuePluginSchedules(ctx, at.Add(30*time.Second)); err != nil || count != 1 {
			t.Fatal("genuine scheduled occurrence not queued", count, err)
		}
		in, err := p.Installation(ctx, instance)
		if err != nil {
			t.Fatal(err)
		}
		input := plugins.HTTPRequest{Key: "synthetic_form_propagation", Method: "GET", Path: "/synthetic-form-propagation"}
		// Exercise the common sealed API admission writer. This is explicitly
		// a synthetic host request, not evidence of a compiled business route.
		if _, err = a.enqueuePluginHTTP(ctx, in, input, pluginAPIScope{Key: input.Key, Method: input.Method, Authority: pluginAPIAuthority{Kind: "anonymous"}}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := p.EnqueueRun(ctx, instance, "reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func pluginFormPropagationAttempt(ctx context.Context, p *PluginStore, tx *sql.Tx, operation string, run PluginRun, previous PluginStoredValue) error {
	next := json.RawMessage(`{"injected_common_write":true}`)
	if operation == "propagate" {
		return p.propagateInstanceConfigTx(ctx, tx, run.InstanceID, run.Generation, run.ConfigRevision, run.ConfigRevision+1, previous.Value, next)
	}
	var invocation map[string]json.RawMessage
	if err := json.Unmarshal(run.Input, &invocation); err != nil {
		return err
	}
	invocation["config"] = next
	nextInput, err := json.Marshal(invocation)
	if err != nil {
		return err
	}
	plugin, _, err := p.pluginSnapshotOwner(ctx, tx, run.InstanceID)
	if err != nil {
		return err
	}
	return p.recordRunConfigTransitionTx(ctx, tx, plugin, run, run.ConfigRevision+1, nextInput, previous.Value, next)
}

func TestPluginFormCommonProjectionDirectWritesDeniedAndDurable(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, target := range []string{"source", "other-instance", "other-store"} {
			if mode == pluginFormPreview && target == "source" {
				continue // Preview has no installed source or genuine pending run.
			}
			for _, operation := range []string{"propagate", "record-transition"} {
				t.Run(pluginFormDispatchModeName(mode)+"/"+target+"/"+operation, func(t *testing.T) {
					p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
					writer, instance := p, in.InstanceID
					if target == "other-store" {
						_, writer = pluginStoreFixture(t)
						installCloneSource(t, writer, builtin.SystemProcessManifest())
						if _, err := writer.SaveConfig(context.Background(), instance, 0, json.RawMessage(`{"enable":false}`)); err != nil {
							t.Fatal(err)
						}
					} else if target == "other-instance" {
						instance = "CategoryEditor"
						installCloneSource(t, writer, builtin.CategoryEditorManifest())
						if _, err := writer.SaveConfig(context.Background(), instance, 0, json.RawMessage(`{"enabled":false,"content":""}`)); err != nil {
							t.Fatal(err)
						}
					}
					pending := pluginFormPropagationPending(t, writer, instance, target != "source")
					previous, err := writer.Config(context.Background(), instance)
					if err != nil {
						t.Fatal(err)
					}
					before, sourceBefore := pluginFormPropagationRows(t, writer), pluginFormPropagationRows(t, p)
					var denied, committed error
					_, err = p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
						tx, err := writer.store.DB.BeginTx(callCtx, nil)
						if err != nil {
							return nil, err
						}
						defer tx.Rollback()
						denied = pluginFormPropagationAttempt(callCtx, writer, tx, operation, pending, previous)
						committed = tx.Commit() // Deliberately swallow denial and COMMIT.
						return json.RawMessage(`{}`), nil
					}))
					if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || committed != nil {
						t.Errorf("common projection escaped immediate/sticky denial: operation=%v dispatch=%v commit=%v", denied, err, committed)
					}
					if !bytes.Equal(before, pluginFormPropagationRows(t, writer)) || !bytes.Equal(sourceBefore, pluginFormPropagationRows(t, p)) {
						t.Fatal("denied direct projection changed durable rows, queued ciphertext, metadata, or proof edges")
					}
				})
			}
		}
	}
}

func TestPluginFormCommonProjectionProjectorHasNoInternalCapability(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, operation := range []string{"propagate", "record-transition"} {
			t.Run(pluginFormDispatchModeName(mode)+"/"+operation, func(t *testing.T) {
				p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
				pending := pluginFormPropagationPending(t, p, in.InstanceID, false)
				previous, err := p.Config(context.Background(), in.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				before := pluginFormPropagationRows(t, p)
				calls := 0
				var denied, transitionErr error
				_, err = p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
					_, transitionErr = pluginFormMechanicalSave(t, p, callCtx, in, view.durable.Revision, view.durable.Value, func(projectCtx context.Context, transaction PluginDefaultsTransaction, _ PluginDefaultsChange) error {
						calls++
						if projectCtx.Value(pluginFormConfigWriteKey{}) != nil {
							t.Error("framework config-projection proof leaked to business callback")
						}
						// Same-package trusted hooks can obtain raw SQL; this test is
						// specifically the guarded common helper, not a SQL sandbox.
						tx := pluginProjectionSQLForTest(t, transaction)
						denied = pluginFormPropagationAttempt(projectCtx, p, tx, operation, pending, previous)
						return nil // Attempt to hide the denied call from the transition.
					})
					return json.RawMessage(`{}`), nil
				}))
				if calls != 1 || pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(transitionErr) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
					t.Errorf("projector denial did not roll back/latch: calls=%d operation=%v transition=%v dispatch=%v", calls, denied, transitionErr, err)
				}
				if !bytes.Equal(before, pluginFormPropagationRows(t, p)) {
					t.Fatal("denied projector partially committed config, input, or chain")
				}
			})
		}
	}
}

func TestPluginFormCommonProjectionCapabilityCannotRebind(t *testing.T) {
	for _, operation := range []string{"config", "propagate", "record-transition"} {
		for _, mismatch := range []string{"store", "tx", "instance", "session", "scope"} {
			t.Run(operation+"/"+mismatch, func(t *testing.T) {
				p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledStarted, false)
				pending := pluginFormPropagationPending(t, p, in.InstanceID, false)
				previous, err := p.Config(context.Background(), in.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				otherSession, err := p.beginPluginFormSession(context.Background(), in, pluginFormOriginInstalled)
				if err != nil {
					t.Fatal(err)
				}
				_, other := pluginStoreFixture(t)
				before := pluginFormPropagationRows(t, p)
				var denied, committed error
				_, err = p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
					tx, e := p.store.DB.BeginTx(callCtx, nil)
					if e != nil {
						return nil, e
					}
					defer tx.Rollback()
					mintCtx, issuer, mintTx, mintInstance := callCtx, p, tx, in.InstanceID
					switch mismatch {
					case "store":
						issuer = other
					case "tx":
						mintTx = nil
					case "instance":
						mintInstance = "another-instance"
					case "session":
						mintCtx = withPluginFormSession(callCtx, otherSession)
					case "scope":
						mintCtx = context.WithValue(callCtx, pluginFormCallbackScopeKey{}, &pluginFormCallbackScope{session: session})
					}
					// Manufacture only a private capability-binding test input;
					// no business evidence or public authorization is invented.
					permit := issuer.pluginFormConfigWriteContext(mintCtx, mintTx, mintInstance).Value(pluginFormConfigWriteKey{})
					attemptCtx := context.WithValue(callCtx, pluginFormConfigWriteKey{}, permit)
					if operation == "config" {
						_, denied = p.saveValueTx(attemptCtx, tx, in.InstanceID, "config", "main", previous.Revision, previous.Value)
					} else {
						denied = pluginFormPropagationAttempt(attemptCtx, p, tx, operation, pending, previous)
					}
					committed = tx.Commit()
					return json.RawMessage(`{}`), nil
				}))
				if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || committed != nil {
					t.Errorf("rebound proof accepted: operation=%v dispatch=%v commit=%v", denied, err, committed)
				}
				if !bytes.Equal(before, pluginFormPropagationRows(t, p)) {
					t.Fatal("rebound proof changed durable state")
				}
			})
		}
	}
}

func TestPluginFormCommonProjectionWarmAndScopeFreeKeepNineProofEdges(t *testing.T) {
	for _, mode := range []string{"started", "unknown", "scope-free"} {
		t.Run(mode, func(t *testing.T) {
			a, first := originHTTPRun(t, builtin.SystemProcessManifest())
			p, ctx := a.pluginStore, context.Background()
			if err := p.FinishRun(ctx, first.ID, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
			pending := pluginFormPropagationPending(t, p, first.InstanceID, true)
			if mode == "unknown" {
				lifecycleEditManifest(t, p, first.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
			}
			in, err := p.Installation(ctx, first.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			previous, err := p.Config(ctx, first.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			beforeOrigins := pluginFormPropagationTable(t, p, "plugin_run_origins")
			var origins int
			if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_origins WHERE run_id=?`, first.ID).Scan(&origins); err != nil || origins != 1 {
				t.Fatal("genuine authenticated HTTP origin missing", origins, err)
			}
			beforeDeliveries := pluginFormPropagationTable(t, p, "plugin_event_deliveries")
			beforeAdmissions := pluginFormPropagationTable(t, p, "plugin_event_admissions")
			var originalAPIScope, originalDefinition []byte
			if err = p.store.DB.QueryRow(`SELECT scope FROM plugin_api_calls WHERE instance_id=?`, in.InstanceID).Scan(&originalAPIScope); err != nil {
				t.Fatal(err)
			}
			if err = p.store.DB.QueryRow(`SELECT definition FROM plugin_schedules WHERE instance_id=?`, in.InstanceID).Scan(&originalDefinition); err != nil {
				t.Fatal(err)
			}
			var cursor int64
			var nextDue string
			if err = p.store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id=?`, in.InstanceID).Scan(&cursor); err != nil {
				t.Fatal(err)
			}
			if err = p.store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=?`, in.InstanceID).Scan(&nextDue); err != nil {
				t.Fatal(err)
			}
			var session *pluginFormSession
			if mode != "scope-free" {
				session, err = p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
				if err != nil {
					t.Fatal(err)
				}
				ctx = withPluginFormSession(ctx, session)
			}
			calls := 0
			revision := previous.Revision
			for step := 0; step < 9; step++ {
				save := func(callCtx context.Context) error {
					noopBefore := pluginFormPropagationRows(t, p)
					noop, err := p.NormalizeInstanceDefaults(callCtx, in, revision, nil)
					if err != nil {
						return err
					}
					if noop.Revision != revision || !bytes.Equal(noop.Value, previous.Value) || !bytes.Equal(noopBefore, pluginFormPropagationRows(t, p)) {
						t.Fatal("missing-only no-op mutated state or lost current image")
					}
					// Equal bytes are intentional: nine independent source Saves
					// must produce nine committed revisions and chain edges.
					value, err := pluginFormMechanicalSave(t, p, callCtx, in, revision, previous.Value, func(projectCtx context.Context, transaction PluginDefaultsTransaction, change PluginDefaultsChange) error {
						calls++
						if projectCtx.Value(pluginFormConfigWriteKey{}) != nil {
							t.Error("projection capability leaked into business projector")
						}
						actual, err := p.scanPluginRun(pluginProjectionSQLForTest(t, transaction).QueryRowContext(projectCtx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, pending.ID))
						if err != nil {
							return err
						}
						sha, err := p.verifyRunConfigChainTx(projectCtx, pluginProjectionSQLForTest(t, transaction), in.PluginID, actual, pending.ConfigRevision, deferredInputSHA(pending.Input), deferredInputSHA(previous.Value))
						if err != nil || sha != deferredInputSHA(change.Previous.Value) || actual.ConfigRevision != change.Previous.Revision {
							t.Fatal("projector lost the original pending preimage chain", err)
						}
						journal, err := transaction.Data(projectCtx, "common-form-journal")
						if err != nil && !errors.Is(err, sql.ErrNoRows) {
							return err
						}
						_, err = transaction.SaveData(projectCtx, "common-form-journal", journal.Revision, map[string]int64{"revision": change.Next.Revision})
						return err
					})
					if err == nil && (value.Revision != revision+1 || !bytes.Equal(value.Value, previous.Value)) {
						t.Fatal("source equal-byte Save was elided or changed")
					}
					if err == nil {
						revision = value.Revision
					}
					return err
				}
				if mode == "scope-free" {
					err = save(ctx)
				} else {
					_, err = p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
						return json.RawMessage(`{}`), save(callCtx)
					}))
				}
				if err != nil {
					t.Fatal("legal common projection rejected", step, err)
				}
			}
			if calls != 9 || revision != previous.Revision+9 {
				t.Fatal("independent nine-save semantics lost", calls, revision)
			}
			for _, table := range []string{"plugin_event_subscriptions", "plugin_event_batches", "plugin_schedules", "plugin_schedule_occurrences", "plugin_api_calls"} {
				var count, wrong int
				if err = p.store.DB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(config_revision<>?),0) FROM `+table+` WHERE instance_id=?`, revision, in.InstanceID).Scan(&count, &wrong); err != nil || count == 0 || wrong != 0 {
					t.Fatal("real common metadata did not propagate", table, count, wrong, err)
				}
			}
			actual, err := p.Run(context.Background(), pending.ID)
			if err != nil || actual.State != "pending" || actual.ConfigRevision != revision {
				t.Fatal("pending head changed identity or missed revisions", err)
			}
			sha, err := p.verifyRunConfigChainTx(context.Background(), p.store.DB, in.PluginID, actual, pending.ConfigRevision, deferredInputSHA(pending.Input), deferredInputSHA(previous.Value))
			if err != nil || sha != deferredInputSHA(previous.Value) {
				t.Fatal("final nine-edge proof chain invalid", err)
			}
			var edges, afterCursor int64
			var afterDue string
			if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=?`, pending.ID).Scan(&edges); err != nil || edges != 9 {
				t.Fatal("equal-byte writes lost independent proof edges", edges, err)
			}
			if err = p.store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id=?`, in.InstanceID).Scan(&afterCursor); err != nil || cursor != afterCursor {
				t.Fatal("projection consumed/reset event cursor", err)
			}
			if err = p.store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=?`, in.InstanceID).Scan(&afterDue); err != nil || nextDue != afterDue {
				t.Fatal("projection moved schedule time", err)
			}
			var afterAPIScope, afterDefinition []byte
			if err = p.store.DB.QueryRow(`SELECT scope FROM plugin_api_calls WHERE instance_id=?`, in.InstanceID).Scan(&afterAPIScope); err != nil {
				t.Fatal(err)
			}
			if err = p.store.DB.QueryRow(`SELECT definition FROM plugin_schedules WHERE instance_id=?`, in.InstanceID).Scan(&afterDefinition); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(originalAPIScope, afterAPIScope) || !bytes.Equal(originalDefinition, afterDefinition) || !bytes.Equal(beforeOrigins, pluginFormPropagationTable(t, p, "plugin_run_origins")) || !bytes.Equal(beforeAdmissions, pluginFormPropagationTable(t, p, "plugin_event_admissions")) || !bytes.Equal(beforeDeliveries, pluginFormPropagationTable(t, p, "plugin_event_deliveries")) {
				t.Fatal("projection rewrote original authorities, declarations, or event deliveries")
			}
			if session != nil && (session.durable.Revision != revision || session.validateFinal(ctx) != nil) {
				t.Fatal("committed projection did not advance request-local anchor")
			}
		})
	}
}

func pluginFormPropagationTable(t *testing.T, p *PluginStore, table string) []byte {
	t.Helper()
	var all map[string]json.RawMessage
	if err := json.Unmarshal(pluginFormPropagationRows(t, p), &all); err != nil {
		t.Fatal(err)
	}
	value, ok := all[table]
	if !ok {
		t.Fatal("snapshot table not included", table)
	}
	return value
}

func TestPluginFormCommonRetainedCallbackContextIsCanceled(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		t.Run(pluginFormDispatchModeName(mode), func(t *testing.T) {
			p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
			var retained context.Context
			_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
				retained = callCtx
				return json.RawMessage(`{}`), nil
			}))
			if err != nil || retained == nil || !errors.Is(retained.Err(), context.Canceled) {
				t.Fatal("returned callback retained a live context", err)
			}
			if ctx.Err() != nil {
				t.Fatal("dispatch canceled its parent request context")
			}
		})
	}
}

func TestPluginFormCommonEntryGuardsPrecedePointerDereference(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, operation := range []string{"schedule-nil-run", "prepare-nil-app", "retry-nil-app", "outcome-nil-call"} {
			t.Run(pluginFormDispatchModeName(mode)+"/"+operation, func(t *testing.T) {
				p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
				before := pluginFormPropagationRows(t, p)
				var denied error
				_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
					switch operation {
					case "schedule-nil-run":
						denied = p.configurePluginSchedulesWithReplacement(callCtx, nil, "empty", nil, time.Now(), nil, nil, nil)
					case "prepare-nil-app":
						_, denied = (*App)(nil).preparePluginDeferred(callCtx, in, PluginRun{})
					case "retry-nil-app":
						_, denied = (*App)(nil).retryPluginDeferred(callCtx, in.InstanceID, "not-a-run")
					default:
						denied = (*pluginDeferredCall)(nil).changeOutcome(callCtx, "prepared", "started", nil, "")
					}
					return json.RawMessage(`{}`), nil
				}))
				if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
					t.Errorf("pre-effect pointer guard missing: operation=%v dispatch=%v", denied, err)
				}
				if !bytes.Equal(before, pluginFormPropagationRows(t, p)) {
					t.Fatal("rejected pointer-based API changed state")
				}
			})
		}
	}
}

func TestPluginFormCommonProjectionProofCannotOverrideReadOnlyOrStickyDenial(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		t.Run(pluginFormDispatchModeName(mode), func(t *testing.T) {
			p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
			before := pluginFormPropagationRows(t, p)
			var denied, committed error
			_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
				if mode == pluginFormInstalledStarted || mode == pluginFormInstalledUnknown {
					// Set a real common-API sticky failure before presenting an
					// otherwise exact internal capability in the same callback.
					if _, e := p.ClaimRun(callCtx); pluginErrorCode(e) != "PLUGIN_CAPABILITY_DENIED" {
						t.Error("fixture did not establish sticky denial", e)
					}
				}
				tx, e := p.store.DB.BeginTx(callCtx, nil)
				if e != nil {
					return nil, e
				}
				defer tx.Rollback()
				permitCtx := p.pluginFormConfigWriteContext(callCtx, tx, in.InstanceID)
				denied = p.propagateInstanceConfigTx(permitCtx, tx, in.InstanceID, in.Generation, view.durable.Revision, view.durable.Revision+1, view.durable.Value, view.durable.Value)
				committed = tx.Commit()
				return json.RawMessage(`{}`), nil
			}))
			if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || committed != nil {
				t.Errorf("internal proof bypassed read-only or sticky denial: operation=%v dispatch=%v commit=%v", denied, err, committed)
			}
			if !bytes.Equal(before, pluginFormPropagationRows(t, p)) {
				t.Fatal("read-only/sticky proof attempt changed state")
			}
		})
	}
}
