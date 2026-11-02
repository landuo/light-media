package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// This gate installs the unchanged real SDK worker. It does not use a direct
// HostCaller stub, synthetic EventManager or direct core-consumer invocation.
func privateTLSEventWorker(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	path := os.Getenv("OWNED_INPUT_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("bound real OwnedInput worker required")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, path, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal("owned worker manifest", err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedInput" || manifest.Entry != "owned-input-fixture" {
		t.Fatal("wrong real fixture identity")
	}
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(binary)), HostContract: pluginResourcesSDKContract}}}}}
	var mu sync.Mutex
	counts := map[string]int{}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{ErrorLog: log.New(io.Discard, "", 0), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.IsAbs() || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected owned artifact request")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(binary)
		default:
			t.Error("unexpected owned artifact path", r.URL.Path)
			w.WriteHeader(400)
		}
	})}}
	// Populate index before accepting requests; there is no upstream or fallback.
	index.Releases[0].Artifacts[0].URL = "http://" + listener.Addr().String() + "/worker"
	peer.Start()
	t.Cleanup(peer.Close)
	req, _ := http.NewRequest("GET", peer.URL+"/index", nil)
	base := http.DefaultTransport.(*http.Transport)
	if base.Proxy != nil {
		proxy, e := base.Proxy(req)
		if e != nil || proxy != nil {
			t.Fatal("unchanged route disallows owned artifact peer; no fallback")
		}
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	// Only an explicit owned empty center URL and synthetic per-instance API key.
	// Keep the share default; do not clear ProxyHost/env or alter module guards.
	empty := ""
	settings.PluginServerURL = &empty
	settings.APIToken = "owned-event-private-tls-api-key"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.closeOriginalSessions)
	body, _ := json.Marshal(map[string]string{"plugin_id": "OwnedInput", "repo_url": peer.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	config := `{"enabled":true,"event_controls":[{"action":"add","kind":"broadcast","type":"download.deleted","method":"event_dynamic"},{"action":"add","kind":"broadcast","type":"site.refreshed","method":"event_dynamic"},{"action":"add","kind":"broadcast","type":"metadata.scrape","method":"event_dynamic"}]}`
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", config, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	mu.Lock()
	actualCounts := map[string]int{}
	for k, v := range counts {
		actualCounts[k] = v
	}
	mu.Unlock()
	if !reflect.DeepEqual(actualCounts, map[string]int{"/index": 1, "/worker": 1}) {
		t.Fatal("actual owned installation routes", actualCounts)
	}
	snapshot := ownedInputAPI(t, a, "GET", "/api/v1/plugin/OwnedInput/events", "")
	var identity struct {
		PID int `json:"pid"`
	}
	if snapshot.Code != 200 || json.Unmarshal(snapshot.Body.Bytes(), &identity) != nil || identity.PID <= 0 || identity.PID == os.Getpid() {
		t.Fatal("actual installed worker identity")
	}
	t.Logf("installed owned worker pid=%d artifact_gets=2", identity.PID)
	return a, cookie
}

func privateTLSSendEvent(t *testing.T, a *App, cookie *http.Cookie, kind string, data any) {
	t.Helper()
	input, _ := json.Marshal(plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: kind, Data: mustCleanSeedJSON(data)})
	// HTTP -> real installed worker -> plugins.SendEvent -> current host scope -> dispatcher.
	ownedSDKEventCall(t, a, cookie, "event.send", string(input))
	a.wg.Wait()
}
func privateTLSRecordedEvents(t *testing.T, a *App, cookie *http.Cookie, kind string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	for _, raw := range ownedInputEvents(t, a, cookie, "OwnedInput") {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &event) != nil {
			t.Fatal("invalid actual event envelope")
		}
		if event.Type == kind {
			out = append(out, raw)
		}
	}
	return out
}
func privateTLSEnvUnchanged(t *testing.T, env []string, transport http.RoundTripper) {
	t.Helper()
	if http.DefaultTransport != transport || !reflect.DeepEqual(os.Environ(), env) {
		t.Fatal("global transport/environment changed")
	}
}

