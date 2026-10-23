package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/schedule"
)

// These are shared-host API regressions, not declarations of a business
// plugin's Form behavior. Fixtures obtain their run, subscription, schedule,
// and deferred identities through the existing production admission paths.
// No test runs a worker or performs a deferred business operation.

type pluginCommonFormMutation struct {
	store        *PluginStore
	call         func(context.Context) error
	verify       func(denied bool)
	changesRows  bool
	skipPositive bool
}

var pluginCommonFormOperations = []string{
	"event-configure", "event-configure-tx", "event-collect-matching", "event-collect-nonmatching", "event-collect-noop",
	"schedule-register-nil-patch", "schedule-deactivate-empty", "schedule-enqueue-due", "schedule-enqueue-missed", "schedule-enqueue-busy", "schedule-claim-tx", "schedule-manual", "schedule-empty-dispatch",
	"lifecycle-write-tx", "lifecycle-generation-tx", "lifecycle-mark-started-tx", "lifecycle-mark-started-noop", "runs-recover", "runs-recover-noop",
	"organization-save", "organization-save-tx", "organization-clone-tx", "organization-clone-noop", "organization-remove-tx", "organization-remove-noop", "organization-mutate", "organization-mutate-noop",
	"notifications-save", "notifications-save-tx",
	"deferred-write-tx", "deferred-prepare", "deferred-prepare-existing", "deferred-change-outcome", "deferred-retry", "deferred-recover", "deferred-recover-noop",
}

// Capture the actual committed values, including encrypted blobs, timestamps,
// cursors, receipts, occurrences and SQLite sequences. Row counts alone cannot
// detect resealing, cursor advancement, history replacement or no-op writes.
func pluginCommonFormRows(t *testing.T, p *PluginStore) map[string][]string {
	t.Helper()
	rows, err := p.store.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	out := make(map[string][]string, len(tables))
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		rows, err := p.store.DB.Query(`SELECT * FROM ` + quoted)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		out[table] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out[table] = append(out[table], string(encoded))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(out[table])
	}
	return out
}

func pluginCommonFormRowsUnchanged(t *testing.T, p *PluginStore, before map[string][]string) {
	t.Helper()
	after := pluginCommonFormRows(t, p)
	if reflect.DeepEqual(before, after) {
		return
	}
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Errorf("Form mutation changed committed table %q", table)
		}
	}
	for table := range after {
		if _, ok := before[table]; !ok {
			t.Errorf("Form mutation created committed table %q", table)
		}
	}
}

// Intentionally commit even after the callee returns an error. A rollback in
// the test would conceal a helper that writes before discovering its denial.
func pluginCommonFormCommittedTx(t *testing.T, p *PluginStore, ctx context.Context, call func(*sql.Tx) error) error {
	t.Helper()
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	operationErr := call(tx)
	if err := tx.Commit(); err != nil {
		t.Fatalf("otherwise-empty caller transaction could not commit: %v", err)
	}
	return operationErr
}

// Mirror pluginFormDispatchFixture's authenticated mode setup, but attach the
// form source to a store containing a genuine mutation target. In particular,
// preview has no installation, and unknown is missing evidence, not a forged
// started marker. Source and mutation target are separate instances.
func pluginCommonFormAttach(t *testing.T, p *PluginStore, mode pluginFormMode) (context.Context, PluginInstallation, *pluginFormSession) {
	t.Helper()
	p.store.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	manifest := builtin.SystemProcessManifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	in := PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}
	origin := pluginFormOriginInstall
	if mode != pluginFormPreview {
		installCloneSource(t, p, manifest)
		stored, err := p.SaveConfig(ctx, in.InstanceID, 0, json.RawMessage(`{"enable":false,"opaque":{"kept":true}}`))
		if err != nil {
			t.Fatal(err)
		}
		switch mode {
		case pluginFormInstalledCold:
		case pluginFormInstalledStarted:
			_, queued, err := p.SaveConfigAndQueue(ctx, in.InstanceID, stored.Revision, stored.Value)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := p.claimRunForInstance(ctx, in.InstanceID)
			if err != nil || claimed.ID != queued.ID {
				t.Fatal("could not establish a real started form source", err)
			}
			if err := finishClaimedFixtureRun(ctx, p, claimed, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
		case pluginFormInstalledUnknown:
			lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
		default:
			t.Fatal("unknown Form mode")
		}
		in = lifecycleInstallation(t, p, in.InstanceID)
		origin = pluginFormOriginInstalled
	}
	session, err := p.beginPluginFormSession(ctx, in, origin)
	if err != nil || session == nil || session.resolution.mode != mode {
		t.Fatal("Form source did not authenticate the expected mode", err)
	}
	return withPluginFormSession(ctx, session), in, session
}

