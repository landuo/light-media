package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/uninstall"
)

func deferredFixture(t *testing.T) (*App, PluginInstallation, PluginRun) {
	t.Helper()
	a := newTestApp(t)
	uninstallInstall(t, a, "CategoryEditor", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"CategoryEditor"}, "private": "synthetic-secret-do-not-log"}, true)
	installation, err := a.pluginStore.Installation(context.Background(), run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	return a, installation, run
}
func deferredIdentity(in PluginInstallation) plugins.Identity {
	return plugins.Identity{PluginID: in.PluginID, InstanceID: in.InstanceID, Version: in.Version}
}
func deferredStillInstalled(t *testing.T, a *App, want bool) {
	t.Helper()
	in, err := a.pluginStore.Installation(context.Background(), "CategoryEditor")
	if err != nil || in.Installed != want {
		t.Fatal("target state", in.Installed, err)
	}
}
func TestPluginDeferredHandshakeHasNoBusinessEffectAndRequiresTrustedExecution(t *testing.T) {
	a, in, run := deferredFixture(t)
	ctx := context.Background()
	d, err := a.preparePluginDeferred(ctx, in, run)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	ack, err := d.handshake(ctx, deferredIdentity(in), uninstall.ApplyCapability, json.RawMessage(` { } `))
	if err != nil {
		t.Fatal(err)
	}
	deferredStillInstalled(t, a, true)
	if err = a.requireDeferredExecution(ctx, &run, uninstall.ApplyCapability); err == nil {
		t.Fatal("ordinary context grants execution")
	}
	forged := context.WithValue(ctx, pluginDeferredExecutionKey{}, d)
	if err = a.requireDeferredExecution(forged, &run, uninstall.ApplyCapability); err == nil {
		t.Fatal("prepared context grants execution")
	}
	if err = d.ready(plugins.RunResult{HostCalls: 1, Result: ack}); err != nil {
		t.Fatal(err)
	}
	result, err := d.execute(ctx, &run)
	if err != nil || !bytes.Contains(result, []byte(`"own_config_reset":true`)) {
		t.Fatal(string(result), err)
	}
	deferredStillInstalled(t, a, false)
	r, sealed, err := a.pluginStore.readDeferred(ctx, a.Store.DB, d.run)
	if err != nil || r.Phase != "confirmed" || !bytes.Equal(r.Result, result) || bytes.Contains(sealed, []byte("synthetic-secret")) {
		t.Fatal("receipt", r.Phase, err)
	}
	if err = d.changePhase(ctx, "prepared", "started", nil); err == nil {
		t.Fatal("confirmed operation starts again")
	}
}

func TestPluginDeferredPayloadAndTerminalProtocolRejectTampering(t *testing.T) {
	for _, scenario := range []string{"wrong-identity", "wrong-method", "extra-field", "duplicate-event", "null", "two-calls", "ignored-bad-call", "forged-result", "no-call", "wrong-count"} {
		t.Run(scenario, func(t *testing.T) {
			a, in, run := deferredFixture(t)
			ctx := context.Background()
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			identity, method, raw := deferredIdentity(in), uninstall.ApplyCapability, json.RawMessage(`{}`)
			switch scenario {
			case "wrong-identity":
				identity.InstanceID = "elsewhere"
			case "wrong-method":
				method = uninstall.OptionsCapability
			case "extra-field":
				raw = json.RawMessage(`{"force":true}`)
			case "duplicate-event":
				raw = json.RawMessage(`{"event_id":"x","event_id":"x"}`)
			case "null":
				raw = json.RawMessage(`null`)
			}
			ack := json.RawMessage(`null`)
			if scenario != "no-call" {
				ack, _ = d.handshake(ctx, identity, method, raw)
			}
			if scenario == "two-calls" {
				_, _ = d.handshake(ctx, identity, method, raw)
			}
			if scenario == "ignored-bad-call" {
				_, _ = d.handshake(ctx, identity, "wrong", raw)
			}
			result := plugins.RunResult{HostCalls: 1, Result: ack}
			if scenario == "forged-result" {
				result.Result = json.RawMessage(`{"applied":true}`)
			}
			if scenario == "wrong-count" {
				result.HostCalls = 2
			}
			if err = d.ready(result); err == nil {
				t.Fatal("invalid worker authorized effect")
			}
			deferredStillInstalled(t, a, true)
			r, _, _ := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
			if r.Phase != "prepared" {
				t.Fatal(r.Phase)
			}
		})
	}
	for _, raw := range []string{`{"event_id":"one","event_id":"one"}`, `{"event_id":"one","extra":null}`, `{"event_id":1}`, `{"event_id":"two"}`, `{"event_id":"one"} []`} {
		if deferredPayloadEqual([]byte(raw), []byte(`{"event_id":"one"}`)) {
			t.Fatal("unsafe event payload", raw)
		}
	}
	if !deferredPayloadEqual([]byte(" { \"event_id\" : \"one\" } "), []byte(`{"event_id":"one"}`)) {
		t.Fatal("valid event whitespace")
	}
}

