package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/sites"
)

const brushPackageFixtureID = "BrushFlowLanduo"

func TestBrushFlowInstalledAMD64Package(t *testing.T) {
	bundle := os.Getenv("SOURCE_BRUSH_FLOW_PACKAGE_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the exact BrushFlow package in an owned Linux amd64 fixture")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("TZ", "Asia/Shanghai")
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	const title = "Owned promotion safety release"
	torrent, hash := workflowTorrent("owned-brush-promotion.mkv")
	downloadRoot := t.TempDir()
	var state struct {
		sync.Mutex
		taskID, tags              string
		adds, starts, samples     int
		pausedOnAdd, savedAtStart bool
	}
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		defer state.Unlock()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-brush-fixture", Path: "/"})
			io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			io.WriteString(w, "v5.0.5")
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.4")
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]any{"save_path": downloadRoot, "auto_tmm_enabled": false})
		case "/api/v2/torrents/categories":
			workflowJSON(w, map[string]any{})
		case "/api/v2/transfer/info":
			state.samples++
			workflowJSON(w, map[string]int{"up_info_speed": 512, "dl_info_speed": 1024, "up_info_data": 1024, "dl_info_data": 2048})
		case "/api/v2/torrents/tags":
			workflowJSON(w, []string{})
		case "/api/v2/torrents/info":
			if state.adds == 0 {
				workflowJSON(w, []any{})
				return
			}
			task := workflowTask(hash, 0)
			task["name"], task["save_path"], task["tags"] = title, downloadRoot, state.tags
			if state.starts > 0 {
				task["state"] = "downloading"
			}
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Error(err)
				http.Error(w, "owned fixture requires a torrent upload", 400)
				return
			}
			state.adds++
			state.tags = r.FormValue("tags")
			state.pausedOnAdd = r.FormValue("stopped") == "True" && r.FormValue("paused") == "True"
			if r.FormValue("savepath") != downloadRoot || !strings.Contains(state.tags, "已整理") || !strings.Contains(state.tags, "刷流-") {
				t.Error("source Add did not preserve the explicit folder and task labels")
			}
			file, _, err := r.FormFile("torrent__0")
			if err != nil {
				t.Error(err)
			} else {
				received, readErr := io.ReadAll(file)
				file.Close()
				if readErr != nil || string(received) != string(torrent) {
					t.Error("source Add did not receive the exact torrent material")
				}
			}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/removeTags", "/api/v2/torrents/deleteTags":
			_ = r.ParseForm()
			if tags := strings.Split(state.tags, ","); len(tags) == 4 {
				state.tags = strings.Join(tags[:3], ",")
			}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/start", "/api/v2/torrents/resume":
			state.starts++
			key := "task." + state.taskID + ".torrents"
			raw, err := a.pluginStore.originalBaseGetData(r.Context(), brushPackageFixtureID, &key)
			var records map[string]struct {
				ClockVersion int `json:"promotion_clock_version"`
			}
			state.savedAtStart = err == nil && json.Unmarshal(raw, &records) == nil && records[hash].ClockVersion == 2
			io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML(title, "", true))
		case "/details.php":
			end := time.Now().In(time.FixedZone("UTC+8", 8*3600)).Add(2 * time.Hour).Format(time.DateTime)
			fmt.Fprintf(w, `<a href="download.php?id=101">下载</a><img class="pro_free">剩余时间<span title="%s">2h</span>`, end)
		case "/download.php":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-brush-site", Name: "Owned brush site", Kind: "jiayuan", BaseURL: tracker.URL, Cookie: "owned=brush-fixture"}, Enabled: true}}
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-brush-qb", Kind: "qbittorrent", BaseURL: qb.URL}, Name: "Owned qB", DownloadPath: downloadRoot}}
	settings.AllowedRoots = []string{downloadRoot}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	installBrushCandidate(t, a, cookie, bundle)
	identity, err := a.Store.nativeSiteByAppID(t.Context(), "owned-brush-site")
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := a.Store.nativeSourcePublicSiteID(t.Context(), a.Store.DB, identity)
	if err != nil || sourceID < 1 {
		t.Fatal("site did not retain its source ID", err)
	}
	const base = "/api/plugins/" + brushPackageFixtureID
	call := func(method, path string, body any) json.RawMessage {
		t.Helper()
		var wire string
		if body != nil {
			wire = string(mustCleanSeedJSON(body))
		}
		response := callTest(t, a.Handler(), method, base+"/api"+path, wire, cookie, "")
		var result struct {
			Success bool
			Data    json.RawMessage
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Success {
			t.Fatalf("BrushFlow %s %s failed: %d %s", method, path, response.Code, response.Body.String())
		}
		return result.Data
	}
	call("POST", "/settings", map[string]any{"enabled": true})
	created := call("POST", "/tasks", map[string]any{"name": "Owned protected brush", "enabled": true, "site_id": sourceID, "downloader": "Owned qB",
		"save_path": downloadRoot, "freeleech": "free", "del_no_free": true, "notify": false, "timezone_offset": 8})
	var detail struct{ Task struct{ ID string } }
	if json.Unmarshal(created, &detail) != nil || detail.Task.ID == "" {
		t.Fatal("source task creation did not return the persistent task identity")
	}
	state.Lock()
	taskID := detail.Task.ID
	state.taskID = taskID
	state.Unlock()
	call("POST", "/tasks/"+taskID+"/run", nil)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		state.Lock()
		if state.starts > 0 {
			if state.adds != 1 || state.starts != 1 || state.samples != 5 || !state.pausedOnAdd || !state.savedAtStart {
				t.Errorf("source order not preserved: adds=%d starts=%d samples=%d paused=%t saved=%t", state.adds, state.starts, state.samples, state.pausedOnAdd, state.savedAtStart)
			}
			state.Unlock()
			if t.Failed() {
				return
			}
			t.Log("native qB protocol saw five source samples, one paused Add, and Start after the exact hash and v2 clock were durable")
			return
		}
		state.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	stateDetail := call("GET", "/tasks/"+taskID, nil)
	t.Fatalf("actual protected source run did not reach the downloader start: %s", stateDetail)
}