func pluginCommonFormRunningTarget(t *testing.T, p *PluginStore, instance string) PluginRun {
	t.Helper()
	queued, err := p.EnqueueRun(context.Background(), instance, "reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.claimRunForInstance(context.Background(), instance)
	if err != nil || run.ID != queued.ID {
		t.Fatal("fixture failed to claim its real queued run", err)
	}
	return run
}

type pluginCommonFormMarshaler struct{ calls *int }

func (m pluginCommonFormMarshaler) MarshalJSON() ([]byte, error) {
	(*m.calls)++
	return []byte(`{"name":"fixture","enabled":false,"future":{"kept":true}}`), nil
}

func pluginCommonFormMutationFixture(t *testing.T, operation string) pluginCommonFormMutation {
	t.Helper()
	ctx := context.Background()
	fixture := pluginCommonFormMutation{changesRows: true}
	switch {
	case strings.HasPrefix(operation, "event-"):
		a, run := pluginEventFixture(t, 0)
		p := a.pluginStore
		fixture.store = p
		if operation == "event-configure" || operation == "event-configure-tx" {
			// Registration authenticates a currently running admission, not the
			// completed setup run returned by pluginEventFixture.
			run = pluginCommonFormRunningTarget(t, p, run.InstanceID)
			fixture.call = func(callCtx context.Context) error {
				if operation == "event-configure" {
					return p.configureEventSubscription(callCtx, run, "refresh", []string{"import.completed"}, time.Second, false)
				}
				return pluginCommonFormCommittedTx(t, p, callCtx, func(tx *sql.Tx) error {
					return p.configureEventSubscriptionTx(callCtx, tx, run, "refresh", []string{"import.completed"}, time.Second, false)
				})
			}
			break
		}
		subscriptions, err := p.activeEventSubscriptions(ctx)
		if err != nil || len(subscriptions) != 1 {
			t.Fatal("fixture subscription missing", err)
		}
		kind := "import.completed"
		if operation == "event-collect-nonmatching" {
			kind = "download.accepted"
		}
		if operation != "event-collect-noop" {
			commitDomainFixture(t, a, domainFixture(kind, "form-guard-event"))
		} else {
			fixture.changesRows = false
		}
		var highWater int64
		if err := p.store.DB.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM domain_events`).Scan(&highWater); err != nil {
			t.Fatal(err)
		}
		fixture.call = func(callCtx context.Context) error {
			return p.collectPluginEventsUntil(callCtx, subscriptions[0], time.Now(), highWater)
		}
		fixture.verify = func(denied bool) {
			var cursor int64
			if err := p.store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id=? AND handler_key='refresh'`, run.InstanceID).Scan(&cursor); err != nil {
				t.Fatal(err)
			}
			wantCursor, wantDeliveries := int64(0), 0
			if !denied {
				wantCursor = highWater
				if operation == "event-collect-matching" {
					wantDeliveries = 1
				}
			}
			if cursor != wantCursor || pluginEventCount(t, a, "plugin_event_deliveries") != wantDeliveries {
				t.Error("event cursor or matching-delivery state differs")
			}
		}
	case strings.HasPrefix(operation, "schedule-"):
		at := time.Now().UTC().Add(time.Hour)
		grace := int64(-1)
		if operation == "schedule-enqueue-missed" {
			grace = 100
		}
		a, _, definition := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000}, grace)
		p := a.pluginStore
		fixture.store = p
		switch operation {
		case "schedule-register-nil-patch", "schedule-deactivate-empty":
			run := pluginCommonFormRunningTarget(t, p, "timer")
			definitions := []pluginScheduleDefinition{definition}
			definitions[0].ID = "Timer.New"
			if operation == "schedule-deactivate-empty" {
				definitions = nil
			}
			effectCalls := 0
			fixture.call = func(callCtx context.Context) error {
				return p.configurePluginSchedulesWithReplacement(callCtx, &run, "services", definitions, at, nil, func(context.Context, *sql.Tx, PluginRun) error {
					effectCalls++
					return nil
				}, nil)
			}
			fixture.verify = func(denied bool) {
				want := 1
				if denied {
					want = 0
				}
				if effectCalls != want {
					t.Errorf("schedule effect calls=%d, want %d", effectCalls, want)
				}
				if !denied {
					var active int
					if err := p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='timer' AND active=1`).Scan(&active); err != nil {
						t.Fatal(err)
					}
					wantActive := 1
					if operation == "schedule-deactivate-empty" {
						wantActive = 0
					}
					if active != wantActive {
						t.Error("scope-free schedule replacement did not apply")
					}
				}
			}
		case "schedule-enqueue-due", "schedule-enqueue-missed", "schedule-enqueue-busy", "schedule-empty-dispatch":
			due := at.Add(time.Second)
			if operation == "schedule-enqueue-missed" {
				due = due.Add(500 * time.Millisecond)
			}
			if operation == "schedule-enqueue-busy" {
				if count, err := p.enqueueDuePluginSchedules(ctx, due); err != nil || count != 1 {
					t.Fatal("fixture failed to establish busy occurrence", count, err)
				}
				due = due.Add(time.Second)
			}
			if operation == "schedule-empty-dispatch" {
				due = at
				fixture.changesRows = false
			}
			fixture.call = func(callCtx context.Context) error {
				_, err := p.enqueueDuePluginSchedules(callCtx, due)
				return err
			}
			fixture.verify = func(denied bool) {
				if denied || operation == "schedule-empty-dispatch" {
					return
				}
				state := "queued"
				if operation == "schedule-enqueue-missed" {
					state = "missed"
				} else if operation == "schedule-enqueue-busy" {
					state = "skipped_busy"
				}
				var count int
				if err := p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE state=?`, state).Scan(&count); err != nil || count != 1 {
					t.Fatal("scope-free dispatch did not exercise intended branch", state, count, err)
				}
			}
		case "schedule-claim-tx":
			if count, err := p.enqueueDuePluginSchedules(ctx, at.Add(time.Second)); err != nil || count != 1 {
				t.Fatal("fixture did not queue a schedule", count, err)
			}
			var runID string
			if err := p.store.DB.QueryRow(`SELECT run_id FROM plugin_schedule_occurrences WHERE state='queued'`).Scan(&runID); err != nil {
				t.Fatal(err)
			}
			run, err := p.Run(ctx, runID)
			if err != nil {
				t.Fatal(err)
			}
			fixture.call = func(callCtx context.Context) error {
				return pluginCommonFormCommittedTx(t, p, callCtx, func(tx *sql.Tx) error {
					code, err := p.claimPluginScheduleTx(callCtx, tx, run, at.Add(time.Second))
					if err == nil && code != "" {
						return bad(code, "unexpected fixture claim outcome")
					}
					return err
				})
			}
		case "schedule-manual":
			fixture.call = func(callCtx context.Context) error {
				_, err := p.enqueueManualPluginService(callCtx, "timer", definition.ID, at)
				return err
			}
		}
	case strings.HasPrefix(operation, "lifecycle-") || operation == "runs-recover":
		_, p := pluginStoreFixture(t)
		fixture.store = p
		installCloneSource(t, p, builtin.CategoryEditorManifest())
		_, queued, err := p.SaveConfigAndQueue(ctx, "CategoryEditor", 0, json.RawMessage(`{"enabled":false}`))
		if err != nil {
			t.Fatal(err)
		}
		run, err := p.claimRunForInstance(ctx, "CategoryEditor")
		if err != nil || run.ID != queued.ID {
			t.Fatal("lifecycle fixture did not genuinely claim a run", err)
		}
		if operation == "lifecycle-mark-started-tx" {
			// Legacy missing evidence is represented exactly as in the existing
			// lifecycle tests; the running run itself remains a genuine claim.
			lifecycleEditManifest(t, p, run.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
		}
		in := lifecycleInstallation(t, p, run.InstanceID)
		fixture.changesRows = operation != "lifecycle-mark-started-noop"
		fixture.skipPositive = operation == "lifecycle-generation-tx"
		fixture.call = func(callCtx context.Context) error {
			if operation == "runs-recover" {
				return p.recoverPluginRuns(callCtx)
			}
			return pluginCommonFormCommittedTx(t, p, callCtx, func(tx *sql.Tx) error {
				switch operation {
				case "lifecycle-write-tx":
					return p.writePluginLifecycleTx(callCtx, tx, in, pluginLifecycleStarted)
				case "lifecycle-generation-tx":
					// Denial must precede checking for the generation transition.
					// Do not prewrite a bump and then attribute our own SQL to Form.
					return p.recordPluginGenerationLifecycleTx(callCtx, tx, in.InstanceID)
				default:
					return p.markPluginExecutionStartedTx(callCtx, tx, run)
				}
			})
		}
	case strings.HasPrefix(operation, "organization-"):
		_, p := pluginStoreFixture(t)
		fixture.store = p
		org, err := p.SaveOrganization(ctx, PluginOrganization{Folders: map[string]json.RawMessage{"kept": json.RawMessage(`{"plugins":["member"],"future":{"kept":true}}`)}, Order: json.RawMessage(`["member"]`)})
		if err != nil {
			t.Fatal(err)
		}
		fixture.changesRows = !strings.HasSuffix(operation, "-noop")
		if operation == "organization-mutate" || operation == "organization-mutate-noop" {
			changeCalls := 0
			fixture.call = func(callCtx context.Context) error {
				return p.mutateOrganization(callCtx, func(current *PluginOrganization) (bool, error) {
					changeCalls++
					if operation == "organization-mutate-noop" {
						return false, nil
					}
					current.Folders["added"] = json.RawMessage(`[]`)
					return true, nil
				})
			}
			fixture.verify = func(denied bool) {
				want := 1
				if denied {
					want = 0
				}
				if changeCalls != want {
					t.Errorf("organization change callback calls=%d, want %d", changeCalls, want)
				}
				current, err := p.Organization(ctx)
				if err != nil {
					t.Fatal(err)
				}
				_, added := current.Folders["added"]
				wantAdded := !denied && operation == "organization-mutate"
				if added != wantAdded {
					t.Error("organization mutation did not retain the expected folder state")
				}
			}
			break
		}
		fixture.call = func(callCtx context.Context) error {
			if operation == "organization-save" {
				_, err := p.SaveOrganization(callCtx, org)
				return err
			}
			return pluginCommonFormCommittedTx(t, p, callCtx, func(tx *sql.Tx) error {
				switch operation {
				case "organization-save-tx":
					_, err := p.saveOrganizationTx(callCtx, tx, org)
					return err
				case "organization-clone-tx":
					return p.cloneOrganizationMemberTx(callCtx, tx, "member", "memberclone")
				case "organization-clone-noop":
					return p.cloneOrganizationMemberTx(callCtx, tx, "absent", "memberclone")
				case "organization-remove-tx":
					return p.removeOrganizationMemberTx(callCtx, tx, "member")
				default:
					return p.removeOrganizationMemberTx(callCtx, tx, "absent")
				}
			})
		}
	case strings.HasPrefix(operation, "notifications-"):
		_, p := pluginStoreFixture(t)
		fixture.store = p
		old, err := p.SaveNotificationChannels(ctx, 0, []any{map[string]any{"name": "kept", "enabled": false}})
		if err != nil {
			t.Fatal(err)
		}
		marshalCalls := 0
		value := []any{pluginCommonFormMarshaler{calls: &marshalCalls}}
		fixture.call = func(callCtx context.Context) error {
			if operation == "notifications-save" {
				_, err := p.SaveNotificationChannels(callCtx, old.Revision, value)
				return err
			}
			return pluginCommonFormCommittedTx(t, p, callCtx, func(tx *sql.Tx) error {
				_, err := p.notificationChannelsSaveTx(callCtx, tx, old.Revision, value)
				return err
			})
		}
		fixture.verify = func(denied bool) {
			if denied && marshalCalls != 0 {
				t.Error("denial occurred after caller-controlled JSON marshaling")
			}
			if !denied && marshalCalls != 1 {
				t.Error("scope-free notification save did not marshal exactly once")
			}
		}
	case operation == "runs-recover-noop" || operation == "deferred-recover-noop":
		_, p := pluginStoreFixture(t)
		fixture.store, fixture.changesRows = p, false
		fixture.call = p.recoverPluginRuns
		if operation == "deferred-recover-noop" {
			fixture.call = p.recoverDeferredRuns
		}
	case strings.HasPrefix(operation, "deferred-"):
		a, in, run := deferredFixture(t)
		p := a.pluginStore
		fixture.store = p
		var d *pluginDeferredCall
		var record pluginDeferredRecord
		var sealed []byte
		var err error
		if operation != "deferred-prepare" {
			d, err = a.preparePluginDeferred(ctx, in, run)
			if err != nil || d == nil {
				t.Fatal("fixture did not create an actual prepared receipt", err)
			}
			record, sealed, err = p.readDeferred(ctx, p.store.DB, run)
			if err != nil || record.Phase != "prepared" {
				t.Fatal("fixture receipt is not prepared", err)
			}
		}
		if operation == "deferred-retry" {
			execution := executionLeaseActivate(t, p, run)
			if err := p.finishRun(execution, run.ID, "failed", "PLUGIN_EXECUTION_FAILED", nil, true); err != nil {
				t.Fatal(err)
			}
			if err := p.releaseSupervisedRun(run); err != nil {
				t.Fatal(err)
			}
		}
		fixture.changesRows = operation != "deferred-prepare-existing"
		fixture.call = func(callCtx context.Context) error {
			switch operation {
			case "deferred-write-tx":
				return pluginCommonFormCommittedTx(t, p, callCtx, func(tx *sql.Tx) error {
					return p.writeDeferred(callCtx, tx, record, sealed)
				})
			case "deferred-prepare", "deferred-prepare-existing":
				_, err := a.preparePluginDeferred(callCtx, in, run)
				return err
			case "deferred-change-outcome":
				return d.changeOutcome(callCtx, "prepared", "started", nil, "")
			case "deferred-retry":
				_, err := a.retryPluginDeferred(callCtx, in.InstanceID, run.ID)
				return err
			default:
				return p.recoverDeferredRuns(callCtx)
			}
		}
		fixture.verify = func(denied bool) {
			deferredStillInstalled(t, a, true)
			if !denied && operation == "deferred-retry" {
				retried, err := p.Run(ctx, run.ID)
				if err != nil || retried.State != "pending" || !bytes.Equal(retried.Input, run.Input) {
					t.Fatal("scope-free retry lost original frozen execution", err)
				}
				var blocks int
				if err := p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_startup_blocks WHERE run_id=?`, run.ID).Scan(&blocks); err != nil || blocks != 0 {
					t.Fatal("scope-free retry retained startup block", blocks, err)
				}
			}
		}
	}
	if fixture.store == nil || fixture.call == nil {
		t.Fatalf("unimplemented fixture operation %q", operation)
	}
	return fixture
}

func TestPluginFormCommonStateMutationsDeniedBeforeEffects(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, destination := range []string{"same-store-other-instance", "foreign-store"} {
			for _, operation := range pluginCommonFormOperations {
				t.Run(pluginFormDispatchModeName(mode)+"/"+destination+"/"+operation, func(t *testing.T) {
					fixture := pluginCommonFormMutationFixture(t, operation)
					source := fixture.store
					if destination == "foreign-store" {
						_, source = pluginStoreFixture(t)
					}
					ctx, in, session := pluginCommonFormPreparedAttach(t, source, mode)
					beforeSource := pluginCommonFormRows(t, source)
					beforeTarget := beforeSource
					if source != fixture.store {
						beforeTarget = pluginCommonFormRows(t, fixture.store)
					}
					// Durable setup and both evidence snapshots precede the actual
					// request. Do not extend or reuse its preparation session.
					requestCtx, requestCancel := context.WithTimeout(context.Background(), 10*time.Second)
					t.Cleanup(requestCancel)
					requestSession, requestErr := source.beginPluginFormSession(requestCtx, in, session.origin)
					if requestErr != nil || requestSession == nil || requestSession.resolution.mode != mode {
						t.Fatal("fresh matrix request did not authenticate expected mode", requestErr)
					}
					session = requestSession
					ctx = withPluginFormSession(requestCtx, requestSession)
					calls := 0
					result, err := source.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
						calls++
						if denied := fixture.call(callCtx); pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
							t.Errorf("common API did not deny immediately: %v", denied)
						}
						return json.RawMessage(`{"swallowed":true}`), nil
					}))
					if calls != 1 || result != nil || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
						t.Errorf("common denial was swallowed: calls=%d, result=%s, error=%v", calls, result, err)
					}
					if pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
						t.Error("common denial did not poison the original form session")
					}
					pluginCommonFormRowsUnchanged(t, source, beforeSource)
					if source != fixture.store {
						pluginCommonFormRowsUnchanged(t, fixture.store, beforeTarget)
					}
					if fixture.verify != nil {
						fixture.verify(true)
					}
				})
			}
		}
	}
}

func TestPluginFormCommonStateMutationsRemainAvailableOutsideDispatch(t *testing.T) {
	for _, operation := range pluginCommonFormOperations {
		t.Run(operation, func(t *testing.T) {
			fixture := pluginCommonFormMutationFixture(t, operation)
			if fixture.skipPositive {
				return // Generation-bump transaction is covered separately below.
			}
			before := pluginCommonFormRows(t, fixture.store)
			if err := fixture.call(context.Background()); err != nil {
				t.Fatalf("scope-free common API rejected its valid fixture: %v", err)
			}
			after := pluginCommonFormRows(t, fixture.store)
			if fixture.changesRows == reflect.DeepEqual(before, after) {
				t.Errorf("scope-free mutation changedRows=%v, want %v", !reflect.DeepEqual(before, after), fixture.changesRows)
			}
			if fixture.verify != nil {
				fixture.verify(false)
			}
		})
	}
}

func TestPluginFormCommonGenerationStampAllowsScopeFreeAtomicBump(t *testing.T) {
	_, p := pluginStoreFixture(t)
	installCloneSource(t, p, builtin.CategoryEditorManifest())
	before := lifecycleInstallation(t, p, "CategoryEditor")
	ctx := context.Background()
	err := pluginCommonFormCommittedTx(t, p, ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=? AND generation=?`, before.InstanceID, before.Generation); err != nil {
			return err
		}
		return p.recordPluginGenerationLifecycleTx(ctx, tx, before.InstanceID)
	})
	if err != nil {
		t.Fatal("scope-free generation stamp failed", err)
	}
	after := lifecycleInstallation(t, p, before.InstanceID)
	state, err := pluginLifecycleState(after)
	if err != nil || after.Generation != before.Generation+1 || state != pluginLifecycleCold {
		t.Fatal("generation and authenticated cold stamp did not commit together", err)
	}
}

