package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func ownedEventInventory(t *testing.T, a *App, cookie *http.Cookie, kind string) []plugins.EventHandlerInfo {
	t.Helper()
	raw := ownedSDKEventCall(t, a, cookie, "event.manage", `{"action":"visualize"}`)
	var all []plugins.EventHandlerInfo
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err, string(raw))
	}
	return slices.DeleteFunc(all, func(h plugins.EventHandlerInfo) bool { return h.EventType != kind })
}

func ownedManagedCallbacks(t *testing.T, a *App, cookie *http.Cookie, instance, kind string) []string {
	t.Helper()
	var result []string
	for _, raw := range ownedInputEvents(t, a, cookie, instance) {
		var event struct {
			Type    string `json:"type"`
			Handler string `json:"handler"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == kind {
			result = append(result, event.Handler)
		}
	}
	slices.Sort(result)
	return result
}

func TestOriginalEventSDKActualMultipleAndDynamicHandlers(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	const kind = "owned.sdk.multiple"
	control := func(body string) {
		t.Helper()
		if got := ownedSDKEventCall(t, a, cookie, "event.manage", body); string(got) != "null" {
			t.Fatal(string(got))
		}
	}
	send := func() {
		t.Helper()
		ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"broadcast","type":"`+kind+`","data":{}}`)
		a.wg.Wait()
	}
	check := func(want bool) {
		t.Helper()
		got := ownedSDKEventCall(t, a, cookie, "event.check", `{"kind":"broadcast","type":"`+kind+`"}`)
		if string(got) != map[bool]string{true: "true", false: "false"}[want] {
			t.Fatal("enabled check", string(got), want)
		}
	}
	inventory := ownedEventInventory(t, a, cookie, kind)
	if len(inventory) != 2 || inventory[0].HandlerIdentifier != "go.plugins.OwnedInput.event_first" || inventory[1].HandlerIdentifier != "go.plugins.OwnedInput.event_second" || inventory[0].Priority != nil {
		t.Fatal("multiple declarations", inventory)
	}
	send()
	if got := ownedManagedCallbacks(t, a, cookie, "OwnedInput", kind); !slices.Equal(got, []string{"event_first", "event_second"}) {
		t.Fatal("multiple actual callbacks", got)
	}
	control(`{"action":"add","kind":"broadcast","type":"` + kind + `","method":"event_first","priority":-10}`)
	control(`{"action":"add","kind":"broadcast","type":"` + kind + `","method":"event_dynamic"}`)
	inventory = ownedEventInventory(t, a, cookie, kind)
	if len(inventory) != 3 || !strings.HasSuffix(inventory[0].HandlerIdentifier, "event_second") || !strings.HasSuffix(inventory[1].HandlerIdentifier, "event_first") || inventory[0].Priority != nil {
		t.Fatal("readd moves tail; broadcast ignores listener priority", inventory)
	}
	control(`{"action":"disable_handler","method":"event_second"}`)
	control(`{"action":"remove","kind":"broadcast","type":"` + kind + `","method":"event_first"}`)
	send()
	if got := ownedManagedCallbacks(t, a, cookie, "OwnedInput", kind); !slices.Equal(got, []string{"event_dynamic", "event_first", "event_second"}) {
		t.Fatal("disabled/removed subscriber ran", got)
	}
	control(`{"action":"disable_class"}`)
	check(false)
	send()
	if got := ownedManagedCallbacks(t, a, cookie, "OwnedInput", kind); len(got) != 3 {
		t.Fatal("disabled class ran", got)
	}
	// get_state after config save sets the class flag again, but does not undo
	// an explicit per-method disable. Init does not repeat static decorators.
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true,"label":"configured"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	check(true)
	inventory = ownedEventInventory(t, a, cookie, kind)
	if len(inventory) != 2 || inventory[0].Status != "disabled" || inventory[1].Status != "enabled" {
		t.Fatal("save reset method flags or registrations", inventory)
	}
	control(`{"action":"enable_handler","method":"event_second"}`)
	control(`{"action":"remove","kind":"broadcast","type":"` + kind + `","method":"event_dynamic"}`)
	send()
	if got := ownedManagedCallbacks(t, a, cookie, "OwnedInput", kind); !slices.Equal(got, []string{"event_dynamic", "event_first", "event_second", "event_second"}) {
		t.Fatal("enable/remove actual callbacks", got)
	}
	control(`{"action":"remove","kind":"broadcast","type":"` + kind + `","method":"event_second"}`)
	check(false)
	// Direct host control decoding, not just SDK validation, rejects unknown
	// target fields and undeclared method strings. No unrelated class is accepted.
	object := originalBaseSDKLoaded(t, a, "OwnedInput")
	for _, raw := range []string{`{"action":"add","kind":"broadcast","type":"owned.sdk.multiple","method":"undeclared"}`, `{"action":"enable_class","plugin_id":"Other"}`} {
		if _, err := a.originalManageEventResource(originalBaseCallScope{object: object}, json.RawMessage(raw)); err == nil {
			t.Fatal("accepted invalid capability request", raw)
		}
	}
	r = ownedInputRequest(t, a, cookie, "OwnedInput", "event.manage", `{"action":"add","kind":"broadcast","type":"owned.sdk.multiple","method":"undeclared"}`)
	if r.Code != 403 {
		t.Fatal("real worker undeclared function not rejected", r.Code)
	}
}

