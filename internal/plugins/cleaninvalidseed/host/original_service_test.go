package seedhost

import (
	"context"
	seed "light-media/internal/plugins/cleaninvalidseed"
	"testing"
)

// Context matching is only a secondary bounded marker, never host authority.
func TestOriginalServiceMarkerBoundToExactInvocationAndMachine(t *testing.T) {
	inv := Invocation{Action: "service", RunID: "owned-run", EventID: "owned-occurrence", InputSHA: "owned-snapshot"}
	ctx := WithOriginalService(context.Background(), inv)
	if originalServiceMatches(ctx, inv) {
		t.Fatal("marker without actual machine accepted")
	}
	ctx = WithMachine(ctx, seed.New(nil))
	if !originalServiceMatches(ctx, inv) {
		t.Fatal("exact marker rejected")
	}
	changed := inv
	changed.RunID = "another-run"
	if originalServiceMatches(ctx, changed) {
		t.Fatal("marker reused for another run")
	}
	changed = inv
	changed.EventID = "another-occurrence"
	if originalServiceMatches(ctx, changed) {
		t.Fatal("marker reused for another occurrence")
	}
	changed = inv
	changed.Action = "run"
	if originalServiceMatches(ctx, changed) {
		t.Fatal("marker grants manual run")
	}
}
