package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/remoteidentifiers"
)

func identifiersSaveRaw(t *testing.T, a *App, cookie *http.Cookie, raw string) {
	t.Helper()
	stored, err := a.pluginStore.Config(context.Background(), "RemoteIdentifiers")
	if err != nil && !missingPluginValue(err) {
		t.Fatal(err)
	}
	request := fmt.Sprintf(`{"revision":%d,"config":%s}`, stored.Revision, raw)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/RemoteIdentifiers/config", request, cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
}
func TestRemoteIdentifiersInstalledASTOracleStateAndForm(t *testing.T) {
	raw, err := os.ReadFile("testdata/remote-identifiers-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		SourceSHA map[string]string `json:"source_sha256"`
		State     []struct {
			Name   string
			Config json.RawMessage
			State  json.RawMessage
			Saved  json.RawMessage
		}
		Form   json.RawMessage
		Denied int `json:"denied_network_calls"`
		Writes int `json:"denied_writes"`
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.SourceSHA["remote"] != "14ec7f5f2964ebd1674305d5294ffa56fb61e2cd1021ea31e50d9675813891a4" || oracle.Denied != 0 || oracle.Writes != 0 {
		t.Fatal("untrusted oracle")
	}
	warm := remoteidentifiers.Cold()
	for _, tc := range oracle.State {
		t.Run(tc.Name, func(t *testing.T) {
			state := remoteidentifiers.Cold()
			if strings.HasPrefix(tc.Name, "warm_") {
				state = warm
				tc.Config = json.RawMessage(`{}`)
				if tc.Name == "warm_none" {
					tc.Config = json.RawMessage(`null`)
				}
			}
			if err := state.Init(tc.Config); err != nil {
				t.Fatal(err)
			}
			if remoteidentifiers.Truth(state.OnlyOnce) {
				state.OnlyOnce = false
			}
			got, _ := json.Marshal(state)
			var a, b any
			json.Unmarshal(got, &a)
			json.Unmarshal(tc.State, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("state differs: %s", tc.Name)
			}
			if tc.Name == "all" {
				warm = state
			}
		})
	}
	form, err := builtin.HandleRemoteIdentifiers(context.Background(), "config_form", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	json.Unmarshal(form, &a)
	json.Unmarshal(oracle.Form, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("source form differs")
	}
}
func TestRemoteIdentifiersSourceNetworkWireAndFreshTLSCookies(t *testing.T) {
	var n atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Header.Get("Cookie") != "" {
			t.Error("fresh RequestUtils session leaked a previous cookie")
		}
		http.SetCookie(w, &http.Cookie{Name: "owned", Value: "test"})
		if strings.Contains(r.URL.Path, "github") {
			if r.UserAgent() != "MoviePilot/v2.15.6" || r.Header.Get("Accept") != "*/*" || r.Header.Get("Content-Type") != "" {
				t.Error("source github defaults differ")
			}
		} else {
			if r.UserAgent() != "python-urllib3/2.7.0" || r.Header.Get("Accept") != "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded; charset=UTF-8" {
				t.Error("source None header deletion differs")
			}
		}
		fmt.Fprint(w, "A => B")
	}))
	defer server.Close()
	settings, _ := identifiersSyntheticNetwork(context.Background())
	settings.GitHubHeaders = map[string]string{"User-Agent": "MoviePilot/v2.15.6"}
	f := identifiersFetcher{base: identifiersTransport(), settings: func(context.Context) (identifiersNetworkSettings, error) { return settings, nil }}
	t.Setenv("https_proxy", "http://127.0.0.1:1")
	for _, path := range []string{"/plain", "/github", "/GitHub", "/plain"} {
		if _, err := f.text(context.Background(), server.URL+path); err != nil {
			t.Fatal(err)
		}
	}
	if n.Load() != 4 {
		t.Fatal(n.Load())
	}
	// A separate normal client retains normal TLS verification.
	if response, err := http.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("Remote verify=False changed global TLS")
	}
}
func TestRemoteIdentifiersSourceProxyAndDependencyBoundaries(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Host != "owned.invalid" {
			t.Error("proxy request target differs")
		}
		fmt.Fprint(w, "A => B")
	}))
	defer proxy.Close()
	settings, _ := identifiersSyntheticNetwork(context.Background())
	settings.Environment = []string{"http_proxy=http://127.0.0.1:1", "no_proxy=*"}
	settings.Proxy = map[string]string{"http": proxy.URL, "https": proxy.URL}
	f := identifiersFetcher{base: identifiersTransport(), settings: func(context.Context) (identifiersNetworkSettings, error) { return settings, nil }}
	if _, err := f.text(context.Background(), "http://owned.invalid/plain"); err != nil || calls.Load() != 1 {
		t.Fatal("explicit proxy did not override env/bypass", calls.Load(), err)
	}
	settings.Proxy = nil
	settings.Environment = []string{"http_proxy=" + proxy.URL}
	if _, err := f.text(context.Background(), "http://owned.invalid/plain"); err != nil || calls.Load() != 2 {
		t.Fatal("None did not use selected source env", calls.Load(), err)
	}
	settings.Proxy = map[string]string{}
	if _, err := f.text(context.Background(), "http://owned.invalid/plain"); pluginErrorCodeInternal(err) != "IDENTIFIERS_SOURCE_DEPENDENCY" || calls.Load() != 2 {
		t.Fatal("unsupported source derivation silently rerouted", err)
	}
	a := newTestApp(t)
	if _, err := a.identifiersSourceNetwork(context.Background()); pluginErrorCodeInternal(err) != "IDENTIFIERS_SOURCE_DEPENDENCY" {
		t.Fatal(err)
	}
	identifiersSeedSource(t, a)
	if _, err := a.identifiersSourceNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"Version":1,"Known":true,"GitHubHeaders":{},"Proxy":null}`, `{"Version":1,"Known":true,"GitHubHeaders":{},"Proxy":null,"Environment":null}`, `{"Version":1,"Known":true,"GitHubHeaders":{"X":"\ud800"},"Proxy":null,"Environment":[]}`} {
		sealed, _ := a.pluginStore.seal("source_network_settings", "system", "main", []byte(raw))
		a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='source_network_settings' AND id='main'`, sealed)
		if _, err := a.identifiersSourceNetwork(context.Background()); pluginErrorCodeInternal(err) != "IDENTIFIERS_SOURCE_DEPENDENCY" {
			t.Fatal("missing/raw-surrogate source silently replaced", err)
		}
	}
	for _, raw := range []string{`"\ud800"`, `"\udc00"`, `"a\ud800x"`} {
		if !identifiersSurrogate([]byte(raw)) {
			t.Fatal("isolated surrogate not detected")
		}
	}
	if identifiersSurrogate([]byte(`"\ud83d\ude00\\ud800"`)) {
		t.Fatal("valid pair/literal escape rejected")
	}
}
func TestRemoteIdentifiersActualSaveEmptyWarmColdAndOnceReset(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	identifiersExecuteNext(t, a)
	stored, _ := a.pluginStore.Config(ctx, "RemoteIdentifiers")
	if string(stored.Value) != "{}" {
		t.Fatal("form defaults became runtime defaults")
	}
	identifiersSaveRaw(t, a, cookie, `{"enable":true,"flitter":true,"cron":"0 0 * * *","file_urls":"","unknown":"owned"}`)
	identifiersExecuteNext(t, a)
	identifiersSaveRaw(t, a, cookie, `{}`)
	// Viewing the card between save and init cannot reset the warm class cache.
	cards, err := a.pluginCards(ctx, false)
	if err != nil || len(cards) != 1 || !cards[0].State {
		t.Fatal("get_state before init lost warm class", err)
	}
	identifiersExecuteNext(t, a)
	cards, _ = a.pluginCards(ctx, false)
	if !cards[0].State {
		t.Fatal("init({}) reset warm enable")
	}
	stored, _ = a.pluginStore.Config(ctx, "RemoteIdentifiers")
	if string(stored.Value) != "{}" {
		t.Fatal("empty source config normalized on save")
	}
	// Reset just the process cache as a pure cold class proof, then reopen actual App.
	directory := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	useBuiltInPluginProcess(t, cold)
	if err := cold.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	identifiersExecuteNext(t, cold)
	cards, err = cold.pluginCards(ctx, false)
	if err != nil || len(cards) != 1 || cards[0].State {
		t.Fatal("cold class did not restore source defaults", err)
	}
	identifiersSaveRaw(t, cold, cookie, `{"onlyonce":true,"unknown":"removed"}`)
	identifiersExecuteNext(t, cold)
	stored, _ = cold.pluginStore.Config(ctx, "RemoteIdentifiers")
	var fields map[string]any
	json.Unmarshal(stored.Value, &fields)
	if len(fields) != 5 || fields["onlyonce"] != false || fields["flitter"] != false || fields["cron"] != builtin.RemoteIdentifiersCron {
		t.Fatal("source full config normalization differs")
	}
	due := identifiersDue(t, cold)
	if count, err := cold.pluginStore.enqueueDuePluginSchedules(ctx, due); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	identifiersExecuteNext(t, cold)
	_, rules, err := cold.recognitionWords(ctx)
	if err != nil || !reflect.DeepEqual(rules, []string{remoteIdentifiersMarker}) {
		t.Fatal("empty URL source should publish marker with no network", err)
	}
	identifiersSaveRaw(t, cold, cookie, `{"enable":false}`)
	identifiersExecuteNext(t, cold)
	var active int
	cold.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id='RemoteIdentifiers' AND active=1`).Scan(&active)
	if active != 0 {
		t.Fatal("disable retained services", active)
	}
	if r := callTest(t, cold.Handler(), "POST", "/api/plugins/RemoteIdentifiers/uninstall", `{}`, cookie, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	_, after, err := cold.recognitionWords(ctx)
	if err != nil || !reflect.DeepEqual(rules, after) {
		t.Fatal("uninstall removed already applied global words")
	}
}
func TestRemoteIdentifiersAbsentImportExactAndColdNoEffects(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	manifest, _ := json.Marshal(builtin.RemoteIdentifiersManifest())
	installation := PluginInstallation{PluginID: "RemoteIdentifiers", InstanceID: "RemoteIdentifiers", Version: "2.4.1", ManifestSchemaVersion: 1, Manifest: manifest, DesiredEnabled: true}
	plan, err := a.pluginStore.BeginMigration(ctx, strings.Repeat("a", 64), "RemoteIdentifiers", "RemoteIdentifiers")
	if err != nil {
		t.Fatal(err)
	}
	in := PluginImport{RunID: plan.ID, Installation: installation, SourceConfigAbsent: true}
	receipt, err := a.pluginStore.Import(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := a.pluginStore.Import(ctx, in); err != nil || receipt != repeated {
		t.Fatal("absence replay differs", err)
	}
	if _, err := a.pluginStore.Config(ctx, "RemoteIdentifiers"); !errorsIsNoRows(err) {
		t.Fatal("absent source became a config row", err)
	}
	if err := a.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT count(*) FROM plugin_values`, `SELECT count(*) FROM plugin_runs`, `SELECT count(*) FROM plugin_schedules`, `SELECT count(*) FROM records WHERE kind='recognition_words'`} {
		var count int
		if err := a.Store.DB.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal("disabled absence import had effects", count, err)
		}
	}
	cookie := setupTestAdmin(t, a)
	form := callTest(t, a.Handler(), "GET", "/api/plugins/RemoteIdentifiers/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), builtin.RemoteIdentifiersCron) {
		t.Fatal(form.Body.String())
	}
	if _, err := a.pluginStore.Config(ctx, "RemoteIdentifiers"); !errorsIsNoRows(err) {
		t.Fatal("opening form persisted UI defaults")
	}
	in.Installation.PluginID = "SystemProcess"
	if _, err := a.pluginStore.Import(ctx, in); pluginErrorCodeInternal(err) != "INVALID_PLUGIN_MIGRATION" {
		t.Fatal("absence marker accepted another plugin", err)
	}
	in.Installation = installation
	in.Config = json.RawMessage(`{}`)
	if _, err := a.pluginStore.Import(ctx, in); pluginErrorCodeInternal(err) != "INVALID_PLUGIN_MIGRATION" {
		t.Fatal("absence marker accepted real config", err)
	}
	directory := a.Dir
	a.Close()
	cold, err := New(Options{DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err := cold.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	var runs int
	cold.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs`).Scan(&runs)
	if runs != 0 {
		t.Fatal("cold import ran")
	}
}
func errorsIsNoRows(err error) bool { return err == sql.ErrNoRows }
func TestRemoteIdentifiersConnectReadTimeoutNotTotal(t *testing.T) {
	if testing.Short() {
		t.Skip("owned 16s source timeout proof")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := w.(http.Flusher)
		fmt.Fprint(w, "A => ")
		f.Flush()
		time.Sleep(8 * time.Second)
		fmt.Fprint(w, "B")
		f.Flush()
		time.Sleep(8 * time.Second)
		fmt.Fprint(w, "\n")
	}))
	defer server.Close()
	f := identifiersFetcher{base: identifiersTransport(), settings: identifiersSyntheticNetwork}
	start := time.Now()
	text, err := f.text(context.Background(), server.URL)
	if err != nil || text != "A => B\n" || time.Since(start) < 15*time.Second {
		t.Fatal("used a total 15s timeout", err)
	}
}
func TestRemoteIdentifiersDuplicateFinalValueAndPythonStrip(t *testing.T) {
	urls, err := identifiersMapping(`{"a":42,"b":" \u001c http://owned.test/b \u001f ","a":"http://owned.test/a"}`)
	if err != nil || !reflect.DeepEqual(urls, []string{"http://owned.test/a", "http://owned.test/b"}) {
		t.Fatal("mapping validated overwritten duplicate or wrong strip", urls, err)
	}
}

func TestRemoteIdentifiersActualSDKDoesNotHoldGlobalLockOrPublishStaleConfig(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, "REMOTE => new")
	}))
	defer server.Close()
	callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, "")
	identifiersExecuteNext(t, a)
	if err := a.saveRecognitionWords(ctx, 0, []string{"USER => original"}); err != nil {
		t.Fatal(err)
	}
	identifiersConfigure(t, a, cookie, 1, builtin.RemoteIdentifiersConfiguration{OnlyOnce: true, FileURLs: server.URL})
	identifiersExecuteNext(t, a)
	due := identifiersDue(t, a)
	a.pluginStore.enqueueDuePluginSchedules(ctx, due)
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	running, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); a.executePluginRun(running, run) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SDK did not reach owned endpoint")
	}
	if !a.pluginMu.TryLock() {
		t.Fatal("Remote GET retained global plugin lock")
	}
	a.pluginMu.Unlock()
	stored, _ := a.pluginStore.Config(ctx, "RemoteIdentifiers")
	if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, "RemoteIdentifiers", stored.Revision, json.RawMessage(`{"enable":false}`)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("parent cancellation did not join")
	}
	_, words, err := a.recognitionWords(ctx)
	if err != nil || !reflect.DeepEqual(words, []string{"USER => original"}) {
		t.Fatal("stale/canceled task published", err)
	}
	var receipt int
	a.Store.DB.QueryRow(`SELECT count(*) FROM recognition_word_updates WHERE run_id=?`, run.ID).Scan(&receipt)
	if receipt != 0 {
		t.Fatal("stale receipt")
	}
	var processes int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE id=? AND state='running'`, run.ID).Scan(&processes); err != nil || processes != 0 {
		t.Fatal("run never finished", err)
	}
}
func TestRemoteIdentifiersOnceNormalizationPrecedesInvalidCron(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, "")
	identifiersExecuteNext(t, a)
	identifiersSaveRaw(t, a, cookie, `{"enable":true,"onlyonce":true,"cron":"bad","unknown":5}`)
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	finished, _ := a.pluginStore.Run(ctx, run.ID)
	if finished.State != "failed" || finished.ErrorCode != "INVALID_PLUGIN_SCHEDULE" {
		t.Fatal("cron failure not preserved", finished.State, finished.ErrorCode)
	}
	stored, _ := a.pluginStore.Config(ctx, "RemoteIdentifiers")
	var c map[string]any
	json.Unmarshal(stored.Value, &c)
	if len(c) != 5 || c["onlyonce"] != false || c["cron"] != "bad" {
		t.Fatal("source updated own config before get_service cron error")
	}
	due := identifiersDue(t, a)
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, due); err != nil || count != 1 {
		t.Fatal("source already started once was removed by later cron failure", count, err)
	}
	identifiersExecuteNext(t, a)
}

