package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
)

// Same-package mechanical observation only. This is not a production method,
// and does not replace any SQL assertion with an out-of-transaction pool read.
func pluginProjectionSQLForTest(t *testing.T, facade PluginDefaultsTransaction) *sql.Tx {
	t.Helper()
	switch transaction := facade.(type) {
	case pluginDefaultsTransaction:
		return transaction.tx
	case *pluginDefaultsTransaction:
		return transaction.tx
	default:
		t.Fatalf("unexpected projection facade %T", facade)
		return nil
	}
}

func pluginProjectionWrapperForTest(t *testing.T, ctx context.Context, tx *sql.Tx, p *PluginStore, instance string) pluginDefaultsTransaction {
	t.Helper()
	installation, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := newPluginDefaultsTransaction(ctx, tx, p, installation)
	t.Cleanup(wrapper.revokeProjection)
	return wrapper
}

func TestPluginProjectionFacadeDynamicMethodSet(t *testing.T) {
	want := []string{"Config", "Data", "SaveData", "VerifyInitialization"}
	for _, value := range []any{pluginDefaultsTransaction{}, &pluginDefaultsTransaction{}} {
		typ := reflect.TypeOf(value)
		var methods []string
		for i := 0; i < typ.NumMethod(); i++ {
			methods = append(methods, typ.Method(i).Name)
		}
		if !reflect.DeepEqual(methods, want) {
			t.Fatalf("%v exposes %v, want exactly %v", typ, methods, want)
		}
		for _, denied := range []string{"Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext", "Store", "Tx", "Commit", "Rollback", "ProofReader"} {
			if _, ok := typ.MethodByName(denied); ok {
				t.Fatalf("%v dynamically exposes %s", typ, denied)
			}
		}
		if _, ok := value.(PluginProofReader); ok {
			t.Fatalf("%v still structurally implements SQL reader", typ)
		}
	}
	iface := reflect.TypeOf((*PluginDefaultsTransaction)(nil)).Elem()
	if iface.NumMethod() != len(want) {
		t.Fatal("public interface contains hidden capabilities", iface)
	}
}

func TestPluginProjectionConfigReadsSameTXNextAndIsolated(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	previous, err := p.Config(ctx, in.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var retained PluginDefaultsTransaction
	result, err := pluginFormMechanicalSave(t, p, ctx, in, previous.Revision, json.RawMessage(`{"enable":false,"next":true}`), func(ctx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
		retained = tx
		current, err := tx.Config(ctx)
		if err != nil || current.Revision != change.Next.Revision || !bytes.Equal(current.Value, change.Next.Value) || current.Revision == change.Previous.Revision {
			t.Fatal("typed config did not observe uncommitted Next", current, err)
		}
		current.Value[0], change.Next.Value[0] = '!', '!'
		again, err := tx.Config(ctx)
		if err != nil || !json.Valid(again.Value) || again.Value[0] != '{' {
			t.Fatal("typed config trusts caller bytes or aliases earlier read", err)
		}
		return nil
	})
	if err != nil || result.Revision != previous.Revision+1 {
		t.Fatal(result, err)
	}
	if _, err = retained.Config(context.Background()); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("returned facade remained live", err)
	}
	if _, err = retained.Data(context.Background(), "missing"); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("returned data facade remained live", err)
	}
	if _, err = retained.SaveData(context.Background(), "late", 0, json.RawMessage(`{}`)); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("returned write facade remained live", err)
	}
	if err = retained.VerifyInitialization(context.Background(), PluginInitializationProof{}); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("returned proof facade remained live", err)
	}
}

