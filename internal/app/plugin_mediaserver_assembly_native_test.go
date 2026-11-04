package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/plugins/mediaservervendors"
)

type ownedNativeMediaSnapshot struct {
	PID      int                              `json:"pid"`
	All      []plugins.MediaServerServiceInfo `json:"all"`
	First    *plugins.MediaServerServiceInfo  `json:"first"`
	Inactive bool                             `json:"inactive"`
}
type ownedNativeMediaRetained struct {
	PID         int                             `json:"pid"`
	Retained    plugins.MediaServerInstance     `json:"retained"`
	Current     *plugins.MediaServerServiceInfo `json:"current"`
	Inactive    bool                            `json:"inactive"`
	StaleDenied bool                            `json:"stale_denied"`
}

func ownedNativeMediaCall(t *testing.T, p *originalPluginInstance, method string, input, output any) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := p.callSession(ctx, plugins.Runner{Timeout: 15 * time.Second}, method, raw, nil)
	if err != nil {
		t.Fatalf("owned actual media SDK %s: %v", method, err)
	}
	if err = json.Unmarshal(result.Result, output); err != nil {
		t.Fatal("owned actual media SDK response", err)
	}
}
func ownedNativeMediaWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("owned actual media lifecycle did not reach the required state")
}

// Reuses the unchanged already-real bridge worker. No --manifest subprocess or
// synthetic Vendor is introduced. The gate binds this binary and its full prior
// actual compilation closure; this immutable manifest is its exact source one.
func ownedNativeMediaArtifact(t *testing.T) (string, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	path := os.Getenv("OWNED_MEDIA_INSTANCE_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("bound actual media instance worker required")
	}
	executable, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedMediaInstanceFixture", Name: "Owned media instance fixture", Version: "1.0", Runtime: "go-executable", Entry: "owned-media-instance-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_form", "capture", "retained", "refresh", "resource", "close", "stop_service", "stop"},
		Capabilities: []string{plugins.BaseSaveData, plugins.MediaServerGetServicesCapability, plugins.MediaServerGetServiceCapability, plugins.MediaServerIsInactiveCapability, plugins.MediaServerRefreshRootCapability, plugins.MediaServerRefreshItemsCapability}}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: manifest.Entry, Method: zip.Deflate}
	header.SetMode(0700)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write(executable); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	packed := buffer.Bytes()
	if int64(len(packed)) > artifacts.MaxArtifactBytes || int64(len(executable)) > artifacts.MaxExpandedBytes {
		t.Fatal("unchanged executable artifact limit")
	}
	packedSHA, binarySHA := sha256.Sum256(packed), sha256.Sum256(executable)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", SHA256: hex.EncodeToString(packedSHA[:]), ExecutableSHA256: hex.EncodeToString(binarySHA[:]), Size: int64(len(packed)), HostContract: pluginResourcesSDKContract}}}}}
	indexes, downloads := new(atomic.Int32), new(atomic.Int32)
	var unexpected atomic.Int32
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			unexpected.Add(1)
			http.Error(w, "unexpected owned artifact request", 400)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexes.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker.zip":
			downloads.Add(1)
			_, _ = w.Write(packed)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	})}}
	index.Releases[0].Artifacts[0].URL = "http://" + listener.Addr().String() + "/worker.zip"
	peer.Start()
	t.Cleanup(func() {
		peer.Close()
		if unexpected.Load() != 0 {
			t.Error("unexpected owned artifact request")
		}
	})
	return peer.URL + "/index", indexes, downloads
}

