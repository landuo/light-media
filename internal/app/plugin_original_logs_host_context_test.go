package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

// Fixed log.py:372 walks the plugin caller's stack even when helper/message.py
// logs a caught synchronous sender error. Go propagates that concrete caller
// through a native context, not through untrusted notification fields.
func TestOriginalInstanceLogSynchronousNoticeHostFailureKeepsCaller(t *testing.T) {
	a, cookie, source := originalNoticeFixture(t)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedNotice/clone", `{"suffix":"logs"}`, cookie, "")
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	clone := originalBaseSDKLoaded(t, a, "OwnedNoticelogs")
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedNotice", `{"enabled":true,"fail":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	for _, object := range []*originalPluginInstance{source, clone} {
		out := postOwnedNotice(t, object, context.Background(), `{"title":"owned scoped failure","channel":"Telegram","immediately":true,"plugin_instance_id":"forged"}`)
		if out.Attempted != 1 || out.Failed != 0 || len(out.Errors) != 0 || out.Queued != 0 {
			t.Fatal("source consumed module exception was rethrown", out)
		}
	}
	for _, id := range []string{"OwnedNotice", "OwnedNoticelogs"} {
		page, err := a.queryLogs(context.Background(), map[string]string{"plugin_instance_id": id, "from": "0001-01-01T00:00:00Z", "limit": "100"})
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, row := range page.Entries {
			if row["event"] == "module_execution_failed" {
				found++
				if row["plugin_id"] != "OwnedNotice" || row["operation"] != "post_notice" || row["plugin_call_id"] == "" || strings.Contains(row["message"].(string), "forged") {
					t.Fatal("actual host error caller was replaced", row)
				}
			}
		}
		if found != 1 {
			t.Fatal("synchronous host error was not attributed exactly once", id, found)
		}
	}
}

// Fixed EventManager.send_event -> synchronous dispatch -> caught receiver
// failure has already unwound the receiver's frame. LoggerManager.__get_caller
// sees the sending plugin (or main.py), as the three-frame source replay proves.
func TestOriginalInstanceLogSynchronousChainFailureKeepsSendingCaller(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	configure := func(id, label string, fail bool) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": true, "label": label, "chain_fail": fail})
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
	}
	configure("OwnedInput", "receiver", true)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/clone", `{"suffix":"logs"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	configure("OwnedInputlogs", "sender clone", false)
	seen := map[string]bool{}
	fresh := func() []map[string]any {
		t.Helper()
		page, err := a.queryLogs(context.Background(), map[string]string{"from": "0001-01-01T00:00:00Z", "limit": "500"})
		if err != nil {
			t.Fatal(err)
		}
		rows := []map[string]any{}
		for _, row := range page.Entries {
			if row["event"] != "chain_handler_failed" {
				continue
			}
			id, _ := row["id"].(string)
			if !seen[id] {
				rows = append(rows, row)
				seen[id] = true
			}
		}
		return rows
	}
	for _, sender := range []string{"OwnedInput", "OwnedInputlogs", ""} {
		var event plugins.MutableEvent
		if sender != "" {
			response := ownedInputRequest(t, a, cookie, sender, "event.send", `{"kind":"chain","type":"owned.sdk.chain","data":{}}`)
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &event) != nil {
				t.Fatal("actual sending SDK did not return synchronous event", sender, response.Code)
			}
		} else {
			event = plugins.MutableEvent{Type: "owned.sdk.chain", Data: json.RawMessage(`{}`)}
			if !a.sendOriginalChainEvent(context.Background(), &event) {
				t.Fatal("native chain did not dispatch")
			}
		}
		var data struct{ Visits []string }
		if json.Unmarshal(event.Data, &data) != nil || len(data.Visits) != 2 {
			t.Fatal("failure stopped later actual SDK receiver", sender)
		}
		rows := fresh()
		if len(rows) != 1 {
			t.Fatal("caught real receiver failure did not produce one log", sender, len(rows))
		}
		row := rows[0]
		if sender == "" {
			if row["plugin_instance_id"] != nil || row["plugin_call_id"] != nil {
				t.Fatal("native sender acquired failed receiver identity", row)
			}
		} else if row["plugin_instance_id"] != sender || row["plugin_id"] != "OwnedInput" || row["plugin_call_id"] == nil || row["operation"] != "http" {
			t.Fatal("synchronous host error belongs to sending SDK, not failing subscriber", sender, row)
		}
	}
}

