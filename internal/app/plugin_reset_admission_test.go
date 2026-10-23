package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
)

// These are isolated synthetic fixtures through the actual HTTP/Store Reset
// entry points. They are not evidence of a real user's plugin reset.
func TestPluginResetAdmissionAuthenticatedHTTP(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	in := pluginFormHTTPInstallation(t, a, "SystemProcess", json.RawMessage(`{"enable":false,"sentinel":"owned-test"}`))
	before, sealed := pluginDefaultsStoredConfig(t, a.pluginStore, in.InstanceID)
	path := "/api/plugins/" + in.InstanceID + "/reset"
	if w := callTest(t, a.Handler(), http.MethodPost, path, "{}", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated reset status %d", w.Code)
	}
	assertPluginDefaultsConfigUnchanged(t, a.pluginStore, in.InstanceID, before, sealed)
	a.pluginStore.runSupervisor.mu.Lock()
	fenced := len(a.pluginStore.runSupervisor.fences)
	a.pluginStore.runSupervisor.mu.Unlock()
	if fenced != 0 {
		t.Fatal("unauthenticated request fenced the instance")
	}
	if w := callTest(t, a.Handler(), http.MethodPost, path, "{}", cookie, ""); w.Code != http.StatusOK {
		t.Fatalf("authenticated reset %d: %s", w.Code, w.Body.String())
	}
	after := lifecycleInstallation(t, a.pluginStore, in.InstanceID)
	if after.Generation != in.Generation+1 || !after.Installed || after.DesiredEnabled {
		t.Fatal("actual reset transaction did not publish the next disabled generation")
	}
	if _, err := a.pluginStore.Config(context.Background(), in.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("actual reset retained owned synthetic config", err)
	}
	a.pluginStore.runSupervisor.mu.Lock()
	fenced = len(a.pluginStore.runSupervisor.fences)
	a.pluginStore.runSupervisor.mu.Unlock()
	if fenced != 0 {
		t.Fatal("confirmed reset did not release admissions")
	}
}

func TestPluginResetAdmissionFinalGenerationGuard(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false,"sentinel":"owned-test"}`))
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	ctx, proof, err := p.beginResetAdmission(ctx, in, false)
	if err != nil {
		t.Fatal(err)
	}
	// A synthetic concurrent lifecycle change occurs after admission but
	// before the final Reset SQL transaction. Its config must be preserved.
	if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, in.InstanceID); err != nil {
		t.Fatal(err)
	}
	err = p.Reset(ctx, in.InstanceID)
	if pluginErrorCode(err) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("stale admission reached the mutation", err)
	}
	if finishErr := proof.finish(err); !errors.Is(finishErr, err) {
		t.Fatal("known rollback lost the failure", finishErr)
	}
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
	p.runSupervisor.mu.Lock()
	fence := p.runSupervisor.fences[in.InstanceID]
	p.runSupervisor.mu.Unlock()
	if fence != nil {
		t.Fatal("known transaction rollback kept a non-native fence")
	}
}

func TestPluginResetAdmissionDirectStoreAndFormScope(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	scope := context.WithValue(ctx, pluginFormCallbackScopeKey{}, &pluginFormCallbackScope{readOnly: true})
	if err := p.Reset(scope, in.InstanceID); err == nil {
		t.Fatal("Form callback reached Reset admission")
	}
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
	if err := p.Reset(ctx, in.InstanceID); err != nil {
		t.Fatal("trusted direct Store.Reset did not acquire its own proof", err)
	}
	if actual := lifecycleInstallation(t, p, in.InstanceID); actual.Generation != in.Generation+1 {
		t.Fatal("direct Reset did not publish")
	}
}

func TestPluginResetAdmissionCanceledJoinRetainsFenceAndData(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	// A synthetic reserved ticket deliberately has no cleanup acknowledgement.
	// This tests the real admission boundary, not a completed native execution.
	ticket := supervisorDraftReserve(t, p.runSupervisor, supervisorDraftAdmission(in.InstanceID, "synthetic-unresolved"))
	joinCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, _, err := p.beginResetAdmission(joinCtx, in, false)
	if pluginErrorCode(err) != "PLUGIN_RESET_UNRESOLVED" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("canceled join was reported as success", err)
	}
	if !ticket.attempt.cancelRequested {
		t.Fatal("captured attempt was not canceled")
	}
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
	p.runSupervisor.mu.Lock()
	fence := p.runSupervisor.fences[in.InstanceID]
	p.runSupervisor.mu.Unlock()
	if fence == nil || fence.phase != pluginRunTransitionQuarantined {
		t.Fatal("unresolved join reopened admissions")
	}
}

func TestPluginResetAdmissionUnknownTransactionCannotRelease(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	_, proof, err := p.beginResetAdmission(ctx, in, false)
	if err != nil {
		t.Fatal(err)
	}
	// Outcome fault injection only. No fake Commit result authorizes deletion.
	proof.mu.Lock()
	proof.outcome = pluginResetUnknown
	proof.mu.Unlock()
	if err = proof.finish(errors.New("synthetic ambiguous commit")); pluginErrorCode(err) != "PLUGIN_RESET_UNRESOLVED" {
		t.Fatal("unknown transaction did not return an explicit failure", err)
	}
	p.runSupervisor.mu.Lock()
	fence := p.runSupervisor.fences[in.InstanceID]
	p.runSupervisor.mu.Unlock()
	if fence == nil || fence.phase != pluginRunTransitionQuarantined {
		t.Fatal("unknown transaction reopened admissions")
	}
}
