package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalInputActualSDKEventErrorDelivery(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	r := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/fail"}`, cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	a.wg.Wait()
	message := a.systemMessages.get("system")
	if len(message) == 0 || strings.Contains(string(message), "OWNED_PRIVATE") {
		t.Fatal("broadcast system message", string(message))
	}
	event := plugins.MutableEvent{Type: "owned.chain.fail", Data: json.RawMessage(`{}`)}
	if !a.sendOriginalChainEvent(context.Background(), &event) || string(event.Data) != `{"mutated":true}` {
		t.Fatal("failure rolled back mutation", string(event.Data))
	}
	a.wg.Wait()
	message = a.systemMessages.get("system")
	if len(message) == 0 || strings.Contains(string(message), "OWNED_PRIVATE") {
		t.Fatal("chain system message", string(message))
	}
	events := ownedInputEvents(t, a, cookie, "OwnedInput")
	found := map[string]bool{}
	for _, raw := range events {
		if strings.Contains(string(raw), "OWNED_PRIVATE") {
			t.Fatal("exception leaked")
		}
		var item struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &item) != nil {
			t.Fatal("bad event")
		}
		if item.Type != "system.error" {
			continue
		}
		var name string
		_ = json.Unmarshal(item.Data["event_type"], &name)
		found[name] = true
		if string(item.Data["traceback"]) != `""` || string(item.Data["diagnostic_source"]) != `"go-sdk-error-code/v1"` || len(item.Data["event_handle"]) == 0 {
			t.Fatal("diagnostic source not explicit", item.Data)
		}
	}
	if !found["plugin.action"] || !found["owned.chain.fail"] {
		t.Fatal("actual error subscriber not called", found)
	}
}