func TestRemoteIdentifiersPrivateAndPublicTimezoneSources(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	identifiersSeedSource(t, a)
	raw := json.RawMessage(`{"Version":1,"Known":true,"Timezone":"UTC","LocalTimezone":"Asia/Shanghai"}`)
	sealed, _ := a.pluginStore.seal("source_runtime_settings", "system", "main", raw)
	if _, err := a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='source_runtime_settings' AND id='main'`, sealed); err != nil {
		t.Fatal(err)
	}
	callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, "")
	identifiersExecuteNext(t, a)
	identifiersSaveRaw(t, a, cookie, `{"enable":true,"onlyonce":true}`)
	identifiersExecuteNext(t, a)
	for id, want := range map[string]string{"RemoteIdentifiers.once": "UTC", "RemoteIdentifiers": "Asia/Shanghai"} {
		var sealed []byte
		if err := a.Store.DB.QueryRow(`SELECT definition FROM plugin_schedules WHERE instance_id='RemoteIdentifiers' AND service_id=? AND active=1`, id).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		clear, err := a.pluginStore.unseal("schedule", "RemoteIdentifiers", id, sealed)
		if err != nil {
			t.Fatal(err)
		}
		var d pluginScheduleDefinition
		if json.Unmarshal(clear, &d) != nil || d.Trigger.Timezone != want {
			t.Fatal("timezone sources were conflated", id)
		}
	}
	// A malformed private timezone fails scheduler construction before the source
	// clears its class onlyonce field. Do not repair it using the local timezone.
	raw = json.RawMessage(`{"Version":1,"Known":true,"Timezone":"Invalid/Owned","LocalTimezone":"Asia/Shanghai"}`)
	sealed, _ = a.pluginStore.seal("source_runtime_settings", "system", "main", raw)
	a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='source_runtime_settings' AND id='main'`, sealed)
	identifiersSaveRaw(t, a, cookie, `{"onlyonce":true}`)
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	installed, _ := a.pluginStore.Installation(ctx, "RemoteIdentifiers")
	stored, _ := a.pluginStore.Config(ctx, "RemoteIdentifiers")
	state, err := a.identifiersClassState(installed, stored.Revision, stored.Value, false)
	if err != nil || !remoteidentifiers.Truth(state.OnlyOnce) {
		t.Fatal("invalid timezone cleared source class before construction", err)
	}
	var c map[string]any
	json.Unmarshal(stored.Value, &c)
	if c["onlyonce"] != true {
		t.Fatal("invalid timezone silently normalized")
	}
}