// The broad matrix also denies foreign instance/store writes. This focused
// matrix ensures that binding to the actual Form source is not itself a grant
// to change lifecycle state, including an already-started helper's no-op.
func TestPluginFormCommonLifecycleSourceInstanceStillDenied(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, operation := range []string{"write", "generation", "mark-started"} {
			if mode == pluginFormInstalledCold && operation == "mark-started" {
				continue // A genuinely claimed cold generation would be started.
			}
			t.Run(pluginFormDispatchModeName(mode)+"/"+operation, func(t *testing.T) {
				p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
				var run PluginRun
				if operation == "mark-started" {
					run = pluginCommonFormRunningTarget(t, p, in.InstanceID)
					if mode == pluginFormInstalledUnknown {
						lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
					}
					in = lifecycleInstallation(t, p, in.InstanceID)
					// The real claim above finishes fixture setup. Capture a new
					// synthetic request now; rebinding the old Form context is
					// intentionally forbidden and is not the mutation under test.
					freshCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					t.Cleanup(cancel)
					var err error
					session, err = p.beginPluginFormSession(freshCtx, in, pluginFormOriginInstalled)
					if err != nil || session == nil || session.resolution.mode != mode {
						t.Fatal("source execution fixture changed requested Form mode", err)
					}
					ctx = withPluginFormSession(freshCtx, session)
				}
				before := pluginCommonFormRows(t, p)
				calls := 0
				result, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
					calls++
					denied := pluginCommonFormCommittedTx(t, p, callCtx, func(tx *sql.Tx) error {
						switch operation {
						case "write":
							state := pluginLifecycleStarted
							if mode == pluginFormInstalledCold {
								state = pluginLifecycleCold
							}
							return p.writePluginLifecycleTx(callCtx, tx, in, state)
						case "generation":
							return p.recordPluginGenerationLifecycleTx(callCtx, tx, in.InstanceID)
						default:
							return p.markPluginExecutionStartedTx(callCtx, tx, run)
						}
					})
					if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
						t.Errorf("source lifecycle helper was not denied immediately: %v", denied)
					}
					return json.RawMessage(`{"swallowed":true}`), nil
				}))
				if calls != 1 || result != nil || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
					t.Errorf("source lifecycle denial was not sticky: calls=%d, error=%v", calls, err)
				}
				pluginCommonFormRowsUnchanged(t, p, before)
			})
		}
	}
}

