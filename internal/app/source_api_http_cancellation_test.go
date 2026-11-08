package app

// Expectations are from source06d39/ee7d/a19, not from the new Go select.
// HTTP-context cancellation here models expiry of an already-admitted waiter.
// It is not a claim that a disconnected TCP caller can receive an HTTP body.
import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/plugins"
	"light-media/internal/sites"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type bridgeGate struct {
	entered, release, canceled, responded chan struct{}
	once                                  sync.Once
	calls                                 atomic.Int64
}

func newBridgeGate() *bridgeGate {
	return &bridgeGate{entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{}, 1), responded: make(chan struct{}, 1)}
}
func (g *bridgeGate) unblock() { g.once.Do(func() { close(g.release) }) }
func bridgeHTTPPeer(t *testing.T) (*httptest.Server, *bridgeGate, *bridgeGate) {
	t.Helper()
	root, child := newBridgeGate(), newBridgeGate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := root
		if r.URL.Path == "/child" {
			g = child
		}
		if g.calls.Add(1) == 1 {
			close(g.entered)
			select {
			case <-g.release:
			case <-r.Context().Done():
				g.canceled <- struct{}{}
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
		if err == nil {
			select {
			case g.responded <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(func() { root.unblock(); child.unblock(); server.Close() })
	return server, root, child
}
func bridgeEntered(t *testing.T, gate *bridgeGate) {
	t.Helper()
	select {
	case <-gate.entered:
	case <-time.After(8 * time.Second):
		t.Fatal("real worker did not reach owned HTTP")
	}
}
func bridgeNotCanceled(t *testing.T, gate *bridgeGate) {
	t.Helper()
	select {
	case <-gate.canceled:
		t.Fatal("waiter cancellation reached physical admitted API/child HTTP")
	default:
	}
}
func bridgePlain500(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != 500 || response.Body.String() != "Internal Server Error" || response.Body.Len() != 21 || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || response.Header().Get("Connection") != "close" {
		t.Fatal("source H11 500 body/header contract", response.Code, response.Header(), response.Body.String())
	}
}
func bridgeInvoke(a *App, address string, ctx context.Context) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest("GET", address, nil).WithContext(ctx)
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, request)
		done <- response
	}()
	return done
}
func bridgeWaitResponse(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-done:
		return response
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP waiter did not return independently of held physical method")
		return nil
	}
}
func bridgeData(t *testing.T, a *App, id, key string) json.RawMessage {
	t.Helper()
	raw, err := a.pluginStore.originalBaseGetData(context.Background(), id, &key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func bridgeSaved(t *testing.T, a *App, id, key string) {
	t.Helper()
	var value struct {
		PID  int    `json:"pid"`
		Post string `json:"post_error"`
	}
	raw := bridgeData(t, a, id, key)
	if json.Unmarshal(raw, &value) != nil || value.PID <= 0 || value.Post != "" {
		t.Fatal("actual owned side effect missing/failed", key, string(raw))
	}
}

func TestSourceAPIHTTPCancellationRealASI(t *testing.T) {
	if os.Getenv("API_HTTP_BRIDGE_ASI") == "" {
		t.Skip("opt-in actual SDK fixture: API_HTTP_BRIDGE_ASI required")
	}
	server, gate, _ := bridgeHTTPPeer(t)
	defer gate.unblock()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.APIToken = "owned-api-bridge-synthetic-key"
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-api-cancel", Name: "Owned API", Kind: "ubits", BaseURL: server.URL + "/", Cookie: "owned=synthetic", UserAgent: "OwnedAPIBridgeUA"}, SiteOptions: SiteOptions{Domain: u.Host}, Enabled: true}}
	ownedDownloaderSave(t, a, settings)
	if !a.addOriginalSourceSiteIndexer(u.Host, map[string]any{"name": "Owned API", "domain": server.URL + "/", "schema": "NexusPhp", "public": false}) {
		t.Fatal("owned source template rejected")
	}
	retainedStoreSaveConfig(t, a, "AutoSignIn", map[string]any{"enabled": false, "notify": false, "cron": "25", "onlyonce": false, "queue_cnt": 1, "sign_sites": []any{}, "login_sites": []any{}, "retry_keyword": "^NEVER-MATCH$", "auto_cf": 0, "clean": false})
	installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("API_HTTP_BRIDGE_ASI"))
	address := "/api/v1/plugin/AutoSignIn/signin_by_domain?url=" + url.QueryEscape(server.URL+"/") + "&apikey=" + settings.APIToken
	wrong := callTest(t, a.Handler(), "GET", strings.Replace(address, settings.APIToken, "wrong-synthetic-key", 1), "", nil, "")
	missing := callTest(t, a.Handler(), "GET", "/api/v1/plugin/AutoSignIn/signin_by_domain?apikey="+settings.APIToken, "", nil, "")
	if wrong.Code != 401 || missing.Code != 422 || gate.calls.Load() != 0 {
		t.Fatal("auth/query guards admitted physical HTTP", wrong.Code, missing.Code, gate.calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := bridgeInvoke(a, address, ctx)
	bridgeEntered(t, gate)
	cancel()
	bridgePlain500(t, bridgeWaitResponse(t, done))
	physicalJoined := make(chan struct{})
	go func() { a.wg.Wait(); close(physicalJoined) }()
	select {
	case <-gate.canceled:
		t.Fatal("waiter cancellation canceled the owned physical HTTP")
	case <-physicalJoined:
		t.Fatal("physical API lifetime returned while owned HTTP was still held")
	case <-time.After(200 * time.Millisecond):
	}
	bridgeNotCanceled(t, gate)
	select {
	case <-gate.responded:
		t.Fatal("owned method completed before release")
	default:
	}
	gate.unblock()
	ordinaryEntryAwait(t, a)
	bridgeNotCanceled(t, gate)
	if gate.calls.Load() != 1 {
		t.Fatal("ASI cancellation caused extra physical request", gate.calls.Load())
	}
	select {
	case <-gate.responded:
	case <-time.After(time.Second):
		t.Fatal("physical ASI response was not written")
	}
}

func TestSourceAPIHTTPOnlyAndIdleHooksBeforeMessageStop(t *testing.T) {
	if os.Getenv("API_HTTP_BRIDGE_FIXTURE") == "" {
		t.Skip("opt-in actual SDK fixture: API_HTTP_BRIDGE_FIXTURE required")
	}
	for _, active := range []bool{false, true} {
		name := "no-live-root"
		if active {
			name = "API-only-with-real-late-child"
		}
		t.Run(name, func(t *testing.T) {
			server, root, child := bridgeHTTPPeer(t)
			defer root.unblock()
			defer child.unblock()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			settings := a.Settings()
			settings.APIToken = "owned-api-bridge-synthetic-key"
			ownedDownloaderSave(t, a, settings)
			const id = "OwnedAPIHTTPBridge"
			retainedStoreSaveConfig(t, a, id, map[string]any{"url": server.URL + "/root", "child_url": server.URL + "/child", "child": true})
			installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("API_HTTP_BRIDGE_FIXTURE"))
			if active {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := bridgeInvoke(a, "/api/v1/plugin/"+id+"/held?apikey="+settings.APIToken, ctx)
				bridgeEntered(t, root)
				cancel()
				bridgePlain500(t, bridgeWaitResponse(t, done))
				bridgeNotCanceled(t, root)
			}
			closed := make(chan error, 1)
			go func() { closed <- a.Close(); close(closed) }()
			t.Cleanup(func() {
				select {
				case <-closed:
				case <-time.After(15 * time.Second):
					t.Error("owned App.Close did not finish during failure cleanup")
				}
			})
			if active {
				privateAwait(t, "API-only hooks ran before late root release", func() bool {
					return string(bridgeData(t, a, id, "hook_stop_service")) != "null"
				})
				bridgeSaved(t, a, id, "hook_close")
				bridgeSaved(t, a, id, "hook_stop_service")
				select {
				case err := <-closed:
					t.Fatal("Go Store.Close raced the admitted API", err)
				default:
				}
				root.unblock()
				bridgeEntered(t, child)
				bridgeSaved(t, a, id, "late_http")
				bridgeNotCanceled(t, child)
				select {
				case err := <-closed:
					t.Fatal("actual Submit child was not joined after root return", err)
				default:
				}
				child.unblock()
			}
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("source hook/late method-child lifetime did not settle")
			}
			view, err := New(Options{DataDir: a.Dir})
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			bridgeSaved(t, view, id, "hook_close")
			bridgeSaved(t, view, id, "hook_stop_service")
			if active {
				bridgeSaved(t, view, id, "late_http")
				bridgeSaved(t, view, id, "late_child")
				if root.calls.Load() != 1 || child.calls.Load() != 1 {
					t.Fatal("actual root/child HTTP cardinality", root.calls.Load(), child.calls.Load())
				}
			} else if root.calls.Load() != 0 || child.calls.Load() != 0 {
				t.Fatal("no-live-root fixture unexpectedly invoked API")
			}
			messages := callTest(t, view.Handler(), "GET", "/api/message/web?count=100", "", cookie, "")
			var out struct {
				Data []struct {
					Title string `json:"title"`
				} `json:"data"`
			}
			if messages.Code != 200 || json.Unmarshal(messages.Body.Bytes(), &out) != nil {
				t.Fatal("cold hook messages unavailable", messages.Code, messages.Body.String())
			}
			seen := map[string]int{}
			for _, m := range out.Data {
				seen[m.Title]++
			}
			if seen["owned-http-bridge-close"] != 1 || seen["owned-http-bridge-stop_service"] != 1 {
				t.Fatal("PM hooks missed pre-message-stop history", seen)
			}
		})
	}
}

func TestSourceAPIHTTPAuthAndResponseValidationRemain(t *testing.T) {
	if os.Getenv("API_HTTP_BRIDGE_FIXTURE") == "" {
		t.Skip("opt-in actual SDK fixture: API_HTTP_BRIDGE_FIXTURE required")
	}
	for _, mode := range []string{"invalid-response", "source-key-rotation"} {
		t.Run(mode, func(t *testing.T) {
			server, root, _ := bridgeHTTPPeer(t)
			defer root.unblock()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			settings := a.Settings()
			settings.APIToken = "owned-api-bridge-synthetic-key"
			ownedDownloaderSave(t, a, settings)
			const id = "OwnedAPIHTTPBridge"
			retainedStoreSaveConfig(t, a, id, map[string]any{"url": server.URL + "/root", "child_url": server.URL + "/child", "invalid": mode == "invalid-response"})
			installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("API_HTTP_BRIDGE_FIXTURE"))
			done := bridgeInvoke(a, "/api/v1/plugin/"+id+"/held?apikey="+settings.APIToken, context.Background())
			bridgeEntered(t, root)
			want := 502
			if mode == "source-key-rotation" {
				settings.APIToken = "owned-api-bridge-replacement-key"
				ownedDownloaderSave(t, a, settings)
				// Real registered Depends + installed ASI source validates its
				// key on entry, not again after the already-entered method ends.
				want = 200
			}
			root.unblock()
			response := bridgeWaitResponse(t, done)
			if response.Code != want || !json.Valid(response.Body.Bytes()) || response.Body.String() == "Internal Server Error" {
				t.Fatal("source bridge weakened existing auth/typed response guard", mode, response.Code, response.Body.String())
			}
			if mode == "source-key-rotation" {
				var success struct {
					Success bool `json:"success"`
				}
				if json.Unmarshal(response.Body.Bytes(), &success) != nil || !success.Success || root.calls.Load() != 1 {
					t.Fatal("source admitted old key result was retracted", response.Code, response.Body.String(), root.calls.Load())
				}
				oldRequest := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+id+"/held?apikey=owned-api-bridge-synthetic-key", "", nil, "")
				if oldRequest.Code != 401 || root.calls.Load() != 1 {
					t.Fatal("new request with rotated old key was admitted", oldRequest.Code, root.calls.Load())
				}
				newRequest := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+id+"/held?apikey="+settings.APIToken, "", nil, "")
				if newRequest.Code != 200 || json.Unmarshal(newRequest.Body.Bytes(), &success) != nil || !success.Success || root.calls.Load() != 2 {
					t.Fatal("new key request failed source validation", newRequest.Code, newRequest.Body.String(), root.calls.Load())
				}
			}
		})
	}
}

