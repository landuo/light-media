package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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

// Real production main -> clone HTTP -> copied artifact/config -> actual SDK.
// Fixed clone/reload/start/uninstall functions supply the success/loading and
// partial code/config side effects, including Unicode/Python import boundaries.
func TestPluginCloneSuffixProductionCLI(t *testing.T) {
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
	bootstrap, _ := json.Marshal(map[string]any{"username": "clone-suffix-acceptance", "password": "owned-clone-suffix-password", "settings": Settings{APIToken: hex.EncodeToString(secret[:]), PluginMarkets: []string{repository.URL + "/index"}}})
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
		request("POST", "/api/auth/login", map[string]string{"username": "clone-suffix-acceptance", "password": "owned-clone-suffix-password"}, 200)
	}
	success := func(method, path string, value any) {
		t.Helper()
		raw := request(method, path, value, 200)
		var v struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &v) != nil || !v.Data.Success {
			t.Fatal("actual catalog management failed", method, path)
		}
	}
	snapshot := func(id string) map[string]json.RawMessage {
		t.Helper()
		var data map[string]json.RawMessage
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+url.PathEscape(id)+"/snapshot", nil, 200), &data) != nil || len(data["pid"]) == 0 {
			t.Fatal("actual suffix SDK snapshot")
		}
		return data
	}
	configRead := func(id string) map[string]json.RawMessage {
		t.Helper()
		var data struct{ Data map[string]json.RawMessage }
		if json.Unmarshal(request("GET", "/api/plugins/"+url.PathEscape(id), nil, 200), &data) != nil {
			t.Fatal("actual suffix configuration read")
		}
		return data.Data
	}
	// Observe only this t.TempDir's encrypted persistent state. Never acquire
	// an app lock, run migrations or expose the key; the real server owns writes.
	secretKey, keyErr := os.ReadFile(filepath.Join(dir, "secret.key"))
	if keyErr != nil || len(secretKey) != 32 {
		t.Fatal("owned persistent observation key unavailable")
	}
	observerDB, openErr := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "light-media.db"))+"?mode=ro")
	if openErr != nil {
		t.Fatal("owned readonly persistence observation unavailable")
	}
	defer observerDB.Close()
	observer := &PluginStore{store: &Store{DB: observerDB, key: secretKey}}
	persistRead := func(id string) map[string]json.RawMessage {
		t.Helper()
		value, e := observer.Config(context.Background(), id)
		var result map[string]json.RawMessage
		if e != nil || json.Unmarshal(value.Value, &result) != nil {
			t.Fatal("owned retained encrypted config unavailable", id, e)
		}
		return result
	}
	source := ownedModuleID
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository.URL + "/index"})
	oraclePath := os.Getenv("PLUGIN_CLONE_SUFFIX_SOURCE_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/standard-clone-suffix/source-oracle.json"
	}
	oracleRaw, e := os.ReadFile(oraclePath)
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		SourceConfig json.RawMessage `json:"source_config"`
		Cases        []struct {
			Suffix          string
			Target          string `json:"target_id"`
			Success, Loaded bool
			PublicConfig    json.RawMessage `json:"public_config"`
			Init            *int            `json:"init_count"`
			Uninstall       *struct {
				Success   bool
				Code      bool `json:"code_exists"`
				Config    json.RawMessage
				Installed bool
				Folder    bool `json:"folder_member"`
				Loaded    bool
			}
		}
	}
	if json.Unmarshal(oracleRaw, &oracle) != nil || len(oracle.Cases) != 28 {
		t.Fatal("fixed clone HTTP/filesystem/uninstall source oracle")
	}
	if !originalBaseJSONTruthy(oracle.SourceConfig) {
		t.Fatal("fixed source initial config input absent")
	}
	success("PUT", "/api/plugins/"+source, oracle.SourceConfig)
	sourceBefore := snapshot(source)
	request("POST", "/api/v1/plugin/folders", map[string]any{"owned": []string{source}}, 200)
	folderHas := func(id string) bool {
		t.Helper()
		var folders map[string][]string
		if json.Unmarshal(request("GET", "/api/v1/plugin/folders", nil, 200), &folders) != nil {
			t.Fatal("real source folders")
		}
		for _, v := range folders["owned"] {
			if v == id {
				return true
			}
		}
		return false
	}
	originals := map[string]json.RawMessage{}
	observations := map[string]any{"source_before": sourceBefore, "clones": map[string]any{}}
	cloneObservations := observations["clones"].(map[string]any)
	unloaded := []string{}
	for _, row := range oracle.Cases {
		raw := request("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": row.Suffix}, 200)
		var response struct {
			Data struct {
				Success bool
				Message string
			}
		}
		if json.Unmarshal(raw, &response) != nil || response.Data.Success != row.Success {
			t.Fatalf("source clone API success mismatch suffix%q body%q", row.Suffix, raw)
		}
		if !row.Success {
			continue
		}
		id := row.Target
		cfg := persistRead(id)
		visible, _ := json.Marshal(configRead(id))
		if !originalBaseJSONEqual(visible, row.PublicConfig) {
			t.Fatal("source public configuration/class-import boundary differs", id)
		}
		if string(cfg["enabled"]) != "false" || string(cfg["enable"]) != "false" || string(cfg["marker"]) != `"owned-source"` || !folderHas(id) {
			t.Fatal("actual source config/installed/folder side effects", id)
		}
		if code := request("GET", "/api/v1/plugin/file/"+url.PathEscape(id)+"/"+manifest.Entry, nil, 200); !bytes.Equal(code, payload) {
			t.Fatal("actual copied compiled artifact differs")
		}
		alias := plugins.OriginalPluginLower(source) + row.Suffix
		if code := request("GET", "/api/v1/plugin/file/"+url.PathEscape(alias)+"/"+manifest.Entry, nil, 200); !bytes.Equal(code, payload) {
			t.Fatal("full Python lower path alias failed", alias)
		}
		cloneObservations[id] = map[string]any{"loaded": row.Loaded, "public_config": json.RawMessage(visible), "stored_config": cfg, "folder": true, "code_sha256": digest}
		if !row.Loaded {
			request("GET", "/api/v1/plugin/"+url.PathEscape(id)+"/snapshot", nil, 404)
			unloaded = append(unloaded, id)
			continue
		}
		got := snapshot(id)
		var calls map[string]int
		if json.Unmarshal(got["calls"], &calls) != nil || row.Init == nil || calls["init"] != *row.Init || bytes.Equal(got["pid"], sourceBefore["pid"]) {
			t.Fatal("fixed HTTP final init/object isolation", id)
		}
		success("PUT", "/api/plugins/"+url.PathEscape(id), map[string]any{"enabled": false, "marker": id, "modules": map[string]any{}})
		wantMarker, _ := json.Marshal(id)
		if !bytes.Equal(configRead(id)["marker"], wantMarker) || string(configRead(source)["marker"]) != `"owned-source"` || !bytes.Equal(snapshot(id)["pid"], got["pid"]) {
			t.Fatal("actual namespace/init isolation", id)
		}
		cloneObservations[id].(map[string]any)["before"] = got
		originals[id] = got["pid"]
	}
	for _, suffix := range []string{"../escape", "/escape", "\\escape", "a\n"} {
		var body struct {
			Data struct {
				Success bool
				Message string
			}
		}
		if json.Unmarshal(request("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": suffix}, 200), &body) != nil || body.Data.Success || body.Data.Message == "" {
			t.Fatal("unsafe path crossed owned component boundary")
		}
	}
	stop()
	start()
	sourceCold := snapshot(source)
	observations["source_cold"] = sourceCold
	if bytes.Equal(sourceCold["pid"], sourceBefore["pid"]) {
		t.Fatal("actual source failed cold restart")
	}
	for id, pid := range originals {
		got := snapshot(id)
		if bytes.Equal(got["pid"], pid) || bytes.Equal(got["pid"], sourceCold["pid"]) || string(configRead(id)["marker"]) != string(mustCloneSuffixJSON(t, id)) {
			t.Fatal("safe suffix cold SDK/config namespace lost", len(id))
		}
		cloneObservations[id].(map[string]any)["cold"] = got
		success("DELETE", "/api/plugins/"+url.PathEscape(id), nil)
		in, e := observer.Installation(context.Background(), id)
		if e != nil || in.Installed {
			t.Fatal("actual clone installed membership retained", id, e)
		}
		cloneObservations[id].(map[string]any)["installed_after_uninstall"] = in.Installed
		request("GET", "/api/v1/plugin/"+url.PathEscape(id)+"/snapshot", nil, 404)
		request("GET", "/api/v1/plugin/file/"+url.PathEscape(id)+"/"+manifest.Entry, nil, 404)
	}
	for _, id := range unloaded {
		request("GET", "/api/v1/plugin/"+url.PathEscape(id)+"/snapshot", nil, 404)
		if string(persistRead(id)["marker"]) != `"owned-source"` || len(configRead(id)) != 0 || !folderHas(id) {
			t.Fatal("source unloaded copy/config/folder changed across cold start")
		}
		success("DELETE", "/api/plugins/"+url.PathEscape(id), nil)
		in, e := observer.Installation(context.Background(), id)
		if e != nil || in.Installed {
			t.Fatal("unloaded uninstall retained installed membership", id, e)
		}
		cloneObservations[id].(map[string]any)["installed_after_uninstall"] = in.Installed
		cloneObservations[id].(map[string]any)["cold_public_config"] = configRead(id)
		cloneObservations[id].(map[string]any)["stored_config_after_uninstall"] = persistRead(id)
		if folderHas(id) {
			t.Fatal("unloaded uninstall retained folder membership")
		}
		request("GET", "/api/v1/plugin/"+url.PathEscape(id)+"/snapshot", nil, 404)
		if code := request("GET", "/api/v1/plugin/file/"+url.PathEscape(id)+"/"+manifest.Entry, nil, 200); !bytes.Equal(code, payload) {
			t.Fatal("source unloaded uninstall should retain copied code")
		}
		if string(persistRead(id)["marker"]) != `"owned-source"` || len(configRead(id)) != 0 {
			t.Fatal("source unloaded uninstall should retain stored configuration while public getter stays empty")
		}
	}
	if string(configRead(source)["marker"]) != `"owned-source"` {
		t.Fatal("clone suffix uninstall removed source config")
	}
	snapshot(source)
	success("DELETE", "/api/plugins/"+source, nil)
	request("GET", "/api/v1/plugin/"+source+"/snapshot", nil, 404)
	if code := request("GET", "/api/v1/plugin/file/"+source+"/"+manifest.Entry, nil, 200); !bytes.Equal(code, payload) {
		t.Fatal("primary retained code stream must stay200 with exact bytes")
	}
	observations["primary_uninstall"] = map[string]any{"api_status": 404, "code_status": 200, "code_sha256": digest}
	if destination := os.Getenv("PLUGIN_CLONE_SUFFIX_PROCESS_EVIDENCE"); destination != "" {
		raw, _ := json.MarshalIndent(observations, "", "  ")
		if os.WriteFile(destination, raw, 0600) != nil {
			t.Fatal("write owned clone boundary evidence")
		}
	}
	t.Log("actual fixed clone API: underscore/21+/96/97/255/Unicode/full lower independent SDK+final init1/config/cold/API-file removal; dot/dash/space/NFKC success+unloaded code/config/folder/cold/uninstall side effects; physical256 and unsafe component failures")
}

func mustCloneSuffixJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
