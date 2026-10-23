package app

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Declarations deliberately ignore enabled in the owned v3 binary. This test
// proves the manager's order, not coincidentally self-filtering plugin methods.
func TestPluginOriginalArtifactManagerStateBeforeDeclarations(t *testing.T) {
	binary := os.Getenv("OWNED_MODULE_FIXTURE_BINARY_V3")
	if binary == "" {
		t.Fatal("OWNED_MODULE_FIXTURE_BINARY_V3 must name the newly compiled instrumented fixture")
	}
	t.Setenv("OWNED_MODULE_FIXTURE_BINARY", binary)
	a, cookie, object := originalArtifactCloneFixture(t)
	const id = "OwnedModuleFixture"
	const sibling = "OwnedModuleFixturedisabledsibling"
	ctx := context.Background()
	count := func(value map[string]any, key string) float64 {
		t.Helper()
		n, ok := value[key].(float64)
		if !ok {
			t.Fatalf("v3 counter %s absent; wrong binary: %+v", key, value)
		}
		return n
	}
	checkOrder := func(value map[string]any) {
		t.Helper()
		calls, ok := value["calls"].([]any)
		if !ok {
			t.Fatal("v3 call trace absent")
		}
		state := ""
		for _, raw := range calls {
			call, ok := raw.(string)
			if !ok {
				t.Fatal("bad trace item")
			}
			if strings.HasPrefix(call, "get_state:") {
				state = strings.TrimPrefix(call, "get_state:")
			}
			if (call == "get_service" || call == "get_command") && state != "true" {
				t.Errorf("declaration without prior true get_state: %v", calls)
			}
		}
	}
	initial := originalArtifactSnapshot(t, object)
	if count(initial, "service_count") != 0 || count(initial, "command_count") != 0 {
		t.Fatal("missing/disabled initial state called declaration")
	}
	save := func(config string) {
		t.Helper()
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, config, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	save(`{"enabled":false,"cron":"0 0 * * *","marker":"disabled"}`)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/clone", `{"suffix":"DisabledSibling"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	other, ok := a.pluginRunningInstances.Load(sibling)
	if !ok {
		t.Fatal("formal sibling missing")
	}
	disabled := originalArtifactSnapshot(t, object)
	if count(disabled, "service_count") != 0 || count(disabled, "command_count") != 0 {
		t.Fatal("disabled registration called declarations")
	}
	activeJobs := func(instance string) int {
		t.Helper()
		var n int
		if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND group_key='original_services' AND active=1`, instance).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if activeJobs(id) != 0 {
		t.Fatal("disabled source registered job")
	}
	if _, _, ok := a.lookupOriginalPluginCommand("/owned_module"); ok {
		t.Fatal("disabled source registered command")
	}
	save(`{"enabled":true,"cron":"0 0 * * *","marker":"enabled"}`)
	enabled := originalArtifactSnapshot(t, object)
	if count(enabled, "service_count") < 1 || count(enabled, "command_count") < 1 {
		t.Fatal("enabled registration did not call real declarations")
	}
	if activeJobs(id) != 1 {
		t.Fatal("enabled source missing actual central job")
	}
	command, instance, ok := a.lookupOriginalPluginCommand("/owned_module")
	if !ok || instance != id || command.Action != "owned_tick" {
		t.Fatalf("actual command routing declaration: %+v %s %v", command, instance, ok)
	}
	checkOrder(enabled)
	save(`{"enabled":false,"cron":"0 0 * * *","marker":"disabled-again"}`)
	stopped := originalArtifactSnapshot(t, object)
	if count(stopped, "service_count") != count(enabled, "service_count") || count(stopped, "command_count") != count(enabled, "command_count") {
		t.Fatal("disabled-again invoked declarations")
	}
	if activeJobs(id) != 0 {
		t.Fatal("disabled-again retained public job qualification")
	}
	if _, _, ok = a.lookupOriginalPluginCommand("/owned_module"); ok {
		t.Fatal("disabled-again retained command")
	}
	checkOrder(stopped)
	otherState := originalArtifactSnapshot(t, other.(*originalPluginInstance))
	if count(otherState, "service_count") != 0 || count(otherState, "command_count") != 0 || activeJobs(sibling) != 0 {
		t.Fatal("source registration leaked declaration or job into disabled sibling")
	}
	if otherState["config"].(map[string]any)["marker"] != "disabled" {
		t.Fatal("source save changed sibling configuration")
	}
	checkOrder(otherState)
	// Additional explicit register while disabled must remain a true no-declare
	// path, including asynchronous full command rebuild.
	a.registerOriginalPlugin(ctx, id)
	a.wg.Wait()
	again := originalArtifactSnapshot(t, object)
	if count(again, "service_count") != count(stopped, "service_count") || count(again, "command_count") != count(stopped, "command_count") {
		t.Fatal("explicit disabled register invoked declarations")
	}
}
