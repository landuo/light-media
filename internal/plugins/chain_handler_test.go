package plugins

import "testing"

func TestChainHandlerManifestDeclaration(t *testing.T) {
	m := Manifest{SchemaVersion: 1, PluginID: "OwnedChain", Name: "Owned chain", Version: "1.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"stop", "first", "second", "event"}}
	p := 0
	m.ChainHandlers = []ChainHandler{{Event: "command.register", Method: "first"}, {Event: "command.register", Method: "second", Priority: &p}}
	if m.Validate() != nil || m.ChainHandlers[0].Order() != 10 || m.ChainHandlers[1].Order() != 0 {
		t.Fatal("default and explicit zero priority")
	}
	m.ChainHandlers = append(m.ChainHandlers, ChainHandler{Event: "other.chain", Method: "first"})
	if m.Validate() != nil {
		t.Fatal("same method may subscribe to separate events")
	}
	for _, bad := range []ChainHandler{{Event: "command.register", Method: "first"}, {Event: "bad/event", Method: "first"}, {Event: "command.register", Method: "undeclared"}} {
		badManifest := m
		badManifest.ChainHandlers = append(append([]ChainHandler(nil), m.ChainHandlers...), bad)
		if badManifest.Validate() == nil {
			t.Fatalf("accepted invalid chain declaration %+v", bad)
		}
	}
	m.ChainHandlers = nil
	m.Events = []string{"command.register"}
	if m.Validate() != nil {
		t.Fatal("default event shorthand")
	}
	m.Methods = []string{"stop", "first"}
	if m.Validate() == nil {
		t.Fatal("shorthand needs event method")
	}
	m.ChainHandlers = []ChainHandler{{Event: "command.register", Method: "first"}}
	if m.Validate() != nil {
		t.Fatal("explicit declaration replaces shorthand")
	}
	// The published ResetHandlers declaration remains valid and unchanged.
	m.ResetHandlers = []PluginDataResetHandler{{Method: "first"}}
	if m.Validate() != nil {
		t.Fatal("reset declaration coexistence")
	}
}
