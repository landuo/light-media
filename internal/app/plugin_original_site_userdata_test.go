package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

type originalSiteUserDataOracle struct {
	NativeInput json.RawMessage `json:"native_input"`
	Cases       []struct {
		Name        string
		Plugin      json.RawMessage `json:"plugin_result"`
		NativeCalls int             `json:"native_calls"`
		Returned    json.RawMessage
		Exception   *string
		Trace       []string
	}
}

func readOriginalSiteUserDataOracle(t *testing.T) originalSiteUserDataOracle {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-event-target/site-updated-consumer-oracle.json")
	var out originalSiteUserDataOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || len(out.Cases) != 15 {
		t.Fatal("fixed SiteUpdated/refresh_userdata source oracle", err)
	}
	return out
}

func TestOriginalSiteUserDataActualSDKFixedSource(t *testing.T) {
	oracle := readOriginalSiteUserDataOracle(t)
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, "http://127.0.0.1:43100")
			handler := "module_execute"
			if sample.Name == "throw" {
				handler = "module_fail"
			}
			raw, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"refresh_userdata": handler}, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(raw))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			calls := 0
			_, result, err := a.originalSiteUserData(context.Background(), site, func() (sites.UserStats, *sites.SourceUserData, error) {
				calls++
				var dto sites.SourceUserData
				if json.Unmarshal(oracle.NativeInput, &dto) != nil {
					// The native DTO boundary is known even when the caller's
					// accumulated list later makes its final projection fail.
					t.Fatal("native source fixture DTO")
				}
				return originalSiteUserDataStats(&dto), &dto, nil
			})
			if calls != sample.NativeCalls {
				t.Fatal("fixed source native invocation count", calls, sample.NativeCalls)
			}
			// Message construction happens after this wrapper. Its malformed-row
			// error is asserted at the actual Event.Send consumer in the next test.
			wantError := sample.Exception != nil && sample.Name != "message_then_bad_row"
			if (err != nil) != wantError {
				t.Fatal("fixed source wrapper failure", err, sample.Exception)
			}
			if !wantError {
				wantData := false
				for _, operation := range sample.Trace {
					wantData = wantData || operation == "update"
				}
				if (result != nil) != wantData {
					t.Fatal("fixed source truthiness/data outcome", result, sample.Trace)
				}
				if result != nil && sample.Exception == nil {
					encoded, _ := json.Marshal(result)
					if !originalBaseJSONEqual(encoded, sample.Returned) {
						t.Fatal("fixed source returned DTO/default fields differ", string(encoded), string(sample.Returned))
					}
				}
			}
			after := ownedModuleRead(t, object)
			var input struct{ Args, Kwargs json.RawMessage }
			var kwargs struct{ Site map[string]json.RawMessage }
			if json.Unmarshal(after.Inputs[handler], &input) != nil || json.Unmarshal(input.Kwargs, &kwargs) != nil || string(input.Args) != "[]" || string(kwargs.Site["domain"]) != `"http://127.0.0.1:43100"` || string(kwargs.Site["id"]) != "1" || after.Calls["get_module"] != before.Calls["get_module"]+1 || after.Calls[handler] != before.Calls[handler]+1 {
				t.Fatal("source site argument or single discovery frame differs")
			}
		})
	}
}

