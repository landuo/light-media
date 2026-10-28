package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionMetadata(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	worker := os.Getenv("OWNED_METADATA_MODULE_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal("actual metadata SDK worker", err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedModuleOverride" || len(manifest.APIs) == 0 {
		t.Fatal("actual metadata API worker manifest", err)
	}
	digest := sha256.Sum256(payload)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hex.EncodeToString(digest[:]), ExecutableSHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repository.URL + "/index"})
	defer success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	base := filepath.Join(dir, "metadata-owned")
	if err = os.Mkdir(base, 0750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "Owned.Movie.2024.mkv")
	video := []byte("owned production metadata synthetic media bytes")
	if err = os.WriteFile(path, video, 0640); err != nil {
		t.Fatal(err)
	}
	item, err := originalLocalFileItem(path)
	if err != nil {
		t.Fatal(err)
	}
	var current struct{ Data Settings }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &current) != nil {
		t.Fatal("actual settings")
	}
	current.Data.AllowedRoots = append(current.Data.AllowedRoots, base)
	request("PUT", "/api/settings", current.Data, 200)
	configure := func(id string, result any) {
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "modules": map[string]string{"metadata_nfo": "module_execute", "metadata_img": "module_execute"}, "result": result})
	}
	read := func(id, label string) ownedModuleSnapshot {
		t.Helper()
		body := request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200)
		var out ownedModuleSnapshot
		if json.Unmarshal(body, &out) != nil || out.PID <= 0 {
			t.Fatal("actual metadata SDK observation", string(body))
		}
		writeOwnedEventsProcessEvidence(t, "metadata-"+label+".json", body)
		return out
	}
	send := func(overwrite bool) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"fileitem": item, "mediainfo": map[string]any{"type": "电影", "title": "Owned production metadata", "tmdb_id": 900043}, "overwrite": overwrite})
		request("POST", "/api/v1/plugin/OwnedInput/input", map[string]any{"operation": "event.send", "input": plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: "metadata.scrape", Data: data}}, 200)
	}
	check := request("POST", "/api/v1/plugin/OwnedInput/input", map[string]any{"operation": "event.check", "input": map[string]any{"kind": "broadcast", "type": "metadata.scrape"}}, 200)
	if string(check) != "true" {
		t.Fatal("actual MetadataScrape handler absent", string(check))
	}
	nfo := filepath.Join(base, "Owned.Movie.2024.nfo")
	waitBytes := func(want []byte) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for {
			actual, e := os.ReadFile(nfo)
			if e == nil && string(actual) == string(want) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("actual metadata file missing/changed got %q error %v", actual, e)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	first := []byte{'o', 'w', 'n', 'e', 'd', 0, 255}
	binaryResult, _ := plugins.ModuleResultBytes(first)
	configure(manifest.PluginID, binaryResult)
	before := read(manifest.PluginID, "before")
	send(false)
	waitBytes(first)
	after := read(manifest.PluginID, "first")
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
		t.Fatal("metadata_img unexpectedly invoked plugin or NFO duplicated")
	}
	var args struct {
		Args   []json.RawMessage
		Kwargs map[string]json.RawMessage
	}
	if json.Unmarshal(after.Inputs["module_execute"], &args) != nil || args.Args == nil || len(args.Args) != 0 || len(args.Kwargs) != 4 || string(args.Kwargs["season"]) != "null" || string(args.Kwargs["episode"]) != "null" {
		t.Fatal("actual source metadata NFO kwargs", string(after.Inputs["module_execute"]))
	}
	configure(manifest.PluginID, "owned overwritten 中文")
	before = read(manifest.PluginID, "skip-before")
	send(false)
	time.Sleep(100 * time.Millisecond)
	after = read(manifest.PluginID, "skipped")
	waitBytes(first)
	if after.Calls["module_execute"] != before.Calls["module_execute"] {
		t.Fatal("existing file did not skip NFO module")
	}
	send(true)
	waitBytes([]byte("owned overwritten 中文"))
	after = read(manifest.PluginID, "overwritten")
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
		t.Fatal("explicit overwrite callback count")
	}
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]string{"suffix": "metadata"})
	cloneID := manifest.PluginID + "metadata"
	defer success("DELETE", "/api/plugins/"+cloneID, nil)
	configure(cloneID, "owned clone actual NFO")
	configure(manifest.PluginID, nil)
	cloneBefore := read(cloneID, "clone-before")
	sourceBefore := read(manifest.PluginID, "source-none-before")
	send(true)
	waitBytes([]byte("owned clone actual NFO"))
	cloneAfter := read(cloneID, "clone-after")
	sourceAfter := read(manifest.PluginID, "source-none-after")
	if cloneAfter.PID == sourceAfter.PID || cloneAfter.Calls["module_execute"]-cloneBefore.Calls["module_execute"] != 1 || sourceAfter.Calls["module_execute"]-sourceBefore.Calls["module_execute"] != 1 {
		t.Fatal("actual metadata clone/source isolation")
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(video) {
		t.Fatal("metadata altered the media file", err)
	}
	stop()
	start()
	waitBytes([]byte("owned clone actual NFO"))
	coldSource, coldClone := read(manifest.PluginID, "cold-source"), read(cloneID, "cold-clone")
	if coldSource.PID == sourceAfter.PID || coldClone.PID == cloneAfter.PID || coldSource.PID == coldClone.PID || coldSource.Calls["module_execute"] != 0 || coldClone.Calls["module_execute"] != 0 {
		t.Fatal("cold metadata config not rebuilt or ordinary event replayed")
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 2 {
		t.Fatal("metadata temporary stages remain", entries, err)
	}
	t.Logf("actual cmd/server MetadataScrape: unregistered local file, exact binary NFO, skip/overwrite and cloned fallback; source PID %d -> %d, clone PID %d -> %d; cold no replay, video unchanged", sourceAfter.PID, coldSource.PID, cloneAfter.PID, coldClone.PID)
}