func TestOriginalEventPrivateTLSDownloadDeleted(t *testing.T) {
	env, transport := os.Environ(), http.DefaultTransport
	for _, kind := range []string{"qb", "tr"} {
		t.Run(kind, func(t *testing.T) {
			a, cookie := privateTLSEventWorker(t)
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "owned-not-a-real-download")
			if err := os.WriteFile(sentinel, []byte("owned bytes remain"), 0600); err != nil {
				t.Fatal(err)
			}
			protocol := &coreIOTorrentPeer{t: t, kind: kind, hash: strings.Repeat("b", 40), dir: dir, owned: true}
			peer := coreIOOwnedTLS(t, http.HandlerFunc(protocol.serve))
			d := DownloaderConfig{Config: download.Config{ID: "owned-event-" + kind, Kind: kind, BaseURL: peer.URL}, Name: "Owned TLS event", DownloadPath: dir}
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{d}
			settings.DefaultDownloader = d.ID
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			privateTLSSendEvent(t, a, cookie, "downloadfile.deleted", map[string]string{"hash": protocol.hash})
			calls, deletes, tags := protocol.snapshot()
			want := []string{"list", "list", "remove_keep_data"}
			if kind == "tr" {
				want = []string{"session", "list", "session", "list", "remove_keep_data"}
			}
			if !reflect.DeepEqual(calls, want) || deletes != 1 || tags != 0 {
				t.Fatal("real event private wire", calls, deletes, tags)
			}
			events := privateTLSRecordedEvents(t, a, cookie, "download.deleted")
			if len(events) != 1 {
				t.Fatal("actual downstream worker event count", len(events))
			}
			var event struct {
				Data struct {
					Hash     string `json:"hash"`
					Torrents []struct {
						Hash       string `json:"hash"`
						Downloader string `json:"downloader"`
					} `json:"torrents"`
				} `json:"data"`
			}
			if json.Unmarshal(events[0], &event) != nil || event.Data.Hash != protocol.hash || len(event.Data.Torrents) != 1 || event.Data.Torrents[0].Hash != protocol.hash || event.Data.Torrents[0].Downloader != d.Name {
				t.Fatal("real default consumer emitted wrong owned torrent DTO")
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "owned bytes remain" {
				t.Fatal("keep-data path changed", err)
			}
			t.Logf("event=%s kind=%s wire=%v downstream_events=1 sentinel_unchanged=true", "downloadfile.deleted", kind, calls)
		})
	}
	privateTLSEnvUnchanged(t, env, transport)
}

