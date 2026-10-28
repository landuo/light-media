package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"light-media/internal/download"
	"light-media/internal/metainfo"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/sites"
)

func ownedDownloadModuleArtifact(t *testing.T, variable string) (plugins.Manifest, string) {
	t.Helper()
	path := os.Getenv(variable)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(path, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedDownloaderControl" {
		t.Fatal("actual owned download artifact", err)
	}
	hash := sha256.Sum256(payload)
	sum := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repo.Close)
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	return manifest, repo.URL + "/index"
}

// The owned provider receives real metainfo bytes over the actual SDK and
// issues its own protocol-side task creation. The peer records the task, which
// the host must independently read before accepting a module tuple.
type ownedDownloadModulePeer struct {
	mu                      sync.Mutex
	Directory               string
	Torrent                 []byte
	Hash                    string
	Present                 bool
	Adds, Forces, Overrides int
	Input                   json.RawMessage
	CheckSourceCategory     bool
	ExpectedSourceCategory  *string
}

func (p *ownedDownloadModulePeer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/api/v2/torrents/info":
		items := []any{}
		if p.Present {
			task := workflowTask(p.Hash, 0)
			task["save_path"], task["tags"] = p.Directory, "light-media"
			items = append(items, task)
		}
		workflowJSON(w, items)
	case "/api/v2/app/preferences":
		workflowJSON(w, map[string]any{"save_path": p.Directory})
	case "/api/v2/torrents/add":
		if r.ParseMultipartForm(4<<20) != nil || r.FormValue("savepath") != p.Directory || r.FormValue("stopped") != "false" {
			http.Error(w, "owned Add input changed", 400)
			return
		}
		if p.CheckSourceCategory {
			want := "false"
			if p.ExpectedSourceCategory != nil {
				want = "true"
			}
			if r.FormValue("autoTMM") != want || p.ExpectedSourceCategory != nil && r.FormValue("category") != *p.ExpectedSourceCategory {
				http.Error(w, "owned source category changed", 400)
				return
			}
		}
		file, _, err := r.FormFile("torrents")
		if err != nil {
			http.Error(w, "owned metainfo absent", 400)
			return
		}
		data, _ := io.ReadAll(file)
		_ = file.Close()
		if !bytes.Equal(data, p.Torrent) {
			http.Error(w, "owned metainfo changed", 400)
			return
		}
		p.Adds++
		p.Present = true
		_, _ = io.WriteString(w, "Ok.")
	case "/api/v2/torrents/setForceStart":
		if r.ParseForm() != nil || r.FormValue("hashes") != p.Hash || r.FormValue("value") != "true" {
			http.Error(w, "owned force input changed", 400)
			return
		}
		p.Forces++
	case "/module-add":
		body, err := io.ReadAll(r.Body)
		var input struct {
			Args   []json.RawMessage
			Kwargs map[string]json.RawMessage
		}
		if err != nil || json.Unmarshal(body, &input) != nil || len(input.Args) != 0 || len(input.Kwargs) != 7 {
			http.Error(w, "owned module input changed", 400)
			return
		}
		content, ok := plugins.DecodeModuleResultBytes(input.Kwargs["content"])
		material, err := metainfo.ParseMaterial(content)
		var path string
		if !ok || !bytes.Equal(content, p.Torrent) || err != nil || !sameHash(material.Hash, p.Hash) || json.Unmarshal(input.Kwargs["download_dir"], &path) != nil || path != p.Directory {
			http.Error(w, "owned module material/path changed", 400)
			return
		}
		p.Overrides++
		p.Present = true
		p.Input = body
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}
func (p *ownedDownloadModulePeer) counts() (int, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Adds, p.Forces, p.Overrides
}

