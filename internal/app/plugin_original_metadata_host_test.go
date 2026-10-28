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
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func installMetadataInputFixture(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	path := os.Getenv("OWNED_INPUT_FIXTURE_BINARY")
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(path, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedInput" {
		t.Fatal("actual SDK input fixture manifest")
	}
	hash := sha256.Sum256(binary)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(binary)), HostContract: pluginResourcesSDKContract}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	body, _ := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
}

func TestOriginalMetadataScrapeActualSDKWritesUnregisteredSidecars(t *testing.T) {
	a, cookie, object := ownedModuleFixture(t, true)
	installMetadataInputFixture(t, a, cookie)
	dir := t.TempDir()
	path := filepath.Join(dir, "Owned.Movie.2024.mkv")
	if err := os.WriteFile(path, []byte("owned synthetic video bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.AllowedRoots = []string{dir}
	settings.APIToken = "owned-metadata-synthetic-api-key"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	item, err := originalLocalFileItem(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	encoded, _ := json.Marshal(item)
	if json.Unmarshal(encoded, &fields) != nil {
		t.Fatal("real FileItem projection")
	}
	configure := func(result any) {
		t.Helper()
		configuration, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"metadata_nfo": "module_execute", "metadata_img": "module_execute"}, "result": result})
		saveOwnedModule(t, a, cookie, object, string(configuration))
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
	}
	send := func(overwrite bool) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"fileitem": fields, "mediainfo": map[string]any{"type": "电影", "title": "Owned movie", "tmdb_id": 900043}, "overwrite": overwrite})
		request, _ := json.Marshal(plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: "metadata.scrape", Data: data})
		ownedSDKEventCall(t, a, cookie, "event.send", string(request))
		a.wg.Wait()
	}
	if got := ownedSDKEventCall(t, a, cookie, "event.check", `{"kind":"broadcast","type":"metadata.scrape"}`); string(got) != "true" {
		t.Fatal("real metadata core consumer absent", string(got))
	}
	first := []byte{'o', 'w', 'n', 'e', 'd', 0, 255}
	firstResult, err := plugins.ModuleResultBytes(first)
	if err != nil {
		t.Fatal(err)
	}
	configure(firstResult)
	before := ownedModuleRead(t, object)
	send(false)
	nfo := filepath.Join(dir, "Owned.Movie.2024.nfo")
	actual, err := os.ReadFile(nfo)
	if err != nil || string(actual) != string(first) {
		t.Fatalf("actual event sidecar bytes %q %v", actual, err)
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
		t.Fatal("metadata_img must bypass plugin and NFO execute once", before.Calls, after.Calls)
	}
	configure("replacement 中文")
	before = ownedModuleRead(t, object)
	send(false)
	after = ownedModuleRead(t, object)
	actual, _ = os.ReadFile(nfo)
	if string(actual) != string(first) || after.Calls["module_execute"] != before.Calls["module_execute"] {
		t.Fatal("existing NFO must skip before invoking NFO module")
	}
	send(true)
	actual, err = os.ReadFile(nfo)
	if err != nil || string(actual) != "replacement 中文" {
		t.Fatal("explicit overwrite did not really replace NFO", string(actual), err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("video changed", err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("ordinary metadata must not invent an asset", count, err)
	}
	// None continues into the independently loaded clone. The two workers
	// provide their own current configuration, never a host-side cloned result.
	clone := ownedModuleClone(t, a, cookie, "metadata")
	cloneConfig, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"metadata_nfo": "module_execute"}, "result": "clone independent NFO"})
	saveOwnedModule(t, a, cookie, clone, string(cloneConfig))
	clone = originalBaseSDKLoaded(t, a, clone.installation.InstanceID)
	configure(nil)
	sourceBefore, cloneBefore := ownedModuleRead(t, object), ownedModuleRead(t, clone)
	send(true)
	actual, err = os.ReadFile(nfo)
	sourceAfter, cloneAfter := ownedModuleRead(t, object), ownedModuleRead(t, clone)
	if err != nil || string(actual) != "clone independent NFO" || sourceAfter.PID == cloneAfter.PID || sourceAfter.Calls["module_execute"]-sourceBefore.Calls["module_execute"] != 1 || cloneAfter.Calls["module_execute"]-cloneBefore.Calls["module_execute"] != 1 {
		t.Fatal("source None/clone concrete file isolation", string(actual), err, sourceAfter, cloneAfter)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatal("temporary sidecar files not cleaned", entries, err)
	}
	// The fixed NFO consumer uses bool(get_file_item), including a plugin's
	// scalar True. Root existence uses the same truth rule; no FileItem member
	// is read from either scalar. Parent/child callers still require a DTO.
	manifest, index := ownedDownloadModuleArtifact(t, "OWNED_DOWNLOADER_CONTROL_FIXTURE_BINARY")
	installBody, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": index})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(installBody), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, `{"enabled":true,"module":"get_file_item","result":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	if err = os.Remove(nfo); err != nil {
		t.Fatal(err)
	}
	sourceBefore, cloneBefore = ownedModuleRead(t, object), ownedModuleRead(t, clone)
	send(false)
	if _, err = os.Stat(nfo); !os.IsNotExist(err) {
		t.Fatal("source scalar True existence must skip NFO before callback", err)
	}
	if ownedModuleRead(t, object).Calls["module_execute"] != sourceBefore.Calls["module_execute"] || ownedModuleRead(t, clone).Calls["module_execute"] != cloneBefore.Calls["module_execute"] {
		t.Fatal("scalar True existence incorrectly entered NFO module")
	}
	send(true)
	actual, err = os.ReadFile(nfo)
	if err != nil || string(actual) != "clone independent NFO" {
		t.Fatal("scalar True root/existence plus overwrite did not enter the actual file writer", string(actual), err)
	}
}
