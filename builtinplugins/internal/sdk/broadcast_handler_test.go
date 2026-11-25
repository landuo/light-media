package plugins

import "testing"

func TestBroadcastHandlerManifestBindsDeclaredMethods(t *testing.T) {
	m := Manifest{SchemaVersion: 1, PluginID: "OwnedBroadcast", Name: "Owned broadcast", Version: "1.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"stop", "first", "second"}, BroadcastHandlers: []BroadcastHandler{{Event: "owned.event", Method: "first"}, {Event: "owned.event", Method: "second"}, {Event: "owned.other", Method: "first"}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []BroadcastHandler{{Event: "owned.event", Method: "first"}, {Event: "owned.event", Method: "undeclared"}, {Event: "bad/event", Method: "first"}} {
		bad := m
		bad.BroadcastHandlers = append(append([]BroadcastHandler(nil), m.BroadcastHandlers...), invalid)
		if bad.Validate() == nil {
			t.Fatal("invalid broadcast declaration accepted", invalid)
		}
	}
}