func TestPluginProjectionConfigRejectsBoundIdentityAndIntegrityDrift(t *testing.T) {
	for _, drift := range []string{"unbound", "instance", "plugin", "version", "generation", "uninstalled", "enabled", "metadata", "foreign-ciphertext", "array"} {
		t.Run(drift, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			clone, err := p.Clone(ctx, in.InstanceID, PluginCloneRequest{Suffix: "proof"})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := p.store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			wrapper := pluginProjectionWrapperForTest(t, ctx, tx, p, clone.InstanceID)
			switch drift {
			case "unbound":
				wrapper = pluginDefaultsTransaction{tx: tx, store: p, instance: clone.InstanceID}
			case "instance":
				wrapper.instance = in.InstanceID
			case "plugin", "version", "generation", "uninstalled", "enabled":
				statement := map[string]string{
					"plugin":      `UPDATE plugin_installations SET plugin_id='OtherPlugin' WHERE instance_id=?`,
					"version":     `UPDATE plugin_installations SET version='9.9.9' WHERE instance_id=?`,
					"generation":  `UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`,
					"uninstalled": `UPDATE plugin_installations SET installed=0 WHERE instance_id=?`,
					"enabled":     `UPDATE plugin_installations SET desired_enabled=1-desired_enabled WHERE instance_id=?`,
				}[drift]
				_, err = tx.ExecContext(ctx, statement, clone.InstanceID)
			case "metadata":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_instance_metadata SET sealed=x'00' WHERE instance_id=?`, clone.InstanceID)
			case "foreign-ciphertext":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_values SET sealed=(SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main') WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID, clone.InstanceID)
			case "array":
				var sealed []byte
				sealed, err = p.seal("config", clone.InstanceID, "main", json.RawMessage(`[]`))
				if err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, sealed, clone.InstanceID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = wrapper.Config(ctx); err == nil {
				t.Fatal("invalid bound config accepted")
			}
		})
	}
}

type pluginProjectionProofFixture struct {
	p            *PluginStore
	installation PluginInstallation
	current      PluginStoredValue
	run          PluginRun
	receipt      pluginDeferredRecord
	proof        PluginInitializationProof
}

// Real SQLite and framework AEAD, with explicitly synthetic init/receipt/chain
// rows. No worker, business service, notification, or transport is started.
func pluginProjectionProofFixtureForTest(t *testing.T, chained bool, action, state string) pluginProjectionProofFixture {
	t.Helper()
	ctx := context.Background()
	_, p := pluginStoreFixture(t)
	installCloneSource(t, p, builtin.SystemProcessManifest())
	in, err := p.Installation(ctx, "SystemProcess")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := p.SaveConfig(ctx, in.InstanceID, 0, json.RawMessage(`{"enable":false,"synthetic":1}`))
	if err != nil {
		t.Fatal(err)
	}
	initialized := origin
	if chained {
		initialized, err = p.SaveConfig(ctx, in.InstanceID, origin.Revision, json.RawMessage(`{"enable":false,"synthetic":2}`))
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := p.SaveConfig(ctx, in.InstanceID, initialized.Revision, json.RawMessage(`{"enable":false,"synthetic":3}`))
	if err != nil {
		t.Fatal(err)
	}
	input := func(config json.RawMessage) json.RawMessage {
		raw, err := json.Marshal(map[string]any{"config": config, "input": nil})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	run := PluginRun{ID: "pr_projection_proof", InstanceID: in.InstanceID, Version: in.Version, Generation: in.Generation, ConfigRevision: initialized.Revision, Action: action, State: state, Input: input(initialized.Value), CreatedAt: now(), UpdatedAt: now()}
	receipt := pluginDeferredRecord{Schema: 1, Plugin: in.PluginID, Instance: in.InstanceID, Version: in.Version, Run: run.ID, Action: action, Method: "synthetic.configure", Generation: in.Generation, Revision: origin.Revision, InputSHA: deferredInputSHA(input(origin.Value)), ConfigSHA: deferredInputSHA(origin.Value), Raw: json.RawMessage(`{}`), Ack: json.RawMessage(`{"accepted":true}`), Result: json.RawMessage(`{"initialized":true}`), Phase: "confirmed"}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	sealed, err := p.seal("run", in.InstanceID, run.ID, run.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_runs(id,instance_id,version,generation,config_revision,action,state,input,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, run.ID, in.InstanceID, run.Version, run.Generation, run.ConfigRevision, run.Action, run.State, sealed, run.CreatedAt, run.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err = p.writeDeferred(ctx, tx, receipt, nil); err != nil {
		t.Fatal(err)
	}
	if chained {
		previous := run
		previous.ConfigRevision, previous.Input = origin.Revision, input(origin.Value)
		if err = p.recordRunConfigTransitionTx(ctx, tx, in.PluginID, previous, initialized.Revision, run.Input, origin.Value, initialized.Value); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	proof := PluginInitializationProof{RunID: run.ID, Action: action, Method: receipt.Method, InitializedRevision: initialized.Revision, InitializedConfigSHA: deferredInputSHA(initialized.Value), OriginRevision: origin.Revision, OriginInputSHA: receipt.InputSHA, OriginConfigSHA: receipt.ConfigSHA, OriginAckSHA: deferredInputSHA(receipt.Ack), OriginRawSHA: deferredInputSHA(receipt.Raw)}
	return pluginProjectionProofFixture{p: p, installation: in, current: current, run: run, receipt: receipt, proof: proof}
}

func (f pluginProjectionProofFixture) transaction(t *testing.T, ctx context.Context) pluginDefaultsTransaction {
	t.Helper()
	tx, err := f.p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return pluginProjectionWrapperForTest(t, ctx, tx, f.p, f.installation.InstanceID)
}

func TestPluginProjectionInitializationProofValidHistoricalHead(t *testing.T) {
	for _, chained := range []bool{false, true} {
		for _, action := range []string{"init", "reload"} {
			for _, state := range []string{"running", "succeeded"} {
				t.Run(action+"/"+state+map[bool]string{false: "/direct", true: "/chain"}[chained], func(t *testing.T) {
					f := pluginProjectionProofFixtureForTest(t, chained, action, state)
					wrapper := f.transaction(t, context.Background())
					if f.current.Revision <= f.proof.InitializedRevision {
						t.Fatal("fixture must retain an older initialized head")
					}
					if err := wrapper.VerifyInitialization(context.Background(), f.proof); err != nil {
						t.Fatal("historical immutable initialization rejected", err)
					}
				})
			}
		}
	}
}

func TestPluginProjectionInitializationProofRejectsEveryRequestMutation(t *testing.T) {
	mutations := map[string]func(*PluginInitializationProof){
		"run":                  func(p *PluginInitializationProof) { p.RunID = "missing" },
		"action":               func(p *PluginInitializationProof) { p.Action = "reload" },
		"non-initialization":   func(p *PluginInitializationProof) { p.Action = "event" },
		"method":               func(p *PluginInitializationProof) { p.Method += ".other" },
		"initialized-revision": func(p *PluginInitializationProof) { p.InitializedRevision++ },
		"initialized-config":   func(p *PluginInitializationProof) { p.InitializedConfigSHA = strings.Repeat("a", 64) },
		"origin-zero":          func(p *PluginInitializationProof) { p.OriginRevision = 0 },
		"origin-after-head":    func(p *PluginInitializationProof) { p.OriginRevision = p.InitializedRevision + 1 },
		"origin-revision":      func(p *PluginInitializationProof) { p.OriginRevision++ },
		"origin-input":         func(p *PluginInitializationProof) { p.OriginInputSHA = strings.Repeat("b", 64) },
		"origin-config":        func(p *PluginInitializationProof) { p.OriginConfigSHA = strings.Repeat("c", 64) },
		"origin-ack":           func(p *PluginInitializationProof) { p.OriginAckSHA = strings.Repeat("d", 64) },
		"origin-raw":           func(p *PluginInitializationProof) { p.OriginRawSHA = strings.Repeat("e", 64) },
		"malformed-digest":     func(p *PluginInitializationProof) { p.OriginRawSHA = "not-a-digest" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := pluginProjectionProofFixtureForTest(t, true, "init", "succeeded")
			wrapper := f.transaction(t, context.Background())
			mutate(&f.proof)
			if err := wrapper.VerifyInitialization(context.Background(), f.proof); pluginErrorCode(err) != "PLUGIN_RUN_PROOF_INVALID" {
				t.Fatal("mutated proof was not safely rejected", err)
			}
		})
	}
}

func TestPluginProjectionInitializationProofRejectsStoredEvidenceDrift(t *testing.T) {
	for _, mutation := range []string{"run-state", "run-action", "run-version", "run-generation", "run-revision", "receipt-phase", "receipt-plugin", "receipt-instance", "receipt-run", "receipt-version", "receipt-generation", "receipt-action", "receipt-method", "receipt-revision", "receipt-input", "receipt-config", "receipt-ack", "receipt-raw", "receipt-result-missing", "receipt-unknown-field", "receipt-missing", "receipt-corrupt", "chain-corrupt", "chain-missing", "chain-successor"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			f := pluginProjectionProofFixtureForTest(t, true, "init", "succeeded")
			wrapper := f.transaction(t, ctx)
			tx := pluginProjectionSQLForTest(t, wrapper)
			var err error
			switch mutation {
			case "run-state", "run-action", "run-version", "run-generation", "run-revision":
				statement := map[string]string{
					"run-state":      `UPDATE plugin_runs SET state='pending' WHERE id=?`,
					"run-action":     `UPDATE plugin_runs SET action='event' WHERE id=?`,
					"run-version":    `UPDATE plugin_runs SET version='0.0.0' WHERE id=?`,
					"run-generation": `UPDATE plugin_runs SET generation=generation+1 WHERE id=?`,
					"run-revision":   `UPDATE plugin_runs SET config_revision=config_revision-1 WHERE id=?`,
				}[mutation]
				_, err = tx.ExecContext(ctx, statement, f.run.ID)
			case "receipt-missing":
				// Preserve the synthetic row while querying a real run without one.
				_, err = tx.ExecContext(ctx, `INSERT INTO plugin_runs(id,instance_id,version,generation,config_revision,action,state,input,created_at,updated_at) SELECT 'pr_without_receipt',instance_id,version,generation,config_revision,action,state,input,created_at,updated_at FROM plugin_runs WHERE id=?`, f.run.ID)
				if err == nil {
					var sealed []byte
					sealed, err = f.p.seal("run", f.installation.InstanceID, "pr_without_receipt", f.run.Input)
					if err == nil {
						_, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET input=? WHERE id='pr_without_receipt'`, sealed)
					}
				}
				f.proof.RunID = "pr_without_receipt"
			case "receipt-corrupt":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_deferred_operations SET sealed=x'00' WHERE run_id=?`, f.run.ID)
			case "chain-corrupt":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_run_config_transitions SET sealed=x'00' WHERE run_id=?`, f.run.ID)
			case "chain-missing":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_run_config_transitions SET run_id='synthetic_other_run' WHERE run_id=?`, f.run.ID)
			case "chain-successor":
				err = f.p.recordRunConfigTransitionTx(ctx, tx, f.installation.PluginID, f.run, f.run.ConfigRevision+1, json.RawMessage(`{"config":{}}`), json.RawMessage(`{}`), json.RawMessage(`{}`))
			default:
				r := f.receipt
				switch mutation {
				case "receipt-phase":
					r.Phase = "started"
				case "receipt-plugin":
					r.Plugin = "OtherPlugin"
				case "receipt-instance":
					r.Instance += ".foreign"
				case "receipt-run":
					r.Run += "_foreign"
				case "receipt-version":
					r.Version = "0.0.0"
				case "receipt-generation":
					r.Generation++
				case "receipt-action":
					r.Action = "reload"
				case "receipt-method":
					r.Method += ".other"
				case "receipt-revision":
					r.Revision++
				case "receipt-input":
					r.InputSHA = strings.Repeat("a", 64)
				case "receipt-config":
					r.ConfigSHA = strings.Repeat("b", 64)
				case "receipt-ack":
					r.Ack = json.RawMessage(`{"other":true}`)
				case "receipt-raw":
					r.Raw = json.RawMessage(`{"other":true}`)
				}
				var raw, sealed []byte
				raw, err = json.Marshal(r)
				if err == nil && (mutation == "receipt-result-missing" || mutation == "receipt-unknown-field") {
					var object map[string]json.RawMessage
					if err = json.Unmarshal(raw, &object); err == nil {
						if mutation == "receipt-result-missing" {
							delete(object, "Result")
						} else {
							object["UnknownProof"] = json.RawMessage(`true`)
						}
						raw, err = json.Marshal(object)
					}
				}
				if err == nil {
					sealed, err = f.p.seal("run_result", f.installation.InstanceID, "deferred:v1:"+f.run.ID, raw)
				}
				if err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE plugin_deferred_operations SET sealed=? WHERE run_id=?`, sealed, f.run.ID)
				}
			}
			if err != nil {
				t.Fatal("invalid test setup", err)
			}
			if err = wrapper.VerifyInitialization(ctx, f.proof); pluginErrorCode(err) != "PLUGIN_RUN_PROOF_INVALID" {
				t.Fatal("tampered evidence was not safely rejected", err)
			}
		})
	}
}