func TestOriginalEventSDKActualDynamicChainCloneAndReload(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	configure := func(id, label string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": true, "label": label})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	configure("OwnedInput", "main")
	configure("OwnedInputtwin", "twin")
	control := func(id, body string) {
		t.Helper()
		r := ownedInputRequest(t, a, cookie, id, "event.manage", body)
		if r.Code != 200 {
			t.Fatal("actual event control", r.Code, r.Body.String())
		}
	}
	const kind = "owned.sdk.dynamic.chain"
	control("OwnedInput", `{"action":"add","kind":"chain","type":"`+kind+`","method":"sdk_chain","priority":10}`)
	control("OwnedInputtwin", `{"action":"add","kind":"chain","type":"`+kind+`","method":"sdk_chain","priority":0}`)
	chain := func(want []string) {
		t.Helper()
		raw := ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"chain","type":"`+kind+`","data":{}}`)
		var event plugins.MutableEvent
		var data struct {
			Visits []string `json:"visits"`
		}
		if json.Unmarshal(raw, &event) != nil || json.Unmarshal(event.Data, &data) != nil || !slices.Equal(data.Visits, want) {
			t.Fatal("dynamic chain mutation/order", string(raw), want)
		}
	}
	chain([]string{"twin", "main"})
	control("OwnedInputtwin", `{"action":"disable_handler","method":"sdk_chain"}`)
	chain([]string{"main"})
	control("OwnedInputtwin", `{"action":"enable_handler","method":"sdk_chain"}`)
	control("OwnedInput", `{"action":"add","kind":"chain","type":"`+kind+`","method":"sdk_chain","priority":-1}`)
	chain([]string{"main", "twin"})
	control("OwnedInput", `{"action":"add","kind":"broadcast","type":"owned.sdk.retained","method":"event_dynamic"}`)
	old := originalBaseSDKLoaded(t, a, "OwnedInput")
	// Queue before reload while the consumer is paused at a real idle boundary.
	a.wg.Wait()
	queue := &a.pluginOriginalBroadcast
	queue.mu.Lock()
	idle := !queue.running && len(queue.pending) == 0
	if idle {
		queue.running = true
	}
	queue.mu.Unlock()
	if !idle {
		t.Fatal("queue not idle")
	}
	resumed := false
	t.Cleanup(func() {
		if !resumed {
			a.drainOriginalBroadcasts()
		}
	})
	ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"broadcast","type":"owned.sdk.retained","data":{"queued":true}}`)
	r = callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	current := originalBaseSDKLoaded(t, a, "OwnedInput")
	if old == current {
		t.Fatal("actual reload did not replace object")
	}
	resumed = true
	a.drainOriginalBroadcasts()
	a.wg.Wait()
	if got := ownedManagedCallbacks(t, a, cookie, "OwnedInput", "owned.sdk.retained"); !slices.Equal(got, []string{"event_dynamic"}) {
		t.Fatal("retained listener did not reach reloaded worker", got)
	}
	chain([]string{"main", "twin"})
	control("OwnedInput", `{"action":"remove","kind":"chain","type":"`+kind+`","method":"sdk_chain"}`)
	chain([]string{"twin"})
	if err := a.stopOriginalPlugin(t.Context(), "OwnedInput"); err != nil {
		t.Fatal(err)
	}
	if current.originalEventMethodEnabled("event_dynamic") || a.originalEventHasHandler(plugins.EventQuery{Kind: plugins.BroadcastEventKind, Type: "owned.sdk.retained"}) {
		t.Fatal("stopped retained listener enabled")
	}
}

func TestOriginalEventSDKActualInitOverridesStaticDeclaration(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	config := `{"enabled":true,"event_controls":[{"action":"remove","kind":"broadcast","type":"owned.sdk.multiple","method":"event_first"},{"action":"add","kind":"broadcast","type":"owned.sdk.multiple","method":"event_dynamic"}]}`
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", config, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"broadcast","type":"owned.sdk.multiple","data":{}}`)
	if got := ownedManagedCallbacks(t, a, cookie, "OwnedInput", "owned.sdk.multiple"); !slices.Equal(got, []string{"event_dynamic", "event_second"}) {
		t.Fatal("static registration overwrote initializer's dynamic choice", got)
	}
}
