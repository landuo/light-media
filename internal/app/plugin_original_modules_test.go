package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

const ownedModuleID = "OwnedModuleOverride"

func ownedModuleFixture(t *testing.T, declares bool) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	variable := "OWNED_MODULE_OVERRIDE_BINARY"
	if !declares {
		variable = "OWNED_MODULE_NO_DECLARATION_BINARY"
	}
	return ownedModuleFixtureVariant(t, declares, variable)
}

// Variant selection is explicit: event-oracle tests need the worker that actually
// declares those handlers without changing unrelated generic module fixtures.
func ownedModuleFixtureVariant(t *testing.T, declares bool, variable string) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv(variable)
	if binary == "" {
		t.Fatalf("%s must name an actual separately compiled owned-module-override worker", variable)
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != ownedModuleID || slices.Contains(manifest.Methods, "get_module") != declares {
		t.Fatal("wrong owned module worker", string(raw))
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-base/v1"}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": ownedModuleID, "repo_url": server.URL + "/index"})
	r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	value, loaded := a.pluginRunningInstances.Load(ownedModuleID)
	if !loaded || !value.(*originalPluginInstance).external {
		t.Fatal("real worker not loaded")
	}
	return a, cookie, value.(*originalPluginInstance)
}

type ownedModuleSnapshot struct {
	TransferEvents []json.RawMessage          `json:"transfer_events"`
	Calls          map[string]int             `json:"calls"`
	Inputs         map[string]json.RawMessage `json:"inputs"`
	PID            int                        `json:"pid"`
}

func ownedModuleRead(t *testing.T, object *originalPluginInstance) ownedModuleSnapshot {
	t.Helper()
	var result ownedModuleSnapshot
	if err := object.readSession(context.Background(), "snapshot", &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func ownedModuleClone(t *testing.T, a *App, cookie *http.Cookie, suffix string) *originalPluginInstance {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"suffix": suffix, "name": "Owned module " + suffix})
	r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+ownedModuleID+"/clone", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	value, loaded := a.pluginRunningInstances.Load(ownedModuleID + suffix)
	if !loaded {
		t.Fatal("real clone not loaded")
	}
	return value.(*originalPluginInstance)
}

func saveOwnedModule(t *testing.T, a *App, cookie *http.Cookie, object *originalPluginInstance, config string) {
	t.Helper()
	ownedDashboardMetadataSave(t, a, cookie, object.installation.InstanceID, config, http.StatusOK)
}

func TestOriginalModulesRegistryActualClonesLifecycle(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	ctx := context.Background()
	before := ownedModuleRead(t, source)
	if groups, err := a.originalPluginModules(ctx, ""); err != nil || len(groups) != 0 {
		t.Fatal(groups, err)
	}
	if after := ownedModuleRead(t, source); after.Calls["get_module"] != before.Calls["get_module"] {
		t.Fatal("disabled getter ran")
	}
	config := `{"enabled":true,"modules":{"fetch":"module_execute"},"result":[1]}`
	saveOwnedModule(t, a, cookie, source, config)
	zeta := ownedModuleClone(t, a, cookie, "zeta")
	alpha := ownedModuleClone(t, a, cookie, "alpha")
	for _, object := range []*originalPluginInstance{zeta, alpha} {
		saveOwnedModule(t, a, cookie, object, config)
	}
	before = ownedModuleRead(t, source)
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil || len(groups) != 3 || groups[0].object != source || groups[1].object != zeta || groups[2].object != alpha || groups[1].PluginName == groups[0].PluginName {
		t.Fatal("source order/clone identities", groups, err)
	}
	if only, err := a.originalPluginModules(ctx, alpha.installation.InstanceID); err != nil || len(only) != 1 || only[0].object != alpha {
		t.Fatal("pid filter", only, err)
	}
	if after := ownedModuleRead(t, source); after.PID != before.PID || after.Calls["init"] != before.Calls["init"] {
		t.Fatal("registry reinitialized worker")
	}
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"fail_module":true}`)
	if groups, err = a.originalPluginModules(ctx, ""); err != nil || len(groups) != 2 || groups[0].object != zeta {
		t.Fatal("declaration failure stopped registry", groups, err)
	}
	for _, falsey := range []string{`null`, `false`, `0`, `""`, `[]`, `{}`} {
		saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":`+falsey+`}`)
		if groups, err = a.originalPluginModules(ctx, ownedModuleID); err != nil || len(groups) != 1 || string(groups[0].Modules) != `[]` {
			t.Fatal("empty source group disappeared", groups, err)
		}
	}
	ownedDashboardMetadataSave(t, a, cookie, ownedModuleID, `{"enabled":true,"fail_state":true}`, http.StatusInternalServerError)
	if groups, err = a.originalPluginModules(ctx, ""); err != nil || len(groups) != 2 {
		t.Fatal("state failure stopped registry", groups, err)
	}
	if err = a.stopOriginalPlugin(ctx, zeta.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	if groups, err = a.originalPluginModules(ctx, ""); err != nil || len(groups) != 1 || groups[0].object != alpha {
		t.Fatal("stopped object remained", groups, err)
	}
}

