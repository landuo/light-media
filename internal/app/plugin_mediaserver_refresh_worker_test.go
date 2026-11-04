package app

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/plugins/mediaservervendors"
)

type ownedMediaRefreshResponse struct {
	PID         int                              `json:"pid"`
	Instance    plugins.MediaServerInstance      `json:"instance"`
	Result      plugins.MediaServerRefreshResult `json:"result"`
	StaleDenied bool                             `json:"stale_denied"`
}

// The exact ordinary factory builder and committed producer select three real
// vendors. This root validates the NEW SDK/dispatcher/result/path seam, not
// every historical vendor algorithm or a two-server cold lifecycle.
func TestOriginalMediaServerRefreshSDKConcreteResultsAndPaths(t *testing.T) {
	environment := os.Environ()
	t.Cleanup(func() {
		if !reflect.DeepEqual(environment, os.Environ()) {
			t.Error("refresh root changed environment")
		}
	})
	if _, present := os.LookupEnv("PLEXAPI_CONFIG_PATH"); present {
		t.Fatal("owned root requires no external Plex config path")
	}
	mediaRoot, outside := t.TempDir(), t.TempDir()
	var escaped strings.Builder
	if err := xml.EscapeText(&escaped, []byte(mediaRoot)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	wire := map[string]int{}
	var invalid string
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reject := func(reason string) { invalid = reason; http.Error(w, "owned request refused", 400) }
		authorization := ""
		if strings.HasPrefix(r.URL.Path, "/j/") {
			authorization = `MediaBrowser Token="owned-media-key"`
		}
		if r.Host != listener.Addr().String() || r.Header.Get("Authorization") != authorization || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" {
			reject("unexpected endpoint/credentials")
			return
		}
		key := r.Method + " " + r.URL.Path
		switch r.URL.Path {
		case "/e/Users", "/j/Users", "/e/emby/Library/SelectableMediaFolders", "/e/System/Info", "/j/System/Info":
			if r.Method != "GET" || r.URL.RawQuery != "api_key=owned-media-key" || r.Header.Get("X-Plex-Token") != "" {
				reject("invalid EJ constructor")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/e/Users", "/j/Users":
				_, _ = w.Write([]byte(`[{"Name":"admin","Id":"owned-user","Policy":{"IsAdministrator":true,"EnableAllFolders":true}}]`))
			case "/e/emby/Library/SelectableMediaFolders":
				_, _ = w.Write([]byte(`[{"Id":"owned-library"}]`))
			default:
				_, _ = w.Write([]byte(`{"Id":"owned-system"}`))
			}
		case "/e/emby/Library/Refresh", "/j/Library/Refresh":
			if r.Method != "POST" || r.URL.RawQuery != "api_key=owned-media-key" || r.Header.Get("X-Plex-Token") != "" {
				reject("invalid EJ refresh")
				return
			}
			w.WriteHeader(500) // original Emby False versus Jellyfin None
		case "/", "/library", "/library/sections", "/library/sections/all/refresh", "/library/sections/1/refresh":
			if r.Method != "GET" || r.Header.Get("X-Plex-Token") != "owned-plex-token" {
				reject("invalid Plex method/token")
				return
			}
			if r.URL.Path == "/library/sections/1/refresh" {
				if r.URL.RawQuery != (url.Values{"path": []string{filepath.Join(mediaRoot, "nested")}}).Encode() {
					reject("invalid exact owned refresh parent")
					return
				}
			} else if r.URL.RawQuery != "" {
				reject("unexpected Plex query")
				return
			}
			w.Header().Set("Content-Type", "application/xml; charset=UTF-8")
			switch r.URL.Path {
			case "/":
				_, _ = w.Write([]byte(`<MediaContainer friendlyName="Owned" machineIdentifier="owned-machine"/>`))
			case "/library":
				_, _ = w.Write([]byte(`<MediaContainer identifier="owned-library" title1="Owned Library"/>`))
			case "/library/sections":
				_, _ = w.Write([]byte(`<MediaContainer><Directory key="1" title="Owned Movies" type="movie"><Location path="` + escaped.String() + `"/></Directory></MediaContainer>`))
			default:
				_, _ = w.Write([]byte(`<MediaContainer/>`))
			}
		default:
			reject("unknown owned endpoint")
			return
		}
		wire[key]++
	})}}
	peer.StartTLS()
	defer peer.Close()
	target, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	route, err := networkproxy.RequestsEnvironmentProxy(environment, target)
	if err != nil || route != nil {
		t.Fatal("ordinary route does not admit owned peer; no fallback")
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, v := range wire {
			n += v
		}
		return n
	}
	repo, indexes, downloads := ownedNativeMediaArtifact(t)
	dir := t.TempDir()
	builder, err := NewOriginalMediaServerFactoryAssembly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: dir, MediaServerFactories: builder})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = a.Close()
		}
	})
	settings := a.Settings()
	empty := ""
	settings.PluginMarkets, settings.PluginLocalRepoPaths = []string{}, []string{}
	settings.PluginServerURL = &empty
	settings.Roots = []Root{}
	settings.AllowedRoots = []string{mediaRoot}
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	a.Start(context.Background())
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
		defer worker.sessionMu.Unlock()
		return worker.session != nil
	})
	if !worker.external || worker.hostContract != pluginResourcesSDKContract {
		t.Fatal("not actual resource worker")
	}
	config, _ := json.Marshal([]any{
		map[string]any{"name": "emby-owned", "type": "emby", "enabled": true, "config": map[string]any{"host": peer.URL + "/e", "apikey": "owned-media-key"}},
		map[string]any{"name": "jelly-owned", "type": "jellyfin", "enabled": true, "config": map[string]any{"host": peer.URL + "/j", "apikey": "owned-media-key"}},
		map[string]any{"name": "plex-owned", "type": "plex", "enabled": true, "config": map[string]any{"host": peer.URL, "token": "owned-plex-token"}},
	})
	response = callTest(t, a.Handler(), "POST", "/api/system/setting/MediaServers", string(config), cookie, "")
	if response.Code != 200 {
		t.Fatal("real producer failed", response.Code)
	}
	assembly := a.originalMediaServerAssembly
	ownedNativeMediaWait(t, func() bool {
		if len(assembly.manager.Running()) != 6 {
			return false
		}
		for _, name := range []string{"EmbyModule", "JellyfinModule", "PlexModule"} {
			o := assembly.manager.RunningModule(name)
			if o == nil || len(o.Instances()) != 1 {
				return false
			}
		}
		return true
	})
	if count() != 8 {
		t.Fatal("true selected constructor count", count())
	}
	invoke := func(input any) ownedMediaRefreshResponse {
		var out ownedMediaRefreshResponse
		ownedNativeMediaCall(t, worker, "refresh", input, &out)
		if out.PID <= 0 {
			t.Fatal("missing actual worker PID")
		}
		return out
	}
	emby := invoke(map[string]any{"name": "emby-owned", "root": true})
	if emby.Result.Kind != "bool" || emby.Result.Boolean == nil || *emby.Result.Boolean {
		t.Fatal("Emby False collapsed")
	}
	jelly := invoke(map[string]any{"name": "jelly-owned", "root": true})
	if jelly.PID != emby.PID || jelly.Result.Kind != "none" || jelly.Result.Boolean != nil {
		t.Fatal("Jellyfin None collapsed")
	}
	plex := invoke(map[string]any{"name": "plex-owned", "root": true})
	if plex.PID != emby.PID || plex.Result.Kind != "plex_library" || plex.Result.Library == nil {
		t.Fatal("Plex concrete Library not retained")
	}
	again := invoke(map[string]any{"reference": plex.Instance.Reference, "root": true})
	if again.Instance.Reference != plex.Instance.Reference || again.Result.Library == nil || again.Result.Library.Reference != plex.Result.Library.Reference {
		t.Fatal("same retained Library acquired new identity")
	}
	table, err := worker.mediaReferences.libraryTable()
	if err != nil {
		t.Fatal(err)
	}
	table.mu.Lock()
	object := table.values[plex.Result.Library.Reference]
	table.mu.Unlock()
	if !mediaservervendors.IsPlexRefreshLibraryObject(object) {
		t.Fatal("library token is not real SDK object")
	}
	item := plugins.MediaServerRefreshItem{Title: json.RawMessage(`"Owned"`), Year: json.RawMessage(`2026`), Type: plugins.MediaServerRefreshTypeMovie, Category: json.RawMessage(`null`)}
	item.TargetPath, _ = json.Marshal(filepath.Join(mediaRoot, "nested", "missing.mkv"))
	refreshed := invoke(map[string]any{"reference": plex.Instance.Reference, "items": []plugins.MediaServerRefreshItem{item}})
	if refreshed.Result.Kind != "none" || refreshed.Result.Boolean != nil || refreshed.Result.Library != nil {
		t.Fatal("Plex by-items None collapsed")
	}
	before := count()
	stale := invoke(map[string]any{"reference": plex.Instance.Reference, "root": true, "previous": true})
	if !stale.StaleDenied || stale.PID != emby.PID || count() != before {
		t.Fatal("expired callback refreshed vendor")
	}
	item.TargetPath, _ = json.Marshal(filepath.Join(outside, "missing.mkv"))
	raw, _ := json.Marshal(map[string]any{"reference": plex.Instance.Reference, "items": []plugins.MediaServerRefreshItem{item}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	_, denied := worker.callSession(ctx, plugins.Runner{Timeout: 15 * time.Second}, "refresh", raw, nil)
	cancel()
	if denied == nil || count() != before {
		t.Fatal("path denial became whole-library fallback")
	}
	if before != 13 || indexes.Load() != 1 || downloads.Load() != 1 {
		t.Fatal("finite true SDK request/artifact closure", before, indexes.Load(), downloads.Load())
	}
	if err = a.stopOriginalPlugin(context.Background(), worker.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.mediaReferences.libraryTable(); !errors.Is(err, mediaserverruntime.ErrReferenceClosed) {
		t.Fatal("retirement recreated Library table")
	}
	table.mu.Lock()
	retired := table.closed && table.values == nil && table.reverse == nil
	table.mu.Unlock()
	if !retired {
		t.Fatal("Library identity outlived plugin retirement")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	mu.Lock()
	defer mu.Unlock()
	if invalid != "" {
		t.Fatal(invalid)
	}
	if wire["GET /library/sections/all/refresh"] != 2 || wire["GET /library/sections/1/refresh"] != 1 || wire["POST /e/emby/Library/Refresh"] != 1 || wire["POST /j/Library/Refresh"] != 1 {
		t.Fatal("actual refresh effects differ", wire)
	}
}
