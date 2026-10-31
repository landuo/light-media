package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// Actual App last-owner routing with the existing serial standard worker.
// Manager.remove is called at its private component boundary, not represented
// as a completed SDK self-remove/daemon test. No fixture or product seam is used.
func TestOriginalRetainedLegacyRemovedLastOwnerTerminatesTransport(t *testing.T) {
	a, _, object := ownedStandardSDKFixture(t)
	object.sessionMu.Lock()
	client := object.session
	object.sessionMu.Unlock()
	if client == nil || client.SupportsConcurrent() || client.SupportsTransportClose() {
		t.Fatal("actual legacy serial worker without transport-close required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	before := ownedStandardSnapshotRead(t, object)
	if before.Nonce <= 0 || before.Calls["close"] != 0 || before.Calls["stop_service"] != 0 || before.Calls["stop"] != 0 {
		t.Fatal("fixture already ran business cleanup", before)
	}
	// The worker declares API/job owners; remove them explicitly while it is
	// still manager-owned so this test isolates one exact last Operation pin.
	a.unregisterOriginalPluginAPIs(ownedStandardFixtureID)
	a.removeOriginalPluginJobs(ownedStandardFixtureID)
	pinned, operation, err := a.pinOriginalRunningObject(ctx, ownedStandardFixtureID)
	if err != nil || pinned != object || operation == nil {
		t.Fatal("exact live legacy object not pinned", err)
	}
	defer object.releaseOriginalRetainedOwner(operation)
	life := operation.lifetime
	scope := originalBaseCallScope{validate: func(ctx context.Context) error { return ctx.Err() }}
	result, err := a.originalSharedManagerRemove(ctx, scope, json.RawMessage(`{"plugin_id":"OwnedStandardFixture"}`))
	if err != nil || string(result) != "null" {
		t.Fatal("logical remove failed", string(result), err)
	}
	if !object.managerRemoved.Load() || object.stopping.Load() {
		t.Fatal("logical remove did not select retained legacy state")
	}
	if _, present := a.pluginRunningInstances.Load(ownedStandardFixtureID); present {
		t.Fatal("removed legacy object remains manager-visible")
	}
	parentCtx, err := object.originalRetainedParentContext(ctx, operation)
	if err != nil {
		t.Fatal(err)
	}
	var after ownedStandardSnapshot
	if err := object.readSession(parentCtx, "snapshot", &after); err != nil || after.Nonce != before.Nonce || after.InitCount != before.InitCount || after.Calls["close"] != 1 || after.Calls["stop_service"] != 1 || after.Calls["stop"] != 0 {
		t.Fatal("logical remove replaced worker or changed source hook counts", after, err)
	}
	markers := make(map[string]PluginStoredValue)
	for _, method := range []string{"close", "stop_service"} {
		row, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "owned_"+method)
		var observed struct {
			Nonce int      `json:"nonce"`
			Order []string `json:"order"`
		}
		wantOrder := []string{"close"}
		if method == "stop_service" {
			wantOrder = append(wantOrder, "stop_service")
		}
		if err != nil || json.Unmarshal(row.Value, &observed) != nil || observed.Nonce != before.Nonce || !reflect.DeepEqual(observed.Order, wantOrder) {
			t.Fatal("real serial source hook marker missing", method, string(row.Value), err)
		}
		markers[method] = row
	}
	if _, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "owned_stop"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("logical remove invoked business stop or storage failed", err)
	}
	select {
	case <-life.retired():
		t.Fatal("legacy child reaped before its last owner release")
	default:
	}
	// This production release must reach closeOriginalRetainedTransport's
	// managerRemoved + !SupportsTransportClose branch, not the old StopWithHost
	// fallback. Its saved intentional terminal is deliberately not exit zero.
	object.releaseOriginalRetainedOwner(operation)
	retainedRuntimeAwait(t, life.retired(), "legacy last-owner actual termination")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	err = client.Stop(stopCtx)
	var terminal *plugins.Error
	if !errors.As(err, &terminal) || terminal.Code != "transport_terminated" {
		t.Fatal("App did not retain actual intentional physical termination receipt", err)
	}
	object.sessionMu.Lock()
	remaining := object.session
	object.sessionMu.Unlock()
	if remaining != nil {
		t.Fatal("physically terminated exact client retained in object")
	}
	if _, present := a.pluginRetainedObjects.Load(object); present {
		t.Fatal("actual retired legacy object leaked cleanup entry")
	}
	for method, before := range markers {
		after, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "owned_"+method)
		if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) {
			t.Fatal("last-owner physical retirement repeated business hook", method, err)
		}
	}
	if _, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "owned_stop"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("last-owner physical retirement introduced business stop", err)
	}
	t.Logf("actual serial PID %d survived logical remove; last exact owner selected App transport-only termination with saved terminal receipt", before.Nonce)
}
