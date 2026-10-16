package autosignin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestAutoSignInSDKProcess(t *testing.T) {
	if !slices.Contains(os.Args, "autosignin-sdk-fixture") {
		return
	}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, Manifest(), Handle); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
func sdkRun(t *testing.T, machine *Machine, method, action string, config any, setup func(*TrustedRun)) plugins.RunResult {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	identity := plugins.Identity{PluginID: "AutoSignIn", InstanceID: "AutoSignIn", Version: Version}
	pinned := TrustedRun{Identity: identity, Method: method, Action: action, Config: config}
	if setup != nil {
		setup(&pinned)
	}
	invocation, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"sign_sites":["worker-forged-site"],"token":"worker-forged-secret"}`), Input: json.RawMessage(`{"url":"https://evil.invalid"}`)})
	out, err := (plugins.Runner{Timeout: 10 * time.Second, MaxHostCalls: 2}).Run(context.Background(), plugins.Command{Path: path, Args: []string{"-test.run=^TestAutoSignInSDKProcess$", "--", "autosignin-sdk-fixture"}}, Manifest(), identity.InstanceID, method, invocation, machine.Bind(pinned))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestAutoSignInRealSDKHostGrantWholeLifecycleHTTPHistoryAndPage(t *testing.T) {
	var requests atomic.Int32
	checkpoint := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "sid=synthetic-host-cookie" || r.Header.Get("Authorization") != "" {
			t.Error("host credentials changed")
		}
		if r.Method != "GET" || r.URL.Path != "/attendance.php" {
			t.Error("source generic endpoint differs")
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		io.WriteString(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer server.Close()
	private := Object{"id": 1, "name": "Synthetic", "url": server.URL + "/", "cookie": "sid=synthetic-host-cookie", "token": "synthetic-host-token", "ua": "synthetic-host-agent"}
	sites := NewRegisteredSites([]Registration{{Site: private}})
	sites.Now = func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }
	sites.Stats = func(context.Context, string, bool, int) error { return nil }
	var saved Object
	machine, err := NewMachine(MachineDependencies{Inventory: func(context.Context) (Inventory, error) {
		return Inventory{Configured: []Object{private}, Indexers: []Object{private}}, nil
	}, Visit: sites.Visit, Lookup: sites.Lookup, Checkpoint: func(_ context.Context, snapshot Object) error {
		checkpoint++
		saved = clone(snapshot).(Object)
		return nil
	}, Now: sites.Now, Emit: func(context.Context, string, Object) error { return nil }, Notify: func(context.Context, Object) error { return nil }, RandomInt: func(a, b int) (int, error) { return a, nil }})
	if err != nil {
		t.Fatal(err)
	}
	form := sdkRun(t, machine, "config_form", "", nil, nil)
	if bytes.Contains(form.Result, []byte("host-cookie")) || bytes.Contains(form.Result, []byte("host-token")) || !bytes.Contains(form.Result, []byte("Synthetic")) {
		t.Fatal("options leaked host secrets or lost site option")
	}
	config := Object{"enabled": true, "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1, "cron": "2.3/9-23", "retry_keyword": "Cookie已失效"}
	sdkRun(t, machine, "invoke", "init", config, nil)
	if !equal(saved["start_time"], 9) || !equal(saved["end_time"], 23) {
		t.Fatal("source service window not retained", saved)
	}
	sdkRun(t, machine, "invoke", "service", nil, nil)
	sdkRun(t, machine, "invoke", "service", nil, nil)
	if requests.Load() != 1 {
		t.Fatal("daily history did not suppress second visit", requests.Load())
	}
	state := asObject(saved["data"])
	daily := asObject(state["签到-2026-01-02"])
	if !Truth(asObject(asObject(daily["results"])["1"])["success"]) {
		t.Fatal("source confirmed signin was not stored", state)
	}
	page := sdkRun(t, machine, "page", "", nil, nil)
	if !bytes.Contains(page.Result, []byte("Synthetic")) || bytes.Contains(page.Result, []byte("host-cookie")) || bytes.Contains(page.Result, []byte("host-token")) {
		t.Fatal("page missing source history or leaked credentials")
	}
	sdkRun(t, machine, "invoke", "init", Object{"enabled": true, "sign_sites": []any{"all"}, "login_sites": []any{}, "cron": "2.3"}, nil)
	if !equal(saved["start_time"], 9) {
		t.Fatal("normal save reset source volatile window")
	}
	sdkRun(t, machine, "invoke", "reload", Object{"enabled": true, "sign_sites": []any{"all"}, "login_sites": []any{}, "cron": "2.3"}, nil)
	if saved["start_time"] != nil || len(asObject(saved["data"])) == 0 {
		t.Fatal("reload failed to reset volatile fields or preserve history")
	}
	api := sdkRun(t, machine, "http", "", nil, func(r *TrustedRun) { r.Domain = server.URL; r.APIKeyAccepted = false })
	var response plugins.HTTPResponse
	if Decode(api.Result, &response) != nil || !bytes.Contains(response.Body, []byte("API密钥错误")) {
		t.Fatal("API boolean credential result lost", string(api.Result))
	}
	if requests.Load() != 1 {
		t.Fatal("invalid API key reached lookup/network")
	}
	api = sdkRun(t, machine, "http", "", nil, func(r *TrustedRun) { r.Domain = server.URL; r.APIKeyAccepted = true })
	if Decode(api.Result, &response) != nil || !bytes.Contains(response.Body, []byte(`"signin_success":true`)) || requests.Load() != 2 {
		t.Fatal("authorized source domain entry failed", string(response.Body), requests.Load())
	}
	sdkRun(t, machine, "invoke", "site_deleted", nil, func(r *TrustedRun) { r.DeletedID = 1 })
	if checkpoint < 6 {
		t.Fatal("host checkpoint hook bypassed", checkpoint)
	}
}
func TestAutoSignInSDKForgedCapabilityIdentityArgumentsAndPublicInventory(t *testing.T) {
	if err := Manifest().Validate(); err != nil {
		t.Fatal(err)
	}
	m, _ := NewMachine(MachineDependencies{Inventory: func(context.Context) (Inventory, error) { return Inventory{}, nil }, Checkpoint: func(context.Context, Object) error { return nil }})
	id := plugins.Identity{PluginID: "AutoSignIn", InstanceID: "AutoSignIn", Version: Version}
	call := m.Bind(TrustedRun{Identity: id, Method: "invoke", Action: "service"})
	for _, test := range []struct {
		id      plugins.Identity
		cap     string
		payload string
	}{{id, ConfigureCapability, `{}`}, {id, RunCapability, `{"url":"https://evil.invalid"}`}, {plugins.Identity{PluginID: id.PluginID, InstanceID: "Other", Version: Version}, RunCapability, `{}`}, {id, RunCapability, `null`}} {
		if _, err := call(context.Background(), test.id, test.cap, json.RawMessage(test.payload)); err == nil {
			t.Fatal("forged host grant accepted")
		}
	}
	original := Inventory{Configured: []Object{{"id": 1, "name": "One", "cookie": "secret", "url": "private", "token": "secret"}}}
	public := publicInventory(original)
	want := Inventory{Configured: []Object{{"id": json.Number("1"), "name": "One"}}, Indexers: []Object{}, Custom: []Object{}}
	if !reflect.DeepEqual(public, want) || strings.Contains(string(mustJSON(public)), "secret") {
		t.Fatal("inventory exposed host-only fields", public)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := call(ctx, id, RunCapability, json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled grant entered machine", err)
	}
}

func TestMachineReloadStopsOldOnlyOnceSchedulerAndRebuildsSourceObject(t *testing.T) {
	var saved Object
	m, err := NewMachine(MachineDependencies{Inventory: func(context.Context) (Inventory, error) { return Inventory{}, nil }, Checkpoint: func(_ context.Context, s Object) error { saved = s; return nil }, Now: func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	id := plugins.Identity{PluginID: "AutoSignIn", InstanceID: "AutoSignIn", Version: Version}
	call := m.Bind(TrustedRun{Identity: id, Method: "invoke", Action: "init", Config: Object{"enabled": false, "onlyonce": true}})
	if _, err = call(context.Background(), id, ConfigureCapability, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !m.engine.SchemaLoaded || m.engine.Scheduler == nil || !m.engine.Scheduler.Running || Truth(m.engine.OnlyOnce) {
		t.Fatal("source onlyonce schedule semantics lost", saved)
	}
	call = m.Bind(TrustedRun{Identity: id, Method: "invoke", Action: "reload", Config: Object{"enabled": false}})
	if _, err = call(context.Background(), id, ConfigureCapability, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	effects := asList(saved["trace"])
	if len(effects) < 2 || asObject(effects[0])["op"] != "scheduler_remove" || asObject(effects[1])["op"] != "scheduler_stop" || m.engine.SchemaLoaded || m.engine.Scheduler != nil {
		t.Fatal("reload skipped old stop or reused old volatile object", effects)
	}
	// Checkpoint receives a serialization snapshot, not a pointer allowing the
	// store callback to mutate the live host instance after its lock is released.
	asObject(saved["config"])["enabled"] = "mutated-owned-snapshot"
	if m.engine.Config["enabled"] == "mutated-owned-snapshot" {
		t.Fatal("checkpoint alias corrupted host object")
	}
}

func TestRegisteredSitesProxyUsesTrustedGlobalOnlyWhenSourceFlagEnabled(t *testing.T) {
	var direct, proxied atomic.Int32
	handler := func(counter *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			io.WriteString(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
		})
	}
	siteServer := httptest.NewServer(handler(&direct))
	defer siteServer.Close()
	proxyServer := httptest.NewServer(handler(&proxied))
	defer proxyServer.Close()
	proxy, _ := url.Parse(proxyServer.URL)
	for _, flag := range []any{false, true} {
		sites := NewRegisteredSites([]Registration{{Site: Object{"id": 1, "name": "Synthetic", "url": siteServer.URL + "/", "cookie": "sid=synthetic-only", "proxy": flag}, ProxyURL: proxy}})
		sites.Stats = func(context.Context, string, bool, int) error { return nil }
		result, err := sites.Visit(context.Background(), Object{"id": 1}, "签到", true)
		if err != nil || !result.Success {
			t.Fatal(result, err)
		}
	}
	if direct.Load() != 1 || proxied.Load() != 1 {
		t.Fatal("global proxy used despite source flag", direct.Load(), proxied.Load())
	}
}
