package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

// Expected truth values are captured from the fixed Python3.12 source getter,
// not from the Go decoder. The executable is the real standard Base SDK fixture
// with only enabled's JSON representation retained; no host/session fake is used.
func TestOriginalRawJSONStateSourceTruthActualApp(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Raw   string `json:"raw"`
			Truth bool   `json:"truth"`
		} `json:"cases"`
	}
	for _, name := range []string{"raw-state-source-oracle.json", "state-scalar-source-oracle.json"} {
		raw, err := os.ReadFile("../../testdata/plugin-state-numeric-20261106/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var part = oracle
		part.Cases = nil
		if err := json.Unmarshal(raw, &part); err != nil || len(part.Cases) < 8 {
			t.Fatal("actual fixed-source state oracle", err)
		}
		oracle.Cases = append(oracle.Cases, part.Cases...)
	}
	a, cookie, object := ownedStandardSDKFixture(t)
	ctx := context.Background()
	for _, c := range oracle.Cases {
		t.Run(c.Raw, func(t *testing.T) {
			response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":`+c.Raw+`}`, cookie, "")
			if response.Code != http.StatusOK {
				t.Errorf("raw config/init/state registration failed: %d %s", response.Code, response.Body.String())
			}
			var result json.RawMessage
			if err := object.readSession(ctx, "get_state", &result); err != nil || string(result) != c.Raw {
				t.Fatalf("actual worker raw state: %s %v", result, err)
			}
			got, err := object.getState(ctx)
			if err != nil || got != c.Truth {
				t.Errorf("actual host getState: got %v, err %v; Python truth %v", got, err, c.Truth)
			}
			object.configMu.Lock()
			enabled := object.eventsEnabled
			object.configMu.Unlock()
			if enabled != c.Truth {
				t.Errorf("registered ordinary event state: got %v want %v", enabled, c.Truth)
			}
			cards, err := a.originalLocalPluginCards(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, card := range cards {
				if card.InstanceID == ownedStandardFixtureID {
					found = true
					if card.State != c.Truth {
						t.Errorf("source local metadata state: got %v want %v", card.State, c.Truth)
					}
				}
			}
			if !found {
				t.Fatal("installed class disappeared after scalar state")
			}
			a.wg.Wait()
			before := ownedStandardSnapshotRead(t, object).Calls["event"]
			a.broadcastOriginalPluginEvent(ctx, "owned.standard", json.RawMessage(`{"source":"numeric-state-fixture"}`))
			a.wg.Wait()
			delta := ownedStandardSnapshotRead(t, object).Calls["event"] - before
			wantDelta := 0
			if c.Truth {
				wantDelta = 1
			}
			if delta != wantDelta {
				t.Errorf("real ordinary event dispatch count: got %d want %d", delta, wantDelta)
			}
		})
	}
}
