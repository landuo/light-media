package app

import (
	"container/heap"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"light-media/internal/plugins"
)

func ownedSDKEventCall(t *testing.T, a *App, cookie *http.Cookie, operation, input string) json.RawMessage {
	t.Helper()
	r := ownedInputRequest(t, a, cookie, "OwnedInput", operation, input)
	if r.Code != 200 {
		t.Fatal("actual SDK event request", r.Code, r.Body.String())
	}
	return append(json.RawMessage(nil), r.Body.Bytes()...)
}

func TestOriginalEventSDKActualBroadcastChainAndCheck(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	configure := func(id, label string, enabled, fail bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"enabled": enabled, "label": label, "chain_fail": fail})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(body), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	configure("OwnedInput", "main", true, false)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	configure("OwnedInputtwin", "twin", true, false)
	for _, input := range []string{`{"kind":"chain","type":"owned.sdk.chain"}`, `{"kind":"broadcast","type":"owned.sdk.broadcast"}`} {
		if got := ownedSDKEventCall(t, a, cookie, "event.check", input); string(got) != "true" {
			t.Fatal("enabled handler check", string(got))
		}
	}
	if got := ownedSDKEventCall(t, a, cookie, "event.check", `{"kind":"chain","type":"owned.missing"}`); string(got) != "false" {
		t.Fatal("missing event check", string(got))
	}
	if got := ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"chain","type":"owned.missing","data":false}`); string(got) != "null" {
		t.Fatal("missing chain must return None", string(got))
	}
	chain := func() plugins.MutableEvent {
		t.Helper()
		raw := ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"chain","type":"owned.sdk.chain","data":{"large":900719925474099312345},"priority":0}`)
		var event plugins.MutableEvent
		if json.Unmarshal(raw, &event) != nil || event.Type != "owned.sdk.chain" || event.Priority == nil || *event.Priority != 0 {
			t.Fatal("chain result envelope", string(raw))
		}
		if _, err := uuid.Parse(event.EventID); err != nil {
			t.Fatal("chain event identity", event.EventID)
		}
		var data struct {
			Visits []string        `json:"visits"`
			Large  json.RawMessage `json:"large"`
		}
		if json.Unmarshal(event.Data, &data) != nil || len(data.Visits) != 2 || !slices.Contains(data.Visits, "main") || !slices.Contains(data.Visits, "twin") || string(data.Large) != "900719925474099312345" {
			t.Fatal("self/other worker chain mutation not committed", string(event.Data))
		}
		if strings.Contains(string(raw), "ordinary_return") {
			t.Fatal("ordinary handler return replaced event")
		}
		return event
	}
	first := chain()
	configure("OwnedInput", "main", true, true)
	second := chain()
	if first.EventID == second.EventID {
		t.Fatal("separate chain sends reused event identity")
	}
	a.wg.Wait()
	if messages := a.systemMessages.get("system"); !strings.Contains(string(messages), "Go 插件执行失败") || strings.Contains(string(messages), "OWNED_PRIVATE") {
		t.Fatal("chain callback error isolation", string(messages))
	}
	if got := ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"broadcast","type":"owned.sdk.broadcast","data":{"marker":"broadcast","large":900719925474099312345},"priority":0}`); string(got) != "null" {
		t.Fatal("broadcast must return None", string(got))
	}
	ids := map[string]bool{}
	for _, instance := range []string{"OwnedInput", "OwnedInputtwin"} {
		found := false
		for _, raw := range ownedInputEvents(t, a, cookie, instance) {
			var event plugins.MutableEvent
			_ = json.Unmarshal(raw, &event)
			if event.Type != "owned.sdk.broadcast" {
				continue
			}
			if event.Priority == nil || *event.Priority != 0 || ids[event.EventID] || !strings.Contains(string(event.Data), "900719925474099312345") {
				t.Fatal("isolated broadcast identity/priority/data", string(raw))
			}
			if _, err := uuid.Parse(event.EventID); err != nil {
				t.Fatal(err)
			}
			ids[event.EventID], found = true, true
		}
		if !found {
			t.Fatal("missing actual broadcast recipient", instance)
		}
	}
	configure("OwnedInput", "main", false, false)
	configure("OwnedInputtwin", "twin", false, false)
	if got := ownedSDKEventCall(t, a, cookie, "event.check", `{"kind":"chain","type":"owned.sdk.chain"}`); string(got) != "false" {
		t.Fatal("check ignored execution enablement", string(got))
	}
	if got := ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"chain","type":"owned.sdk.chain","data":[]}`); string(got) != "null" {
		t.Fatal("disabled chain must return None", string(got))
	}
	r = ownedInputAPI(t, a, "POST", "/api/v1/plugin/OwnedInput/input", `{"operation":"event.send","stale":true,"input":{"kind":"broadcast","type":"owned.sdk.broadcast","data":{}}}`)
	if r.Code != 403 {
		t.Fatal("retained caller reused event capability", r.Code)
	}
}

func TestOriginalEventSDKActualPriorityBacklog(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	a.wg.Wait()
	state := &a.pluginOriginalBroadcast
	// Hold the consumer between iterations so real SDK requests accumulate.
	// Inspect dequeue order separately from callback completion: the original
	// thread pool likewise does not guarantee completion order across events.
	state.mu.Lock()
	idle := !state.running && len(state.pending) == 0
	state.running = true
	state.mu.Unlock()
	if !idle {
		t.Fatal("queue not idle")
	}
	resumed := false
	t.Cleanup(func() {
		if !resumed {
			a.drainOriginalBroadcasts()
		}
	})
	for _, priority := range []int{30, 0, -5, 10} {
		input, _ := json.Marshal(plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: "owned.sdk.broadcast", Data: json.RawMessage(`false`), Priority: &priority})
		if got := ownedSDKEventCall(t, a, cookie, "event.send", string(input)); string(got) != "null" {
			t.Fatal(string(got))
		}
	}
	state.mu.Lock()
	copy := append(originalBroadcastHeap(nil), state.pending...)
	state.mu.Unlock()
	var priorities []int
	for len(copy) > 0 {
		priorities = append(priorities, *heap.Pop(&copy).(originalQueuedBroadcast).event.Priority)
	}
	if !slices.Equal(priorities, []int{-5, 0, 10, 30}) {
		t.Fatal("actual queue lost event priority", priorities)
	}
	resumed = true
	a.drainOriginalBroadcasts()
	count := 0
	for _, raw := range ownedInputEvents(t, a, cookie, "OwnedInput") {
		var event plugins.MutableEvent
		_ = json.Unmarshal(raw, &event)
		if event.Type == "owned.sdk.broadcast" {
			count++
			if string(event.Data) != `{}` {
				t.Fatal("Event(data or {}) not applied", string(event.Data))
			}
		}
	}
	if count != 4 {
		t.Fatal("queued events did not reach real worker", count)
	}
	a.closeOriginalBackgroundAdmission()
	event, err := originalNewEvent("owned.sdk.broadcast", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.enqueueOriginalBroadcast(t.Context(), event) {
		t.Fatal("shutdown admitted broadcast")
	}
	a.wg.Wait()
}
