package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func sourcePresenceFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("PLUGIN_SOURCE_DOWNLOADER_FIXTURE_BINARY"))
	value, ok := a.pluginRunningInstances.Load(m.PluginID)
	if !ok {
		t.Fatal("actual source worker not loaded")
	}
	return a, cookie, value.(*originalPluginInstance)
}
func sourcePresenceRead(t *testing.T, p *originalPluginInstance, id string) plugins.DownloaderSourceConnectionV1 {
	t.Helper()
	var out plugins.DownloaderSourceConnectionV1
	if err := hostDownloaderCall(t, p, "source_connection", plugins.DownloaderConnectionRequestV1{DownloaderID: id}, &out); err != nil {
		t.Fatal("source connection", err)
	}
	return out
}

func TestSourceConnectionActualSDKCachedAndNoSIDFullChain(t *testing.T) {
	for _, body := range []string{"", "Ok."} {
		t.Run("login="+body, func(t *testing.T) {
			a, _, object := sourcePresenceFixture(t)
			var auth, query, adds, cleanup atomic.Int32
			var offline atomic.Bool
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					auth.Add(1)
					if offline.Load() {
						http.Error(w, "owned offline", 503)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "custom-auth", Value: "owned", Path: "/"})
					io.WriteString(w, body)
				case "/api/v2/app/webapiVersion":
					if offline.Load() {
						http.Error(w, "offline", 503)
					} else {
						io.WriteString(w, "2.11.4")
					}
				case "/api/v2/torrents/info":
					query.Add(1)
					if offline.Load() {
						http.Error(w, "owned query offline", 503)
						return
					}
					cookie, err := r.Cookie("custom-auth")
					if err != nil || cookie.Value != "owned" {
						t.Error("source query lost custom login cookie")
					}
					io.WriteString(w, `[{"hash":"`+ownedDownloaderHash+`","tags":"owned-marker"}]`)
				case "/api/v2/torrents/add":
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/removeTags":
					w.WriteHeader(200)
				case "/api/v2/torrents/deleteTags":
					cleanup.Add(1)
					w.WriteHeader(200)
				default:
					t.Error("unexpected peer operation", r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer peer.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL, Username: "owned-user", Password: "owned-pass"}, Name: "Owned"}}
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			if auth.Load() != 0 {
				t.Fatal("uninitialized config save probed")
			}
			connection := sourcePresenceRead(t, object, "owned")
			if !connection.Exists || connection.Inactive || auth.Load() != 1 {
				t.Fatal("noSID source ctor fact", connection, auth.Load())
			}
			request := plugins.DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}}
			var tasks plugins.DownloaderSourceTasksResultV1
			if err := hostDownloaderCall(t, object, "source_tasks", request, &tasks); err != nil || tasks.NativeError || len(tasks.Tasks) != 1 || query.Load() != 1 {
				t.Fatal("ctor success did not reach actual source query", tasks, err, query.Load())
			}
			before := query.Load()
			if err := hostDownloaderCall(t, object, "legacy_tasks", request, nil); err == nil || query.Load() != before {
				t.Fatal("ordinary strict login changed", err, query.Load())
			}
			var add plugins.DownloaderSourceMutationResultV1
			if err := hostDownloaderCall(t, object, "source_add", plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned material"), Paused: true, Tags: []string{"owned-marker"}}}, &add); err != nil || !add.Accepted || add.Outcome != "confirmed" {
				t.Fatal("source add lost noSID login", add, err)
			}
			var found plugins.DownloaderFindByTagV1
			if err := hostDownloaderCall(t, object, "source_find", plugins.DownloaderFindByTagRequestV1{DownloaderID: "owned", Tags: []string{"owned-marker"}}, &found); err != nil || found.Hash == nil || *found.Hash != ownedDownloaderHash || adds.Load() != 1 || cleanup.Load() != 1 {
				t.Fatal("source lookup/cleanup bypassed or retried add", found, err, adds.Load(), cleanup.Load())
			}
			// The cached constructor remains present when its subsequent query fails.
			offline.Store(true)
			beforeAuth := auth.Load()
			for i := 0; i < 3; i++ {
				if got := sourcePresenceRead(t, object, "owned"); !got.Exists || got.Inactive {
					t.Fatal("offline fresh probe replaced cached fact", got)
				}
			}
			if auth.Load() != beforeAuth {
				t.Fatal("source getter sent fresh auth probe")
			}
			if err := hostDownloaderCall(t, object, "source_tasks", request, &tasks); err != nil || !tasks.NativeError || len(tasks.Tasks) != 0 {
				t.Fatal("cached-success source query error lost", tasks, err)
			}
			if got := sourcePresenceRead(t, object, "owned"); got.Inactive {
				t.Fatal("query error cleared successful constructor fact")
			}
			var fresh plugins.DownloaderConnectionV1
			if err := hostDownloaderCall(t, object, "legacy_connection", plugins.DownloaderConnectionRequestV1{DownloaderID: "owned"}, &fresh); err != nil || !fresh.Inactive {
				t.Fatal("ordinary connection stopped probing", fresh, err)
			}
			offline.Store(false)
			// Cancel after one actual legacy lookup login. Its strict auth must never
			// query/cleanup on this noSID peer. Native9x3 timing is separately regressed.
			before = query.Load()
			raw, _ := json.Marshal(plugins.DownloaderFindByTagRequestV1{DownloaderID: "owned", Tags: []string{"owned-marker"}})
			ctx, cancel := context.WithTimeout(context.Background(), 4200*time.Millisecond)
			defer cancel()
			_, err := object.callSession(ctx, plugins.Runner{Timeout: 5 * time.Second}, "legacy_find", raw, nil)
			if err == nil || query.Load() != before || cleanup.Load() != 1 || adds.Load() != 1 {
				t.Fatal("legacy Find strict/no-retry behavior changed", err, query.Load(), cleanup.Load(), adds.Load())
			}
		})
	}
}

