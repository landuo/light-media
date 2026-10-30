package app

import (
	"encoding/json"
	"testing"
	"time"
)

// Actual installed process, ordinary PUT configuration -> registerOriginalPlugin
// -> updateOriginalPluginJobs -> get_state/get_service. No substitute method.
func TestOriginalServiceGetterFailureReplacesAllRegistrations(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	original, ok := a.pluginRunningInstances.Load("OwnedInput")
	if !ok || !original.(*originalPluginInstance).external {
		t.Fatal("actual worker missing")
	}
	config := func(enabled, broken, empty bool) {
		services := []any{}
		if !empty {
			for _, id := range []string{"first", "second"} {
				services = append(services, map[string]any{"id": id, "name": "Owned " + id, "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "input": map[string]any{"mode": "list-fail", "progress": 37}})
			}
		}
		raw, _ := json.Marshal(map[string]any{"enabled": enabled, "getter_error": broken, "services": services})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	count := func(want int) {
		t.Helper()
		a.pluginOriginalDirectJobs.mu.Lock()
		n := 0
		for key := range a.pluginOriginalDirectJobs.jobs {
			if key.instance == "OwnedInput" {
				n++
			}
		}
		a.pluginOriginalDirectJobs.mu.Unlock()
		if n != want {
			t.Fatalf("logical registry got %d want %d", n, want)
		}
		list := a.originalPublicServices(time.Now())
		if len(list) != want {
			t.Fatalf("public clocks got %d want %d", len(list), want)
		}
	}
	for _, c := range []struct {
		name                   string
		enabled, broken, empty bool
		want                   int
	}{{"registered", true, false, false, 2}, {"getter_failure_removes", true, true, false, 0}, {"getter_recovery", true, false, false, 2}, {"empty_removes", true, false, true, 0}, {"restored", true, false, false, 2}, {"disabled_removes", false, false, false, 0}, {"enabled_restores", true, false, false, 2}} {
		t.Run(c.name, func(t *testing.T) {
			config(c.enabled, c.broken, c.empty)
			count(c.want)
			same, _ := a.pluginRunningInstances.Load("OwnedInput")
			if same != original {
				t.Fatal("ordinary config replaced worker object")
			}
		})
	}
	config(true, true, false)
	count(0)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	count(0)
	next, ok := a.pluginRunningInstances.Load("OwnedInput")
	if !ok || next == original {
		t.Fatal("reload did not replace actual worker")
	}
	config(true, false, false)
	count(2)
	t.Log("normal installed worker: ordinary registration replaces all old metadata before getter failure; empty/disabled also clear, config recovery same object; reload failed getter remains empty then recovers")
}
