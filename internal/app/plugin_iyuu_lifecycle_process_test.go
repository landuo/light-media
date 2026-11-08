package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/web"
)

const (
	iyuuLifecycleSource = "IYUUAutoSeed"
	iyuuLifecycleClone  = "IYUUAutoSeedlifecycle"
	iyuuBrowserMarker   = "owned-browser-marker"
)

// Runs the source's init/stop/reload behavior against two actual Linux worker
// processes. The scan rows hit each instance's own failure cache and therefore
// never enter the tracker/add path already exercised by the seeded acceptance.
func TestIYUULinuxLifecycleAndClone(t *testing.T) {
	worker := os.Getenv("SOURCE_IYUU_WORKER")
	if runtime.GOOS != "linux" || worker == "" || os.Getenv("SOURCE_IYUU_LIFECYCLE") != "1" {
		t.Skip("requires the actual IYUU worker in the isolated Linux fixture")
	}
	ctx := t.Context()
	sourceHash := strings.Repeat("1", 40)
	cloneHash := strings.Repeat("2", 40)
	var sourceScans, cloneScans atomic.Int32
	primary := iyuuLifecycleDownloader(t, sourceHash, &sourceScans)
	secondary := iyuuLifecycleDownloader(t, cloneHash, &cloneScans)
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir, Web: web.Handler()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			_ = a.Close()
		}
	})
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "source", Kind: "qbittorrent", BaseURL: primary}, Name: "Owned source qB"},
		{Config: download.Config{ID: "clone", Kind: "qbittorrent", BaseURL: secondary}, Name: "Owned clone qB"},
	}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	manifest := installOwnedResourceProcessWorker(t, a, cookie, worker)
	if manifest.PluginID != iyuuLifecycleSource || manifest.Version != "2.22" {
		t.Fatal("unexpected IYUU lifecycle worker")
	}
	save := func(id, downloader, hash, labels string) {
		config, err := json.Marshal(map[string]any{
			"enabled": true, "cron": "13 3 * * mon", "onlyonce": true, "token": "owned-lifecycle-token",
			"downloaders": []string{downloader}, "error_caches": []string{hash}, "labelsafterseed": labels,
		})
		if err != nil {
			t.Fatal(err)
		}
		r := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/"+id, string(config), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	complete := func(id string) {
		privateAwait(t, "IYUU "+id+" one-shot finished", func() bool {
			page, err := a.queryLogs(ctx, map[string]string{"plugin_instance_id": id, "from": "0001-01-01T00:00:00Z", "limit": "100"})
			if err == nil {
				for _, row := range page.Entries {
					if row["event"] == "plugin_message" && row["message"] == "辅种任务执行完成" {
						return true
					}
				}
			}
			return false
		})
	}
	save(iyuuLifecycleSource, "Owned source qB", sourceHash, "owned-source")
	complete(iyuuLifecycleSource)
	source := originalBaseSDKLoaded(t, a, iyuuLifecycleSource)
	sourceSession := iyuuLifecycleSession(t, source, true)
	before := iyuuLifecycleConfig(t, a, iyuuLifecycleSource)

	historyKey := strings.Repeat("a", 40)
	sourceHistory := json.RawMessage(`[{"downloader":"Owned source qB","torrents":["owned-source"]}]`)
	cloneHistory := json.RawMessage(`[{"downloader":"Owned clone qB","torrents":["owned-clone"]}]`)
	if err := a.pluginStore.originalBaseSaveData(ctx, iyuuLifecycleSource, historyKey, sourceHistory); err != nil {
		t.Fatal(err)
	}
	cacheKey := strings.Repeat("a", 32) + ".torrent"
	sourceCache := iyuuLifecycleCachePath(t, a, iyuuLifecycleSource, cacheKey)
	sourceMaterial := []byte("d4:infod6:lengthi1e4:name7:src.mkvee")
	cloneMaterial := []byte("d4:infod6:lengthi1e4:name7:cln.mkvee")
	if err := os.WriteFile(sourceCache, sourceMaterial, 0600); err != nil {
		t.Fatal(err)
	}
	r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+iyuuLifecycleSource+"/clone", `{"suffix":"Lifecycle"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	clone := originalBaseSDKLoaded(t, a, iyuuLifecycleClone)
	cloneSession := iyuuLifecycleSession(t, clone, false)
	if clone == source || cloneSession == sourceSession || clone.installation.PluginID != source.installation.PluginID {
		t.Fatal("clone did not start its own actual IYUU process")
	}
	inherited := iyuuLifecycleConfig(t, a, iyuuLifecycleClone)
	var expectedClone map[string]json.RawMessage
	if err := json.Unmarshal(before.Value, &expectedClone); err != nil {
		t.Fatal(err)
	}
	expectedClone["enabled"] = json.RawMessage("false")
	expected, err := json.Marshal(expectedClone)
	if err != nil {
		t.Fatal(err)
	}
	if !originalBaseJSONEqual(inherited.Value, expected) {
		t.Fatal("clone did not inherit the source's consumed configuration")
	}
	if _, err := a.pluginStore.Data(ctx, iyuuLifecycleClone, historyKey); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("source history was copied to clone", err)
	}
	cloneRoot, err := a.originalBaseDataPath(ctx, iyuuLifecycleClone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cloneRoot, "torrents", cacheKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source torrent cache was copied to clone", err)
	}
	save(iyuuLifecycleClone, "Owned clone qB", cloneHash, "owned-clone")
	complete(iyuuLifecycleClone)
	cloneCache := iyuuLifecycleCachePath(t, a, iyuuLifecycleClone, cacheKey)
	if err := os.WriteFile(cloneCache, cloneMaterial, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.originalBaseSaveData(ctx, iyuuLifecycleClone, historyKey, cloneHistory); err != nil {
		t.Fatal(err)
	}
	if saved := iyuuLifecycleConfig(t, a, iyuuLifecycleSource); saved.Revision != before.Revision || !bytes.Equal(saved.Value, before.Value) {
		t.Fatal("clone configuration save changed source config")
	}

	for _, instance := range []struct {
		id      string
		object  *originalPluginInstance
		session *plugins.Client
	}{
		{iyuuLifecycleSource, source, sourceSession},
		{iyuuLifecycleClone, clone, cloneSession},
	} {
		r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+instance.id+"/reload", `{}`, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		if replacement := originalBaseSDKLoaded(t, a, instance.id); replacement == instance.object || iyuuLifecycleSession(t, replacement, true) == instance.session {
			t.Fatal("reload did not replace the actual IYUU session", instance.id)
		}
		var state bool
		if err := instance.object.readSession(ctx, "get_state", &state); err == nil {
			t.Fatal("old IYUU session remained callable", instance.id)
		}
	}
	iyuuLifecycleNoReplay(t, &sourceScans, &cloneScans, "reload")
	hotSource := originalBaseSDKLoaded(t, a, iyuuLifecycleSource)
	hotClone := originalBaseSDKLoaded(t, a, iyuuLifecycleClone)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, old := range []*originalPluginInstance{hotSource, hotClone} {
		old.sessionMu.Lock()
		active := old.session != nil
		old.sessionMu.Unlock()
		if active {
			t.Fatal("host shutdown left an IYUU process attached")
		}
	}
	a = nil
	a, err = New(Options{DataDir: dir, Web: web.Handler()})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	coldSource := originalBaseSDKLoaded(t, a, iyuuLifecycleSource)
	coldClone := originalBaseSDKLoaded(t, a, iyuuLifecycleClone)
	if coldSource == hotSource || coldClone == hotClone || iyuuLifecycleSession(t, coldSource, true) == iyuuLifecycleSession(t, coldClone, true) {
		t.Fatal("cold startup did not open two fresh IYUU processes")
	}
	iyuuLifecycleNoReplay(t, &sourceScans, &cloneScans, "cold startup")
	iyuuLifecycleExpectConfig(t, a, iyuuLifecycleSource, "Owned source qB", sourceHash, "owned-source")
	iyuuLifecycleExpectConfig(t, a, iyuuLifecycleClone, "Owned clone qB", cloneHash, "owned-clone")
	for _, row := range []struct {
		id, cache string
		history   json.RawMessage
		material  []byte
	}{
		{iyuuLifecycleSource, sourceCache, sourceHistory, sourceMaterial},
		{iyuuLifecycleClone, cloneCache, cloneHistory, cloneMaterial},
	} {
		data, err := a.pluginStore.Data(ctx, row.id, historyKey)
		if err != nil || !originalBaseJSONEqual(data.Value, row.history) {
			t.Fatal("cold IYUU history crossed instances or disappeared", row.id, err)
		}
		content, err := os.ReadFile(row.cache)
		if err != nil || !bytes.Equal(content, row.material) {
			t.Fatal("cold IYUU torrent cache crossed instances or disappeared", row.id, err)
		}
	}
	t.Log("actual IYUU Linux source/clone sessions reloaded and cold-started; each consumed one-shot ran once; distinct configuration, history and torrent cache namespaces survived")
	if os.Getenv("SOURCE_IYUU_LIFECYCLE_BROWSER") == "1" {
		iyuuLifecycleBrowser(t, a)
		iyuuLifecycleExpectConfig(t, a, iyuuLifecycleSource, "Owned source qB", sourceHash, "owned-source")
		iyuuLifecycleExpectConfig(t, a, iyuuLifecycleClone, "Owned clone qB", cloneHash, iyuuBrowserMarker)
		iyuuLifecycleNoReplay(t, &sourceScans, &cloneScans, "clone browser save")
		t.Log("browser clone form saved its marker to the actual cold App without changing source configuration")
	}
}

func iyuuLifecycleDownloader(t *testing.T, hash string, scans *atomic.Int32) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "owned-lifecycle", Value: "proof", Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/webapiVersion":
			_, _ = io.WriteString(w, "2.11.4")
		case "/api/v2/torrents/info":
			if err := r.ParseForm(); err != nil || r.Form.Get("filter") != "completed" {
				t.Error("lifecycle fixture received a non-scan qB request", err)
			}
			scans.Add(1)
			_ = json.NewEncoder(w).Encode([]map[string]any{seededIYUUTorrent(hash, "uploading", "owned", 1)})
		default:
			t.Error("unexpected owned qB lifecycle request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func iyuuLifecycleSession(t *testing.T, object *originalPluginInstance, enabled bool) *plugins.Client {
	t.Helper()
	var state bool
	if err := object.readSession(t.Context(), "get_state", &state); err != nil || state != enabled {
		t.Fatal("actual IYUU worker did not restore the expected schedule state", object.installation.InstanceID, err)
	}
	object.sessionMu.Lock()
	defer object.sessionMu.Unlock()
	if object.session == nil {
		t.Fatal("IYUU worker session was missing")
	}
	return object.session
}

func iyuuLifecycleConfig(t *testing.T, a *App, id string) PluginStoredValue {
	t.Helper()
	stored, err := a.pluginStore.Config(t.Context(), id)
	if err != nil {
		t.Fatal("IYUU configuration missing", id, err)
	}
	return stored
}

func iyuuLifecycleExpectConfig(t *testing.T, a *App, id, downloader, hash, labels string) {
	t.Helper()
	var config struct {
		Enabled     bool     `json:"enabled"`
		OnlyOnce    *bool    `json:"onlyonce"`
		Downloaders []string `json:"downloaders"`
		ErrorCache  []string `json:"error_caches"`
		Labels      string   `json:"labelsafterseed"`
	}
	stored := iyuuLifecycleConfig(t, a, id)
	if json.Unmarshal(stored.Value, &config) != nil || !config.Enabled || config.OnlyOnce == nil || *config.OnlyOnce ||
		len(config.Downloaders) != 1 || config.Downloaders[0] != downloader || len(config.ErrorCache) != 1 ||
		config.ErrorCache[0] != hash || config.Labels != labels {
		t.Fatal("IYUU consumed flag or independent configuration diverged", id, config)
	}
}

func iyuuLifecycleCachePath(t *testing.T, a *App, id, key string) string {
	t.Helper()
	dir, err := a.originalBaseDataPath(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "torrents")
	if info, err := os.Stat(cache); err != nil || !info.IsDir() {
		t.Fatal("actual worker did not create private torrent cache", id, err)
	}
	return filepath.Join(cache, key)
}

func iyuuLifecycleNoReplay(t *testing.T, source, clone *atomic.Int32, phase string) {
	t.Helper()
	time.Sleep(4 * time.Second)
	if source.Load() != 1 || clone.Load() != 1 {
		t.Fatal("IYUU consumed one-shot ran again after "+phase, source.Load(), clone.Load())
	}
}

func iyuuLifecycleBrowser(t *testing.T, a *App) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:5191")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err := os.WriteFile("/evidence/browser-ready", []byte("127.0.0.1:5191\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("browser-ready: actual cold App is serving 127.0.0.1:5191")
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/evidence/browser-done"); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("browser handoff timed out")
}
