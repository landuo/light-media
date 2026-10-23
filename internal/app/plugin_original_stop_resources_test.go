package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	serverstatus "light-media/internal/plugins/serverstatus"
)

// This checks actual Go host resource release on removing a loaded instance.
// The original MPServerStatus.stop_service is pass; this is not a claim that
// the Python business method itself explicitly cancels network requests.
func TestPluginOriginalStopReleasesOnlyOwnedServerStatusProbe(t *testing.T) {
	a, network := serverStatusTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	installCloneSource(t, a.pluginStore, serverstatus.Manifest())
	if _, err := a.pluginStore.SaveConfig(ctx, "MPServerStatus", 0, map[string]any{"enable": true}); err != nil {
		t.Fatal(err)
	}
	sibling, err := a.pluginStore.Clone(ctx, "MPServerStatus", PluginCloneRequest{Suffix: "StopSibling"})
	if err != nil {
		t.Fatal(err)
	}
	if a.startOriginalPlugin(ctx, "MPServerStatus") == nil || a.startOriginalPlugin(ctx, sibling.InstanceID) == nil {
		t.Fatal("actual initializer did not load owned instances")
	}
	source, err := a.pluginStore.Installation(ctx, "MPServerStatus")
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.pluginStore.Config(ctx, source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	siblingBefore, err := a.pluginStore.Config(ctx, sibling.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.SaveData(ctx, source.InstanceID, "owned-stop-history", 0, []string{"preserve"}); err != nil {
		t.Fatal(err)
	}
	dataBefore, err := a.pluginStore.Data(ctx, source.InstanceID, "owned-stop-history")
	if err != nil {
		t.Fatal(err)
	}
	a.pluginServerStatus.mu.Lock()
	targetSlot := a.pluginServerStatus.slots[source.InstanceID]
	siblingSlot := a.pluginServerStatus.slots[sibling.InstanceID]
	a.pluginServerStatus.mu.Unlock()
	if targetSlot == nil || siblingSlot == nil || targetSlot == siblingSlot {
		t.Fatal("real configure did not create separate slots")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	network.mu.Lock()
	network.entered, network.release = entered, release
	network.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := a.pluginServerStatusSnapshot(ctx, source, before.Revision, before.Value, "dashboard", json.RawMessage(`{"include_network_details":false}`))
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("owned probe never entered", ctx.Err())
	}
	a.stopOriginalPlugin(ctx, source.InstanceID)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("actual probe cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not cancel waiting owned probe")
	}
	if _, ok := a.pluginRunningInstances.Load(source.InstanceID); ok {
		t.Fatal("stopped running object retained")
	}
	if _, ok := a.pluginRunningInstances.Load(sibling.InstanceID); !ok {
		t.Fatal("sibling running object removed")
	}
	a.pluginServerStatus.mu.Lock()
	_, targetExists := a.pluginServerStatus.slots[source.InstanceID]
	unchangedSibling := a.pluginServerStatus.slots[sibling.InstanceID] == siblingSlot
	a.pluginServerStatus.mu.Unlock()
	if targetExists || !unchangedSibling {
		t.Fatal("target invalidation affected wrong slots")
	}
	after, err := a.pluginStore.Config(ctx, source.InstanceID)
	if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Value, after.Value) {
		t.Fatal("stop changed target config", err)
	}
	siblingAfter, err := a.pluginStore.Config(ctx, sibling.InstanceID)
	if err != nil || siblingBefore.Revision != siblingAfter.Revision || !bytes.Equal(siblingBefore.Value, siblingAfter.Value) {
		t.Fatal("stop changed sibling config", err)
	}
	dataAfter, err := a.pluginStore.Data(ctx, source.InstanceID, "owned-stop-history")
	if err != nil || dataBefore.Revision != dataAfter.Revision || !bytes.Equal(dataBefore.Value, dataAfter.Value) {
		t.Fatal("stop deleted target history", err)
	}
	if n, dns, tls := network.counts(); n != 1 || dns != 0 || tls != 0 {
		t.Fatalf("unexpected synthetic network counts status=%d dns=%d tls=%d", n, dns, tls)
	}
}