func TestOriginalEventPrivateTLSMetadataImages(t *testing.T) {
	env, transport := os.Environ(), http.DefaultTransport
	a, cookie := privateTLSEventWorker(t)
	var mu sync.Mutex
	var routes []string
	peer := coreIOOwnedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.IsAbs() || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected owned image request")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		routes = append(routes, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/poster.jpg":
			_, _ = w.Write([]byte("owned-event-poster"))
		case "/backdrop.jpg":
			_, _ = w.Write([]byte("owned-event-backdrop"))
		default:
			t.Error("unexpected owned image path", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(originalMetadataImageClient.CloseIdleConnections)
	dir := t.TempDir()
	source := filepath.Join(dir, "Owned.TLS.Movie.2024.mkv")
	if err := os.WriteFile(source, []byte("owned synthetic movie"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.Roots = []Root{{ID: "owned-event-images", Path: dir, Kind: "mixed"}}
	settings.AllowedRoots = []string{dir}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	item, err := originalLocalFileItem(source)
	if err != nil {
		t.Fatal(err)
	}
	// Both explicit image fields avoid any catalog/recognition query in this finite gate.
	info := map[string]any{"type": "电影", "title": "Owned TLS Movie", "tmdb_id": 900043, "poster_path": peer.URL + "/poster.jpg", "backdrop_path": peer.URL + "/backdrop.jpg"}
	privateTLSSendEvent(t, a, cookie, "metadata.scrape", map[string]any{"fileitem": item, "mediainfo": info, "overwrite": false})
	for name, want := range map[string]string{"poster.jpg": "owned-event-poster", "backdrop.jpg": "owned-event-backdrop", "fanart.jpg": "owned-event-backdrop"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want {
			t.Fatal("real event image sidecar", name, err)
		}
	}
	nfo, err := os.ReadFile(filepath.Join(dir, "Owned.TLS.Movie.2024.nfo"))
	if err != nil || !bytes.Contains(nfo, []byte("Owned TLS Movie")) {
		t.Fatal("existing native NFO chain failed", err)
	}
	mu.Lock()
	actual := append([]string(nil), routes...)
	mu.Unlock()
	if !reflect.DeepEqual(actual, []string{"/backdrop.jpg", "/backdrop.jpg", "/poster.jpg"}) {
		t.Fatal("fixed image HTTP effects", actual)
	}
	if len(privateTLSRecordedEvents(t, a, cookie, "metadata.scrape")) != 1 {
		t.Fatal("real worker metadata broadcast absent")
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "owned synthetic movie" {
		t.Fatal("source movie changed", err)
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("ordinary event minted an asset", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 5 {
		t.Fatal("sidecar stage residue", err, len(files))
	}
	t.Logf("event=metadata.scrape https_requests=%d image_sidecars=3 nfo=1 source_unchanged=true", len(actual))
	privateTLSEnvUnchanged(t, env, transport)
}

func TestOriginalEventPrivateTLSSiteUpdated(t *testing.T) {
	env, transport := os.Environ(), http.DefaultTransport
	a, cookie := privateTLSEventWorker(t)
	var mu sync.Mutex
	var routes []string
	home := `<html><head><link rel="icon" href="/favicon.ico"></head><body><a href="logout.php">out</a><a href="userdetails.php?id=7">owned-user</a><div>上传：2 GB 下载：1 GB 分享率：2</div></body></html>`
	peer := coreIOOwnedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.IsAbs() || r.Header.Get("Cookie") != "owned-site-cookie" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected owned site request")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		routes = append(routes, r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.RequestURI() {
		case "/":
			_, _ = io.WriteString(w, home)
		case "/userdetails.php?id=7":
			_, _ = io.WriteString(w, `<a href="logout.php">out</a><table><tr><td>等级</td><td>User</td></tr></table>`)
		case "/getusertorrentlistajax.php?userid=7&type=seeding":
			_, _ = io.WriteString(w, `<table><tr><td>名称</td><td>大小</td><td>在做种</td></tr></table>`)
		case "/favicon.ico":
			_, _ = w.Write([]byte("owned-event-icon"))
		default:
			t.Error("unexpected owned site path", r.URL.RequestURI())
			w.WriteHeader(400)
		}
	}))
	// Existing known ubits allows owned loopback. No Nexus/public DNS bypass.
	site := installResourceBrowseSite(t, a, peer.URL)
	if !site.Enabled || site.Kind != "ubits" || site.Proxy {
		t.Fatal("owned site fixture boundary")
	}
	privateTLSSendEvent(t, a, cookie, "site.updated", map[string]string{"domain": peer.URL})
	mu.Lock()
	actual := append([]string(nil), routes...)
	mu.Unlock()
	sort.Strings(actual)
	want := []string{"/", "/", "/favicon.ico", "/getusertorrentlistajax.php?userid=7&type=seeding", "/userdetails.php?id=7"}
	sort.Strings(want)
	// userdata/icon are independent goroutines, serialized per-site, not a fixed cross-consumer order.
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("actual private userdata/icon HTTP requests", actual)
	}
	var sealed []byte
	var account siteSnapshot
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.openSiteAccountSnapshot("site_stats_latest", site.ID, sealed, &account); err != nil || account.SourceData == nil || account.SourceData.Username == nil || *account.SourceData.Username != "owned-user" || account.SourceData.Upload != 2*1073741824 || account.Connection.Status != "ok" {
		t.Fatal("actual native TLS account commit", err, account.Connection.Status)
	}
	if bytes.Contains(sealed, []byte("owned-user")) {
		t.Fatal("account payload was not sealed")
	}
	var record struct{ Sealed []byte }
	var icon originalSiteIcon
	if err := a.Store.Get(context.Background(), "site_icon", site.ID, &record); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Unseal(record.Sealed, &icon); err != nil || icon.SiteID != site.ID || icon.URL != peer.URL+"/favicon.ico" || string(icon.Data) != "owned-event-icon" {
		t.Fatal("actual native TLS icon commit", err)
	}
	response := callTest(t, a.Handler(), "GET", "/api/sites/manage/"+site.ID+"/icon", "", cookie, "")
	if response.Code != 200 || response.Body.String() != "owned-event-icon" {
		t.Fatal("real authenticated icon response", response.Code)
	}
	if count := len(privateTLSRecordedEvents(t, a, cookie, "site.refreshed")); count != 1 {
		t.Fatal("actual refreshed worker event", count)
	}
	t.Logf("event=site.updated https_requests=%d userdata_and_icon_sealed=true downstream_events=1", len(actual))
	privateTLSEnvUnchanged(t, env, transport)
}
