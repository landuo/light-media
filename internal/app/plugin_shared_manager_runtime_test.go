package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Exact independently extracted values from actual source component execution.
// This is not a recording generated from the Go implementation.
//
//go:embed testdata/shared-plugin-manager/lifecycle-source.json
var managerLifecycleSource []byte

type managerSourceCase struct {
	Case   string `json:"case"`
	Result struct {
		Returned  json.RawMessage `json:"returned"`
		Exception string          `json:"exception"`
		Message   string          `json:"message"`
	} `json:"result"`
	Trace []json.RawMessage `json:"trace"`
}
type managerSourceValues struct {
	Warm struct {
		Manager []managerSourceCase `json:"manager_algorithms"`
		Schema  struct {
			Defaults json.RawMessage `json:"defaults"`
			Cases    []struct {
				Descriptors json.RawMessage `json:"descriptors"`
			} `json:"cases"`
		} `json:"actual_schema"`
	} `json:"warm"`
}

func managerSource(t *testing.T) managerSourceValues {
	t.Helper()
	if sum := sha256.Sum256(managerLifecycleSource); hex.EncodeToString(sum[:]) != "4732897f3ddffc2c46839ab9d610bb727b02e8ba1637e2ef69bdf25119bf4fac" {
		t.Fatal("source capture bytes changed")
	}
	var source managerSourceValues
	if err := json.Unmarshal(managerLifecycleSource, &source); err != nil || len(source.Warm.Manager) == 0 || len(source.Warm.Schema.Cases) != 2 {
		t.Fatal("actual source observations missing", err)
	}
	return source
}
func managerSourceObservation(t *testing.T, source managerSourceValues, name string) managerSourceCase {
	t.Helper()
	for _, observation := range source.Warm.Manager {
		if observation.Case == name {
			return observation
		}
	}
	t.Fatal("missing actual source case", name)
	return managerSourceCase{}
}
func managerJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	if !originalBaseJSONEqual(got, want) {
		t.Fatalf("JSON differs\ngot  %s\nwant %s", got, want)
	}
}