func TestOriginalModulesJSONAggregationActualWorkers(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	zeta := ownedModuleClone(t, a, cookie, "zeta")
	alpha := ownedModuleClone(t, a, cookie, "alpha")
	objects := []*originalPluginInstance{source, zeta, alpha}
	args := json.RawMessage(`[9007199254740993123456789,{"text":"位置参数"}]`)
	kwargs := json.RawMessage(`{"number":9007199254740993123456789,"text":"关键字"}`)
	for _, tc := range []struct {
		name, initial string
		returns       [3]string
		want          string
		calls         [3]int
	}{
		{"null_then_lists", `null`, [3]string{`null`, `[]`, `[1]`}, `[1]`, [3]int{1, 1, 1}},
		{"list_ignores_scalar", `[]`, [3]string{`[1]`, `false`, `[9007199254740993123456789]`}, `[1,9007199254740993123456789]`, [3]int{1, 1, 1}},
		{"list_ignores_null", `[0]`, [3]string{`null`, `[]`, `[2]`}, `[0,2]`, [3]int{1, 1, 1}},
		{"array_of_nulls_is_list", `null`, [3]string{`[null,null]`, `[1]`, `[]`}, `[null,null,1]`, [3]int{1, 1, 1}},
		{"tuple_of_nulls_continues", `null`, [3]string{`{"__plugin_module_tuple":[null,null]}`, `[1]`, `[2]`}, `[1,2]`, [3]int{1, 1, 1}},
		{"empty_tuple_continues", `null`, [3]string{`{"__plugin_module_tuple":[]}`, `null`, `[2]`}, `[2]`, [3]int{1, 1, 1}},
		{"tuple_false_is_terminal", `null`, [3]string{`{"__plugin_module_tuple":[false,null]}`, `[1]`, `[2]`}, `{"__plugin_module_tuple":[false,null]}`, [3]int{1, 0, 0}},
		{"list_ignores_tuple", `[0]`, [3]string{`{"__plugin_module_tuple":[1]}`, `{"__plugin_module_tuple":[]}`, `[2]`}, `[0,2]`, [3]int{1, 1, 1}},
		{"initial_empty_tuple", `{"__plugin_module_tuple":[null]}`, [3]string{`null`, `null`, `{"__plugin_module_tuple":[]}`}, `{"__plugin_module_tuple":[]}`, [3]int{1, 1, 1}},
		{"tuple_envelope_with_extra_key_is_dictionary", `null`, [3]string{`{"__plugin_module_tuple":[],"data":1}`, `[1]`, `[2]`}, `{"__plugin_module_tuple":[],"data":1}`, [3]int{1, 0, 0}},
		{"false_terminal", `null`, [3]string{`null`, `false`, `[3]`}, `false`, [3]int{1, 1, 0}},
		{"zero_terminal", `null`, [3]string{`0`, `[2]`, `[3]`}, `0`, [3]int{1, 0, 0}},
		{"empty_text_terminal", `null`, [3]string{`""`, `[2]`, `[3]`}, `""`, [3]int{1, 0, 0}},
		{"empty_object_terminal", `null`, [3]string{`{}`, `[2]`, `[3]`}, `{}`, [3]int{1, 0, 0}},
		{"initial_scalar_terminal", `false`, [3]string{`[1]`, `[2]`, `[3]`}, `false`, [3]int{0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := [3]ownedModuleSnapshot{}
			for i, object := range objects {
				saveOwnedModule(t, a, cookie, object, `{"enabled":true,"modules":{"fetch":"module_execute"},"result":`+tc.returns[i]+`}`)
				before[i] = ownedModuleRead(t, object)
			}
			result, err := a.invokeOriginalPluginModules(context.Background(), "fetch", json.RawMessage(tc.initial), args, kwargs)
			if err != nil || !originalBaseJSONEqual(result, json.RawMessage(tc.want)) {
				t.Fatalf("aggregation=%s want=%s err=%v", result, tc.want, err)
			}
			for i, object := range objects {
				after := ownedModuleRead(t, object)
				if after.Calls["module_execute"]-before[i].Calls["module_execute"] != tc.calls[i] {
					t.Fatal("call order/short circuit", i, after.Calls, before[i].Calls)
				}
				if tc.calls[i] != 0 {
					var input map[string]json.RawMessage
					if json.Unmarshal(after.Inputs["module_execute"], &input) != nil || len(input) != 2 || !originalBaseJSONEqual(input["args"], args) || !originalBaseJSONEqual(input["kwargs"], kwargs) {
						t.Fatal("parameters changed", string(after.Inputs["module_execute"]))
					}
				}
			}
		})
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("entered retired queue", runs, err)
	}
}

