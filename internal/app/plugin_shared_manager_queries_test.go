package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"light-media/internal/plugins"
)

type managerRegistryObservation struct {
	Classes []string `json:"classes"`
	Running []string `json:"running"`
}
type managerQueryOracle struct {
	Initial      managerRegistryObservation `json:"initial"`
	BeforeStop   managerRegistryObservation `json:"before_stop"`
	AfterStop    managerRegistryObservation `json:"after_stop"`
	AfterRestart managerRegistryObservation `json:"after_restart"`
	ConfigCases  []struct {
		Input, Manager, Base json.RawMessage
		Error                *string
	} `json:"config_cases"`
	IDCases []struct {
		Input, Manager json.RawMessage
		Error          *string
	} `json:"id_cases"`
	InitTrace []struct {
		ID               string
		Classes, Running []string
		Config, Base     json.RawMessage
	} `json:"init_trace"`
	StopTrace []struct {
		ID               string
		Classes, Running []string
	} `json:"stop_trace"`
	CacheAfterFailure json.RawMessage `json:"cache_after_failure"`
}

func managerOracle(t *testing.T) managerQueryOracle {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/shared-plugin-manager/query-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var out managerQueryOracle
	if err = json.Unmarshal(raw, &out); err != nil || len(out.ConfigCases) != 11 || len(out.InitTrace) != 3 {
		t.Fatal("source witness missing", err)
	}
	return out
}

func managerInstall(t *testing.T, a *App, cookie *http.Cookie, variant string) {
	t.Helper()
	index, binary := originalMarketOwnedIndex(t, "OWNED_MANAGER_"+variant+"_BINARY")
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(binary)
		default:
			t.Error("unexpected artifact request")
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	res := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(mustCleanSeedJSON(map[string]any{"plugin_id": index.PluginID, "repo_url": server.URL + "/index"})), cookie, "")
	assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
	a.wg.Wait()
}

