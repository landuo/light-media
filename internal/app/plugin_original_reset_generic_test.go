package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestOriginalResetGenericDeclarationsActualWorkers(t *testing.T) {
	// The existing exhaustive actual-worker suite must run on binaries whose
	// manifests use generic declarations AND conflicting compatibility forms.
	for _, key := range []string{"OWNED_RESET_MUTATOR", "OWNED_RESET_OBSERVER"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		raw, err := exec.CommandContext(ctx, os.Getenv(key), "--manifest").Output()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var manifest plugins.Manifest
		if err = json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		if err = manifest.Validate(); err != nil {
			t.Fatal(err)
		}
		if len(manifest.ChainHandlers) != 2 || len(manifest.ResetHandlers) != 1 || len(manifest.Events) != 1 {
			t.Fatalf("not a generic/compat precedence fixture: %s", raw)
		}
		for _, handler := range manifest.ChainHandlers {
			if handler.Event != "plugin.data.reset" {
				t.Fatalf("wrong canonical event %s", handler.Event)
			}
		}
	}
	// Covers arbitrary JSON mutation, failure continuation, full invocation order
	// and actual HTTP reset. Its exact trace assertions also reject double calls.
	TestOriginalResetChainActualWorkers(t)
}

func TestOriginalResetGenericCompatibilityPrecedence(t *testing.T) {
	zero := 0
	negative := -5
	compat := []plugins.PluginDataResetHandler{{Method: "a", Priority: &negative}, {Method: "b", Priority: &zero}, {Method: "c"}}
	manifest := plugins.Manifest{ResetHandlers: compat, Events: []string{"plugin.data.reset"}, ChainHandlers: []plugins.ChainHandler{{Event: "plugin.data.reset", Method: "explicit"}}}
	if got := originalResetChainCompatibilityDeclarations(manifest); len(got) != 0 {
		t.Fatalf("explicit duplicated with compatibility: %+v", got)
	}
	manifest.ChainHandlers = []plugins.ChainHandler{{Event: "command.register", Method: "unrelated"}}
	got := originalResetChainCompatibilityDeclarations(manifest)
	if len(got) != 3 || got[0].Order() != -5 || got[1].Order() != 0 || got[2].Order() != 10 || got[2].Method != "c" {
		t.Fatalf("compat priority/order lost: %+v", got)
	}
	manifest.ResetHandlers = nil
	got = originalResetChainCompatibilityDeclarations(manifest)
	if len(got) != 1 || got[0].Event != "plugin.data.reset" || got[0].Method != "event" || got[0].Order() != 10 {
		t.Fatalf("canonical shorthand lost: %+v", got)
	}
	manifest.Events = []string{"plugin_data_reset"}
	if got = originalResetChainCompatibilityDeclarations(manifest); len(got) != 0 {
		t.Fatal("underscore alias accepted")
	}
}
