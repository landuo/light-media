package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestOriginalCoreEventConsumersActualSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/core-consumer-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Event     string
			Data      json.RawMessage
			Calls     []json.RawMessage
			Exception *string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("fixed concrete event consumer oracle", err)
	}
	a, cookie := installOwnedInputFixture(t)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true,"services":[{"id":"core-reload","name":"Core reload source","handler":"run_service","input":{"mode":"tuple-ok","progress":37},"trigger":{"kind":"interval","interval_ms":3600000,"timezone":"UTC"}}]}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	if len(a.originalPublicServices(time.Now())) != 1 {
		t.Fatal("owned source service declaration invalid")
	}
	for _, sample := range oracle.Cases {
		send := func(data json.RawMessage) {
			t.Helper()
			input, _ := json.Marshal(plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: sample.Event, Data: data})
			ownedSDKEventCall(t, a, cookie, "event.send", string(input))
			a.wg.Wait()
		}
		if got := ownedSDKEventCall(t, a, cookie, "event.check", `{"kind":"broadcast","type":"`+sample.Event+`"}`); string(got) != "true" {
			t.Fatal("existing core handler absent from actual check", sample.Event, string(got))
		}
		switch sample.Event {
		case "command.excute":
			before := ownedInputEvents(t, a, cookie, "OwnedInput")
			send(sample.Data)
			after := ownedInputEvents(t, a, cookie, "OwnedInput")
			wantDelta := len(sample.Calls)
			if sample.Exception != nil {
				wantDelta++
			}
			if len(after)-len(before) != wantDelta {
				t.Fatal("concrete Command did not consume actual SDK event once", string(sample.Data), len(after)-len(before), len(sample.Calls))
			}
			if sample.Exception != nil {
				var event struct {
					Type string
					Data struct{ Type, EventType string }
				}
				if json.Unmarshal(after[len(after)-1], &event) != nil || event.Type != "system.error" {
					t.Fatal("source malformed command exception not reported as ordinary event error")
				}
			}
			if len(sample.Calls) > 0 {
				var call struct {
					Arguments string          `json:"data_str"`
					Channel   string          `json:"channel"`
					Source    string          `json:"source"`
					User      json.RawMessage `json:"userid"`
				}
				var event struct {
					Type string
					Data struct {
						Arguments string          `json:"arg_str"`
						Channel   string          `json:"channel"`
						Source    string          `json:"source"`
						User      json.RawMessage `json:"user"`
						Action    string          `json:"action"`
					}
				}
				if json.Unmarshal(sample.Calls[0], &call) != nil || json.Unmarshal(after[len(after)-1], &event) != nil || event.Type != "plugin.action" || event.Data.Action != "command" || event.Data.Arguments != call.Arguments || event.Data.Channel != call.Channel || event.Data.Source != call.Source || !originalBaseJSONEqual(event.Data.User, call.User) {
					t.Fatal("concrete Command consumed different source args/context", string(after[len(after)-1]))
				}
			}
		case "plugin.reload":
			a.removeOriginalPluginJobs("OwnedInput")
			if len(a.originalPublicServices(time.Now())) != 0 {
				t.Fatal("controlled old scheduler state not removed")
			}
			send(sample.Data)
			if len(a.originalPublicServices(time.Now())) != len(sample.Calls) {
				t.Fatal("concrete Scheduler did not rebuild source plugin declarations", string(sample.Data))
			}
		case "module.reload":
			a.pluginOriginalCommandsMu.Lock()
			a.pluginOriginalCommands = nil
			a.pluginOriginalCommandsMu.Unlock()
			send(sample.Data)
			if _, _, present := a.lookupOriginalPluginCommand("/owned"); !present {
				t.Fatal("module.reload did not rebuild actual plugin commands")
			}
		}
	}
	// A native command already ran the same core consumer. It must still notify
	// observers but cannot execute its declared plugin event twice.
	before := len(ownedInputEvents(t, a, cookie, "OwnedInput"))
	if accepted, err := a.publishOriginalCommandEvent(context.Background(), originalCommandEvent{Text: "/owned direct", Channel: "Web", Source: "owned-source", User: "owned-user"}); err != nil || !accepted {
		t.Fatal(accepted, err)
	}
	a.wg.Wait()
	if got := len(ownedInputEvents(t, a, cookie, "OwnedInput")) - before; got != 1 {
		t.Fatal("native producer double executed after generic core bridge", got)
	}
}