func TestSharedManagerRuntimeSourceDefaultsAndGuards(t *testing.T) {
	source := managerSource(t)
	defaults, _ := json.Marshal(originalManagerDescriptorDefaults())
	managerJSONEqual(t, defaults, source.Warm.Schema.Defaults)
	a, scope, identity, active := managerMutationFixture(t)
	call := func(method, input string) (json.RawMessage, error) {
		return a.pluginOriginalResourceHost(context.Background(), scope, identity, method, json.RawMessage(input))
	}
	for _, value := range []struct{ method, input, source string }{
		{plugins.SharedPluginInit, `{"plugin_id":"Missing","conf":{"owned":1}}`, "init_missing"},
		{plugins.SharedPluginState, `{"plugin_id":"Missing"}`, "get_state_missing"},
	} {
		out, err := call(value.method, value.input)
		if err != nil {
			t.Fatal(err)
		}
		managerJSONEqual(t, out, managerSourceObservation(t, source, value.source).Result.Returned)
	}
	if out, err := call(plugins.SharedLocalPlugins, `{}`); err != nil || string(out) != `[]` {
		t.Fatalf("empty local array: %s %v", out, err)
	}
	for _, input := range []string{`{}`, `{"plugin_id":null}`, `{"plugin_id":3}`, `{"plugin_id":"Missing","conf":true}`, `{"plugin_id":"\ud800"}`, `{"plugin_id":"Missing","extra":true}`} {
		if _, err := call(plugins.SharedPluginState, input); err == nil {
			t.Fatal("invalid state input accepted", input)
		}
	}
	if _, err := call(plugins.SharedPluginInit, `{"plugin_id":"Missing"}`); err == nil {
		t.Fatal("omitted explicit conf accepted")
	}
	// Conf is raw finite JSON, not a string-ID scalar decoder. The source/SDK
	// escape boundary is retained without inventing arbitrary Python values.
	if out, err := call(plugins.SharedPluginInit, `{"plugin_id":"Missing","conf":{"raw":"\ud800","integer":9007199254740993}}`); err != nil || string(out) != "null" {
		t.Fatalf("raw conf boundary changed: %s %v", out, err)
	}
	active.Store(false)
	for _, method := range []string{plugins.SharedPluginInit, plugins.SharedPluginState, plugins.SharedLocalPlugins} {
		input := `{"plugin_id":"Missing"}`
		if method == plugins.SharedPluginInit {
			input = `{"plugin_id":"Missing","conf":null}`
		} else if method == plugins.SharedLocalPlugins {
			input = `{}`
		}
		if _, err := call(method, input); err == nil {
			t.Fatal("revoked call accepted", method)
		}
	}
	active.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.pluginOriginalResourceHost(ctx, scope, identity, plugins.SharedPluginState, json.RawMessage(`{"plugin_id":"Missing"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
}

type managerWorkerReply struct {
	Value json.RawMessage `json:"value"`
	Error string          `json:"error"`
	PID   int             `json:"pid"`
}
type managerLifecycleWorkerSnapshot struct {
	Conf  json.RawMessage   `json:"conf"`
	Trace []json.RawMessage `json:"trace"`
}

func managerRuntimeWorker(t *testing.T) (*App, *originalPluginInstance) {
	t.Helper()
	executable := os.Getenv("OWNED_MANAGER_FIXTURE_BINARY")
	if executable == "" {
		t.Fatal("OWNED_MANAGER_FIXTURE_BINARY must bind the independently built concurrent SDK worker")
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, err := exec.CommandContext(ctx, executable, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "Owned" || !slices.Equal(manifest.Capabilities, []string{plugins.SharedPluginInit, plugins.SharedPluginRemove, plugins.SharedPluginConfigDelete, plugins.SharedPluginDataDelete, plugins.SharedPluginState, plugins.SharedLocalPlugins, plugins.SharedSchedulerRemovePluginJob, plugins.SharedSchedulerUpdatePluginJob}) {
		t.Fatal("owned manager manifest differs")
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hash, ExecutableSHA256: hash, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	var indexHits, artifactHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected owned artifact method")
			w.WriteHeader(502)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			t.Error("unexpected owned artifact path")
			w.WriteHeader(502)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": "Owned", "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("Owned")
	if !ok {
		t.Fatal("actual worker not loaded")
	}
	object := value.(*originalPluginInstance)
	object.sessionMu.Lock()
	concurrent := object.session != nil && object.session.SupportsConcurrent()
	object.sessionMu.Unlock()
	if !object.external || object.hostContract != pluginResourcesSDKContract || !concurrent {
		t.Fatal("actual ordinary concurrent negotiation missing")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 {
		t.Fatal("artifact acquisition differs")
	}
	t.Cleanup(func() {
		if indexHits.Load() != 1 || artifactHits.Load() != 1 {
			t.Error("unexpected artifact reacquisition")
		}
	})
	return a, object
}
func managerWorkerQuery(t *testing.T, object *originalPluginInstance, input string) managerWorkerReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := object.callOriginalMethod(ctx, "query", json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	var result managerWorkerReply
	if json.Unmarshal(raw.Result, &result) != nil || result.PID <= 0 || result.PID == os.Getpid() {
		t.Fatalf("not an actual separate worker: %s", raw.Result)
	}
	return result
}
func managerWorkerConfigure(t *testing.T, object *originalPluginInstance, state json.RawMessage, initError, stateError bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"op": "configure", "state": state, "init_error": initError, "state_error": stateError})
	if reply := managerWorkerQuery(t, object, string(raw)); reply.Error != "" {
		t.Fatal(reply.Error)
	}
}
func managerWorkerInspect(t *testing.T, object *originalPluginInstance) managerLifecycleWorkerSnapshot {
	t.Helper()
	reply := managerWorkerQuery(t, object, `{"op":"inspect"}`)
	var result managerLifecycleWorkerSnapshot
	if reply.Error != "" || json.Unmarshal(reply.Value, &result) != nil {
		t.Fatal("worker inspection failed", reply.Error)
	}
	return result
}

func TestSharedManagerRuntimeActualConcurrentInitState(t *testing.T) {
	source := managerSource(t)
	a, object := managerRuntimeWorker(t)
	managerMutationSeed(t, a, "Owned")
	for _, value := range []struct {
		name, state string
		enabled     bool
	}{{"init_enabled", "true", true}, {"init_disabled", "false", false}} {
		managerWorkerConfigure(t, object, json.RawMessage(value.state), false, false)
		reply := managerWorkerQuery(t, object, `{"op":"init","plugin_id":"Owned","conf":{"owned":1}}`)
		if reply.Error != "" {
			t.Fatal(reply.Error)
		}
		observed := managerSourceObservation(t, source, value.name)
		managerJSONEqual(t, reply.Value, observed.Result.Returned)
		snapshot := managerWorkerInspect(t, object)
		trace, _ := json.Marshal(snapshot.Trace)
		wantTrace, _ := json.Marshal(observed.Trace[:2])
		managerJSONEqual(t, trace, wantTrace)
		managerJSONEqual(t, snapshot.Conf, json.RawMessage(`{"owned":1}`))
		object.configMu.RLock()
		enabled := object.eventsEnabled
		object.configMu.RUnlock()
		if enabled != value.enabled {
			t.Fatal("source event enable/disable differs")
		}
		managerMutationConfig(t, a, "Owned", `{"persisted":"before-delete"}`)
		if current, _ := a.pluginRunningInstances.Load("Owned"); current != object {
			t.Fatal("direct init replaced object")
		}
	}
	state := managerSourceObservation(t, source, "get_state_json_value").Result.Returned
	managerWorkerConfigure(t, object, state, false, false)
	reply := managerWorkerQuery(t, object, `{"op":"state","plugin_id":"Owned"}`)
	if reply.Error != "" {
		t.Fatal(reply.Error)
	}
	managerJSONEqual(t, reply.Value, state)
	if got := managerWorkerInspect(t, object); len(got.Trace) != 1 {
		t.Fatal("raw state getter not exactly once")
	}
	for _, value := range []struct {
		name                  string
		initError, stateError bool
	}{{"init_init_error", true, false}, {"get_state_callback_error", false, true}} {
		managerWorkerConfigure(t, object, state, value.initError, value.stateError)
		reply := managerWorkerQuery(t, object, `{"op":"init","plugin_id":"Owned","conf":{"owned":1}}`)
		observed := managerSourceObservation(t, source, value.name)
		// The source exception establishes failure, not a license to expose its
		// text to the worker. Existing concurrent HostCaller intentionally maps
		// a failed host_result to the code-only host_operation_failed boundary.
		if observed.Result.Exception == "" || observed.Result.Message == "" || reply.Error != "plugin: host_operation_failed" {
			t.Fatalf("source failure / existing SDK failure classification differs: %s", reply.Error)
		}
		snapshot := managerWorkerInspect(t, object)
		wantCount := 2
		if value.initError {
			wantCount = 1
		}
		if len(snapshot.Trace) != wantCount {
			t.Fatal("failure replayed getter/services", snapshot.Trace)
		}
		managerJSONEqual(t, snapshot.Conf, json.RawMessage(`{"owned":1}`))
		managerMutationConfig(t, a, "Owned", `{"persisted":"before-delete"}`)
	}
	// Restore after both failures in the same actual worker; no reload or retry.
	managerWorkerConfigure(t, object, state, false, false)
	if reply := managerWorkerQuery(t, object, `{"op":"init","plugin_id":"Owned","conf":null}`); reply.Error != "" {
		t.Fatal(reply.Error)
	}
	managerJSONEqual(t, managerWorkerInspect(t, object).Conf, json.RawMessage(`null`))
	managerMutationConfig(t, a, "Owned", `{"persisted":"before-delete"}`)
	t.Logf("actual concurrent worker PID %d: self init/raw state/error recovery; no job/API refresh", reply.PID)
}

func TestSharedManagerRuntimeLocalSourceDescriptors(t *testing.T) {
	source := managerSource(t)
	a, object := managerRuntimeWorker(t)
	state := managerSourceObservation(t, source, "get_state_json_value").Result.Returned
	managerWorkerConfigure(t, object, state, false, false)
	// Controlled class metadata reproduces the source component's attributes,
	// including absent version/description. It is not a claim that the executable
	// installer accepts a manifest without its mandatory version/name fields.
	value, _ := a.pluginOriginalClasses.Load("Owned")
	class := value.(originalPluginClass)
	class.installation.Manifest = json.RawMessage(`{"name":"Owned","plugin_order":2,"auth_level":1,"methods":["get_page"]}`)
	a.storeOriginalPluginClass("Owned", class)
	a.storeOriginalPluginClass("Empty", originalPluginClass{installation: PluginInstallation{PluginID: "Empty", InstanceID: "Empty", Manifest: json.RawMessage(`{"name":"Owned","plugin_order":2,"auth_level":1}`)}})
	a.pluginOriginalRunningMu.Lock()
	a.pluginOriginalClassOrder = []string{"Empty", "Owned"}
	a.pluginOriginalRunningMu.Unlock()
	reply := managerWorkerQuery(t, object, `{"op":"local"}`)
	if reply.Error != "" {
		t.Fatal(reply.Error)
	}
	managerJSONEqual(t, reply.Value, source.Warm.Schema.Cases[0].Descriptors)
	if got := managerWorkerInspect(t, object); len(got.Trace) != 1 {
		t.Fatal("local descriptor read state more than once")
	}
	// Empty is a class-only component here: matching descriptor output does not
	// claim the source's separate failing-Empty callback trace was replayed.
	managerWorkerConfigure(t, object, state, false, true)
	reply = managerWorkerQuery(t, object, `{"op":"local"}`)
	if reply.Error != "" {
		t.Fatal("per-object state error escaped descriptor list", reply.Error)
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(reply.Value, &rows) != nil || len(rows) != 2 || string(rows[1]["state"]) != "false" {
		t.Fatal("state error did not become False", string(reply.Value))
	}
	if got := managerWorkerInspect(t, object); len(got.Trace) != 1 {
		t.Fatal("failed getter replayed")
	}
	managerWorkerConfigure(t, object, state, false, false)
	if reply = managerWorkerQuery(t, object, `{"op":"local"}`); reply.Error != "" {
		t.Fatal(reply.Error)
	}
	managerJSONEqual(t, reply.Value, source.Warm.Schema.Cases[0].Descriptors)
}