func TestSourceAPIHTTPUnmarkedAndSerialDoNotGainBridge(t *testing.T) {
	for _, mode := range []string{"unmarked-concurrent", "serial"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "serial" && os.Getenv("API_HTTP_BRIDGE_SERIAL") == "" || mode == "unmarked-concurrent" && os.Getenv("API_HTTP_BRIDGE_UNMARKED") == "" {
				t.Skipf("opt-in actual SDK fixture: %s variant binary required", mode)
			}
			server, root, _ := bridgeHTTPPeer(t)
			defer root.unblock()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			settings := a.Settings()
			settings.APIToken = "owned-api-bridge-synthetic-key"
			ownedDownloaderSave(t, a, settings)
			const id = "OwnedAPIHTTPBridge"
			retainedStoreSaveConfig(t, a, id, map[string]any{"url": server.URL + "/root", "child_url": server.URL + "/child"})
			variable := "API_HTTP_BRIDGE_UNMARKED"
			if mode == "serial" {
				variable = "API_HTTP_BRIDGE_SERIAL"
			}
			installOwnedResourceProcessWorker(t, a, cookie, os.Getenv(variable))
			object := originalBaseSDKLoaded(t, a, id)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := bridgeInvoke(a, "/api/v1/plugin/"+id+"/held?apikey="+settings.APIToken, ctx)
			bridgeEntered(t, root)
			cancel()
			object.submitted.mu.Lock()
			sourceLeases := 0
			for lease := range object.submitted.leases {
				if lease.publicService {
					sourceLeases++
				}
			}
			object.submitted.mu.Unlock()
			if sourceLeases != 0 {
				t.Fatal("legacy/default route gained source retention", sourceLeases)
			}
			if mode == "unmarked-concurrent" {
				response := bridgeWaitResponse(t, done)
				if response.Body.String() == "Internal Server Error" && response.Code == 500 {
					t.Fatal("unmarked client acquired the source HTTP waiter bridge")
				}
			} else {
				select {
				case response := <-done:
					t.Fatal("serial bounded exchange acquired early source cancellation", response.Code, response.Body.String())
				case <-time.After(100 * time.Millisecond):
				}
				root.unblock()
				response := bridgeWaitResponse(t, done)
				// The legacy RPC drains its bounded exchange. Its unchanged
				// outer request-authority transaction then sees canceled ctx,
				// preserving the old JSON502 instead of acquiring source500.
				if response.Code != 502 || !json.Valid(response.Body.Bytes()) || response.Body.String() == "Internal Server Error" {
					t.Fatal("existing serial canceled-request guard regressed", response.Code, response.Body.String())
				}
			}
		})
	}
}

