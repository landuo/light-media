package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// Exercise the actual SDK capability, lifecycle publication and native owner.
// A watcher failure after publication must remain visible to the user instead
// of producing a successful run while its durable transition is still pending.
func TestPluginRemoveLinkPublishedConfigureFailureIsNotSuccess(t *testing.T) {
	a, item := historyRemovalFixture(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	var opens atomic.Int32
	a.pluginRemoveLink.watch = func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		if opens.Add(1) > 1 {
			return nil, errors.New("owned observer creation failure")
		}
		return &cleanupEffectsIdleWatcher{events: make(chan cleanuphost.Event), errs: make(chan error)}, nil
	}
	cfg := map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0] + "\n" + a.Settings().Roots[0].Path, "delayed_deletion": true, "delay_seconds": 3600, "delete_history": false, "delete_torrents": false, "notify": false, "scrap": false}
	initial := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), cfg, true)
	a.executePluginRun(ctx, initial)
	done, err := a.pluginStore.Run(ctx, initial.ID)
	if err != nil || done.State != "succeeded" || opens.Load() != 1 {
		t.Fatalf("initial SDK/observer: state=%s opens=%d error=%v", done.State, opens.Load(), err)
	}
	source, err := os.ReadFile(item.Source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.ReadFile(item.Target)
	if err != nil {
		t.Fatal(err)
	}
	current, err := a.pluginStore.Config(ctx, initial.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var next map[string]any
	if err = json.Unmarshal(current.Value, &next); err != nil {
		t.Fatal(err)
	}
	next["delay_seconds"] = 7200
	removeLinkHTTPCall(t, a, cookie, "PUT", "/api/plugins/RemoveLink/config", map[string]any{"revision": current.Revision, "config": next})
	run := removeLinkOwnerClaim(t, a, initial.InstanceID)
	a.executePluginRun(ctx, run)
	got, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 2 {
		t.Fatalf("failure did not reach the actual second observer: opens=%d state=%s code=%s", opens.Load(), got.State, got.ErrorCode)
	}
	if got.State == "succeeded" || got.ErrorCode == "" {
		t.Fatalf("native watcher failed but actual SDK reported state=%s code=%s", got.State, got.ErrorCode)
	}
	owner := a.pluginRemoveLink.owners[initial.InstanceID]
	durable, err := owner.j.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Transition == nil || durable.Transition.RunID != run.ID || durable.Transition.Phase == "applied" {
		t.Fatalf("failed configure falsely applied: transition=%+v", durable.Transition)
	}
	for name, want := range map[string][]byte{item.Source: source, item.Target: target} {
		got, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("owned media changed after failed configure: %v", err)
		}
	}
	t.Logf("actual SDK failure state=%s code=%s transition=%s observer_creates=%d", got.State, got.ErrorCode, durable.Transition.Phase, opens.Load())
	if len(a.pluginProcess) != 0 {
		t.Fatal("SDK process slot leaked after native failure")
	}
}
