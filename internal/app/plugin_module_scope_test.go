package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The user explicitly excludes workflow/AI surfaces even where the fixed
// MoviePilot manager registers them. Removed routes must not return a success
// envelope or expose compatibility placeholders to an authenticated caller.
func TestPluginModuleScopeExcludedRoutesAbsent(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	for _, path := range []string{
		"/api/workflow/plugin/actions",
		"/api/workflow/plugin/actions?plugin_id=OwnedStandardFixture",
		"/api/plugins/agent/tools",
		"/api/agent/tools",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			response := callTest(t, a.Handler(), method, path, `{}`, cookie, "")
			if response.Code != http.StatusNotFound {
				t.Errorf("excluded %s %s returned %d: %s", method, path, response.Code, response.Body.String())
			}
		}
	}
}

func TestPluginModuleScopeExcludedEventsActualSDK(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	for _, kind := range []string{"workflow.execute", "workflow.execution", "agent.tokens.usage", "agent.llm.provider"} {
		for _, operation := range []string{"event.check", "event.send", "event.manage"} {
			fields := map[string]any{"kind": "broadcast", "type": kind}
			if operation == "event.manage" {
				fields["action"], fields["method"] = "add", "event_dynamic"
			}
			raw, _ := json.Marshal(fields)
			response := ownedInputRequest(t, a, cookie, "OwnedInput", operation, string(raw))
			if response.Code != 403 {
				t.Fatalf("actual SDK exposed excluded %s %s: %d", operation, kind, response.Code)
			}
		}
	}
	// The ordinary event bus remains live after all refusals.
	if got := ownedSDKEventCall(t, a, cookie, "event.check", `{"kind":"broadcast","type":"owned.sdk.broadcast"}`); string(got) != "true" {
		t.Fatal("ordinary event support was lost")
	}
}