// This direct owned method is an explicitly labelled default-SDK control, not
// a substituted original API ingress. Only real API/event admission may retain.
func TestSourceAPIHTTPDefaultSubmitStillCancels(t *testing.T) {
	if os.Getenv("API_HTTP_BRIDGE_FIXTURE") == "" {
		t.Skip("opt-in actual SDK fixture: API_HTTP_BRIDGE_FIXTURE required")
	}
	server, root, child := bridgeHTTPPeer(t)
	defer root.unblock()
	defer child.unblock()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	const id = "OwnedAPIHTTPBridge"
	retainedStoreSaveConfig(t, a, id, map[string]any{"url": server.URL + "/root", "child_url": server.URL + "/child"})
	installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("API_HTTP_BRIDGE_FIXTURE"))
	object := originalBaseSDKLoaded(t, a, id)
	if _, err := object.callOriginalMethod(context.Background(), "default_root", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	bridgeEntered(t, child)
	object.submitted.mu.Lock()
	retained := false
	for lease := range object.submitted.leases {
		retained = retained || lease.publicService || lease.privateScheduler
	}
	object.submitted.mu.Unlock()
	if retained {
		t.Fatal("default Submit gained a retained owner without source ingress")
	}
	if err := a.stopOriginalPlugin(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("default Submit physical HTTP was no longer canceled")
	}
	ordinaryEntryAwait(t, a)
	if raw := bridgeData(t, a, id, "late_child"); string(raw) != "null" {
		t.Fatal("default canceled child persisted unauthorized late data", string(raw))
	}
}