func TestRemoteIdentifiersPythonNonfiniteDuplicateAndIntegerLimit(t *testing.T) {
	for _, raw := range []string{`{"a":NaN,"a":"http://owned.test/a"}`, `{"a":Infinity,"a":"http://owned.test/a"}`, `{"a":-Infinity,"a":"http://owned.test/a"}`, `{"a":[NaN,Infinity,-Infinity,{"deep":NaN}],"a":"http://owned.test/a"}`} {
		got, err := identifiersMapping(raw)
		if err != nil || !reflect.DeepEqual(got, []string{"http://owned.test/a"}) {
			t.Fatal("valid source last duplicate rejected", err)
		}
	}
	for _, raw := range []string{`{"a":NaN}`, `{"a":Infinity}`, `{"a":-Infinity}`, `{"a":NaNfalse,"a":"http://owned.test/a"}`, `{"a":` + strings.Repeat("1", 4301) + `,"a":"http://owned.test/a"}`} {
		if _, err := identifiersMapping(raw); err == nil {
			t.Fatal("source invalid final value/integer/JSON was accepted")
		}
	}
	got, err := identifiersMapping(`{"NaN":"http://owned.test/NaN","Infinity":"http://owned.test/Infinity","-Infinity":"http://owned.test/-Infinity"}`)
	if err != nil || !reflect.DeepEqual(got, []string{"http://owned.test/NaN", "http://owned.test/Infinity", "http://owned.test/-Infinity"}) {
		t.Fatal("quoted literals were rewritten", err)
	}
	raw, err := os.ReadFile("testdata/remote-identifiers-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Max         int `json:"int_max_str_digits"`
		Collections []struct {
			Name   string
			Result []string
			Error  any
			Calls  []string
			Delays []int
		}
		Denied int `json:"denied_network_calls"`
		Writes int `json:"denied_writes"`
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.Max != 4300 || oracle.Denied != 0 || oracle.Writes != 0 {
		t.Fatal("source integer/IO oracle changed")
	}
	for _, c := range oracle.Collections {
		switch c.Name {
		case "nonfinite_duplicate":
			if c.Error != nil || len(c.Result) != 4 || len(c.Calls) != 4 {
				t.Fatal("actual source duplicate oracle failed")
			}
		case "nonfinite_nested_duplicate":
			if c.Error != nil || len(c.Result) != 2 || len(c.Calls) != 2 {
				t.Fatal("actual source nested oracle failed")
			}
		case "nonfinite_final", "large_int_duplicate":
			if c.Error == nil || len(c.Calls) != 3 || !reflect.DeepEqual(c.Delays, []int{5, 10}) {
				t.Fatal("actual source rejection oracle changed")
			}
		}
	}
}

func TestRemoteIdentifiersURLBasicAuthSourceEncodingAndOrder(t *testing.T) {
	var expected atomic.Value
	expected.Store("")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != expected.Load().(string) {
			t.Error("source URL basic auth encoding/override differs")
		}
		fmt.Fprint(w, "A => B")
	}))
	defer server.Close()
	settings, _ := identifiersSyntheticNetwork(context.Background())
	settings.GitHubHeaders = map[string]string{"Authorization": "Bearer owned-source-token"}
	f := identifiersFetcher{base: identifiersTransport(), settings: func(context.Context) (identifiersNetworkSettings, error) { return settings, nil }}
	cases := []struct {
		Userinfo, Path string
		Bytes          []byte
	}{
		{"owned-user:owned-pass", "/github", []byte("owned-user:owned-pass")},
		{"%C3%A9:p%C3%A4ss", "/plain", []byte{0xe9, ':', 'p', 0xe4, 's', 's'}},
		{":", "/plain", nil}, {":owned", "/plain", []byte(":owned")}, {"owned", "/plain", nil}, {"a%25b:p", "/plain", []byte("a%b:p")},
	}
	for _, c := range cases {
		want := ""
		if c.Bytes != nil {
			want = "Basic " + base64.StdEncoding.EncodeToString(c.Bytes)
		}
		expected.Store(want)
		raw := strings.Replace(server.URL, "://", "://"+c.Userinfo+"@", 1) + c.Path
		if _, err := f.text(context.Background(), raw); err != nil {
			t.Fatal("owned source basic auth failed", err)
		}
	}
	before := calls.Load()
	raw := strings.Replace(server.URL, "://", "://%E4%B8%AD:p@", 1) + "/plain"
	if _, err := f.text(context.Background(), raw); err == nil || calls.Load() != before {
		t.Fatal("source non-Latin1 credentials were silently rewritten/sent")
	}
}
