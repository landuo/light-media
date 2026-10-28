package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Real production main -> catalog/person route -> plugin registry -> actual
// SDK process. Fixed TmdbChain.async_person_detail kwargs and MediaPerson DTO
// are checked, including falsey rejection and persistent cloned configuration.
func TestPluginCatalogPersonProductionCLI(t *testing.T) {
	server, worker := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY"), os.Getenv("OWNED_CATALOG_MODULE_PROCESS_BINARY")
	if server == "" || worker == "" {
		t.Skip("unverified: production main and actual snapshot-enabled catalog worker required")
	}
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID || len(manifest.APIs) != 1 {
		t.Fatal("actual catalog module fixture identity")
	}
	digest := originalFrontendHash(payload)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
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
	dir := t.TempDir()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	bootstrap, _ := json.Marshal(map[string]any{"username": "catalog43-acceptance", "password": "owned-catalog43-password", "settings": Settings{APIToken: hex.EncodeToString(secret[:]), PluginMarkets: []string{repository.URL + "/index"}}})
	boot := exec.Command(server, "--data-dir", dir, "--bootstrap-stdin")
	boot.Stdin = bytes.NewReader(bootstrap)
	if out, e := boot.CombinedOutput(); e != nil || !bytes.Contains(out, []byte("bootstrap_complete")) {
		t.Fatal("owned catalog bootstrap")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	origin := "http://" + address
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	var process *exec.Cmd
	stop := func() {
		t.Helper()
		if process == nil {
			return
		}
		_ = process.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		select {
		case e := <-done:
			if e != nil {
				t.Error("actual catalog shutdown", e)
			}
		case <-time.After(12 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("catalog shutdown timeout")
		}
		process = nil
	}
	defer stop()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	request := func(method, path string, value any, want int) []byte {
		t.Helper()
		var body []byte
		if value != nil {
			body, _ = json.Marshal(value)
		}
		r, e := http.NewRequest(method, origin+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		response, e := client.Do(r)
		if e != nil {
			t.Fatal("actual catalog request", method, path)
		}
		defer response.Body.Close()
		out, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != want {
			t.Fatalf("actual catalog %s %s status%d want%d; body withheld", method, path, response.StatusCode, want)
		}
		return out
	}
	start := func() {
		t.Helper()
		process = exec.Command(server, "--listen", address, "--data-dir", dir)
		process.Env = append(os.Environ(), "MP_SERVER_HOST=")
		process.Stdout = log
		process.Stderr = log
		if process.Start() != nil {
			t.Fatal("start production catalog")
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			res, e := client.Get(origin + "/api/auth/status")
			if e == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("catalog listener unavailable")
			}
			time.Sleep(30 * time.Millisecond)
		}
		request("POST", "/api/auth/login", map[string]string{"username": "catalog43-acceptance", "password": "owned-catalog43-password"}, 200)
	}
	success := func(method, path string, value any) {
		t.Helper()
		raw := request(method, path, value, 200)
		var v struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &v) != nil || !v.Data.Success {
			t.Fatal("actual catalog management failed", method, path)
		}
	}
	configure := func(id string, result json.RawMessage, enabled bool) {
		t.Helper()
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": enabled, "modules": map[string]string{"async_tmdb_person_detail": "module_execute", "tmdb_person_detail": "module_fail", "async_search_persons": "module_fail"}, "result": result})
	}
	snapshot := func(id string) ownedModuleSnapshot {
		t.Helper()
		var v ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &v) != nil || v.PID <= 0 {
			t.Fatal("actual catalog worker snapshot")
		}
		return v
	}
	person := func() map[string]json.RawMessage {
		t.Helper()
		var v struct{ Data map[string]json.RawMessage }
		if json.Unmarshal(request("GET", "/api/catalog/person?id=120", nil, 200), &v) != nil {
			t.Fatal("catalog person response")
		}
		return v.Data
	}
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedModuleID, "repo_url": repository.URL + "/index"})
	configure(ownedModuleID, json.RawMessage(`{"id":"120","name":"Owned source person","title":"kept source DTO metadata","images":{"owned":"source"}}`), true)
	before := snapshot(ownedModuleID)
	value := person()
	after := snapshot(ownedModuleID)
	if string(value["name"]) != `"Owned source person"` || string(value["title"]) != `"kept source DTO metadata"` || len(value) != 25 || after.Calls["module_execute"] != before.Calls["module_execute"]+1 || after.Calls["module_fail"] != before.Calls["module_fail"] {
		t.Fatal("real person extension/full DTO did not execute")
	}
	var input struct{ Args, Kwargs json.RawMessage }
	_ = json.Unmarshal(after.Inputs["module_execute"], &input)
	if string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, json.RawMessage(`{"person_id":120}`)) {
		t.Fatal("actual source person kwargs differ")
	}
	configure(ownedModuleID, json.RawMessage(`false`), true)
	request("GET", "/api/catalog/person?id=120", nil, 500)
	configure(ownedModuleID, json.RawMessage(`{}`), true)
	if got := person(); string(got["id"]) != "null" || string(got["type"]) != "1" || len(got) != 25 {
		t.Fatal("empty source DTO did not terminate")
	}
	configure(ownedModuleID, json.RawMessage(`{"id":120,"name":"Owned source person"}`), true)
	clone := ownedModuleID + "catalog"
	success("POST", "/api/plugins/"+ownedModuleID+"/clone", map[string]any{"suffix": "catalog"})
	configure(clone, json.RawMessage(`{"id":120,"name":"Owned clone person","title":"clone only"}`), true)
	configure(ownedModuleID, json.RawMessage(`null`), true)
	sourceBefore, cloneBefore := snapshot(ownedModuleID), snapshot(clone)
	value = person()
	sourceAfter, cloneAfter := snapshot(ownedModuleID), snapshot(clone)
	if string(value["name"]) != `"Owned clone person"` || sourceBefore.PID == cloneBefore.PID || sourceAfter.Calls["module_execute"] != sourceBefore.Calls["module_execute"]+1 || cloneAfter.Calls["module_execute"] != cloneBefore.Calls["module_execute"]+1 {
		t.Fatal("real source None did not continue to independent clone")
	}
	stop()
	start()
	sourceCold, cloneCold := snapshot(ownedModuleID), snapshot(clone)
	if sourceCold.PID == sourceAfter.PID || cloneCold.PID == cloneAfter.PID || sourceCold.PID == cloneCold.PID || string(person()["title"]) != `"clone only"` {
		t.Fatal("cold catalog module config/instance identity recovery")
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	configure(ownedModuleID, json.RawMessage(`{"id":120,"name":"source survives clone uninstall"}`), true)
	if string(person()["name"]) != `"source survives clone uninstall"` {
		t.Fatal("clone uninstall affected source person host")
	}
	success("DELETE", "/api/plugins/"+ownedModuleID, nil)
	t.Logf("actual production person extension: source/clone PID%d/%d→cold%d/%d; kwargs, DTO defaults, false500, clone isolation/restart/uninstall verified", sourceBefore.PID, cloneBefore.PID, sourceCold.PID, cloneCold.PID)
}