// These are explicitly host-component authority controls with a real negotiated
// worker. Only the positive lifecycle tests above exercise genuine factories.
func TestSourceAPIHTTPStopMessageAuthorityDoesNotExpand(t *testing.T) {
	if os.Getenv("API_HTTP_BRIDGE_FIXTURE") == "" {
		t.Skip("opt-in actual SDK fixture: API_HTTP_BRIDGE_FIXTURE required")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	const id = "OwnedAPIHTTPBridge"
	installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("API_HTTP_BRIDGE_FIXTURE"))
	p := originalBaseSDKLoaded(t, a, id)
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if client == nil {
		t.Fatal("real negotiated client missing")
	}
	identity := plugins.Identity{PluginID: p.installation.PluginID, InstanceID: p.installation.InstanceID, Version: p.installation.Version}
	for _, mode := range []string{"fake-stop-name", "physical-stop", "returned", "empty-key"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			if mode == "physical-stop" || mode == "returned" {
				// Default false business marker: exactly the unchanged physical
				// stop authority, not an API/worker supplied ownership flag.
				ctx = context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
			} else if mode == "empty-key" {
				ctx = context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{})
			}
			callback, revoke := p.originalBaseSessionHost(ctx, "stop", nil)
			defer revoke()
			if mode == "returned" {
				revoke()
			}
			if _, err := callback(ctx, identity, plugins.PostMessageCapability, json.RawMessage(`{"channel":"Web","title":"owned-stop-key-probe","businessHook":true}`)); err == nil {
				t.Fatal("ordinary/physical/returned scope forged source hook notification authority", mode)
			}
		})
	}
}