func TestPluginDeferredWorkerFixture(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--deferred-worker" || i+1 >= len(os.Args) {
			continue
		}
		mode := os.Args[i+1]
		handler := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			if method == "stop" {
				return json.RawMessage(`null`), nil
			}
			if method != "invoke" {
				return nil, errors.New("fixture invoke only")
			}
			if mode == "no-call" {
				return json.RawMessage(`null`), nil
			}
			ack, err := host(ctx, uninstall.ApplyCapability, json.RawMessage(`{}`))
			if err != nil {
				return nil, err
			}
			switch mode {
			case "fail":
				return nil, errors.New("synthetic worker failure after handshake")
			case "forged":
				return json.RawMessage(`{"applied":true}`), nil
			case "twice":
				_, _ = host(ctx, uninstall.ApplyCapability, json.RawMessage(`{}`))
			case "extra":
				_, _ = host(ctx, uninstall.OptionsCapability, json.RawMessage(`{}`))
			}
			return ack, nil
		}
		if plugins.Serve(context.Background(), os.Stdin, os.Stdout, uninstall.Manifest(), handler) != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}
func TestPluginDeferredRealWorkerFailureDoesNotStartOperation(t *testing.T) {
	for _, mode := range []string{"valid", "fail", "forged", "twice", "extra", "no-call"} {
		t.Run(mode, func(t *testing.T) {
			a, _, run := deferredFixture(t)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			a.pluginProcess = make(chan struct{}, 1)
			a.pluginCommand = func(string) (plugins.Command, error) {
				return plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginDeferredWorkerFixture$", "--", "--deferred-worker", mode}}, nil
			}
			a.executePluginRun(context.Background(), run)
			current, err := a.pluginStore.Run(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.pluginProcess) != 0 {
				t.Fatal("worker slot leaked")
			}
			if mode == "valid" {
				if current.State != "succeeded" || !bytes.Contains(current.Result, []byte(`"own_config_reset":true`)) {
					t.Fatal(current.State, current.ErrorCode, string(current.Result))
				}
				deferredStillInstalled(t, a, false)
			} else {
				if current.State != "failed" {
					t.Fatal("invalid worker succeeded", current.State, current.ErrorCode)
				}
				deferredStillInstalled(t, a, true)
				if bytes.Contains(current.Result, []byte(`"deferred"`)) {
					t.Fatal("ack stored as business result")
				}
				r, _, err := a.pluginStore.readDeferred(context.Background(), a.Store.DB, run)
				if err != nil || r.Phase != "prepared" {
					t.Fatal(r.Phase, err)
				}
			}
		})
	}
}
func TestPluginDeferredRecoveryNeverReplaysStartedOrCreatesFreshStartupRun(t *testing.T) {
	for _, phase := range []string{"prepared", "started", "uncertain", "confirmed"} {
		t.Run(phase, func(t *testing.T) {
			a, in, run := deferredFixture(t)
			ctx := context.Background()
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "prepared" {
				if err = d.changePhase(ctx, "prepared", "started", nil); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "uncertain" {
				if err = d.changePhase(ctx, "started", "uncertain", nil); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "confirmed" {
				if err = d.changePhase(ctx, "started", "confirmed", json.RawMessage(`{"actual":"synthetic confirmed receipt"}`)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			if err = a.pluginStore.recoverPluginRuns(ctx); err != nil {
				t.Fatal(err)
			}
			actual, _ := a.pluginStore.Run(ctx, run.ID)
			expected := "reconcile_pending"
			if phase == "prepared" {
				expected = "pending"
			}
			if phase == "confirmed" {
				expected = "succeeded"
			}
			if actual.State != expected {
				t.Fatal(phase, actual.State)
			}
			if phase == "started" || phase == "uncertain" {
				if err = a.initializeNativePlugins(ctx); err != nil {
					t.Fatal(err)
				}
				var count int
				if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&count); err != nil || count != 1 {
					t.Fatal("new run bypassed uncertain original", count, err)
				}
			}
			deferredStillInstalled(t, a, true)
		})
	}
}
func TestPluginDeferredRunIdentityAndSealedTamperingFailClosed(t *testing.T) {
	a, in, run := deferredFixture(t)
	ctx := context.Background()
	d, err := a.preparePluginDeferred(ctx, in, run)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"generation", "revision", "input", "action", "run", "instance"} {
		forged := run
		switch field {
		case "generation":
			forged.Generation++
		case "revision":
			forged.ConfigRevision++
		case "input":
			forged.Input = json.RawMessage(`{}`)
		case "action":
			forged.Action = "reload"
		case "run":
			forged.ID = "other"
		case "instance":
			forged.InstanceID = "elsewhere"
		}
		if deferredSameRun(d.record, in, forged) {
			t.Fatal("forged bound identity", field)
		}
	}
	_, sealed, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = a.Store.DB.Exec(`UPDATE plugin_deferred_operations SET sealed=? WHERE run_id=?`, sealed, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.readDeferred(ctx, a.Store.DB, run); err == nil {
		t.Fatal("corrupt receipt accepted")
	}
	if err = a.pluginStore.recoverDeferredRuns(ctx); err == nil {
		t.Fatal("recovery ignored corrupt receipt")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = d.handshake(canceled, deferredIdentity(in), uninstall.ApplyCapability, json.RawMessage(`{}`)); err == nil {
		t.Fatal("canceled handshake")
	}
	if strings.Contains(string(sealed), "synthetic-secret") {
		t.Fatal("private source leaked")
	}
}

func TestPluginDeferredStartedRequiresCurrentRunningInstallation(t *testing.T) {
	for _, mutation := range []string{"terminal", "revision", "reset", "uninstall", "input"} {
		t.Run(mutation, func(t *testing.T) {
			a, in, run := deferredFixture(t)
			ctx := context.Background()
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "terminal":
				err = finishClaimedFixtureRun(ctx, a.pluginStore, run, "failed", "PLUGIN_EXECUTION_FAILED", nil)
			case "revision":
				_, err = a.pluginStore.SaveConfig(ctx, run.InstanceID, run.ConfigRevision, map[string]any{"plugin_ids": []string{}})
			case "reset":
				err = a.pluginStore.Reset(ctx, run.InstanceID)
			case "uninstall":
				err = a.pluginStore.Uninstall(ctx, run.InstanceID)
			case "input":
				var sealed []byte
				sealed, err = a.pluginStore.seal("run", run.InstanceID, run.ID, []byte(`{"action":"init","config":{}}`))
				if err == nil {
					_, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, sealed, run.ID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = d.changePhase(ctx, "prepared", "started", nil); err == nil {
				t.Fatal("stale run entered actual execution")
			}
			r, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
			if err != nil || r.Phase != "prepared" {
				t.Fatal("known unstarted became uncertain", r.Phase, err)
			}
			deferredStillInstalled(t, a, true)
		})
	}
}
func TestPluginDeferredConfirmedReceiptRepairsFailedResultStorage(t *testing.T) {
	for _, failureCode := range []string{"PLUGIN_RESULT_SAVE_FAILED", "PLUGIN_DEFERRED_UNCERTAIN"} {
		t.Run(failureCode, func(t *testing.T) {
			a, in, run := deferredFixture(t)
			ctx := context.Background()
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			ack, err := d.handshake(ctx, deferredIdentity(in), uninstall.ApplyCapability, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if err = d.ready(plugins.RunResult{HostCalls: 1, Result: ack}); err != nil {
				t.Fatal(err)
			}
			result, err := d.execute(ctx, &run)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`CREATE TRIGGER synthetic_deferred_result_failure BEFORE UPDATE OF result ON plugin_runs WHEN NEW.state='succeeded' BEGIN SELECT RAISE(ABORT,'synthetic final result failure'); END`); err != nil {
				t.Fatal(err)
			}
			execution := executionLeaseActivate(t, a.pluginStore, run)
			defer run.executionLease.runCleanup()
			if err = a.pluginStore.finishRun(execution, run.ID, "succeeded", "", result, false); err == nil || !strings.Contains(err.Error(), "synthetic final result failure") {
				t.Fatal("terminal write did not reach injected result save failure", err)
			}
			if err = a.pluginStore.failPluginResult(execution, run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err != nil {
				t.Fatal(err)
			}
			if err = a.pluginStore.releaseSupervisedRun(run); err != nil {
				t.Fatal(err)
			}
			if failureCode != "PLUGIN_RESULT_SAVE_FAILED" {
				if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET error_code=? WHERE id=?`, failureCode, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			failed, _ := a.pluginStore.Run(ctx, run.ID)
			if failed.State != "failed" {
				t.Fatal(failed.State)
			}
			if _, err = a.Store.DB.Exec(`DROP TRIGGER synthetic_deferred_result_failure`); err != nil {
				t.Fatal(err)
			}
			// Reinstall the target. Recovery must consume the old receipt rather than
			// entering its host again and deleting this new installed generation.
			raw, _ := json.Marshal(builtin.CategoryEditorManifest())
			if err = a.pluginStore.Install(ctx, PluginInstallation{InstanceID: "CategoryEditor", PluginID: "CategoryEditor", Version: builtin.CategoryEditorManifest().Version, ManifestSchemaVersion: 1, Manifest: raw, DesiredEnabled: true}); err != nil {
				t.Fatal(err)
			}
			if err = a.pluginStore.recoverPluginRuns(ctx); err != nil {
				t.Fatal(err)
			}
			recovered, _ := a.pluginStore.Run(ctx, run.ID)
			if recovered.State != "succeeded" || !bytes.Equal(recovered.Result, result) || recovered.ConfigRevision != 2 {
				t.Fatal("confirmed receipt was not recovered", recovered.State, recovered.ConfigRevision)
			}
			deferredStillInstalled(t, a, true)
		})
	}
}

func TestPluginDeferredRetryPreservesOriginalRunAndRejectsStartedStaleOrConcurrentWork(t *testing.T) {
	for _, scenario := range []string{"safe", "started", "uncertain", "confirmed", "new-config", "new-generation", "other-pending"} {
		t.Run(scenario, func(t *testing.T) {
			a, in, run := deferredFixture(t)
			ctx := context.Background()
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "started" || scenario == "uncertain" || scenario == "confirmed" {
				if err = d.changePhase(ctx, "prepared", "started", nil); err != nil {
					t.Fatal(err)
				}
				if scenario != "started" {
					if err = d.changePhase(ctx, "started", scenario, json.RawMessage(`null`)); err != nil {
						t.Fatal(err)
					}
				}
			}
			execution := executionLeaseActivate(t, a.pluginStore, run)
			defer run.executionLease.runCleanup()
			if err = a.pluginStore.finishRun(execution, run.ID, "failed", "PLUGIN_EXECUTION_FAILED", nil, true); err != nil {
				t.Fatal(err)
			}
			if err = a.pluginStore.releaseSupervisedRun(run); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "new-config":
				_, err = a.pluginStore.SaveConfig(ctx, run.InstanceID, run.ConfigRevision, map[string]any{"plugin_ids": []string{}})
			case "new-generation":
				err = a.pluginStore.Reset(ctx, run.InstanceID)
			case "other-pending":
				_, err = a.pluginStore.EnqueueRun(ctx, run.InstanceID, "init", nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			retried, err := a.retryPluginDeferred(ctx, run.InstanceID, run.ID)
			if scenario != "safe" {
				if err == nil {
					t.Fatal("unsafe retry accepted", scenario)
				}
				deferredStillInstalled(t, a, true)
				return
			}
			if err != nil || retried.ID != run.ID || retried.State != "pending" || !bytes.Equal(retried.Input, run.Input) {
				t.Fatal("retry replaced frozen run", retried.ID, retried.State, err)
			}
			var blocks int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_startup_blocks WHERE run_id=?`, run.ID).Scan(&blocks); err != nil || blocks != 0 {
				t.Fatal("retry kept startup block", blocks, err)
			}
			claimed, err := a.pluginStore.ClaimRun(ctx)
			if err != nil || claimed.ID != run.ID {
				t.Fatal(claimed.ID, err)
			}
			useBuiltInPluginProcess(t, a)
			a.executePluginRun(ctx, claimed)
			final, _ := a.pluginStore.Run(ctx, run.ID)
			if final.State != "succeeded" {
				t.Fatal(final.State, final.ErrorCode)
			}
			deferredStillInstalled(t, a, false)
		})
	}
}
func TestPluginDeferredRecoveryRetainsLegacyCommittedUninstallReceipt(t *testing.T) {
	for _, self := range []bool{false, true} {
		t.Run(map[bool]string{false: "target", true: "self"}[self], func(t *testing.T) {
			a, _, run := deferredFixture(t)
			ctx := context.Background()
			if self {
				config, err := a.pluginStore.Config(ctx, run.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = a.pluginStore.SaveConfigAndQueue(ctx, run.InstanceID, config.Revision, map[string]any{"plugin_ids": []string{"CategoryEditor", "PluginUnInstall"}}); err != nil {
					t.Fatal(err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='canceled' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
				run, err = a.pluginStore.ClaimRun(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Direct trusted host exercises the older no-deferred receipt format.
			uninstallApply(t, a, &run)
			execution := executionLeaseActivate(t, a.pluginStore, run)
			defer run.executionLease.runCleanup()
			if err := a.pluginStore.failPluginResult(execution, run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err != nil {
				t.Fatal(err)
			}
			if err := a.pluginStore.releaseSupervisedRun(run); err != nil {
				t.Fatal(err)
			}
			if err := a.pluginStore.recoverPluginRuns(ctx); err != nil {
				t.Fatal(err)
			}
			recovered, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || recovered.State != "succeeded" || !bytes.Contains(recovered.Result, []byte(`"own_config_reset":true`)) {
				t.Fatal("legacy committed receipt", recovered.State, err)
			}
		})
	}
}

func TestPluginDeferredRetryHTTPUsesOriginalRunAndShowsOnlySafeActionMetadata(t *testing.T) {
	a, _, run := deferredFixture(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	handler := a.Handler()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginCommand = func(string) (plugins.Command, error) {
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginDeferredWorkerFixture$", "--", "--deferred-worker", "fail"}}, nil
	}
	a.executePluginRun(ctx, run)
	history := callTest(t, handler, "GET", "/api/plugins/PluginUnInstall/runs", "", cookie, "")
	if history.Code != 200 || !strings.Contains(history.Body.String(), `"retryable":true`) {
		t.Fatal("safe retry action missing", history.Code, history.Body.String())
	}
	for _, secret := range []string{"synthetic-secret", "InputSHA", `"deferred"`, `"input"`, `"result"`} {
		if strings.Contains(history.Body.String(), secret) {
			t.Fatal("private operation data in history", secret)
		}
	}
	body, _ := json.Marshal(map[string]string{"run_id": run.ID})
	if w := callTest(t, handler, "POST", "/api/plugins/PluginUnInstall/retry", string(body), nil, ""); w.Code != 401 {
		t.Fatal("unauthenticated retry", w.Code)
	}
	if w := callTest(t, handler, "POST", "/api/plugins/PluginUnInstall/retry", string(body), cookie, "http://evil.example"); w.Code != 403 {
		t.Fatal("cross-origin retry", w.Code)
	}
	if w := callTest(t, handler, "POST", "/api/plugins/CategoryEditor/retry", string(body), cookie, ""); w.Code == 200 {
		t.Fatal("cross-instance retry")
	}
	response := callTest(t, handler, "POST", "/api/plugins/PluginUnInstall/retry", string(body), cookie, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), run.ID) || strings.Contains(response.Body.String(), "synthetic-secret") {
		t.Fatal("retry request", response.Code, response.Body.String())
	}
	claimed, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || claimed.ID != run.ID || !bytes.Equal(claimed.Input, run.Input) {
		t.Fatal("HTTP retry replaced invocation", claimed.ID, err)
	}
	useBuiltInPluginProcess(t, a)
	a.executePluginRun(ctx, claimed)
	after := callTest(t, handler, "GET", "/api/plugins/PluginUnInstall/runs", "", cookie, "")
	if after.Code != 200 || strings.Contains(after.Body.String(), `"retryable":true`) {
		t.Fatal("finished task retained retry action", after.Code, after.Body.String())
	}
	deferredStillInstalled(t, a, false)
}