func TestOriginalSiteUpdatedActualSDKConsumerCommitMessagesAndClone(t *testing.T) {
	a, cookie, object := ownedModuleFixture(t, true)
	installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("OWNED_INPUT_FIXTURE_BINARY"))
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/favicon.ico" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		requests.Add(1)
		t.Error("terminal module DTO/false performed native site request", r.URL.Path)
		w.WriteHeader(500)
	}))
	defer peer.Close()
	site := installResourceBrowseSite(t, a, peer.URL)
	a.cfgMu.Lock()
	a.config.APIToken = "owned-site-updated-api-key"
	a.cfgMu.Unlock()
	configure := func(instance string, value json.RawMessage) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"refresh_userdata": "module_execute"}, "result": value})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance, string(body), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	configure(ownedModuleID, json.RawMessage(`null`))
	clone := ownedModuleClone(t, a, cookie, "site")
	configure(clone.installation.InstanceID, json.RawMessage(`{"username":"clone","upload":99,"message_unread":2,"ratio":0.5}`))
	object = originalBaseSDKLoaded(t, a, ownedModuleID)
	clone = originalBaseSDKLoaded(t, a, clone.installation.InstanceID)
	if ownedModuleRead(t, object).PID == ownedModuleRead(t, clone).PID {
		t.Fatal("actual site module clone process is shared")
	}
	send := func(value any) {
		t.Helper()
		raw, _ := json.Marshal(plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: "site.updated", Data: mustCleanSeedJSON(value)})
		ownedSDKEventCall(t, a, cookie, "event.send", string(raw))
		a.wg.Wait()
	}
	count := func(kind string) int {
		t.Helper()
		var n int
		if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind=?`, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, data := range []any{map[string]any{}, map[string]any{"domain": ""}, map[string]any{"domain": "missing.test"}, map[string]any{"domain": 123}} {
		send(data)
	}
	if count("site_stats_history") != 0 || count("notification") != 0 {
		t.Fatal("source missing/unknown domain had side effects")
	}
	send(map[string]any{"domain": peer.URL})
	if count("site_stats_history") != 1 || count("notification") != 2 || requests.Load() != 0 {
		t.Fatal("source DTO did not commit before summary/ratio notices", count("site_stats_history"), count("notification"), requests.Load())
	}
	var sealed []byte
	var snapshot siteSnapshot
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&sealed); err != nil || a.Store.openSiteAccountSnapshot("site_stats_latest", site.ID, sealed, &snapshot) != nil || snapshot.SourceData == nil || snapshot.SourceData.Upload != 99 || *snapshot.SourceData.Username != "clone" {
		t.Fatal("actual module clone DTO did not reach persisted account view", err)
	}
	// Source false is terminal: it must not reach the later clone or native.
	configure(ownedModuleID, json.RawMessage(`false`))
	cloneBefore := ownedModuleRead(t, clone).Calls["module_execute"]
	send(map[string]any{"domain": standardSiteEventDomain(peer.URL)})
	if ownedModuleRead(t, clone).Calls["module_execute"] != cloneBefore || count("notification") != 2 || requests.Load() != 0 {
		t.Fatal("source false continued into clone/core or posted messages")
	}
	configure(ownedModuleID, json.RawMessage(`{"username":"source","message_unread":2,"message_unread_contents":[["head","date","body","owned-message-source"],null]}`))
	send(map[string]any{"domain": peer.URL})
	if count("notification") != 3 {
		t.Fatal("earlier source message was lost after later invalid row", count("notification"))
	}
	events := ownedInputEvents(t, a, cookie, "OwnedInput")
	found := false
	for _, event := range events {
		found = found || strings.Contains(string(event), "site.updated") && strings.Contains(string(event), "system.error")
	}
	if !found {
		t.Fatal("later invalid source message did not report real ordinary event error")
	}
	// Message source dedup uses real deletable history, not a permanent receipt.
	configure(ownedModuleID, json.RawMessage(`{"message_unread":1,"message_unread_contents":[["head","date","body","owned-message-source"]]}`))
	send(map[string]any{"domain": peer.URL})
	if count("notification") != 3 {
		t.Fatal("real notification source did not deduplicate")
	}
	items, err := a.Store.List(context.Background(), "notification", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	for _, raw := range items {
		var record notificationRecord
		if err = json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		n, err := a.openNotification(record)
		if err != nil {
			t.Fatal(err)
		}
		if n.Source == "owned-message-source" {
			if _, err = a.Store.DB.Exec(`DELETE FROM records WHERE kind='notification' AND id=?`, n.ID); err != nil {
				t.Fatal(err)
			}
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("source-bearing actual encrypted message not found")
	}
	send(map[string]any{"domain": peer.URL})
	if count("notification") != 3 {
		t.Fatal("deleted message history incorrectly reserved source")
	}
	// The original DB write precedes its event/messages. A real SQL failure
	// must retain the last committed observation and prevent those later effects.
	configure(ownedModuleID, json.RawMessage(`{"username":"must-rollback","upload":500,"message_unread":1}`))
	var beforeFacts int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed'`).Scan(&beforeFacts); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_site_refresh_failure BEFORE INSERT ON records WHEN NEW.kind='site_stats_history' BEGIN SELECT RAISE(ABORT,'owned account failure'); END`); err != nil {
		t.Fatal(err)
	}
	send(map[string]any{"domain": peer.URL})
	var afterFacts int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed'`).Scan(&afterFacts); err != nil {
		t.Fatal(err)
	}
	if afterFacts != beforeFacts || count("notification") != 3 {
		t.Fatal("failed account write emitted source fact or notice")
	}
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&sealed); err != nil || a.Store.openSiteAccountSnapshot("site_stats_latest", site.ID, sealed, &snapshot) != nil || snapshot.SourceData.Upload == 500 {
		t.Fatal("failed account write replaced previous committed data", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_site_refresh_failure`); err != nil {
		t.Fatal(err)
	}
	send(map[string]any{"domain": peer.URL})
	if count("notification") != 4 {
		t.Fatal("source actual refresh did not recover after SQL failure")
	}
}