func TestOriginalInstanceLogAuthTicketGetterFailureKeepsCallerAndAnonymousGlobal(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedAuthFixture(t, a)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedAuth/clone", `{"suffix":"logs"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	for _, id := range []string{"OwnedAuth", "OwnedAuthlogs"} {
		response := ownedAuthConfig(t, a, cookie, id, `{"enabled":true,"fail_getter":true}`)
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	// The anonymous provider registry is an ordinary core call, not a plugin
	// invocation. Knowing the failed provider's ID is not caller attribution.
	if response = callTest(t, a.Handler(), "GET", "/api/auth/providers", "", nil, ""); response.Code != 200 {
		t.Fatal("anonymous provider failure isolation changed", response.Code)
	}
	for _, id := range []string{"OwnedAuth", "OwnedAuthlogs"} {
		response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+id+"/authenticate", ownedAuthProofBody(t, 1, "plugin:"+id), nil, "")
		if response.Code != 403 {
			t.Fatal("failed real declaration issued an auth ticket", id, response.Code)
		}
	}
	page, err := a.queryLogs(context.Background(), map[string]string{"from": "0001-01-01T00:00:00Z", "limit": "500"})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range page.Entries {
		if row["event"] != "auth_provider_declaration_failed" {
			continue
		}
		owner, _ := row["plugin_instance_id"].(string)
		counts[owner]++
		if owner != "" && (row["plugin_call_id"] == nil || row["operation"] != "http") {
			t.Fatal("trusted ticket call lost actual scope", row)
		}
		if owner == "" && row["plugin_call_id"] != nil {
			t.Fatal("anonymous registry invented a plugin scope", row)
		}
	}
	if counts["OwnedAuth"] != 1 || counts["OwnedAuthlogs"] != 1 || counts[""] != 2 {
		t.Fatal("actual SDK and anonymous provider errors mixed owners", counts)
	}
}

func TestOriginalInstanceLogBackgroundContextDoesNotInventPluginOwner(t *testing.T) {
	a := newTestApp(t)
	a.logOriginalPluginContext(context.Background(), "ERROR", "notification_failed", "owned background failure")
	page, err := a.queryLogs(context.Background(), map[string]string{"from": "0001-01-01T00:00:00Z", "limit": "100"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range page.Entries {
		if row["message"] == "owned background failure" {
			found = true
			if row["plugin_instance_id"] != nil || row["plugin_call_id"] != nil {
				t.Fatal("background acquired fake plugin caller", row)
			}
		}
	}
	if !found {
		t.Fatal("ordinary host log disappeared")
	}
}

func TestOriginalInstanceLogSynchronousModuleFailureKeepsOriginatingCaller(t *testing.T) {
	a, cookie, source := originalNoticeFixture(t)
	configure := func(id string, body string) {
		t.Helper()
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, body, cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
	}
	configure("OwnedNotice", `{"enabled":true,"fail":true}`)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedNotice/clone", `{"suffix":"modulelogs"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	configure("OwnedNoticemodulelogs", `{"enabled":false,"fail":true}`)
	clone := originalBaseSDKLoaded(t, a, "OwnedNoticemodulelogs")
	for _, object := range []*originalPluginInstance{source, clone} {
		out := postOwnedNotice(t, object, context.Background(), `{"title":"owned synchronous module failure","channel":"Telegram","immediately":true}`)
		if out.Attempted != 1 || out.Failed != 1 {
			t.Fatal("real module failed-send result changed", out)
		}
	}
	for _, id := range []string{"OwnedNotice", "OwnedNoticemodulelogs"} {
		page, err := a.queryLogs(context.Background(), map[string]string{"plugin_instance_id": id, "from": "0001-01-01T00:00:00Z", "limit": "100"})
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, entry := range page.Entries {
			if entry["event"] == "module_execution_failed" {
				found++
				if entry["plugin_call_id"] == "" || entry["operation"] != "post_notice" {
					t.Fatal("synchronous module error lost origin phase", entry)
				}
			}
		}
		if found != 1 {
			t.Fatal("provider failure was attributed to receiver instead of originating plugin", id, found)
		}
	}
}
