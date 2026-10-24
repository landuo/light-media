package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalCommandMenuProjection(t *testing.T) {
	rows := map[string]map[string]any{
		"/visible":      {"cmd": "/visible", "desc": "description", "category": "category", "pid": "clone-id", "event": "must.not.leak", "data": map[string]any{"action": "not_a_method"}},
		"/hidden":       {"show": false, "desc": "hidden", "pid": "source-id"},
		"/null_show":    {"show": nil},
		"/default_show": {"desc": "default"},
	}
	menu, executable := originalCommandMenuMetadata(rows)
	if !executable["/hidden"] || !executable["/null_show"] || !executable["/visible"] || !executable["/redo"] {
		t.Fatal("execution name subset")
	}
	if _, ok := menu["/hidden"]; ok {
		t.Fatal("show=false projected")
	}
	if _, ok := menu["/null_show"]; ok {
		t.Fatal("falsey explicit show projected")
	}
	if _, ok := menu["/default_show"]; !ok {
		t.Fatal("default show omitted")
	}
	if !originalBaseJSONEqual(menu["/visible"], json.RawMessage(`{"type":"plugin","description":"description","category":"category","pid":"clone-id"}`)) {
		t.Fatalf("plugin projection %s", menu["/visible"])
	}
	if !originalBaseJSONEqual(menu["/redo"], json.RawMessage(`{"type":"preset","description":"手动整理","category":null}`)) {
		t.Fatalf("preset projection %s", menu["/redo"])
	}
}

func TestOriginalChainRegistrationReplacementSnapshot(t *testing.T) {
	a := newTestApp(t)
	seen := []string{}
	secondEnabled := true
	add := func(id string, priority int, enabled func() bool, invoke func()) {
		a.registerOriginalChainHandler("owned.chain", originalMutableEventHandler{ID: id, Instance: id, Priority: priority, Enabled: enabled, Invoke: func(context.Context, *plugins.MutableEvent) error {
			seen = append(seen, id)
			if invoke != nil {
				invoke()
			}
			return nil
		}})
	}
	add("a", 10, nil, nil)
	add("b", 10, func() bool { return secondEnabled }, nil)
	add("zero", 0, nil, func() { secondEnabled = false })
	add("a", 10, nil, nil)
	event := &plugins.MutableEvent{Type: "owned.chain", Data: json.RawMessage(`{}`)}
	if !a.sendOriginalChainEvent(context.Background(), event) {
		t.Fatal("no dispatch")
	}
	if !slices.Equal(seen, []string{"zero", "b", "a"}) {
		t.Fatalf("replacement order/enabled snapshot %v", seen)
	}
	seen = nil
	a.sendOriginalChainEvent(context.Background(), event)
	if !slices.Equal(seen, []string{"zero", "a"}) {
		t.Fatalf("next enabled snapshot %v", seen)
	}
	for _, id := range []string{"a", "b", "zero"} {
		a.unregisterOriginalObjectChainHandlers(id)
	}
	if a.sendOriginalChainEvent(context.Background(), event) {
		t.Fatal("unregistered event dispatched")
	}
}

