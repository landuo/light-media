package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/removelink"
	"reflect"
	"testing"
)

func TestRuntimeSupportCardsUsePackageMetadataForEveryInstance(t *testing.T) {
	a := newTestApp(t)
	m := removelink.Manifest()
	// Simulate already installed manifests from before this optional field existed.
	m.RuntimeSupport = nil
	for _, id := range []string{"RemoveLink", "owned-independent-instance"} {
		uninstallInstall(t, a, id, m, map[string]any{"enabled": false}, false)
	}
	cards, err := a.pluginCards(context.Background(), false)
	if err != nil || len(cards) != 2 {
		t.Fatalf("cards %d: %v", len(cards), err)
	}
	for _, c := range cards {
		if !reflect.DeepEqual(c.RuntimeSupport, removelink.Manifest().RuntimeSupport) {
			t.Fatal("instance did not inherit package support", c.InstanceID)
		}
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil || len(wire["runtime_support"]) == 0 {
			t.Fatal("support missing on wire", err)
		}
	}
	market, err := newTestApp(t).pluginCards(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, c := range market {
		if c.PluginID == "RemoveLink" {
			found++
		}
		if c.PluginID == "RemoveLink" && !reflect.DeepEqual(c.RuntimeSupport, removelink.Manifest().RuntimeSupport) {
			t.Fatal("market support missing")
		}
	}
	if found != 1 {
		t.Fatalf("expected one uninstalled RemoveLink market card, got %d", found)
	}
}