func TestOriginalDownloadNativeHostActualSDKAndProtocol(t *testing.T) {
	for _, boundary := range []string{"tuple_owns_add", "false_terminal", "empty_list_after_core", "plugin_exception_core", "unconfirmed_tuple"} {
		t.Run(boundary, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			directory, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			torrent, hash := workflowTorrent("Owned.Download.Module.2024.mkv")
			peerState := &ownedDownloadModulePeer{Directory: directory, Torrent: torrent, Hash: hash}
			peer := httptest.NewServer(http.HandlerFunc(peerState.serveHTTP))
			defer peer.Close()
			d := DownloaderConfig{Config: download.Config{ID: "owned-download", Kind: "qb", BaseURL: peer.URL, ForceResume: true, AutoCategory: true}, Name: "Owned downloader"}
			peerState.CheckSourceCategory = true
			a.config.AllowedRoots = []string{directory}
			a.config.Downloaders = []DownloaderConfig{d}
			a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-site", Cookie: "owned-cookie"}}}
			manifest, index := ownedDownloadModuleArtifact(t, "OWNED_DOWNLOADER_CONTROL_FIXTURE_BINARY")
			install, _ := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": index})
			r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(install), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			var value any = map[string]any{plugins.ModuleTupleKey: []any{"Owned downloader", hash, "Original", ""}}
			endpoint := ""
			if boundary == "tuple_owns_add" {
				endpoint = peer.URL + "/module-add"
			}
			if boundary == "false_terminal" {
				value = false
			}
			if boundary == "empty_list_after_core" {
				value = []any{}
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "module": "download", "result": value, "endpoint": endpoint, "fail": boundary == "plugin_exception_core"})
			r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(config), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			ref, err := a.persistMaterial(context.Background(), torrent, hash)
			if err != nil {
				t.Fatal(err)
			}
			in := NativeDownload{ID: "native_owned_download", SiteID: "owned-site", ResourceTitle: "Owned.Download.Module.2024", Hash: hash, MaterialRef: ref, DownloaderID: d.ID, DownloaderSnapshot: downloaderSnapshot(d), DownloadPath: directory, SourceDownloader: json.RawMessage(`null`), State: "planned"}
			if err = a.Store.Put(context.Background(), "native_download", in.ID, in); err != nil {
				t.Fatal(err)
			}
			job := importClaim(t, a, "native_download", in.ID)
			err = a.runNativeDownload(context.Background(), job, in.ID)
			accepted := boundary == "tuple_owns_add" || boundary == "plugin_exception_core"
			if (err == nil) != accepted {
				t.Fatal("source caller branch differs", boundary, err)
			}
			var stored NativeDownload
			if loadErr := a.Store.Get(context.Background(), "native_download", in.ID, &stored); loadErr != nil || stored.SourceModuleStarted != true || (stored.State == "accepted") != accepted {
				t.Fatal("actual host module state", stored, loadErr)
			}
			adds, forces, overrides := peerState.counts()
			wantCore := 0
			if boundary == "empty_list_after_core" || boundary == "plugin_exception_core" {
				wantCore = 1
			}
			wantOverride := 0
			if boundary == "tuple_owns_add" {
				wantOverride = 1
			}
			if adds != wantCore || forces != wantCore || overrides != wantOverride {
				t.Fatal("actual native/core versus provider side effects", adds, forces, overrides)
			}
			before := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", "", cookie, "")
			if before.Code != 200 {
				t.Fatal(before.Code, before.Body.String())
			}
			var snap struct {
				Calls, PID int
				Input      struct{ Kwargs map[string]json.RawMessage }
			}
			if json.Unmarshal(before.Body.Bytes(), &snap) != nil || snap.Calls != 1 || snap.PID <= 0 || len(snap.Input.Kwargs) != 7 || string(snap.Input.Kwargs["cookie"]) != `"owned-cookie"` || string(snap.Input.Kwargs["downloader"]) != `null` || string(snap.Input.Kwargs["category"]) != `null` || string(snap.Input.Kwargs["episodes"]) != `null` {
				t.Fatal("actual SDK source kwargs", before.Body.String())
			}
			// A retry/cold record must not call the module again, including []
			// after a real native Add. The source stack never replays that call.
			if retryErr := a.runNativeDownload(context.Background(), job, in.ID); accepted && retryErr != nil || !accepted && (retryErr == nil || !strings.Contains(retryErr.Error(), "插件已调用")) {
				t.Fatal("unsubmitted module call was replayed", retryErr)
			}
			after := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", "", cookie, "")
			var afterSnap struct{ Calls int }
			_ = json.Unmarshal(after.Body.Bytes(), &afterSnap)
			if afterSnap.Calls != 1 {
				t.Fatal("module replay", after.Body.String())
			}
		})
	}
}