// Real ordinary builder -> App.Start -> committed explicit producer -> actual
// core dispatcher -> real Emby/Jellyfin objects -> installed SDK process. This
// is same-process App reconstruction, NOT a cmd/server two-process CLI claim.
// All six factories are real, but this finite root selects only these two IO
// vendors; unselected Plex/Trime/Ugreen/ZSpace are not claimed IO-tested here.
func TestOriginalMediaServerNativeAssemblyProducerSDKAndCold(t *testing.T) {
	environment := os.Environ()
	t.Cleanup(func() {
		if !reflect.DeepEqual(environment, os.Environ()) {
			t.Error("native media root changed environment")
		}
	})
	// An operator-owned external Plex configuration is not read by this owned
	// default-profile fixture. Stop rather than overwrite or redirect its setting.
	if _, present := os.LookupEnv("PLEXAPI_CONFIG_PATH"); present {
		t.Fatal("owned default-profile root requires no explicit external Plex config path")
	}
	var wireMu sync.Mutex
	wire := []string{}
	var unexpected atomic.Int32
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := ""
		if strings.HasPrefix(r.URL.Path, "/j/") {
			authorization = `MediaBrowser Token="owned-media-key"`
		}
		if r.Method != "GET" || r.URL.RawQuery != "api_key=owned-media-key" || r.Header.Get("Authorization") != authorization || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			unexpected.Add(1)
			http.Error(w, "unexpected owned media request", 400)
			return
		}
		wireMu.Lock()
		wire = append(wire, r.URL.Path)
		wireMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/e/Users", "/j/Users":
			_, _ = w.Write([]byte(`[{"Name":"admin","Id":"owned-user","Policy":{"IsAdministrator":true,"EnableAllFolders":true}}]`))
		case "/e/emby/Library/SelectableMediaFolders":
			_, _ = w.Write([]byte(`[{"Id":"owned-library"}]`))
		case "/e/System/Info", "/j/System/Info":
			_, _ = w.Write([]byte(`{"Id":"owned-system"}`))
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	})}}
	peer.StartTLS()
	defer peer.Close()
	target, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	route, err := networkproxy.RequestsEnvironmentProxy(environment, target)
	if err != nil || route != nil {
		t.Fatal("normal existing route does not admit this owned peer; no route fallback")
	}
	wireCount := func() int { wireMu.Lock(); defer wireMu.Unlock(); return len(wire) }
	repo, indexes, downloads := ownedNativeMediaArtifact(t)
	dir := t.TempDir()
	var active *App
	t.Cleanup(func() {
		if active != nil {
			_ = active.Close()
		}
	})
	open := func() *App {
		builder, e := NewOriginalMediaServerFactoryAssembly(context.Background(), dir)
		if e != nil {
			t.Fatal("ordinary real builder", e)
		}
		a, e := New(Options{DataDir: dir, MediaServerFactories: builder})
		if e != nil {
			t.Fatal(e)
		}
		active = a
		if a.originalMediaServerAssembly == nil || len(a.originalMediaServerAssembly.definitions) != 6 {
			t.Fatal("ordinary builder omitted concrete factory definitions")
		}
		return a
	}
	a := open()
	settings := a.Settings()
	settings.PluginMarkets, settings.PluginLocalRepoPaths = []string{}, []string{}
	empty := ""
	settings.PluginServerURL = &empty
	// StatisticShare/default public permissions are not changed. No arbitrary
	// vendor config, source FNOS mapping or private credentials are injected.
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	a.Start(context.Background())
	h := a.originalMediaServerAssembly
	h.mu.RLock()
	waiting := h.waitingForExplicitConfig
	h.mu.RUnlock()
	if !waiting || len(h.manager.Running()) != 0 || wireCount() != 0 {
		t.Fatal("unknown origin became a fake loaded registry")
	}
	install, _ := json.Marshal(map[string]string{"plugin_id": "OwnedMediaInstanceFixture", "repo_url": repo})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(install), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	var worker *originalPluginInstance
	ownedNativeMediaWait(t, func() bool {
		value, ok := a.pluginRunningInstances.Load("OwnedMediaInstanceFixture")
		if !ok {
			return false
		}
		worker = value.(*originalPluginInstance)
		worker.sessionMu.Lock()
		live := worker.session != nil
		worker.sessionMu.Unlock()
		return live
	})
	if !worker.external || worker.hostContract != pluginResourcesSDKContract {
		t.Fatal("not the actual installed resource worker")
	}
	makeValue := func(both bool) string {
		values := []any{map[string]any{"name": "alpha", "type": "emby", "enabled": true, "config": map[string]any{"host": peer.URL + "/e", "apikey": "owned-media-key"}}}
		if both {
			values = append(values, map[string]any{"name": "beta", "type": "jellyfin", "enabled": true, "config": map[string]any{"host": peer.URL + "/j", "apikey": "owned-media-key"}})
		}
		raw, e := json.Marshal(values)
		if e != nil {
			t.Fatal(e)
		}
		return string(raw)
	}
	post := func(value string, status int) {
		t.Helper()
		w := callTest(t, a.Handler(), "POST", "/api/system/setting/MediaServers", value, cookie, "")
		if w.Code != status {
			t.Fatal("actual ordinary producer status", w.Code)
		}
		if bytes.Contains(w.Body.Bytes(), []byte("owned-media-key")) {
			t.Fatal("producer leaked synthetic private value")
		}
	}
	waitInstances := func(expectJelly bool) {
		t.Helper()
		ownedNativeMediaWait(t, func() bool {
			if len(h.manager.Running()) != 6 {
				return false
			}
			e := h.manager.RunningModule("EmbyModule")
			j := h.manager.RunningModule("JellyfinModule")
			if e == nil || j == nil || len(e.Instances()) != 1 {
				return false
			}
			if expectJelly {
				return len(j.Instances()) == 1
			}
			return len(j.Instances()) == 0
		})
	}
	post(makeValue(false), 200)
	waitInstances(false)
	if wireCount() != 3 {
		t.Fatal("first real Emby constructor request count", wireCount())
	}
	firstVendor := h.manager.RunningModule("EmbyModule").Instances()[0].Vendor
	if _, ok := firstVendor.(*mediaservervendors.Emby); !ok {
		t.Fatal("recording/proxy Emby substituted")
	}
	var initial ownedNativeMediaSnapshot
	ownedNativeMediaCall(t, worker, "capture", nil, &initial)
	if initial.PID <= 0 || initial.First == nil || len(initial.All) != 1 || initial.Inactive || string(initial.First.Type) != `"emby"` {
		t.Fatal("real initial SDK projection")
	}
	originalReference := initial.First.Instance.Reference
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_native_media_abort BEFORE UPDATE ON records WHEN OLD.kind='source_media_servers_v1' AND OLD.id='value' BEGIN SELECT RAISE(ABORT,'owned native media abort'); END`); err != nil {
		t.Fatal(err)
	}
	post(makeValue(true), 500)
	if wireCount() != 3 || h.manager.RunningModule("EmbyModule").Instances()[0].Vendor != firstVendor {
		t.Fatal("failed async commit rebuilt actual vendor")
	}
	hot, e := a.originalMediaServerConfig.get(context.Background())
	if e != nil || !sharedSystemConfigEqual(hot, json.RawMessage(makeValue(false))) {
		t.Fatal("failed commit changed sealed hot fact", e)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_native_media_abort`); err != nil {
		t.Fatal(err)
	}
	post(makeValue(true), 200)
	ownedNativeMediaWait(t, func() bool {
		e, j := h.manager.RunningModule("EmbyModule"), h.manager.RunningModule("JellyfinModule")
		if e == nil || j == nil {
			return false
		}
		emby, jelly := e.Instances(), j.Instances()
		return len(emby) == 1 && len(jelly) == 1 && emby[0].Vendor != firstVendor
	})
	waitInstances(true)
	if wireCount() != 8 {
		t.Fatal("real configuration-event constructor request count", wireCount())
	}
	jelly := h.manager.RunningModule("JellyfinModule").Instances()[0].Vendor
	if _, ok := jelly.(*mediaservervendors.Jellyfin); !ok {
		t.Fatal("recording/proxy Jellyfin substituted")
	}
	var retained ownedNativeMediaRetained
	ownedNativeMediaCall(t, worker, "retained", nil, &retained)
	if retained.PID != initial.PID || retained.Inactive || !retained.StaleDenied || retained.Retained.Reference != originalReference || retained.Current == nil || retained.Current.Instance.Reference == originalReference {
		t.Fatal("reload expired old vendor or reused stale invocation")
	}
	// Explicit real manager Reload exercises Stop->Load and same Services publish,
	// not a synthetic owner replacement. Source Emby/Jellyfin stop remains pass.
	services := h.host.services
	oldOwner := h.manager.RunningModule("EmbyModule")
	if err = a.reloadOriginalMediaServerAssembly(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.host.services != services || h.manager.RunningModule("EmbyModule") == oldOwner || wireCount() != 13 {
		t.Fatal("manager reload publication/real constructor mismatch")
	}
	ownedNativeMediaCall(t, worker, "retained", nil, &retained)
	if retained.Inactive || retained.Retained.Reference != originalReference || !retained.StaleDenied {
		t.Fatal("explicit manager reload invalidated retained source object")
	}
	var sealed []byte
	if err = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='source_media_servers_v1' AND id='value'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("owned-media-key")) {
		t.Fatal("raw config was not sealed")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
	if err = h.current(context.Background()); err == nil {
		t.Fatal("closed actual App remained current")
	}
	cold := open()
	cold.Start(context.Background())
	h = cold.originalMediaServerAssembly
	var coldWorker *originalPluginInstance
	ownedNativeMediaWait(t, func() bool {
		v, ok := cold.pluginRunningInstances.Load("OwnedMediaInstanceFixture")
		if !ok {
			return false
		}
		coldWorker = v.(*originalPluginInstance)
		coldWorker.sessionMu.Lock()
		live := coldWorker.session != nil
		coldWorker.sessionMu.Unlock()
		return live
	})
	waitInstances(true)
	var final ownedNativeMediaSnapshot
	ownedNativeMediaCall(t, coldWorker, "capture", nil, &final)
	if final.PID <= 0 || final.PID == initial.PID || final.Inactive || len(final.All) != 2 || final.First == nil || final.First.Instance.Reference == originalReference {
		t.Fatal("cold actual worker/manager/sealed config did not rebuild")
	}
	if indexes.Load() != 1 || downloads.Load() != 1 || wireCount() != 18 || unexpected.Load() != 0 {
		t.Fatal("cold used unexpected artifacts or media requests", indexes.Load(), downloads.Load(), wireCount(), unexpected.Load())
	}
	wireMu.Lock()
	counts := map[string]int{}
	for _, path := range wire {
		counts[path]++
	}
	wireMu.Unlock()
	if !reflect.DeepEqual(counts, map[string]int{"/e/Users": 4, "/e/emby/Library/SelectableMediaFolders": 4, "/e/System/Info": 4, "/j/Users": 3, "/j/System/Info": 3}) {
		t.Fatal("actual source constructor endpoint multiset differs")
	}
	// Every original factory is real, while this finite selected config proves
	// only concrete EJ constructors. Zero-instance owners are not six IO passes.
	if got := h.manager.CatalogIDs(); !reflect.DeepEqual(got, []string{"EmbyModule", "JellyfinModule", "PlexModule", "TrimeMediaModule", "UgreenModule", "ZSpaceModule"}) {
		t.Fatal("native discovery order changed")
	}
	if err = cold.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
	t.Logf("actual App generations=2 workers=%d,%d owned TLS requests=18 artifact GETs=2 real selected vendors=Emby,Jellyfin", initial.PID, final.PID)
}

// Compile-time reference documents the accepted concrete Factory result type;
// this test does not define or install any replacement implementation.
var _ mediaserverruntime.Vendor = (*mediaservervendors.Emby)(nil)