func TestSourceAPIHTTPPrivateOnlyHookMessage(t *testing.T) {
	if os.Getenv("API_HTTP_BRIDGE_PRIVATE") == "" {
		t.Skip("opt-in actual SDK fixture: API_HTTP_BRIDGE_PRIVATE required")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	const id = "OwnedAPIHTTPBridge"
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("API_HTTP_BRIDGE_PRIVATE"))
	if m.ManagedPublicServices || !m.ManagedPrivateScheduler {
		t.Fatal("actual private-only compiled manifest required")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	bridgeSaved(t, view, id, "hook_close")
	bridgeSaved(t, view, id, "hook_stop_service")
}

// Fixed MP PluginManager.remove_plugin -> stop calls close, then stop_service
// before deleting running/class maps. This exercises the fourth genuine Go
// factory through a real concurrent SDK initializer; no StopKey is fabricated.
func TestSourceAPIHTTPLiveInitializerSelfRemovalMessages(t *testing.T) {
	if os.Getenv("API_HTTP_BRIDGE_SELFREMOVE") == "" {
		t.Skip("opt-in actual SDK fixture: API_HTTP_BRIDGE_SELFREMOVE required")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	const id = "OwnedAPIHTTPBridge"
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("API_HTTP_BRIDGE_SELFREMOVE"))
	if !m.ManagedPublicServices {
		t.Fatal("source-negotiated actual SDK required")
	}
	object := originalBaseSDKLoaded(t, a, id)
	if string(bridgeData(t, a, id, "hook_close")) != "null" || string(bridgeData(t, a, id, "hook_stop_service")) != "null" {
		t.Fatal("setup already ran business hooks")
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"self_remove":true,"owned_retained":"live-init"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	ordinaryEntryAwait(t, a)
	bridgeSaved(t, a, id, "hook_close")
	bridgeSaved(t, a, id, "hook_stop_service")
	if _, ok := a.pluginRunningInstances.Load(id); ok {
		t.Fatal("real live initializer did not remove running object")
	}
	if _, ok := a.pluginOriginalClasses.Load(id); ok {
		t.Fatal("source class survived manager removal")
	}
	if originalObjectStillLoaded(object) {
		t.Fatal("old object remains published")
	}
	in, err := a.pluginStore.Installation(context.Background(), id)
	if err != nil || !in.Installed {
		t.Fatal("remove_plugin unexpectedly uninstalled persistent membership", in, err)
	}
	config, err := a.pluginStore.Config(context.Background(), id)
	if err != nil || !originalBaseJSONEqual(config.Value, json.RawMessage(`{"self_remove":true,"owned_retained":"live-init"}`)) {
		t.Fatal("source logical removal purged configuration", string(config.Value), err)
	}
	if info, err := os.Stat(filepath.Join(a.pluginPackageRoot(), id)); err != nil || !info.IsDir() {
		t.Fatal("source logical removal purged code", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	bridgeSaved(t, view, id, "hook_close")
	bridgeSaved(t, view, id, "hook_stop_service")
	messages := callTest(t, view.Handler(), "GET", "/api/message/web?count=100", "", cookie, "")
	var out struct {
		Data []struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	if messages.Code != 200 || json.Unmarshal(messages.Body.Bytes(), &out) != nil {
		t.Fatal("cold live-init hook history unavailable", messages.Code, messages.Body.String())
	}
	counts := map[string]int{}
	for _, item := range out.Data {
		counts[item.Title]++
	}
	if counts["owned-http-bridge-close"] != 1 || counts["owned-http-bridge-stop_service"] != 1 {
		t.Fatal("live-init business hooks did not notify exactly once", counts)
	}
}