func TestOriginalDownloadIntentHostActualSDKAndProtocol(t *testing.T) {
	for _, boundary := range []string{"tuple_owns_add", "empty_list_after_core", "plugin_exception_core"} {
		t.Run(boundary, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			directory, library := filepath.Join(base, "downloads"), filepath.Join(base, "library")
			if err = os.MkdirAll(directory, 0750); err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(library, 0750); err != nil {
				t.Fatal(err)
			}
			torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
			state := &ownedDownloadModulePeer{Directory: directory, Torrent: torrent, Hash: hash}
			peer := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
			defer peer.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
				case "/download.php":
					_, _ = w.Write(torrent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, peer.URL)
			a.config.Downloaders[0].Name = "Owned downloader"
			a.config.Downloaders[0].ForceResume = true
			a.config.Downloaders[0].AutoCategory = true
			category := "华语电影"
			state.CheckSourceCategory = true
			state.ExpectedSourceCategory = &category
			a.config.Downloaders[0].DownloadPath = directory
			a.config.AllowedRoots = []string{directory}
			a.config.Roots = []Root{{ID: "root", Path: library, Kind: "mixed"}}
			a.config.Directories = []DirectoryRule{{ID: "owned-download", SourcePath: directory, RootID: "root", MediaType: "movie", Mode: "hardlink"}}
			media := importFixture(t, a, "movie")
			manifest, index := ownedDownloadModuleArtifact(t, "OWNED_DOWNLOADER_CONTROL_FIXTURE_BINARY")
			install, _ := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": index})
			r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(install), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			var value any = map[string]any{plugins.ModuleTupleKey: []any{"Owned downloader", hash, "Original", ""}}
			endpoint := ""
			if boundary == "tuple_owns_add" {
				endpoint = peer.URL + "/module-add"
			}
			if boundary == "empty_list_after_core" {
				value = []any{}
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "module": "download", "result": value, "endpoint": endpoint, "fail": boundary == "plugin_exception_core"})
			r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(config), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			preview, err := a.previewDownload(context.Background(), PreviewRequest{SearchRequest: SearchRequest{MediaType: media.MediaType, TMDBID: media.ID}, SiteID: "fixture-site", ResourceID: "101", DownloadPath: directory}, false)
			if err != nil {
				t.Fatal(err)
			}
			in, err := a.confirmDownload(context.Background(), preview.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			if string(preview.SourceDownloader) != "null" || !originalBaseJSONEqual(in.SourceDownloader, preview.SourceDownloader) {
				t.Fatal("automatic downloader choice was replaced by resolved name", string(preview.SourceDownloader), string(in.SourceDownloader))
			}
			job, err := a.claim(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = a.runDownload(context.Background(), job, in.ID)
			accepted := boundary != "empty_list_after_core"
			if (err == nil) != accepted {
				t.Fatal("source intent Add branch", err)
			}
			var stored Intent
			if loadErr := a.Store.Get(context.Background(), "intent", in.ID, &stored); loadErr != nil || (stored.State == "accepted") != accepted || !stored.SourceModuleStarted {
				t.Fatal("actual intent module state", stored.State, loadErr)
			}
			adds, forces, overrides := state.counts()
			wantCore := 0
			if boundary != "tuple_owns_add" {
				wantCore = 1
			}
			wantOverride := 0
			if boundary == "tuple_owns_add" {
				wantOverride = 1
			}
			if adds != wantCore || forces != wantCore || overrides != wantOverride {
				t.Fatal("intent native core receipt/effects", adds, forces, overrides)
			}
			observation := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", "", cookie, "")
			var snap struct {
				Calls int
				Input struct{ Kwargs map[string]json.RawMessage }
			}
			if json.Unmarshal(observation.Body.Bytes(), &snap) != nil || snap.Calls != 1 || string(snap.Input.Kwargs["cookie"]) != `"fixture-cookie"` || string(snap.Input.Kwargs["category"]) != `"华语电影"` || string(snap.Input.Kwargs["downloader"]) != `null` {
				t.Fatal("intent category/cookie/automatic choice", observation.Body.String())
			}
			if retry := a.runDownload(context.Background(), job, in.ID); accepted && retry != nil || !accepted && retry == nil {
				t.Fatal("intent replay", retry)
			}
		})
	}
}