func managerWorker(t *testing.T, a *App, id string) *originalPluginInstance {
	t.Helper()
	value, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatal("actual worker not running", id)
	}
	p := value.(*originalPluginInstance)
	if !p.external || p.hostContract != pluginResourcesSDKContract {
		t.Fatal("not actual resource SDK", id)
	}
	return p
}
func managerCall(t *testing.T, p *originalPluginInstance, method string, input any) (json.RawMessage, error) {
	t.Helper()
	out, err := p.callOriginalMethod(context.Background(), method, mustCleanSeedJSON(input))
	return out.Result, err
}
func managerQuery(t *testing.T, p *originalPluginInstance, method string, target any) json.RawMessage {
	t.Helper()
	out, err := managerCall(t, p, "query", map[string]any{"method": method, "target": target})
	if err != nil {
		t.Fatal("actual query", method, err)
	}
	return out
}
func managerEqual(t *testing.T, got json.RawMessage, want any) {
	t.Helper()
	if !originalBaseJSONEqual(got, mustCleanSeedJSON(want)) {
		t.Fatalf("query got=%s want=%s", got, mustCleanSeedJSON(want))
	}
}
func managerBaseSet(t *testing.T, p *originalPluginInstance, target string, value any) error {
	t.Helper()
	_, err := managerCall(t, p, "base", map[string]any{"target": target, "value": value, "write": true})
	return err
}
func managerBaseGet(t *testing.T, p *originalPluginInstance, target string, key *string) json.RawMessage {
	t.Helper()
	out, err := managerCall(t, p, "base", map[string]any{"target": target, "key": key})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type managerWorkerSnapshot struct {
	PID         int
	InitCount   int `json:"init_count"`
	Calls       map[string]int
	StaleDenied bool `json:"stale_denied"`
}

func managerSnapshot(t *testing.T, p *originalPluginInstance) managerWorkerSnapshot {
	t.Helper()
	raw, err := managerCall(t, p, "snapshot", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	var out managerWorkerSnapshot
	if json.Unmarshal(raw, &out) != nil || out.PID == os.Getpid() || out.PID <= 0 || !out.StaleDenied {
		t.Fatal("not isolated active worker", string(raw))
	}
	return out
}

func TestSharedManagerQueriesActualRegistryAndLifecycle(t *testing.T) {
	oracle := managerOracle(t)
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "BETA")
	beta := managerWorker(t, a, "OwnedBeta")
	if err := managerBaseSet(t, beta, "OwnedAlpha", map[string]any{"enabled": true, "value": "initial", "": "empty-key"}); err != nil {
		t.Fatal(err)
	}
	managerInstall(t, a, cookie, "ALPHA")
	alpha := managerWorker(t, a, "OwnedAlpha")
	managerInstall(t, a, cookie, "BROKEN")
	if _, ok := a.pluginRunningInstances.Load("OwnedBroken"); ok {
		t.Fatal("init failure became running")
	}
	if _, ok := a.pluginOriginalClasses.Load("OwnedBroken"); !ok {
		t.Fatal("init failure lost imported class")
	}
	managerEqual(t, managerQuery(t, alpha, "ids", nil), oracle.Initial.Classes)
	managerEqual(t, managerQuery(t, beta, "running", nil), oracle.Initial.Running)
	if state, err := beta.getState(context.Background()); err != nil || state {
		t.Fatal("disabled worker excluded or enabled", err)
	}
	for _, step := range oracle.InitTrace {
		var got struct {
			ID               string
			Classes, Running []string
			Config, Base     json.RawMessage
			PID              int
		}
		raw := managerBaseGet(t, alpha, step.ID, new("init_observation"))
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != step.ID || !slices.Equal(got.Classes, step.Classes) || !slices.Equal(got.Running, step.Running) || !originalBaseJSONEqual(got.Config, step.Config) || !originalBaseJSONEqual(got.Base, step.Base) {
			t.Fatal("source init registry/config differs", string(raw), step)
		}
		if step.ID == "OwnedBroken" && syscall.Kill(got.PID, 0) == nil {
			t.Fatal("failed init process leaked", got.PID)
		}
	}
	before := managerSnapshot(t, alpha)
	other := managerSnapshot(t, beta)
	if before.PID == other.PID {
		t.Fatal("workers share process")
	}
	if err := a.stopOriginalPlugin(context.Background(), "OwnedBeta"); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, alpha, "ids", nil), oracle.AfterStop.Classes)
	managerEqual(t, managerQuery(t, alpha, "running", nil), oracle.AfterStop.Running)
	var stopped struct{ Classes, Running []string }
	if json.Unmarshal(managerBaseGet(t, alpha, "OwnedBeta", new("stop_observation")), &stopped) != nil || !slices.Equal(stopped.Classes, oracle.StopTrace[0].Classes) || !slices.Equal(stopped.Running, oracle.StopTrace[0].Running) {
		t.Fatal("cleanup query did not see original registry", stopped)
	}
	if syscall.Kill(other.PID, 0) == nil {
		t.Fatal("stopped worker still alive")
	}
	if a.startOriginalPlugin(context.Background(), "OwnedBeta") == nil {
		t.Fatal("actual restart failed")
	}
	managerEqual(t, managerQuery(t, alpha, "ids", nil), oracle.AfterRestart.Classes)
	managerEqual(t, managerQuery(t, alpha, "running", nil), oracle.AfterRestart.Running)
	if after := managerSnapshot(t, alpha); after.InitCount != before.InitCount || after.Calls["get_state"] != before.Calls["get_state"] {
		t.Fatal("queries reinitialized or invoked state")
	}
	res := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedAlpha/clone", `{"suffix":"Copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
	a.wg.Wait()
	clone := managerWorker(t, a, "OwnedAlphacopy")
	if managerSnapshot(t, clone).PID == before.PID {
		t.Fatal("clone not distinct")
	}
	want := append(slices.Clone(oracle.AfterRestart.Classes), "OwnedAlphacopy")
	managerEqual(t, managerQuery(t, clone, "ids", nil), want)
	if err := managerBaseSet(t, alpha, "OwnedAlphacopy", map[string]any{"ordinary": "clone-only"}); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, alpha, "config", "OwnedAlphacopy"), map[string]any{"ordinary": "clone-only"})
	managerEqual(t, managerQuery(t, clone, "config", "OwnedAlpha"), map[string]any{"enabled": true, "value": "initial"})
	t.Logf("actual registry/source/init/cleanup/clone workers alpha=%d beta=%d clone=%d", before.PID, other.PID, managerSnapshot(t, clone).PID)
}

