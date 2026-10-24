package app

import (
	"reflect"
	"testing"
)

// Pinned Command.__build_plugin_commands calls get_plugin_commands() without
// pid: registration legitimately observes get_state on disabled siblings.
// Only that exact instrumented read is allowed; every other member stays equal.
func assertOriginalArtifactOnlyDisabledStateObserved(t *testing.T, before, after map[string]any) {
	t.Helper()
	for _, key := range []string{"version", "init_count", "stop_count", "failure_count", "config", "state_count", "service_count", "command_count", "calls"} {
		if _, ok := before[key]; !ok {
			t.Fatalf("baseline missing actual fixture member %s", key)
		}
		if _, ok := after[key]; !ok {
			t.Fatalf("after missing actual fixture member %s", key)
		}
	}
	oldState, ok := before["state_count"].(float64)
	if !ok {
		t.Fatal("invalid actual state counter")
	}
	newState, ok := after["state_count"].(float64)
	if !ok || newState < oldState || oldState < 0 || oldState != float64(int(oldState)) || newState != float64(int(newState)) {
		t.Fatalf("invalid or decreasing state counter %v -> %v", before["state_count"], after["state_count"])
	}
	oldCalls, ok := before["calls"].([]any)
	if !ok {
		t.Fatal("invalid baseline trace")
	}
	newCalls, ok := after["calls"].([]any)
	if !ok || len(newCalls) < len(oldCalls) {
		t.Fatal("actual call trace shrank or changed type")
	}
	if !reflect.DeepEqual(oldCalls, newCalls[:len(oldCalls)]) {
		t.Fatal("historical calls rewritten", oldCalls, newCalls)
	}
	suffix := newCalls[len(oldCalls):]
	if len(suffix) != int(newState-oldState) {
		t.Fatalf("state observations do not explain counter delta: suffix=%v state=%v->%v", suffix, oldState, newState)
	}
	for _, call := range suffix {
		if call != "get_state:false" {
			t.Fatalf("unexpected sibling method instead of disabled state observation: %v", call)
		}
	}
	oldMembers, newMembers := map[string]any{}, map[string]any{}
	for key, value := range before {
		if key != "calls" && key != "state_count" {
			oldMembers[key] = value
		}
	}
	for key, value := range after {
		if key != "calls" && key != "state_count" {
			newMembers[key] = value
		}
	}
	if !reflect.DeepEqual(oldMembers, newMembers) {
		t.Fatalf("actual sibling members changed beyond allowed registry read: before=%+v after=%+v", oldMembers, newMembers)
	}
}