func TestOriginalModulesRateLimitPreservesWorkerAndSkipsSystemError(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "ratelimit")
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":{"fetch":"module_execute"},"rate_limited":true}`)
	saveOwnedModule(t, a, cookie, clone, `{"enabled":true,"modules":{"fetch":"module_execute"},"result":[2]}`)
	a.wg.Wait()
	a.systemMessages.get("system")
	before := ownedModuleRead(t, source)
	var attempts originalModuleAttempts
	result, err := a.invokeOriginalPluginModulesObserved(context.Background(), "fetch", nil, nil, nil, &attempts)
	if err != nil || string(result) != `[2]` || attempts.Called != 2 || attempts.Failed != 0 {
		t.Fatal("expected throttle did not continue", string(result), err, attempts)
	}
	a.wg.Wait()
	if messages := a.systemMessages.get("system"); len(messages) != 0 {
		t.Fatal("expected throttle became a system error", string(messages))
	}
	cloneBefore := ownedModuleRead(t, clone)
	result, err = a.invokeOriginalPluginModules(context.Background(), "fetch", nil, nil, json.RawMessage(`{"raise_exception":true}`))
	if !errors.Is(err, plugins.ErrModuleRateLimited) || string(result) != `null` || ownedModuleRead(t, clone).Calls["module_execute"] != cloneBefore.Calls["module_execute"] {
		t.Fatal("raise_exception swallowed throttle", string(result), err)
	}
	if after := ownedModuleRead(t, source); after.PID != before.PID || after.Calls["init"] != before.Calls["init"] {
		t.Fatal("throttle killed or reinitialized the worker", before, after)
	}
}

func TestOriginalModulesFailureAndBindingIsolation(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "healthy")
	saveOwnedModule(t, a, cookie, clone, `{"enabled":true,"modules":{"fetch":"module_execute"},"result":[2]}`)
	for _, binding := range []string{`"module_fail"`, `"not_declared"`, `42`} {
		saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":{"fetch":`+binding+`}}`)
		result, err := a.invokeOriginalPluginModules(context.Background(), "fetch", json.RawMessage(`[1]`), nil, nil)
		if err != nil || !originalBaseJSONEqual(result, json.RawMessage(`[1,2]`)) {
			t.Fatal("failure did not preserve/continue", string(result), err)
		}
		before := ownedModuleRead(t, clone)
		result, err = a.invokeOriginalPluginModules(context.Background(), "fetch", json.RawMessage(`[1]`), nil, json.RawMessage(`{"raise_exception":true}`))
		if err == nil || string(result) != `[1]` || ownedModuleRead(t, clone).Calls["module_execute"] != before.Calls["module_execute"] {
			t.Fatal("raise_exception did not stop", string(result), err)
		}
	}
	for _, binding := range []string{`null`, `false`, `0`, `""`} {
		saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":{"fetch":`+binding+`}}`)
		result, err := a.invokeOriginalPluginModules(context.Background(), "fetch", nil, nil, json.RawMessage(`{"raise_exception":true}`))
		if err != nil || string(result) != `[2]` {
			t.Fatal("falsey callable was invoked", string(result), err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.invokeOriginalPluginModules(ctx, "fetch", nil, nil, nil); err == nil {
		t.Fatal("cancelled call continued")
	}
	for _, invalid := range []struct{ args, kwargs string }{{`null`, `{}`}, {`{}`, `{}`}, {`[]`, `null`}, {`[]`, `[]`}} {
		if _, err := a.invokeOriginalPluginModules(context.Background(), "fetch", nil, json.RawMessage(invalid.args), json.RawMessage(invalid.kwargs)); err == nil {
			t.Fatal("invalid parameter shape accepted", invalid)
		}
	}
}

func TestOriginalModulesMissingDeclarationBeforeState(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, false)
	ownedDashboardMetadataSave(t, a, cookie, ownedModuleID, `{"enabled":true,"fail_state":true}`, http.StatusInternalServerError)
	before := ownedModuleRead(t, source)
	groups, err := a.originalPluginModules(context.Background(), "")
	after := ownedModuleRead(t, source)
	if err != nil || !reflect.DeepEqual(groups, []originalPluginModuleGroup{}) || before.Calls["get_state"] != after.Calls["get_state"] {
		t.Fatal("missing declaration queried state", groups, err, before, after)
	}
}
