package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOriginalInputFixedMessageSourceActualSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/input-oracle.json")
	var oracle struct {
		Cases []struct {
			Name, Text                 string
			Expired, Consumed, Pending bool
			Events                     []struct {
				Type string
				Data map[string]json.RawMessage
			}
			Notifications []map[string]json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 6 {
		t.Fatal("fixed input source oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie := installOwnedInputFixture(t)
			registered := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true,"event_controls":[{"action":"add","kind":"broadcast","type":"notice.message","method":"event_dynamic"}]}`, cookie, "")
			assertOriginalRegistrySuccess(t, registered.Code, registered.Body.Bytes())
			a.wg.Wait()
			request := ownedInputCreate(t, a, cookie, "OwnedInput")
			if sample.Expired {
				request.TimeoutSeconds = 1
				_, err = a.pluginOriginalInput.Create(request, time.Now().Add(-2*time.Second))
				if err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(map[string]any{"text": sample.Text})
			response := callTest(t, a.Handler(), "POST", "/api/v1/message/web", string(body), cookie, "")
			if sample.Consumed && response.Code != 200 {
				t.Fatal("source consumed input", response.Code, response.Body.String())
			}
			allEvents := ownedInputEvents(t, a, cookie, "OwnedInput")
			events := []json.RawMessage{}
			notices := []map[string]json.RawMessage{}
			for _, raw := range allEvents {
				var event struct {
					Type string
					Data map[string]json.RawMessage
				}
				if json.Unmarshal(raw, &event) != nil {
					t.Fatal("actual event envelope")
				}
				if event.Type == "notice.message" {
					notices = append(notices, event.Data)
				} else {
					events = append(events, raw)
				}
			}
			// Source expired slash input continues to command ingress. Its command
			// declaration publishes plugin.action after the source input expiry event.
			expectedEvents := len(sample.Events)
			if sample.Expired && strings.HasPrefix(strings.TrimSpace(sample.Text), "/") {
				expectedEvents++
			}
			if len(events) != expectedEvents {
				t.Fatalf("source events=%d, actual=%s", expectedEvents, events)
			}
			if len(sample.Events) != 0 {
				var event struct {
					Type string
					Data map[string]json.RawMessage
				}
				if json.Unmarshal(events[0], &event) != nil || event.Type != sample.Events[0].Type {
					t.Fatal("actual source event missing", string(events[0]))
				}
				for _, key := range []string{"plugin_id", "input_text", "cancelled", "expired", "payload"} {
					got, gotPresent := event.Data[key]
					want, wantPresent := sample.Events[0].Data[key]
					if gotPresent != wantPresent || gotPresent && !originalBaseJSONEqual(got, want) {
						t.Fatal("source event field", key, string(event.Data[key]), string(sample.Events[0].Data[key]))
					}
				}
			}
			query := ownedInputRequest(t, a, cookie, "OwnedInput", "get", `{"user_id":"admin","channel":"Web","source":"admin"}`)
			if pending := strings.TrimSpace(query.Body.String()) != "null"; pending != sample.Pending {
				t.Fatal("source pending", pending, sample.Pending, query.Body.String())
			}
			if len(notices) != len(sample.Notifications) {
				t.Fatal("actual ordinary notice callback", len(notices), len(sample.Notifications))
			}
			if len(sample.Notifications) != 0 {
				for _, key := range []string{"title", "channel", "source", "userid", "username"} {
					if !originalBaseJSONEqual(notices[0][key], sample.Notifications[0][key]) {
						t.Fatal("actual source notice field", key, string(notices[0][key]), string(sample.Notifications[0][key]))
					}
				}
			}
		})
	}
}