func TestPluginProjectionInitializationProofRejectsForeignInstanceRun(t *testing.T) {
	ctx := context.Background()
	f := pluginProjectionProofFixtureForTest(t, true, "init", "succeeded")
	clone, err := f.p.Clone(ctx, f.installation.InstanceID, PluginCloneRequest{Suffix: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wrapper := pluginProjectionWrapperForTest(t, ctx, tx, f.p, clone.InstanceID)
	if err = wrapper.VerifyInitialization(ctx, f.proof); pluginErrorCode(err) != "PLUGIN_RUN_PROOF_INVALID" {
		t.Fatal("foreign valid init run authorized another instance", err)
	}
}

func TestPluginProjectionTypedFailureCannotBeSwallowed(t *testing.T) {
	for _, operation := range []string{"config", "proof"} {
		t.Run(operation, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			before, beforeSealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			var denial error
			_, err := pluginFormMechanicalSave(t, p, ctx, in, before.Revision, json.RawMessage(`{"enable":false,"next":true}`), func(ctx context.Context, facade PluginDefaultsTransaction, _ PluginDefaultsChange) error {
				if operation == "config" {
					if _, err := pluginProjectionSQLForTest(t, facade).ExecContext(ctx, `UPDATE plugin_values SET sealed=x'00' WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID); err != nil {
						return err
					}
					_, denial = facade.Config(ctx)
				} else {
					denial = facade.VerifyInitialization(ctx, PluginInitializationProof{})
				}
				if denial == nil {
					t.Fatal("typed validation unexpectedly succeeded")
				}
				return nil // Deliberately hide the validation failure.
			})
			if err == nil || err != denial {
				t.Fatal("typed failure was not retained", err, denial)
			}
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, beforeSealed)
		})
	}
}

func TestPluginProjectionOperationalErrorsRemainIdentifiable(t *testing.T) {
	for _, operation := range []string{"config", "proof"} {
		for _, failure := range []string{"caller-canceled", "bound-canceled", "deadline", "tx-done", "database-error"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				f := pluginProjectionProofFixtureForTest(t, true, "init", "succeeded")
				boundCtx, boundCancel := context.WithCancel(context.Background())
				defer boundCancel()
				wrapper := f.transaction(t, boundCtx)
				ctx, callerCancel := context.WithCancel(context.Background())
				defer callerCancel()
				var want error
				switch failure {
				case "caller-canceled":
					callerCancel()
					want = context.Canceled
				case "bound-canceled":
					boundCancel()
					want = context.Canceled
				case "deadline":
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer deadlineCancel()
					want = context.DeadlineExceeded
				case "tx-done":
					if err := wrapper.tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					want = sql.ErrTxDone
				case "database-error":
					// DDL only in this disposable SQLite transaction; no user data
					// or files are removed. Rollback restores the fixture schema.
					if _, err := wrapper.tx.ExecContext(ctx, `ALTER TABLE plugin_installations RENAME TO projection_fixture_installations`); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if operation == "config" {
					_, err = wrapper.Config(ctx)
				} else {
					err = wrapper.VerifyInitialization(ctx, f.proof)
				}
				if err == nil || want != nil && !errors.Is(err, want) || want == nil && pluginErrorCode(err) != "" {
					t.Fatal("operational failure was hidden or reclassified", err)
				}
			})
		}
	}
}

type pluginProjectionRevokeMarshaler struct{ revoke func() }

func (m pluginProjectionRevokeMarshaler) MarshalJSON() ([]byte, error) {
	m.revoke()
	return []byte(`{"should_not_persist":true}`), nil
}

func TestPluginProjectionSaveDataRechecksAfterUserSerialization(t *testing.T) {
	for _, failure := range []string{"revoked", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			tx, err := p.store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			bound, cancel := context.WithCancel(ctx)
			defer cancel()
			wrapper := pluginProjectionWrapperForTest(t, bound, tx, p, in.InstanceID)
			revoke := wrapper.revokeProjection
			if failure == "canceled" {
				revoke = cancel
			}
			_, err = wrapper.SaveData(context.Background(), "late-marshaler", 0, pluginProjectionRevokeMarshaler{revoke: revoke})
			if failure == "revoked" && pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || failure == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("post-Marshaler revocation was ignored", err)
			}
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='late-marshaler'`, in.InstanceID).Scan(&count); err != nil || count != 0 {
				t.Fatal("revoked serialization wrote data", count, err)
			}
		})
	}
}

func TestPluginProjectionUnboundWrapperCannotFallback(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wrapper := pluginDefaultsTransaction{tx: tx, store: p, instance: in.InstanceID}
	if _, err = wrapper.Data(ctx, "missing"); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("unbound Data fallback", err)
	}
	calls := 0
	if _, err = wrapper.SaveData(ctx, "unbound", 0, pluginFormDispatchMarshalProbe{calls: &calls}); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || calls != 0 {
		t.Fatal("unbound SaveData fallback", calls, err)
	}
	if _, err = wrapper.Config(ctx); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("unbound Config fallback", err)
	}
	if err = wrapper.VerifyInitialization(ctx, PluginInitializationProof{}); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("unbound proof fallback", err)
	}
}

func TestPluginProjectionSaveDataCannotEraseOriginalFormScope(t *testing.T) {
	p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledCold, false)
	calls := 0
	_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
		tx, err := p.store.DB.BeginTx(callCtx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		wrapper := pluginProjectionWrapperForTest(t, callCtx, tx, p, in.InstanceID)
		_, denied := wrapper.SaveData(context.Background(), "erased-scope", 0, pluginFormDispatchMarshalProbe{calls: &calls})
		if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" || calls != 0 {
			t.Fatal("Background erased original scope before serialization", calls, denied)
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	}))
	if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || calls != 0 {
		t.Fatal("original scope denial was not sticky", calls, err)
	}
}