// Test-only preparation budget, separate from the new10s request below.
// Existing helper/callers remain unchanged; no attribution of old package timeout.
func pluginCommonFormPreparedAttach(t *testing.T, p *PluginStore, mode pluginFormMode) (context.Context, PluginInstallation, *pluginFormSession) {
	t.Helper()
	setupStarted := time.Now()
	defer func() { t.Logf("matrix_setup_elapsed=%s", time.Since(setupStarted)) }()
	p.store.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	manifest := builtin.SystemProcessManifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	in := PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}
	origin := pluginFormOriginInstall
	if mode != pluginFormPreview {
		installCloneSource(t, p, manifest)
		stored, err := p.SaveConfig(ctx, in.InstanceID, 0, json.RawMessage(`{"enable":false,"opaque":{"kept":true}}`))
		if err != nil {
			t.Fatal(err)
		}
		switch mode {
		case pluginFormInstalledCold:
		case pluginFormInstalledStarted:
			_, queued, err := p.SaveConfigAndQueue(ctx, in.InstanceID, stored.Revision, stored.Value)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := p.claimRunForInstance(ctx, in.InstanceID)
			if err != nil || claimed.ID != queued.ID {
				t.Fatal("could not establish a real started form source", err)
			}
			if err := finishClaimedFixtureRun(ctx, p, claimed, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
		case pluginFormInstalledUnknown:
			lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
		default:
			t.Fatal("unknown Form mode")
		}
		in = lifecycleInstallation(t, p, in.InstanceID)
		origin = pluginFormOriginInstalled
	}
	session, err := p.beginPluginFormSession(ctx, in, origin)
	if err != nil || session == nil || session.resolution.mode != mode {
		t.Fatal("Form source did not authenticate the expected mode", err)
	}
	return withPluginFormSession(ctx, session), in, session
}
