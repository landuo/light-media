package app

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaservervendors"
)

// This is a new App integration composition, not another replay of every
// vendor HTTP leaf. Both selected objects are built by the ordinary builder.
func TestOriginalMediaServerTrimeZSpaceHostSDKAndCold(t *testing.T) {
	environment := os.Environ()
	t.Cleanup(func() {
		if !reflect.DeepEqual(environment, os.Environ()) {
			t.Error("host root changed environment")
		}
	})
	if _, present := os.LookupEnv("PLEXAPI_CONFIG_PATH"); present {
		t.Fatal("owned root cannot read an external Plex profile")
	}
	mediaRoot, dir := t.TempDir(), t.TempDir()
	var mu sync.Mutex
	trimeWire, zspaceWire := []string{}, []string{}
	invalid := ""
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reject := func(reason string) { invalid = reason; http.Error(w, "owned request refused", 400) }
		if r.Host != listener.Addr().String() || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Plex-Token") != "" {
			reject("unexpected origin or cross-client credentials")
			return
		}
		body, e := io.ReadAll(r.Body)
		if e != nil {
			reject("body read failed")
			return
		}
		key := r.Method + " " + r.URL.Path
		var value any
		var contentEncoding string
		if strings.HasPrefix(r.URL.Path, "/v/api/v1/") {
			if r.URL.RawQuery != "" || r.Header.Get("X-Emby-Token") != "" || r.Header.Get("User-Agent") == "" || r.Header.Get("Accept-Encoding") != "gzip, deflate" || r.Header.Get("Connection") != "keep-alive" {
				reject("invalid ordinary Trime headers/query")
				return
			}
			auth, e := url.ParseQuery(r.Header.Get("authx"))
			nonce, ne := strconv.Atoi(auth.Get("nonce"))
			timestamp, te := strconv.ParseInt(auth.Get("timestamp"), 10, 64)
			if e != nil || ne != nil || te != nil || len(auth) != 3 || nonce < 100000 || nonce > 999999 || timestamp < time.Now().Add(-time.Minute).UnixMilli() || timestamp > time.Now().Add(time.Minute).UnixMilli() {
				reject("ordinary nonce/time signature absent")
				return
			}
			payload := md5.Sum(body)
			signature := md5.Sum([]byte(strings.Join([]string{"NDzZTVxnRKP8Z0jXg1VAMonaG8akvh", r.URL.Path, auth.Get("nonce"), auth.Get("timestamp"), hex.EncodeToString(payload[:]), "16CCEB3D-AB42-077D-36A1-F355324E4237"}, "_")))
			if auth.Get("sign") != hex.EncodeToString(signature[:]) {
				reject("actual Trime signature mismatch")
				return
			}
			anonymous := r.URL.Path == "/v/api/v1/sys/version" || r.URL.Path == "/v/api/v1/login"
			if (anonymous && r.Header.Get("Authorization") != "") || (!anonymous && r.Header.Get("Authorization") != "owned-trime-token") {
				reject("Trime token scope mismatch")
				return
			}
			switch key {
			case "GET /v/api/v1/sys/version":
				value = map[string]any{"version": "owned", "mediasrvVersion": "1"}
			case "POST /v/api/v1/login":
				var fields map[string]any
				if json.Unmarshal(body, &fields) != nil || fields["username"] != "owned-trime-user" || fields["password"] != "owned-trime-password" || fields["app_name"] != "trimemedia-web" {
					reject("unexpected Trime login input")
					return
				}
				value = map[string]any{"token": "owned-trime-token"}
			case "GET /v/api/v1/user/info":
				value = map[string]any{"guid": "owned-user", "username": "owned-trime-user", "is_admin": 1}
			case "GET /v/api/v1/mdb/list":
				value = []any{map[string]any{"guid": "owned-lib", "category": "Movie", "name": "Owned", "posters": []any{}, "dir_list": []string{mediaRoot}}}
			case "POST /v/api/v1/item/list":
				if !json.Valid(body) {
					reject("invalid actual Trime count body")
					return
				}
				value = map[string]any{"total": 1}
			case "GET /v/api/v1/task/running":
				value = []any{}
			case "POST /v/api/v1/mdb/scanall", "POST /v/api/v1/mdb/scan/owned-lib", "POST /v/api/v1/user/logout":
				if len(body) != 0 {
					reject("unexpected refresh/logout payload")
					return
				}
				value = true
			default:
				reject("unknown Trime method/path")
				return
			}
			value = map[string]any{"code": 0, "data": value}
			contentEncoding = "gzip"
			trimeWire = append(trimeWire, key)
		} else if strings.HasPrefix(r.URL.Path, "/z/emby/") {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Authx") != "" || r.Header.Get("X-Emby-Authorization") != `MediaBrowser Client="MoviePilot", Device="requests", DeviceId="1", Version="1.0.0"` {
				reject("ZSpace credential/header scope mismatch")
				return
			}
			login := r.URL.Path == "/z/emby/Users/AuthenticateByName"
			if (login && r.Header.Get("X-Emby-Token") != "") || (!login && r.Header.Get("X-Emby-Token") != "owned-z-token") {
				reject("ZSpace token mismatch")
				return
			}
			if r.URL.Path != "/z/emby/Items" && r.URL.RawQuery != "" && r.URL.Path != "/z/emby/Items/owned-series/Refresh" {
				reject("unexpected ZSpace query")
				return
			}
			switch key {
			case "POST /z/emby/Users/AuthenticateByName":
				var fields map[string]any
				if json.Unmarshal(body, &fields) != nil || len(fields) != 2 || fields["Username"] != "owned-z-user" || fields["Pw"] != "owned-z-password" {
					reject("unexpected ZSpace login input")
					return
				}
				value = map[string]any{"AccessToken": "owned-z-token", "User": map[string]any{"Id": "owned-z-user-id"}}
			case "GET /z/emby/Library/SelectableMediaFolders":
				value = []any{}
			case "GET /z/emby/System/Info":
				value = map[string]any{"Id": "owned-z-system"}
			case "GET /z/emby/Items":
				expected := url.Values{"IncludeItemTypes": {"Series"}, "Fields": {"ProductionYear"}, "StartIndex": {"0"}, "Recursive": {"true"}, "SearchTerm": {"Owned"}, "Limit": {"10"}, "IncludeSearchTypes": {"false"}}
				if !reflect.DeepEqual(r.URL.Query(), expected) {
					reject("typed enum did not take original Series path")
					return
				}
				value = map[string]any{"Items": []any{map[string]any{"Name": "Owned", "ProductionYear": 2024, "Id": "owned-series"}}}
			case "POST /z/emby/Items/owned-series/Refresh":
				if r.URL.RawQuery != "Recursive=true" || len(body) != 0 {
					reject("invalid ZSpace matching-item refresh")
					return
				}
				value = map[string]any{}
			case "POST /z/emby/Library/Refresh":
				if len(body) != 0 {
					reject("invalid ZSpace root refresh body")
					return
				}
				value = map[string]any{}
			default:
				reject("unknown ZSpace method/path")
				return
			}
			contentEncoding = "deflate"
			zspaceWire = append(zspaceWire, key)
		} else {
			reject("unknown owned service path")
			return
		}
		raw, e := json.Marshal(value)
		if e != nil {
			reject("owned response serialization failed")
			return
		}
		var compressed bytes.Buffer
		if contentEncoding == "gzip" {
			writer := gzip.NewWriter(&compressed)
			if _, e = writer.Write(raw); e == nil {
				e = writer.Close()
			}
		} else {
			writer := zlib.NewWriter(&compressed)
			if _, e = writer.Write(raw); e == nil {
				e = writer.Close()
			}
		}
		if e != nil {
			reject("owned response compression failed")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.Header().Set("Content-Encoding", contentEncoding)
		_, _ = w.Write(compressed.Bytes())
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
	constructorTrime := []string{"GET /v/api/v1/sys/version", "POST /v/api/v1/login", "GET /v/api/v1/user/info", "GET /v/api/v1/mdb/list", "POST /v/api/v1/item/list"}
	constructorZ := []string{"POST /z/emby/Users/AuthenticateByName", "GET /z/emby/Library/SelectableMediaFolders", "GET /z/emby/System/Info"}
	checkWire := func(trime, zspace []string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if invalid != "" || !reflect.DeepEqual(trimeWire, trime) || !reflect.DeepEqual(zspaceWire, zspace) {
			t.Fatalf("owned host request sequence mismatch: invalid=%q trime=%v zspace=%v", invalid, trimeWire, zspaceWire)
		}
	}
	repo, indexes, downloads := ownedNativeMediaArtifact(t)
	var active *App
	t.Cleanup(func() {
		if active != nil {
			_ = active.Close()
		}
	})
	open := func() *App {
		t.Helper()
		builder, e := NewOriginalMediaServerFactoryAssembly(context.Background(), dir)
		if e != nil {
			t.Fatal(e)
		}
		a, e := New(Options{DataDir: dir, MediaServerFactories: builder})
		if e != nil {
			t.Fatal(e)
		}
		active = a
		if a.originalMediaServerAssembly == nil || len(a.originalMediaServerAssembly.definitions) != 6 {
			t.Fatal("ordinary six-factory builder missing")
		}
		return a
	}
	liveWorker := func(a *App) *originalPluginInstance {
		t.Helper()
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
			t.Fatal("real installed SDK worker missing")
		}
		return worker
	}
	waitVendors := func(a *App) {
		t.Helper()
		ownedNativeMediaWait(t, func() bool {
			h := a.originalMediaServerAssembly
			if len(h.manager.Running()) != 6 {
				return false
			}
			tr, zs := h.manager.RunningModule("TrimeMediaModule"), h.manager.RunningModule("ZSpaceModule")
			return tr != nil && zs != nil && len(tr.Instances()) == 1 && len(zs.Instances()) == 1
		})
		if _, ok := a.originalMediaServerAssembly.manager.RunningModule("TrimeMediaModule").Instances()[0].Vendor.(*mediaservervendors.TrimeMedia); !ok {
			t.Fatal("Trime config proxy substituted")
		}
		if _, ok := a.originalMediaServerAssembly.manager.RunningModule("ZSpaceModule").Instances()[0].Vendor.(*mediaservervendors.ZSpace); !ok {
			t.Fatal("ZSpace config proxy substituted")
		}
	}
	invoke := func(worker *originalPluginInstance, input any) ownedMediaRefreshResponse {
		t.Helper()
		var result ownedMediaRefreshResponse
		ownedNativeMediaCall(t, worker, "refresh", input, &result)
		if result.PID <= 0 || result.Result.Kind != "bool" || result.Result.Boolean == nil || !*result.Result.Boolean || result.Result.Library != nil {
			t.Fatal("actual vendor true result not preserved")
		}
		return result
	}
	a := open()
	settings := a.Settings()
	empty := ""
	settings.PluginMarkets, settings.PluginLocalRepoPaths = []string{}, []string{}
	settings.PluginServerURL = &empty
	settings.Roots, settings.AllowedRoots = []Root{}, []string{mediaRoot}
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	a.Start(context.Background())
	install, _ := json.Marshal(map[string]string{"plugin_id": "OwnedMediaInstanceFixture", "repo_url": repo})
	installed := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(install), cookie, "")
	assertOriginalRegistrySuccess(t, installed.Code, installed.Body.Bytes())
	worker := liveWorker(a)
	config, _ := json.Marshal([]any{
		map[string]any{"name": "trime-owned", "type": "trimemedia", "enabled": true, "config": map[string]any{"host": peer.URL + "/v", "username": "owned-trime-user", "password": "owned-trime-password"}},
		map[string]any{"name": "zspace-owned", "type": "zspace", "enabled": true, "config": map[string]any{"host": peer.URL + "/z", "username": "owned-z-user", "password": "owned-z-password"}},
	})
	posted := callTest(t, a.Handler(), "POST", "/api/system/setting/MediaServers", string(config), cookie, "")
	if posted.Code != 200 || bytes.Contains(posted.Body.Bytes(), []byte("owned-trime-password")) || bytes.Contains(posted.Body.Bytes(), []byte("owned-z-password")) {
		t.Fatal("actual admin producer status/privacy")
	}
	waitVendors(a)
	checkWire(constructorTrime, constructorZ)
	trime := invoke(worker, map[string]any{"name": "trime-owned", "root": true})
	zspace := invoke(worker, map[string]any{"name": "zspace-owned", "root": true})
	path, _ := json.Marshal(filepath.Join(mediaRoot, "Owned", "Owned.mkv"))
	item := plugins.MediaServerRefreshItem{Title: json.RawMessage(`"Owned"`), Year: json.RawMessage(`2024`), Type: plugins.MediaServerRefreshTypeMovie, Category: json.RawMessage(`"Movies"`), TargetPath: path}
	trimeItems := invoke(worker, map[string]any{"reference": trime.Instance.Reference, "items": []plugins.MediaServerRefreshItem{item}})
	zspaceItems := invoke(worker, map[string]any{"reference": zspace.Instance.Reference, "items": []plugins.MediaServerRefreshItem{item}})
	if zspace.PID != trime.PID || trimeItems.PID != trime.PID || zspaceItems.PID != trime.PID || trimeItems.Instance.Reference != trime.Instance.Reference || zspaceItems.Instance.Reference != zspace.Instance.Reference {
		t.Fatal("warm worker/retained vendor identity changed")
	}
	warmTrime := append(append([]string{}, constructorTrime...), "GET /v/api/v1/task/running", "POST /v/api/v1/mdb/scanall", "GET /v/api/v1/task/running", "POST /v/api/v1/mdb/scan/owned-lib")
	warmZ := append(append([]string{}, constructorZ...), "POST /z/emby/Library/Refresh", "GET /z/emby/Items", "POST /z/emby/Items/owned-series/Refresh")
	checkWire(warmTrime, warmZ)
	var sealed []byte
	if err = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='source_media_servers_v1' AND id='value'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("owned-trime-password")) || bytes.Contains(sealed, []byte("owned-z-password")) {
		t.Fatal("producer persisted unsealed credentials")
	}
	old := a.originalMediaServerAssembly
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
	if old.current(context.Background()) == nil {
		t.Fatal("old App admitted new vendor IO")
	}
	warmTrime = append(warmTrime, "POST /v/api/v1/user/logout")
	checkWire(warmTrime, warmZ)
	cold := open()
	cold.Start(context.Background())
	coldWorker := liveWorker(cold)
	waitVendors(cold)
	coldTrime := invoke(coldWorker, map[string]any{"name": "trime-owned", "root": true})
	coldZ := invoke(coldWorker, map[string]any{"name": "zspace-owned", "root": true})
	if coldTrime.PID <= 0 || coldTrime.PID == trime.PID || coldZ.PID != coldTrime.PID || coldTrime.Instance.Reference == trime.Instance.Reference || coldZ.Instance.Reference == zspace.Instance.Reference {
		t.Fatal("cold worker/vendor identity was not rebuilt")
	}
	var coldSealed []byte
	if err = cold.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='source_media_servers_v1' AND id='value'`).Scan(&coldSealed); err != nil || !bytes.Equal(sealed, coldSealed) {
		t.Fatal("cold sealed fact changed", err)
	}
	if err = cold.Close(); err != nil {
		t.Fatal(err)
	}
	active = nil
	expectedTrime := append(append([]string{}, warmTrime...), constructorTrime...)
	expectedTrime = append(expectedTrime, "GET /v/api/v1/task/running", "POST /v/api/v1/mdb/scanall", "POST /v/api/v1/user/logout")
	expectedZ := append(append([]string{}, warmZ...), constructorZ...)
	expectedZ = append(expectedZ, "POST /z/emby/Library/Refresh")
	checkWire(expectedTrime, expectedZ)
	if len(expectedTrime) != 18 || len(expectedZ) != 10 || indexes.Load() != 1 || downloads.Load() != 1 {
		t.Fatal("finite real host IO counts")
	}
	t.Logf("real Trime/ZSpace App generations=2 worker PIDs=%d,%d HTTPS=18+10 artifact GETs=2", trime.PID, coldTrime.PID)
}
