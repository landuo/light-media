package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalTransferRenameActualSDKFixedCaller(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/transfer-rename-oracle.json")
	var oracle struct {
		Cases []struct {
			Name, Template, Result string
			Variables              map[string]any
			Base, Source           *string
			Patches                map[string]map[string]json.RawMessage
			Fail                   map[string]bool
			Events                 []struct {
				Type  string
				Input json.RawMessage
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("fixed rename caller oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			t.Cleanup(a.closeOriginalSessions)
			config := a.Settings()
			config.APIToken = "owned-transfer-events-test-key"
			if err := a.saveSettings(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			binary := os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")
			if binary == "" {
				t.Fatal("actual transfer SDK worker required")
			}
			manifest := installOrderWorker(t, a, cookie, binary)
			body, _ := json.Marshal(map[string]any{"enabled": true, "patches": sample.Patches, "fail": sample.Fail})
			response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(body), cookie, "")
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			a.wg.Wait()
			got, err := a.originalTransferRenamePath(context.Background(), sample.Template, sample.Variables, sample.Base, sample.Source, nil)
			if err != nil || got != sample.Result {
				t.Fatal("fixed rename caller result", got, sample.Result, err)
			}
			observed := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+manifest.PluginID+"/observations", "")
			var events []struct {
				Type string
				Data json.RawMessage
			}
			if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &events) != nil || len(events) != 2 {
				t.Fatal("actual SDK rename trace", observed.Code, observed.Body.String())
			}
			for i, event := range sample.Events {
				if events[i].Type != event.Type || !originalBaseJSONEqual(events[i].Data, event.Input) {
					t.Fatal("fixed before/after render event input", i, string(events[i].Data), string(event.Input))
				}
			}
		})
	}
}
