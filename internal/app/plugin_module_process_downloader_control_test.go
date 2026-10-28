package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionDownloaderControl(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	const hash = "0123456789012345678901234567890123456789"
	const unmanaged = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	path := os.Getenv("OWNED_DOWNLOADER_CONTROL_PROCESS_FIXTURE_BINARY")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(path, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedDownloaderControl" {
		t.Fatal("actual owned control artifact", err)
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
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
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	dataPath := filepath.Join(dir, "owned-control-data.bin")
	if err = os.WriteFile(dataPath, []byte("owned non-media acceptance data"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	present, state := true, "downloading"
	calls := map[string]int{}
	inputs := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if r.URL.Query().Get("filter") == "downloading" {
				calls["downloading-list"]++
			}
			items := []any{}
			if present {
				item := workflowTask(hash, 0)
				item["save_path"], item["state"], item["tags"] = dir, state, "light-media"
				items = append(items, item)
			}
			other := workflowTask(unmanaged, 0)
			other["save_path"], other["tags"] = dir, "not-owned"
			items = append(items, other)
			if selected := r.URL.Query().Get("hashes"); selected != "" {
				filtered := []any{}
				for _, item := range items {
					if item.(map[string]any)["hash"] == selected {
						filtered = append(filtered, item)
					}
				}
				items = filtered
			}
			workflowJSON(w, items)
		case "/api/v2/torrents/start", "/api/v2/torrents/stop", "/api/v2/torrents/delete":
			if r.Method != "POST" || r.ParseForm() != nil || r.FormValue("hashes") != hash {
				t.Error("owned native control protocol arguments")
			}
			calls[r.URL.Path]++
			if strings.HasSuffix(r.URL.Path, "/start") {
				state = "downloading"
			} else if strings.HasSuffix(r.URL.Path, "/stop") {
				state = "pausedDL"
			} else {
				present = false
				if r.FormValue("deleteFiles") != "false" {
					t.Error("owned keep-data flag changed")
				}
			}
		case "/module-pause", "/module-delete":
			var input struct{ Args, Kwargs json.RawMessage }
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&input) != nil {
				t.Error("owned module receiver input")
			}
			body, _ := json.Marshal(input)
			var fields map[string]json.RawMessage
			if string(input.Args) != `[]` || json.Unmarshal(input.Kwargs, &fields) != nil || string(fields["hashs"]) != `["`+hash+`"]` || string(fields["downloader"]) != `"Owned downloader"` {
				t.Error("actual source wrapper kwargs changed")
			}
			inputs = append(inputs, body)
			calls[r.URL.Path]++
			if r.URL.Path == "/module-pause" {
				state = "pausedDL"
			} else {
				if string(fields["delete_file"]) != "true" || len(fields) != 3 {
					t.Error("owned module data-delete flag")
				}
				present = false
				if err := os.Remove(dataPath); err != nil {
					t.Error("owned test-file removal", err)
				}
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	settings := seed.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-control", Kind: "qb", BaseURL: peer.URL}, Name: "Owned downloader", DownloadPath: dir}}
	if err = seed.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	initial := NativeDownload{ID: "native_owned_control", Hash: hash, DownloaderID: "owned-control", DownloadPath: dir, State: "accepted"}
	if err = seed.Store.Put(context.Background(), "native_download", initial.ID, initial); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	defer success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	configure := func(method string, value any, endpoint string, fail bool) {
		success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "module": method, "result": value, "endpoint": endpoint, "fail": fail})
	}
	action := func(hash, op string, status int) {
		request("POST", "/api/downloads/task-action", map[string]any{"downloader_id": "owned-control", "hash": hash, "action": op}, status)
	}
	if os.Getenv("PLUGIN_PROCESS_DOWNLOADER_LIST_FIXTURE") == "true" {
		oraclePath := os.Getenv("PLUGIN_PROCESS_DOWNLOADER_LIST_ORACLE")
		if oraclePath == "" {
			oraclePath = "../../testdata/standard-module-host/downloader-list-oracle.json"
		}
		raw, err := os.ReadFile(oraclePath)
		var oracle struct {
			Cases []struct {
				Name         string
				PluginResult []map[string]any `json:"plugin_result"`
				Observations []struct {
					Kind   string
					Kwargs json.RawMessage
				}
			}
		}
		// The full oracle also contains scalar cases; read the one source model
		// case independently instead of coercing scalars into model slices.
		var samples struct{ Cases []json.RawMessage }
		if err != nil || json.Unmarshal(raw, &samples) != nil {
			t.Fatal("fixed list oracle", err)
		}
		for _, sample := range samples.Cases {
			var name struct{ Name string }
			if json.Unmarshal(sample, &name) == nil && name.Name == "list_before_core" {
				wrapper, _ := json.Marshal(map[string]any{"Cases": []json.RawMessage{sample}})
				if json.Unmarshal(wrapper, &oracle) != nil {
					t.Fatal("fixed source list model")
				}
			}
		}
		if len(oracle.Cases) != 1 || len(oracle.Cases[0].PluginResult) != 1 {
			t.Fatal("source list result not captured")
		}
		model := oracle.Cases[0].PluginResult[0]
		model["hash"] = strings.Repeat("c", 40)
		model["save_path"], model["path"], model["content_path"] = dir, filepath.Join(dir, "Owned.S02E03.1080p"), filepath.Join(dir, "Owned.S02E03.1080p")
		configure("list_torrents", []any{model}, "", false)
		list := func(want int) []byte {
			body := request("GET", "/api/downloads?downloader_id=owned-control", nil, 200)
			var response struct {
				Data struct {
					Instances []struct{ Tasks []download.Task }
				}
			}
			if json.Unmarshal(body, &response) != nil || len(response.Data.Instances) != 1 || len(response.Data.Instances[0].Tasks) != want {
				t.Fatal("actual module/core list", string(body), want)
			}
			if want == 2 {
				first := response.Data.Instances[0].Tasks[0]
				wantSource, _ := json.Marshal(model)
				if first.Hash != strings.Repeat("c", 40) || !originalBaseJSONEqual(first.SourceTorrent, wantSource) || first.Progress != .37 || response.Data.Instances[0].Tasks[1].Hash != hash {
					t.Fatal("actual source model/percent/list-before-core changed")
				}
			}
			return body
		}
		listed := list(2)
		writeOwnedEventsProcessEvidence(t, "modules43-list-native-response.json", listed)
		observation := request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 200)
		var actual struct {
			Calls, PID int
			Input      struct {
				Args   []json.RawMessage
				Kwargs json.RawMessage
			}
		}
		if json.Unmarshal(observation, &actual) != nil || actual.Calls != 1 || actual.PID <= 0 || actual.Input.Args == nil || len(actual.Input.Args) != 0 || !originalBaseJSONEqual(actual.Input.Kwargs, oracle.Cases[0].Observations[0].Kwargs) {
			t.Fatal("actual list SDK kwargs do not match fixed wrapper")
		}
		writeOwnedEventsProcessEvidence(t, "modules43-list-sdk-snapshot.json", observation)
		mu.Lock()
		beforeQueries := calls["downloading-list"]
		mu.Unlock()
		configure("list_torrents", false, "", false)
		list(0)
		mu.Lock()
		if calls["downloading-list"] != beforeQueries {
			t.Error("false scalar queried native list")
		}
		mu.Unlock()
		configure("list_torrents", []any{}, "", false)
		list(1)
		configure("list_torrents", nil, "", true)
		list(1)
		t.Log("actual Linux ordinary list_torrents: source wrapper kwargs once; full DownloaderTorrent and 37 percent preserved, before owned native result; foreign native task excluded; false scalar prevents core list; empty list and throwing provider fall through")
	}
	beforeObservation := request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 200)
	var beforeControl struct{ Calls int }
	if json.Unmarshal(beforeObservation, &beforeControl) != nil {
		t.Fatal("actual SDK control baseline")
	}
	configure("stop_torrents", true, peer.URL+"/module-pause", false)
	action(hash, "pause", 200)
	mu.Lock()
	if calls["/module-pause"] != 1 || calls["/api/v2/torrents/stop"] != 0 || state != "pausedDL" {
		t.Error("actual terminal provider did not pause exactly once")
	}
	mu.Unlock()
	observation := request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 200)
	var snapshot struct {
		Calls, PID int
		Input      json.RawMessage
	}
	if json.Unmarshal(observation, &snapshot) != nil || snapshot.Calls-beforeControl.Calls != 1 || snapshot.PID <= 0 {
		t.Fatal("actual SDK control invocation not observed")
	}
	writeOwnedEventsProcessEvidence(t, "modules43-control-sdk-snapshot.json", observation)
	configure("start_torrents", false, "", false)
	action(hash, "start", 400)
	mu.Lock()
	if calls["/api/v2/torrents/start"] != 0 || state != "pausedDL" {
		t.Error("false scalar ran native control")
	}
	mu.Unlock()
	configure("start_torrents", true, "", true)
	action(hash, "start", 200)
	mu.Lock()
	if calls["/api/v2/torrents/start"] != 1 || state != "downloading" {
		t.Error("callback failure did not fall back to actual native start")
	}
	mu.Unlock()
	configure("remove_torrents", []any{}, "", false)
	action(hash, "remove_keep_data", 400)
	mu.Lock()
	if present || calls["/api/v2/torrents/delete"] != 1 {
		t.Error("source empty list did not call actual core delete before false result")
	}
	present = true
	mu.Unlock()
	if _, err = os.Stat(dataPath); err != nil {
		t.Fatal("keep-data removed owned file", err)
	}
	configure("remove_torrents", true, peer.URL+"/module-delete", false)
	action(unmanaged, "remove_with_data", 400)
	mu.Lock()
	if calls["/module-delete"] != 0 {
		t.Error("ownership guard invoked provider for foreign task")
	}
	mu.Unlock()
	action(hash, "remove_with_data", 200)
	if _, err = os.Stat(dataPath); !os.IsNotExist(err) {
		t.Fatal("actual provider did not delete only its owned test file", err)
	}
	mu.Lock()
	evidence, _ := json.Marshal(map[string]any{"calls": calls, "module_inputs": inputs, "present": present, "state": state})
	mu.Unlock()
	writeOwnedEventsProcessEvidence(t, "modules43-control-peer.json", evidence)
	stop()
	inspector, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var current NativeDownload
	if err = inspector.Store.Get(context.Background(), "native_download", initial.ID, &current); err != nil || current.State != "cancelled" {
		t.Fatal("actual removal did not cancel owning native row", err)
	}
	if err = inspector.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	t.Log("actual Linux downloader controls: terminal SDK provider pauses/owns data deletion through real owned HTTP recipient; false scalar skips core; thrown callback falls back to real native start; empty list core-delete applies while source false and owning cancellation persists; foreign task guard blocks module; only owned non-media test file removed; cold state cancelled")
}