func TestOriginalCommandRegisterActualWorkers(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	mutator := installOrderWorker(t, a, cookie, os.Getenv("OWNED_COMMAND_MUTATOR"))
	observer := installOrderWorker(t, a, cookie, os.Getenv("OWNED_COMMAND_OBSERVER"))
	trace := filepath.Join(t.TempDir(), "commands.jsonl")
	configure := func(id, label, mode, source, command string, enabled, failInit bool) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": enabled, "trace_path": trace, "label": label, "mode": mode, "source": source, "command": command, "fail_init": failInit})
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		if failInit {
			if response.Code < 400 {
				t.Fatal("expected failed init")
			}
		} else {
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		}
		a.wg.Wait()
	}
	clear := func() {
		t.Helper()
		if err := os.WriteFile(trace, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func(pid string) (originalCommandRegisterDecision, error) {
		t.Helper()
		a.pluginOriginalCommandsMu.Lock()
		defer a.pluginOriginalCommandsMu.Unlock()
		return a.prepareOriginalCommandRegistration(ctx, pid)
	}
	registered := func() json.RawMessage {
		t.Helper()
		a.pluginOriginalCommandsMu.Lock()
		defer a.pluginOriginalCommandsMu.Unlock()
		raw, _ := json.Marshal(a.pluginOriginalRegisteredCommands)
		return raw
	}
	resetPrepared := func() {
		a.pluginOriginalCommandsMu.Lock()
		a.pluginOriginalRegisteredCommands = nil
		a.pluginOriginalCommandsMu.Unlock()
	}
	requirePrepared := func(result originalCommandRegisterDecision, err error) {
		t.Helper()
		if !result.ShouldRegister || !errors.Is(err, errOriginalCommandRegisterProviderUnavailable) {
			t.Fatalf("selection must not pretend provider delivery: %+v %v", result, err)
		}
	}
	configure(observer.PluginID, "observer", "observe", "", "/owned_observer", true, false)
	configure(mutator.PluginID, "mutator", "observe", "", "/owned_register", true, false)
	clear()
	resetPrepared()
	result, err := prepare("")
	requirePrepared(result, err)
	assertOrderTrace(t, trace, []string{"mutator:chain_first", "mutator:chain_second", "observer:event"})
	for _, row := range readOrderTrace(t, trace) {
		var envelope struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(row.Input, &envelope) != nil || envelope.Type != "command.register" {
			t.Fatalf("envelope %s", row.Input)
		}
		if string(envelope.Data["origin"]) != `"CommandChain"` || string(envelope.Data["service"]) != "null" || string(envelope.Data["source"]) != `"未知拦截源"` {
			t.Fatalf("schema defaults %s", row.Input)
		}
	}
	if _, ok := result.Commands["/owned_register_hidden"]; ok {
		t.Fatal("hidden command leaked into initial menu")
	}
	if !originalBaseJSONEqual(result.Commands["/owned_register"], json.RawMessage(`{"type":"plugin","description":"original executable","category":"owned","pid":"OwnedCommandMutator"}`)) {
		t.Fatalf("initial metadata %s", result.Commands["/owned_register"])
	}
	result, err = prepare("")
	if err != nil || result.ShouldRegister || result.Changed || result.Forced {
		t.Fatalf("unchanged must skip provider: %+v %v", result, err)
	}
	// An arbitrary ordinary RPC return is not a mutation snapshot.
	configure(mutator.PluginID, "mutator", "ordinary", "", "/owned_register", true, false)
	resetPrepared()
	clear()
	result, err = prepare("")
	requirePrepared(result, err)
	if result.Cancelled || len(result.Commands) == 0 {
		t.Fatal("ordinary callback return changed event")
	}
	// Mutation is shared synchronously, including changes preceding errors/panics.
	for _, mode := range []string{"mutate", "mutate_error", "mutate_panic"} {
		configure(mutator.PluginID, "mutator", mode, "", "/owned_register", true, false)
		resetPrepared()
		clear()
		result, err = prepare("")
		requirePrepared(result, err)
		assertOrderTrace(t, trace, []string{"mutator:chain_first", "mutator:chain_second", "observer:event"})
		if _, ok := result.Commands["/not_executable"]; ok {
			t.Fatal("unknown command escaped executable subset")
		}
		if _, ok := result.Commands["/redo"]; ok {
			t.Fatal("menu removal lost")
		}
		if !originalBaseJSONEqual(result.Commands["/owned_register"], json.RawMessage(`{"type":"plugin","description":"changed menu only","category":null,"opaque":900719925474099312345}`)) {
			t.Fatalf("mutation lost %s", result.Commands["/owned_register"])
		}
		for _, row := range readOrderTrace(t, trace)[1:] {
			var e struct {
				Data struct {
					Commands map[string]json.RawMessage `json:"commands"`
				} `json:"data"`
			}
			if json.Unmarshal(row.Input, &e) != nil || !originalBaseJSONEqual(e.Data.Commands["/owned_register"], result.Commands["/owned_register"]) {
				t.Fatalf("next handler did not see mutation %s", row.Input)
			}
		}
		command, instance, ok := a.lookupOriginalPluginCommand("/owned_register")
		if !ok || instance != mutator.PluginID || command.Description != "original executable" || command.Action != "not_a_method" {
			t.Fatalf("menu mutation touched execution: %+v %s %v", command, instance, ok)
		}
		if _, _, ok = a.lookupOriginalPluginCommand("/owned_register_hidden"); !ok {
			t.Fatal("hidden executable removed")
		}
	}
	// Cancel takes precedence over force, but never rolls back command rebuilding.
	before := registered()
	configure(mutator.PluginID, "mutator", "cancel", mutator.PluginID, "/changed_after_cancel", true, false)
	if !originalBaseJSONEqual(before, registered()) {
		t.Fatal("cancel changed selected menu")
	}
	if _, _, ok := a.lookupOriginalPluginCommand("/changed_after_cancel"); !ok {
		t.Fatal("cancel blocked executable rebuild")
	}
	clear()
	result, err = prepare(mutator.PluginID)
	if err != nil || !result.Cancelled || result.ShouldRegister || result.Forced {
		t.Fatalf("cancel precedence %+v %v", result, err)
	}
	assertOrderTrace(t, trace, []string{"mutator:chain_first", "mutator:chain_second", "observer:event"})
	for _, mode := range []string{"empty", "unknown"} {
		configure(mutator.PluginID, "mutator", mode, "", "/owned_register", true, false)
		before = registered()
		result, err = prepare(mutator.PluginID)
		if err != nil || result.ShouldRegister || len(result.Commands) != 0 || !originalBaseJSONEqual(before, registered()) {
			t.Fatalf("empty filtered selection %+v %v", result, err)
		}
	}
	configure(mutator.PluginID, "mutator", "empty", mutator.PluginID, "/owned_register", true, false)
	result, err = prepare(mutator.PluginID)
	requirePrepared(result, err)
	if !result.Forced || len(result.Commands) != 0 {
		t.Fatalf("force empty %+v", result)
	}
	configure(mutator.PluginID, "mutator", "source", mutator.PluginID, "/owned_register", true, false)
	result, err = prepare(mutator.PluginID)
	requirePrepared(result, err)
	if !result.Forced || result.Changed {
		t.Fatalf("source-matching unchanged force %+v", result)
	}
	for _, pid := range []string{"", observer.PluginID} {
		result, err = prepare(pid)
		if err != nil || result.ShouldRegister || result.Forced {
			t.Fatalf("wrong source forced %+v %v", result, err)
		}
	}
	// Business-disabled current objects are excluded from both command collection
	// and the enabled handler snapshot, not merely inferred from installation.
	configure(mutator.PluginID, "mutator", "source", mutator.PluginID, "/owned_register", false, false)
	clear()
	_, _ = prepare("")
	assertOrderTrace(t, trace, []string{"observer:event"})
	if _, _, ok := a.lookupOriginalPluginCommand("/owned_register"); ok {
		t.Fatal("disabled command still collected")
	}
	configure(mutator.PluginID, "mutator", "observe", "", "/owned_register", true, false)
	// Clone binds to its own persistent worker and moves independently on reload.
	clone := mutator.PluginID + "isolated"
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+mutator.PluginID+"/clone", `{"suffix":"Isolated"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	configure(clone, "clone", "observe", "", "/owned_clone", true, false)
	clear()
	_, _ = prepare("")
	assertOrderTrace(t, trace, []string{"mutator:chain_first", "mutator:chain_second", "clone:chain_first", "clone:chain_second", "observer:event"})
	sourceObject := originalBaseSDKLoaded(t, a, mutator.PluginID)
	cloneObject := originalBaseSDKLoaded(t, a, clone)
	if sourceObject == cloneObject {
		t.Fatal("clone shares calling object")
	}
	if _, err := a.reloadOriginalPlugin(ctx, mutator.PluginID); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if originalObjectStillLoaded(sourceObject) {
		t.Fatal("old object remains loaded")
	}
	clear()
	_, _ = prepare("")
	assertOrderTrace(t, trace, []string{"clone:chain_first", "clone:chain_second", "mutator:chain_first", "mutator:chain_second", "observer:event"})
	pids := map[string]int{}
	for _, row := range readOrderTrace(t, trace) {
		pids[row.Label] = row.PID
	}
	if pids["clone"] == pids["mutator"] {
		t.Fatal("clone shares worker process")
	}
	if err := a.stopOriginalPlugin(ctx, clone); err != nil {
		t.Fatal(err)
	}
	clear()
	_, _ = prepare("")
	assertOrderTrace(t, trace, []string{"mutator:chain_first", "mutator:chain_second", "observer:event"})
	a.pluginOriginalEventsMu.Lock()
	for _, handlers := range a.pluginOriginalChainHandlers {
		for _, handler := range handlers {
			if handler.Instance == clone {
				t.Error("stopped clone handler retained")
			}
		}
	}
	a.pluginOriginalEventsMu.Unlock()
	// Failed replacement start must not retain callbacks to a stale object.
	configure(mutator.PluginID, "mutator", "observe", "", "/owned_register", true, true)
	if _, err := a.reloadOriginalPlugin(ctx, mutator.PluginID); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if _, ok := a.pluginRunningInstances.Load(mutator.PluginID); ok {
		t.Fatal("failed replacement loaded")
	}
	clear()
	_, _ = prepare("")
	assertOrderTrace(t, trace, []string{"observer:event"})
	if err := a.stopOriginalPlugin(ctx, observer.PluginID); err != nil {
		t.Fatal(err)
	}
	clear()
	event := &plugins.MutableEvent{Type: "command.register", Data: json.RawMessage(`{}`)}
	if a.sendOriginalChainEvent(ctx, event) {
		t.Fatal("empty chain dispatched")
	}
	if rows := readOrderTrace(t, trace); !reflect.DeepEqual(rows, []orderObservation{}) {
		t.Fatalf("unexpected stopped dispatch %v", rows)
	}
}