func installBrushCandidate(t *testing.T, a *App, cookie *http.Cookie, bundle string) {
	t.Helper()
	const archiveName = "BrushFlowLanduo-5.2.18-linux-amd64.zip"
	raw, err := os.ReadFile(filepath.Join(bundle, artifacts.IndexName))
	if err != nil {
		t.Fatal(err)
	}
	var index artifacts.RepositoryIndex
	if json.Unmarshal(raw, &index) != nil || index.SchemaVersion != 1 || len(index.Plugins) != 1 ||
		index.Plugins[0].PluginID != brushPackageFixtureID || index.Plugins[0].CurrentVersion != "5.2.18" || len(index.Plugins[0].Releases) != 1 || len(index.Plugins[0].Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected BrushFlow candidate index")
	}
	release := &index.Plugins[0].Releases[0]
	artifact := &release.Artifacts[0]
	archive, err := os.ReadFile(filepath.Join(bundle, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if release.Manifest.Frontend == nil || len(release.Manifest.Frontend.Exposes) != 4 || artifact.HostContract != pluginResourcesSDKContract || artifact.SHA256 != digest {
		t.Fatal("candidate did not declare the exact SDK and native frontend")
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + artifacts.IndexName:
			_ = json.NewEncoder(w).Encode(index)
		case "/" + archiveName:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	artifact.URL = repository.URL + "/" + archiveName
	body := mustCleanSeedJSON(map[string]string{"plugin_id": brushPackageFixtureID, "repo_url": repository.URL + "/" + artifacts.IndexName})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	originalBaseSDKLoaded(t, a, brushPackageFixtureID)
	t.Logf("installed indexed Linux amd64 worker and Vue bundle %s", digest)
}