func TestSharedManagerQueriesActualConfigSourceAndFailure(t *testing.T) {
	oracle := managerOracle(t)
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	before := managerSnapshot(t, p)
	for i, row := range oracle.ConfigCases {
		if err := managerBaseSet(t, p, "OwnedAlpha", row.Input); err != nil {
			t.Fatal(i, err)
		}
		got, err := managerCall(t, p, "query", map[string]any{"method": "config", "target": "OwnedAlpha"})
		if (err != nil) != (row.Error != nil) {
			t.Fatalf("source config %d error=%v source=%v", i, err, row.Error)
		}
		if err == nil {
			managerEqual(t, got, row.Manager)
		}
		managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), row.Base)
	}
	for _, row := range oracle.IDCases {
		got, err := managerCall(t, p, "query", map[string]any{"method": "config", "target": row.Input})
		if (err != nil) != (row.Error != nil) {
			t.Fatalf("id %s error=%v", row.Input, err)
		}
		if err == nil {
			managerEqual(t, got, row.Manager)
		}
	}
	if err := managerBaseSet(t, p, "NotImported", map[string]any{"ordinary": "stored but no class"}); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, p, "config", "NotImported"), map[string]any{})
	managerEqual(t, managerBaseGet(t, p, "NotImported", nil), map[string]any{"ordinary": "stored but no class"})
	stable := map[string]any{"ordinary": map[string]any{"list": []int{1}}, "enabled": true}
	if err := managerBaseSet(t, p, "OwnedAlpha", stable); err != nil {
		t.Fatal(err)
	}
	first := managerQuery(t, p, "config", "OwnedAlpha")
	var changed map[string]any
	_ = json.Unmarshal(first, &changed)
	changed["ordinary"] = "caller mutation"
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), stable)
	_, err := a.Store.DB.Exec(`CREATE TRIGGER owned_manager_fail BEFORE UPDATE ON plugin_values WHEN OLD.instance_id='OwnedAlpha' AND OLD.kind='config' BEGIN SELECT RAISE(ABORT,'owned config failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = managerBaseSet(t, p, "OwnedAlpha", oracle.CacheAfterFailure); err == nil {
		t.Fatal("SQL fault not reached")
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), oracle.CacheAfterFailure)
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_manager_fail`); err != nil {
		t.Fatal(err)
	}
	after := managerSnapshot(t, p)
	if after.PID != before.PID || after.InitCount != before.InitCount || after.Calls["get_state"] != before.Calls["get_state"] {
		t.Fatal("query mutated object", before, after)
	}
	// A fresh host restores the SQL fact, not the process-local failed-write cache.
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	empty := []string{}
	cold, err := New(Options{DataDir: dir, PluginMarketsOverride: &empty})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	cp := managerWorker(t, cold, "OwnedAlpha")
	managerEqual(t, managerQuery(t, cp, "config", "OwnedAlpha"), stable)
	if managerSnapshot(t, cp).PID == before.PID {
		t.Fatal("cold reused worker")
	}
}

func TestSharedManagerQueriesActualHTTPAuthConcurrencyAndScopes(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	if err := managerBaseSet(t, p, "OwnedAlpha", map[string]any{"ordinary": "safe query", "enabled": false}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := server.Client()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("POST", server.URL+"/api/v1/plugin/OwnedAlpha/query", strings.NewReader(`{"method":"config","target":"OwnedAlpha"}`))
			if err != nil {
				t.Error(err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(cookie)
			res, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				t.Errorf("actual HTTP status %d", res.StatusCode)
			}
			body, err := io.ReadAll(res.Body)
			if err != nil || !originalBaseJSONEqual(body, json.RawMessage(`{"ordinary":"safe query","enabled":false}`)) {
				t.Error("concurrent actual query did not return saved config", err)
			}
		}()
	}
	wg.Wait()
	res := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAlpha/query", `{"method":"config","target":"OwnedAlpha"}`, cookie, "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	managerEqual(t, res.Body.Bytes(), map[string]any{"ordinary": "safe query", "enabled": false})
	anon := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAlpha/query", `{"method":"ids"}`, nil, "")
	if anon.Code != 401 {
		t.Fatal("anonymous query", anon.Code)
	}
	denied := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAlpha/query", `{"method":"ids"}`, cookie, "http://cross-origin.invalid")
	if denied.Code != 403 {
		t.Fatal("cross-origin query", denied.Code)
	}
	page := callTest(t, a.Handler(), "GET", "/api/plugins/OwnedAlpha/page", "", cookie, "")
	if page.Code != 200 {
		t.Fatal("real plugin page", page.Code, page.Body.String())
	}
	// Host admission cannot be borrowed by a plugin-supplied identity or retained
	// after the actual phase closes, even with a recognized capability name.
	callback, revoke := p.originalBaseSessionHost(context.Background(), "query", nil)
	identity := plugins.Identity{PluginID: p.installation.PluginID, InstanceID: p.installation.InstanceID, Version: p.installation.Version}
	wrong := identity
	wrong.InstanceID = "OwnedOther"
	if _, err := callback(context.Background(), wrong, plugins.SharedPluginIDs, json.RawMessage(`{}`)); err == nil {
		t.Fatal("forged identity accepted")
	}
	for _, bad := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"extra":true}`), json.RawMessage(`{} {}`)} {
		if _, err := callback(context.Background(), identity, plugins.SharedPluginIDs, bad); err == nil {
			t.Fatal("malformed accepted", string(bad))
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := callback(cancelled, identity, plugins.SharedPluginIDs, json.RawMessage(`{}`)); err == nil {
		t.Fatal("cancelled context accepted")
	}
	revoke()
	if _, err := callback(context.Background(), identity, plugins.SharedPluginIDs, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expired phase accepted")
	}
	managerInstall(t, a, cookie, "DENIED")
	unprivileged := managerWorker(t, a, "OwnedDenied")
	if _, err := managerCall(t, unprivileged, "query", map[string]any{"method": "ids"}); err == nil {
		t.Fatal("undeclared capability accepted")
	}
	if managerSnapshot(t, p).PID <= 0 {
		t.Fatal("query session lost")
	}
	if !reflect.DeepEqual(a.pluginOriginalRunningOrder, []string{"OwnedAlpha", "OwnedDenied"}) {
		t.Fatal("query changed registry")
	}
}