func TestSourceConnectionLifecycleOrderReconnectSettingsCloneAndCold(t *testing.T) {
	a, cookie, object := sourcePresenceFixture(t)
	var mu sync.Mutex
	trace := []string{}
	var recovered atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
		mu.Lock()
		trace = append(trace, id)
		mu.Unlock()
		if id == "failed" && !recovered.Load() {
			http.Error(w, "owned initial auth failure", 401)
			return
		}
		if id == "tr" {
			var in map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&in)
			if string(in["method"]) != `"session-get"` {
				t.Error("TR constructor used a stats probe")
			}
			io.WriteString(w, `{"result":"success","arguments":{"version":"3.00","rpc-version":16}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/app/version") {
			t.Error("unexpected constructor request", r.URL.Path)
		}
		io.WriteString(w, "v5.1.2")
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "tr", Kind: "transmission", BaseURL: peer.URL + "/tr"}, Name: "shared"},
		{Config: download.Config{ID: "shadow", Kind: "qbittorrent", BaseURL: peer.URL + "/shadow", APIKey: "owned"}, Name: "shared"},
		{Config: download.Config{ID: "failed", Kind: "qbittorrent", BaseURL: peer.URL + "/failed", APIKey: "owned"}, Name: "other"},
		{Config: download.Config{ID: "winning", Kind: "qbittorrent", BaseURL: peer.URL + "/winning", APIKey: "owned"}, Name: "shared"},
	}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err := hostDownloaderCall(t, object, "configurations", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := len(trace)
	mu.Unlock()
	if n != 0 {
		t.Fatal("configuration read probed")
	}
	if got := sourcePresenceRead(t, object, "winning"); !got.Exists || got.Inactive {
		t.Fatal(got)
	}
	mu.Lock()
	initial := append([]string{}, trace...)
	mu.Unlock()
	if !reflect.DeepEqual(initial, []string{"winning", "failed", "tr"}) {
		t.Fatal("module kind/last-value first-slot order", initial)
	}
	if got := sourcePresenceRead(t, object, "shadow"); got.Exists || got.Inactive {
		t.Fatal("shadowed wrapper reported present", got)
	}
	if got := sourcePresenceRead(t, object, "failed"); !got.Exists || !got.Inactive {
		t.Fatal("failed native constructor not inactive", got)
	}
	// Actual source+clone reinitialization must retain shared native facts.
	response := callTest(t, a.Handler(), "POST", "/api/plugins/SourceDownloaderTest/clone", `{"suffix":"presence"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("SourceDownloaderTestpresence")
	if !ok {
		t.Fatal("clone missing")
	}
	clone := value.(*originalPluginInstance)
	if got := sourcePresenceRead(t, clone, "failed"); !got.Inactive {
		t.Fatal("clone changed shared fact")
	}
	if _, err := a.reloadOriginalPlugin(context.Background(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	mu.Lock()
	n = len(trace)
	mu.Unlock()
	if n != 3 {
		t.Fatal("TT reload/clone reconnected native modules", trace)
	}
	recovered.Store(true)
	a.initializeOriginalCommonService(time.Now())
	if err := a.runOriginalService(context.Background(), "", "scheduler_job", true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	afterCommon := append([]string{}, trace...)
	mu.Unlock()
	if !reflect.DeepEqual(afterCommon, []string{"winning", "failed", "tr", "failed"}) {
		t.Fatal("common retried successful constructor", afterCommon)
	}
	if got := sourcePresenceRead(t, clone, "failed"); got.Inactive {
		t.Fatal("common failed to reconnect inactive wrapper")
	}
	if err := a.runOriginalService(context.Background(), "", "scheduler_job", true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n = len(trace)
	mu.Unlock()
	if n != 4 {
		t.Fatal("common reprobed active clients")
	}
	settings = a.Settings()
	settings.Downloaders[0].Name = "renamed"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	afterSave := append([]string{}, trace...)
	mu.Unlock()
	if !reflect.DeepEqual(afterSave[4:], []string{"winning", "failed", "tr"}) {
		t.Fatal("settings did not rebuild all initialized modules", afterSave)
	}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n = len(trace)
	mu.Unlock()
	if n != 7 {
		t.Fatal("unchanged downloaders rebuilt")
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	// This is the actual App.Start lifecycle; main itself is separately tested.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cold.Start(ctx)
	mu.Lock()
	afterCold := append([]string{}, trace...)
	mu.Unlock()
	if !reflect.DeepEqual(afterCold[7:], []string{"winning", "failed", "tr"}) {
		t.Fatal("cold Start reused persisted presence or wrong order", afterCold)
	}
	cold.pluginOriginalSourceConnections.mu.Lock()
	initialized := cold.pluginOriginalSourceConnections.initialized
	cold.pluginOriginalSourceConnections.mu.Unlock()
	if !initialized {
		t.Fatal("Start did not initialize source modules")
	}
}

func TestSourceConnectionScopeAndCancellationNeverBecomeInactive(t *testing.T) {
	a, _, object := sourcePresenceFixture(t)
	var network atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { network.Add(1); io.WriteString(w, "Ok.") }))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, Name: "owned"}}
	a.cfgMu.Unlock()
	var checks atomic.Int32
	sentinel := errors.New("owned constructor transient scope denial")
	scope := originalBaseCallScope{installation: object.installation, object: object, validate: func(context.Context) error {
		if checks.Add(1) == 5 {
			return sentinel
		}
		return nil
	}}
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	raw := json.RawMessage(`{"downloader_id":"owned"}`)
	if out, err := a.pluginOriginalResourceHost(context.Background(), scope, identity, plugins.DownloaderSourceConnectionCapability, raw); len(out) != 0 || !errors.Is(err, sentinel) {
		t.Fatal("transient constructor guard became inactive fact", string(out), err, checks.Load())
	}
	a.pluginOriginalSourceConnections.mu.Lock()
	initialized := a.pluginOriginalSourceConnections.initialized
	a.pluginOriginalSourceConnections.mu.Unlock()
	if initialized || network.Load() != 0 {
		t.Fatal("denied constructor cached or dispatched")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.pluginOriginalResourceHost(ctx, scope, identity, plugins.DownloaderSourceConnectionCapability, raw); !errors.Is(err, context.Canceled) {
		t.Fatal("constructor cancellation became success", err)
	}
}

func TestSourceConnectionCommonRespectsPluginModuleTerminal(t *testing.T) {
	a, cookie, provider := ownedModuleFixture(t, true)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("PLUGIN_SOURCE_DOWNLOADER_FIXTURE_BINARY"))
	value, _ := a.pluginRunningInstances.Load(manifest.PluginID)
	object := value.(*originalPluginInstance)
	var calls atomic.Int32
	var recovered atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !recovered.Load() {
			http.Error(w, "owned initial failure", 401)
		} else {
			io.WriteString(w, "v5.1.2")
		}
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL, APIKey: "owned"}, Name: "Owned"}}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if got := sourcePresenceRead(t, object, "owned"); !got.Inactive || calls.Load() != 1 {
		t.Fatal("initial failure fact", got, calls.Load())
	}
	recovered.Store(true)
	a.initializeOriginalCommonService(time.Now())
	saveOwnedModule(t, a, cookie, provider, `{"enabled":true,"modules":{"scheduler_job":"module_execute"},"result":false}`)
	if err := a.runOriginalService(context.Background(), "", "scheduler_job", true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("terminal plugin False did not short-circuit native reconnect")
	}
	saveOwnedModule(t, a, cookie, provider, `{"enabled":true,"modules":{"scheduler_job":"module_execute"},"result":[]}`)
	if err := a.runOriginalService(context.Background(), "", "scheduler_job", true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("plugin list did not continue to native fallback")
	}
	if got := sourcePresenceRead(t, object, "owned"); got.Inactive {
		t.Fatal("native fallback failed to reconnect")
	}
}