// Deliberately withhold AfterFunc delivery, so this test does not depend on
// scheduler luck. Err/Done/Deadline still come from a real standard context.
// Value hides cancelCtx internals so context.AfterFunc uses this explicit hook.
type pluginProjectionUndeliveredCancelContext struct{ context.Context }

func (c pluginProjectionUndeliveredCancelContext) Value(any) any { return nil }
func (c pluginProjectionUndeliveredCancelContext) AfterFunc(func()) func() bool {
	return func() bool { return true }
}

func TestPluginProjectionNarrowCallerCancellationRollsBackSwallowedSave(t *testing.T) {
	for _, failure := range []string{"cancel-in-marshal", "deadline-in-marshal"} {
		t.Run(failure, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			before, beforeSealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			want := error(context.Canceled)
			if failure == "deadline-in-marshal" {
				want = context.DeadlineExceeded
			}
			var denied error
			_, err := pluginFormMechanicalSave(t, p, ctx, in, before.Revision, json.RawMessage(`{"enable":false,"next":true}`), func(callCtx context.Context, facade PluginDefaultsTransaction, _ PluginDefaultsChange) error {
				if _, err := facade.SaveData(callCtx, "before-caller-cancel", 0, json.RawMessage(`{"earlier":true}`)); err != nil {
					return err
				}
				var caller context.Context
				var cancel context.CancelFunc
				if failure == "deadline-in-marshal" {
					caller, cancel = context.WithDeadline(callCtx, time.Now().Add(20*time.Millisecond))
				} else {
					caller, cancel = context.WithCancel(callCtx)
				}
				defer cancel()
				narrow := pluginProjectionUndeliveredCancelContext{Context: caller}
				stopDuringMarshal := func() {
					if failure == "deadline-in-marshal" {
						<-caller.Done() // Deterministic timer event, no sleep/polling.
					} else {
						cancel()
					}
				}
				_, denied = facade.SaveData(narrow, "after-caller-cancel", 0, pluginProjectionRevokeMarshaler{revoke: stopDuringMarshal})
				if !errors.Is(denied, want) || callCtx.Err() != nil {
					t.Fatal("narrow caller reason lost or parent canceled", denied, callCtx.Err())
				}
				var count int
				if err := pluginProjectionSQLForTest(t, facade).QueryRowContext(callCtx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='after-caller-cancel'`, in.InstanceID).Scan(&count); err != nil || count != 0 {
					t.Fatal("narrow cancellation reached SQL write", count, err)
				}
				return nil // The canceled subcall must still poison this projection.
			})
			if !errors.Is(err, want) || !errors.Is(denied, want) || ctx.Err() != nil {
				t.Fatal("swallowed narrow cancellation committed Next", err, denied, ctx.Err())
			}
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, beforeSealed)
			for _, key := range []string{"before-caller-cancel", "after-caller-cancel"} {
				if _, err = p.Data(ctx, in.InstanceID, key); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("canceled projection retained journal data", key, err)
				}
			}
		})
	}
}

func TestPluginProjectionCombinedContextRetainsEarlierDeadlineAndReason(t *testing.T) {
	f := pluginProjectionProofFixtureForTest(t, true, "init", "succeeded")
	bound, boundCancel := context.WithTimeout(context.Background(), time.Second)
	defer boundCancel()
	wrapper := f.transaction(t, bound)
	caller, callerCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer callerCancel()
	narrow := pluginProjectionUndeliveredCancelContext{Context: caller}
	combined, cancel, err := wrapper.projectionContext(narrow)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	wantDeadline, _ := caller.Deadline()
	gotDeadline, ok := combined.Deadline()
	if !ok || !gotDeadline.Equal(wantDeadline) {
		t.Fatal("combined context widened caller deadline", gotDeadline, wantDeadline)
	}
	<-caller.Done()
	<-combined.Done()
	if !errors.Is(combined.Err(), context.DeadlineExceeded) || bound.Err() != nil {
		t.Fatal("caller deadline was converted to Canceled or widened", combined.Err(), bound.Err())
	}
	if err = wrapper.projectionContextError(narrow, context.Canceled); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("operation cancellation was not restored to the caller reason", err)
	}
	if err = wrapper.checkProjection(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("narrow deadline was not retained for rollback", err)
	}
}
