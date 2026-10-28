package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Reuses the existing register_commands host module and owned provider fixture;
// the sink is an isolated HTTP observer, not an external message integration.
func verifyOwnedProductionCommandProvider(t *testing.T, request func(string, string, any, int) []byte) {
	t.Helper()
	path := os.Getenv("OWNED_COMMAND_PROCESS_FIXTURE_BINARY")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(path, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedCommandMutator" {
		t.Fatal("actual owned command provider artifact")
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
	type delivery struct {
		PID   int
		Input struct {
			Args   []json.RawMessage
			Kwargs map[string]json.RawMessage
		}
	}
	received := make(chan delivery, 20)
	var status atomic.Int64
	status.Store(204)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value delivery
		if json.NewDecoder(r.Body).Decode(&value) != nil {
			t.Error("actual menu delivery shape")
		}
		received <- value
		w.WriteHeader(int(status.Load()))
	}))
	defer sink.Close()
	success := func(method, path string, body any) {
		t.Helper()
		var response struct{ Data struct{ Success bool } }
		if json.Unmarshal(request(method, path, body, 200), &response) != nil || !response.Data.Success {
			t.Fatal("actual command provider operation", method, path)
		}
	}
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	defer success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	config := map[string]any{"enabled": true, "provider": true, "endpoint": sink.URL, "mode": "mutate", "command": "/owned_menu"}
	configure := func() { success("PUT", "/api/plugins/"+manifest.PluginID, config) }
	pop := func() delivery {
		t.Helper()
		select {
		case value := <-received:
			if value.PID <= 0 {
				t.Fatal("actual provider PID")
			}
			return value
		case <-time.After(5 * time.Second):
			t.Fatal("actual command provider did not receive registration")
			return delivery{}
		}
	}
	configure()
	value := pop()
	if value.Input.Args == nil || len(value.Input.Args) != 0 || len(value.Input.Kwargs) != 1 {
		t.Fatal("actual register_commands module call shape")
	}
	var commands map[string]json.RawMessage
	if json.Unmarshal(value.Input.Kwargs["commands"], &commands) != nil || !originalBaseJSONEqual(commands["/owned_menu"], json.RawMessage(`{"type":"plugin","description":"changed menu only","category":null,"opaque":900719925474099312345}`)) {
		t.Fatal("actual command.register mutation not delivered", string(value.Input.Kwargs["commands"]))
	}
	for _, key := range []string{"/not_executable", "/owned_menu_hidden", "/redo"} {
		if _, ok := commands[key]; ok {
			t.Fatal("source menu subset/hidden", key)
		}
	}
	config["source"] = manifest.PluginID
	config["menu_result"] = false
	status.Store(503)
	configure()
	failed := pop()
	status.Store(204)
	configure()
	recovered := pop()
	if failed.PID != value.PID || recovered.PID != value.PID {
		t.Fatal("config save replaced live command provider")
	}
	t.Logf("actual Linux register_commands host module: chain-mutated filtered menu through SDK PID%d; forced actual HTTP503 then forced204 recovery, normal false module result accepted", value.PID)
}