func TestSourceConnectionTR500ConstructorQueryAndMutationChain(t *testing.T) {
	for _, mode := range []string{"confirmed", "unknown", "confirmed_revoked", "status404", "status502", "wrong-tag"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := sourcePresenceFixture(t)
			status := 500
			if mode == "status404" {
				status = 404
			}
			if mode == "status502" {
				status = 502
			}
			var queries, writes atomic.Int32
			var allowQuery atomic.Bool
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Method    string
					Arguments json.RawMessage
					Tag       *int
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					t.Error("bad TR request")
					return
				}
				if in.Method == "session_get" {
					http.Error(w, "ordinary modern probe strict", 500)
					return
				}
				switch in.Method {
				case "session-get":
					w.WriteHeader(status)
					io.WriteString(w, `{"result":"success","arguments":{"version":"3.00","rpc-version":16}}`)
				case "torrent-get":
					queries.Add(1)
					if !allowQuery.Load() {
						t.Error("unexpected query")
					}
					w.WriteHeader(status)
					tag := ""
					if mode == "wrong-tag" {
						tag = `,"tag":999`
					}
					io.WriteString(w, `{"result":"success"`+tag+`,"arguments":{"torrents":[{"hashString":"`+ownedDownloaderHash+`","status":6}]}}`)
				case "torrent-add":
					writes.Add(1)
					if mode == "confirmed_revoked" {
						a.cfgMu.Lock()
						enabled := false
						a.config.Downloaders[0].Enabled = &enabled
						a.cfgMu.Unlock()
					}
					if mode == "unknown" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
						return
					}
					w.WriteHeader(status)
					io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"hashString":"`+ownedDownloaderHash+`","id":1}}}`)
				default:
					t.Error("unexpected TR operation", in.Method)
				}
			}))
			defer peer.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: peer.URL}, Name: "Owned"}}
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			if got := sourcePresenceRead(t, object, "owned"); !got.Exists || got.Inactive {
				t.Fatal("actual TR500 ctor was rejected", got)
			}
			allowQuery.Store(true)
			request := plugins.DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}}
			var rows plugins.DownloaderSourceTasksResultV1
			if err := hostDownloaderCall(t, object, "source_tasks", request, &rows); err != nil || rows.NativeError || len(rows.Tasks) != 1 || queries.Load() != 1 {
				t.Fatal("TR500 constructor success did not reach source query rows", rows, err, queries.Load())
			}
			if err := hostDownloaderCall(t, object, "legacy_tasks", request, nil); err == nil || queries.Load() != 1 {
				t.Fatal("ordinary TR strict HTTP contract changed", err, queries.Load())
			}
			var out plugins.DownloaderSourceMutationResultV1
			if err := hostDownloaderCall(t, object, "source_add", plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Paused: true}}, &out); err != nil {
				t.Fatal("source TR receipt lost", err)
			}
			unknown := mode == "unknown"
			outcome := "confirmed"
			if unknown {
				outcome = "unknown"
			}
			code := ""
			if mode == "confirmed_revoked" {
				code = "CALL_CHANGED_AFTER_MUTATION"
			}
			if out.Accepted == unknown || out.NativeError != unknown || out.Outcome != outcome || out.FailureCode != code || writes.Load() != 1 {
				t.Fatal("SourceTR500 altered confirmed/unknown or retried mutation", out, writes.Load())
			}
		})
	}
}

func TestSourceConnectionFindActualSDKIndependentCleanupFailures(t *testing.T) {
	for _, mode := range []string{"500", "EOF", "204", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := sourcePresenceFixture(t)
			var remove, global atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/info":
					if mode == "revoked" {
						a.cfgMu.Lock()
						disabled := false
						a.config.Downloaders[0].Enabled = &disabled
						a.cfgMu.Unlock()
					}
					io.WriteString(w, `[{"hash":"`+ownedDownloaderHash+`","tags":"marker"}]`)
				case "/api/v2/torrents/removeTags":
					remove.Add(1)
					if mode == "500" {
						http.Error(w, "owned failure", 500)
						return
					}
					if mode == "EOF" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					w.WriteHeader(204)
				case "/api/v2/torrents/deleteTags":
					global.Add(1)
					w.WriteHeader(204)
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer peer.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, Name: "Owned"}}
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			var out plugins.DownloaderFindByTagV1
			err := hostDownloaderCall(t, object, "source_find", plugins.DownloaderFindByTagRequestV1{DownloaderID: "owned", Tags: []string{"marker"}}, &out)
			if mode == "revoked" {
				if err == nil || remove.Load() != 0 || global.Load() != 0 {
					t.Fatal("revoked owner reached cleanup", err, remove.Load(), global.Load())
				}
				return
			}
			if err != nil || out.Hash == nil || *out.Hash != ownedDownloaderHash || remove.Load() != 1 || global.Load() != 1 {
				t.Fatal("source cleanup sequence or observed hash changed", out, err, remove.Load(), global.Load())
			}
		})
	}
}

func TestSourceConnectionActualSDKActiveAReadableDuringBReconnect(t *testing.T) {
	a, _, object := sourcePresenceFixture(t)
	var aCalls, bCalls atomic.Int32
	var reconnect atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/a/") {
			aCalls.Add(1)
			io.WriteString(w, "v5.1.2")
			return
		}
		bCalls.Add(1)
		if !reconnect.Load() {
			http.Error(w, "owned initial B failure", 401)
			return
		}
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "v5.1.2")
	}))
	defer peer.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "a", Kind: "qbittorrent", BaseURL: peer.URL + "/a", APIKey: "owned"}, Name: "A"}, {Config: download.Config{ID: "b", Kind: "qbittorrent", BaseURL: peer.URL + "/b", APIKey: "owned"}, Name: "B"}}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if got := sourcePresenceRead(t, object, "a"); got.Inactive {
		t.Fatal(got)
	}
	reconnect.Store(true)
	a.initializeOriginalCommonService(time.Now())
	commonDone := make(chan error, 1)
	go func() { commonDone <- a.runOriginalService(context.Background(), "", "scheduler_job", true) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("B real auth reconnect was not reached")
	}
	result := make(chan error, 1)
	go func() {
		raw := json.RawMessage(`{"downloader_id":"a"}`)
		value, err := object.callSession(context.Background(), plugins.Runner{Timeout: 2 * time.Second}, "source_connection", raw, nil)
		if err == nil {
			var out plugins.DownloaderSourceConnectionV1
			err = json.Unmarshal(value.Result, &out)
			if err == nil && (!out.Exists || out.Inactive) {
				err = errors.New("cached A changed")
			}
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("healthy A blocked behind B auth")
	}
	if aCalls.Load() != 1 || bCalls.Load() != 2 {
		t.Fatal("healthy getter probed or B duplicate", aCalls.Load(), bCalls.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	if release, err := a.sourceConnectionProbeGate(ctx); err == nil {
		release()
		t.Fatal("second initializer entered occupied gate")
	} else if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("gate wait ignored caller cancellation", err)
	}
	close(release)
	select {
	case err := <-commonDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("B did not complete")
	}
}
