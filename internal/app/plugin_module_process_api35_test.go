package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Source: plugin.py:873 API getter ignores business get_state; plugin endpoint
// route registration removes previous per-instance routes then adds both API
// versions, with anonymous/default API-key/bear authentication. This gate runs
// independent production cmd/server and SDK processes; only plugin/repo are owned.
func TestPluginAPI35ProductionCLI(t *testing.T) {
	serverBinary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY")
	first := os.Getenv("API35_PROCESS_FIXTURE_BINARY")
	second := os.Getenv("API35_PROCESS_V2_FIXTURE_BINARY")
	if serverBinary == "" || first == "" || second == "" {
		t.Skip("unverified: production server and two actual owned API35 artifacts required")
	}
	type release struct {
		declaration artifacts.Release
		payload     []byte
		worker      []byte
	}
	load := func(name, version string) release {
		t.Helper()
		payload, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := exec.Command(name, "--manifest").Output()
		if err != nil {
			t.Fatal(err)
		}
		var manifest plugins.Manifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != "OwnedAPI35Fixture" || manifest.Version != version || manifest.Validate() != nil {
			t.Fatal("incorrect actual API35 manifest")
		}
		worker := payload
		entryPath := os.Getenv("API35_PROCESS_FRONTEND_ENTRY")
		if entryPath == "" {
			entryPath = "../../testdata/standard-api/ui/remoteEntry.js"
		}
		entry, err := os.ReadFile(entryPath)
		if err != nil {
			t.Fatal(err)
		}
		stylesheetPath := os.Getenv("API35_PROCESS_FRONTEND_STYLES")
		if stylesheetPath == "" {
			stylesheetPath = "../../testdata/standard-api/ui/ownedapi35fixture-ui.css"
		}
		stylesheet, err := os.ReadFile(stylesheetPath)
		if err != nil {
			t.Fatal(err)
		}
		entry = []byte(strings.ReplaceAll(string(entry), "__OWNED_API35_VERSION__", version))
		var compressed bytes.Buffer
		archive := zip.NewWriter(&compressed)
		inventory := []artifacts.PackageFile{}
		for _, member := range []struct {
			name string
			data []byte
		}{{manifest.Entry, worker}, {"frontend/assets/remoteEntry.js", entry}, {"frontend/assets/ownedapi35fixture-ui.css", stylesheet}, {"frontend/assets/owned-备份.css", []byte("owned-css")}, {"frontend/assets/owned-💫.css", []byte("owned-css")}, {"frontend/assets/owned-é.css", []byte("owned-css")}} {
			file, err := archive.Create(member.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = file.Write(member.data); err != nil {
				t.Fatal(err)
			}
			inventory = append(inventory, artifacts.PackageFile{Path: member.name, Size: int64(len(member.data)), SHA256: originalFrontendHash(member.data)})
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		payload = compressed.Bytes()
		digest := sha256.Sum256(payload)
		sum := hex.EncodeToString(digest[:])
		workerDigest := sha256.Sum256(worker)
		return release{artifacts.Release{Version: version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", SHA256: sum, ExecutableSHA256: hex.EncodeToString(workerDigest[:]), Size: int64(len(payload)), HostContract: pluginPureSDKContract, Files: inventory}}}, payload, worker}

	}
	v1, v2 := load(first, "1.0"), load(second, "2.0")
	var promoted atomic.Bool
	index := artifacts.Index{SchemaVersion: 1, PluginID: "OwnedAPI35Fixture", CurrentVersion: "1.0", Releases: []artifacts.Release{v1.declaration, v2.declaration}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			copy := index
			if promoted.Load() {
				copy.CurrentVersion = "2.0"
			}
			_ = json.NewEncoder(w).Encode(copy)
		case "/one":
			w.Write(v1.payload)
		case "/two":
			w.Write(v2.payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/one"
	index.Releases[1].Artifacts[0].URL = repository.URL + "/two"
	dir := t.TempDir()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	key := hex.EncodeToString(secret[:])
	bootstrap, _ := json.Marshal(map[string]any{"username": "api35-acceptance", "password": "owned-api35-password", "settings": Settings{APIToken: key, PluginMarkets: []string{repository.URL + "/index"}}})
	boot := exec.Command(serverBinary, "--data-dir", dir, "--bootstrap-stdin")
	boot.Stdin = bytes.NewReader(bootstrap)
	if raw, err := boot.CombinedOutput(); err != nil || !bytes.Contains(raw, []byte("bootstrap_complete")) {
		t.Fatal("owned bootstrap failed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
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
		case err := <-done:
			if err != nil {
				t.Error("actual shutdown failed", err)
			}
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("shutdown timeout")
		}
		process = nil
	}
	defer func() {
		if destination := os.Getenv("API35_PROCESS_DIAGNOSTIC_LOG"); destination != "" {
			raw, _ := os.ReadFile(filepath.Join(dir, "process.log"))
			_ = os.WriteFile(destination, raw, 0600)
		}
	}()
	defer stop()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	plain := &http.Client{Timeout: 20 * time.Second}
	request := func(method, path string, body []byte, auth string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, origin+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Correlation-ID", "owned-correlation")
		if strings.Contains(path, "/plugin/file/") {
			req.Header.Set("Range", "bytes=0-1")
		}
		if auth == "key" {
			req.Header.Set("X-API-KEY", key)
		}
		if auth == "wrong" {
			req.Header.Set("X-API-KEY", "owned-wrong-key")
		}
		selected := plain
		if auth == "cookie" {
			selected = client
		}
		resp, err := selected.Do(req)
		if err != nil {
			t.Fatal("actual API transport failed", method, strings.SplitN(path, "?", 2)[0])
		}
		defer resp.Body.Close()
		if want == 200 && strings.Contains(path, "/plugin/file/") && strings.HasSuffix(path, url.PathEscape("owned-é.css")) {
			wantHeader := append([]byte("inline; filename=owned-"), byte(0xe9))
			wantHeader = append(wantHeader, []byte(".css")...)
			if !bytes.Equal([]byte(resp.Header.Get("Content-Disposition")), wantHeader) {
				t.Fatal("actual source Latin1 header wire differs")
			}
		}
		if strings.Contains(path, "/frontend/") && !strings.Contains(path, "/plugin/file/") && strings.Contains(path, "/assets/owned-") && resp.Header.Get("Content-Disposition") != "" {
			t.Fatal("Go frontend ABI unexpectedly adopted Source inline header")
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("actual %s %s status=%d expected=%d; response withheld", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, want)
		}
		return raw
	}
	start := func() {
		t.Helper()
		process = exec.Command(serverBinary, "--listen", address, "--data-dir", dir)
		process.Env = append(os.Environ(), "MP_SERVER_HOST=")
		process.Stdout = log
		process.Stderr = log
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(12 * time.Second)
		for {
			resp, err := plain.Get(origin + "/api/auth/status")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("actual server listener unavailable")
			}
			time.Sleep(30 * time.Millisecond)
		}
		credentials, _ := json.Marshal(map[string]string{"username": "api35-acceptance", "password": "owned-api35-password"})
		request("POST", "/api/auth/login", credentials, "cookie", 200)
	}
	management := func(method, path string, value any) {
		t.Helper()
		raw, _ := json.Marshal(value)
		result := request(method, path, raw, "cookie", 200)
		var success struct{ Data struct{ Success bool } }
		if json.Unmarshal(result, &success) != nil || !success.Data.Success {
			t.Fatal("management action did not actually succeed", method, path)
		}
	}
	const source = "OwnedAPI35Fixture"
	const clone = source + "copy"
	state := func(id, path, auth string) map[string]json.RawMessage {
		t.Helper()
		var value map[string]json.RawMessage
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+path, nil, auth, 200), &value) != nil || len(value["pid"]) == 0 {
			t.Fatal("actual SDK snapshot absent", id, path)
		}
		return value
	}
	start()
	management("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository.URL + "/index"})
	management("PUT", "/api/plugins/"+source, map[string]any{"enabled": false, "marker": "source", "mode": "default"})
	before := state(source, "/state", "key")
	state(source, "/state", "cookie")
	for _, endpoint := range []string{"/noop", "/object"} {
		method := "GET"
		if endpoint == "/noop" {
			method = "POST"
		}
		var business map[string]any
		if json.Unmarshal(request(method, "/api/v2/plugin/"+source+endpoint, []byte(`{"owned":true}`), "cookie", 200), &business) != nil {
			t.Fatal("ordinary business DTO missing")
		}
		if endpoint == "/noop" && business["success"] != false {
			t.Fatal("business false wire changed", business)
		}
		if endpoint == "/object" && business["error"] == nil {
			t.Fatal("ordinary error field changed", business)
		}
	}
	request("GET", "/api/v1/plugin/file/"+source+"/owned-api35-fixture", nil, "key", 403)
	if raw := request("GET", "/api/v1/plugin/file/"+strings.ToLower(source)+"/owned-api35-fixture?inert=1", nil, "cookie", 200); !bytes.Equal(raw, v1.worker) {
		t.Fatal("actual rooted package stream bytes/range behavior differs")
	}
	var staticDescriptor struct {
		Data struct {
			EntryPath string `json:"entry_path"`
		}
	}
	if json.Unmarshal(request("GET", "/api/plugins/"+source+"/frontend", nil, "cookie", 200), &staticDescriptor) != nil {
		t.Fatal("actual pinned frontend descriptor for filename contract")
	}
	for _, name := range []string{"owned-备份.css", "owned-💫.css"} {
		var failure struct{ Detail string }
		if json.Unmarshal(request("GET", "/api/v1/plugin/file/"+source+"/frontend/assets/"+url.PathEscape(name), nil, "cookie", 500), &failure) != nil || failure.Detail != "Internal Server Error" {
			t.Fatal("actual fixed nonLatin1 inline-header exception")
		}
		pinned := strings.TrimSuffix("/api"+staticDescriptor.Data.EntryPath, "remoteEntry.js") + url.PathEscape(name)
		if data := request("GET", pinned, nil, "cookie", 200); string(data) != "owned-css" {
			t.Fatal("pinned Go ABI has no source inline-header failure; physical bytes differ")
		}
	}
	if data := request("GET", "/api/v1/plugin/file/"+source+"/frontend/assets/"+url.PathEscape("owned-é.css"), nil, "cookie", 200); string(data) != "owned-css" {
		t.Fatal("actual Latin1 filename stream control")
	}
	request("GET", "/api/v1/plugin/file/"+source+"/", nil, "cookie", 403)
	request("GET", "/api/v1/plugin/file/"+source+"/../outside", nil, "cookie", 403)

	request("GET", "/api/v1/plugin/"+source+"/anonymous", nil, "", 200)
	request("GET", "/api/v1/plugin/"+source+"/default", nil, "", 401)
	request("GET", "/api/v1/plugin/"+source+"/default", nil, "key", 200)
	request("POST", "/api/v1/plugin/"+source+"/default", nil, "key", 405)
	request("GET", "/api/v2/plugin/"+source+"/state", nil, "", 401)
	request("GET", "/api/v2/plugin/"+source+"/state", nil, "wrong", 401)
	request("GET", "/api/v2/plugin/"+source+"/state?apikey="+key, nil, "", 200)
	request("GET", "/api/plugins/"+source+"/api/state", nil, "cookie", 200)
	payload := []byte("{ \"message\": \"签名回调\" }\n")
	raw := request("PATCH", "/api/v1/plugin/"+source+"/change/owned?name=first&name=last&proof="+key, payload, "key", 201)
	var result struct{ Request plugins.HTTPRequest }
	if json.Unmarshal(raw, &result) != nil || !bytes.Equal(result.Request.Body, payload) || result.Request.Params["item"] != "owned" || result.Request.LastQuery("name") != "last" || !result.Request.CredentialMatches["proof"] || len(result.Request.Query["proof"]) != 0 || len(result.Request.Query["apikey"]) != 0 || result.Request.Headers["x-correlation-id"] != "owned-correlation" {
		t.Fatal("actual path/query/credential/header/body binding changed")
	}
	request("POST", "/api/v1/plugin/"+source+"/change/item", payload, "key", 422)
	request("POST", "/api/v1/plugin/"+source+"/change/item?name=x", nil, "key", 422)
	if raw := request("DELETE", "/api/v1/plugin/"+source+"/empty", nil, "key", 204); len(raw) != 0 {
		t.Fatal("actual204 has a body")
	}
	request("GET", "/api/v1/plugin/"+source+"/failure", nil, "key", 503)
	largeBinary := bytes.Repeat([]byte{255, 0, 1, 254, 10}, 128<<10)
	if raw := request("POST", "/api/v1/plugin/"+source+"/binary", largeBinary, "key", 200); !bytes.Equal(raw, largeBinary) {
		t.Fatal("ordinary binary API upload/download bytes changed")
	}

	after := state(source, "/state", "key")
	for _, field := range []string{"pid", "init_count", "config", "version"} {
		if !bytes.Equal(after[field], before[field]) {
			t.Fatal("API method reinitialized plugin", field)
		}
	}
	management("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": "copy"})
	management("PUT", "/api/plugins/"+clone, map[string]any{"enabled": false, "marker": "clone", "mode": "default"})
	copied := state(clone, "/state", "key")
	if bytes.Equal(copied["pid"], before["pid"]) || bytes.Equal(copied["config"], before["config"]) {
		t.Fatal("clone borrowed source object/config")
	}

	frontend := func(id string) originalFrontendDescriptor {
		var envelope struct{ Data originalFrontendDescriptor }
		if json.Unmarshal(request("GET", "/api/plugins/"+id+"/frontend", nil, "cookie", 200), &envelope) != nil || envelope.Data.InstanceID != id {
			t.Fatal("actual frontend descriptor absent", id)
		}
		return envelope.Data
	}
	clonedFront := frontend(clone)
	entry := request("GET", "/api"+clonedFront.EntryPath, nil, "cookie", 200)
	if !bytes.Contains(entry, []byte("plugin/"+clone+"/")) || !bytes.Contains(entry, []byte("['OwnedAPI','35Fixture'].join('')")) {
		t.Fatal("literal/global clone identity or runtime source identity was lost")
	}
	cssRoot := strings.TrimSuffix("/api"+clonedFront.EntryPath, "remoteEntry.js")
	css := request("GET", cssRoot+strings.ToLower(clone)+"-ui.css", nil, "cookie", 200)
	if !bytes.Contains(css, []byte("--"+strings.ToLower(clone)+"-probe")) {
		t.Fatal("class/CSS/file clone derivation absent")
	}
	request("GET", cssRoot+strings.ToLower(source)+"-ui.css", nil, "cookie", 404)
	request("GET", "/api/v1/plugin/folders", nil, "cookie", 200)
	request("GET", "/api/v2/user/config/PluginOrder", nil, "cookie", 200)
	grandchild := clone + "nested"
	management("POST", "/api/plugins/"+clone+"/clone", map[string]any{"suffix": "nested"})
	third := state(grandchild, "/state", "cookie")
	if bytes.Equal(third["pid"], copied["pid"]) {
		t.Fatal("grandclone borrowed its source SDK")
	}
	grandFront := frontend(grandchild)
	if raw := request("GET", "/api"+grandFront.EntryPath, nil, "cookie", 200); !bytes.Contains(raw, []byte("plugin/"+grandchild+"/")) {
		t.Fatal("grandclone used canonical instead of actual source class")
	}
	management("DELETE", "/api/plugins/"+grandchild, nil)
	request("GET", "/api/v1/plugin/"+grandchild+"/state", nil, "cookie", 404)
	management("PUT", "/api/plugins/"+source, map[string]any{"enabled": false, "marker": "source", "mode": "alternate"})
	request("GET", "/api/v1/plugin/"+source+"/state", nil, "key", 404)
	changed := state(source, "/state_changed", "key")
	if !bytes.Equal(changed["pid"], before["pid"]) {
		t.Fatal("config change did not reinit same loaded object")
	}
	state(clone, "/state", "key")
	promoted.Store(true)
	management("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository.URL + "/index", "force": true})
	request("GET", "/api/v1/plugin/"+source+"/state_changed", nil, "key", 404)
	updated := state(source, "/updated", "key")
	if bytes.Equal(updated["pid"], changed["pid"]) || string(updated["version"]) != `"2.0"` || !bytes.Equal(updated["config"], changed["config"]) {
		t.Fatal("actual update route/object/config binding")
	}
	state(clone, "/state", "key")
	stop()
	start()
	cold := state(source, "/updated", "key")
	coldClone := state(clone, "/state", "key")
	if bytes.Equal(cold["pid"], updated["pid"]) || bytes.Equal(coldClone["pid"], copied["pid"]) || !bytes.Equal(cold["config"], updated["config"]) || !bytes.Equal(coldClone["config"], copied["config"]) {
		t.Logf("owned clone config before=%s after=%s", copied["config"], coldClone["config"])
		t.Fatalf("cold API route/object/config recovery: source_new_pid=%t clone_new_pid=%t source_config_equal=%t clone_config_equal=%t", !bytes.Equal(cold["pid"], updated["pid"]), !bytes.Equal(coldClone["pid"], copied["pid"]), bytes.Equal(cold["config"], updated["config"]), bytes.Equal(coldClone["config"], copied["config"]))
	}

	// A browser navigation aborts a global Axios request. It must not destroy
	// the original loaded object merely because this Go worker is serial.
	aliveBefore := state(clone, "/state", "cookie")
	cancelCtx, cancelRequest := context.WithCancel(context.Background())
	slowReq, err := http.NewRequestWithContext(cancelCtx, "GET", origin+"/api/v1/plugin/"+clone+"/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	slowDone := make(chan error, 1)
	go func() {
		resp, err := client.Do(slowReq)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		slowDone <- err
	}()
	time.Sleep(250 * time.Millisecond)
	cancelRequest()
	select {
	case err := <-slowDone:
		if err == nil {
			t.Fatal("controlled slow HTTP was not canceled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled HTTP did not return")
	}
	state(source, "/updated", "cookie")
	aliveAfter := state(clone, "/state", "cookie")
	for _, field := range []string{"pid", "init_count", "config", "version"} {
		if !bytes.Equal(aliveBefore[field], aliveAfter[field]) {
			t.Fatal("browser cancellation restarted/reinitialized the actual serial object", field)
		}
	}
	request("GET", "/api/plugins/"+clone+"/frontend", nil, "cookie", 200)
	request("GET", "/api/plugins/"+clone+"/page", nil, "cookie", 200)
	t.Log("actual HTTP cancellation drained without killing serial SDK; same PID/init/config and frontend/page remain usable")
	if statePath := os.Getenv("API35_PROCESS_BROWSER_STATE"); statePath != "" {
		encoded, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "repository": repository.URL, "source_id": source, "clone_id": clone})
		if os.WriteFile(statePath, encoded, 0600) != nil {
			t.Fatal("write API35 browser state")
		}
		t.Log("API35 browser ready; owned source2/clone1 installed; waiting for completion marker")
		deadline := time.Now().Add(45 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				old := process.Process.Pid
				stop()
				start()
				value, _ := json.Marshal(map[string]int{"previous_pid": old, "current_pid": process.Process.Pid})
				_ = os.Remove(statePath + ".restart")
				if os.WriteFile(statePath+".restart.done", value, 0600) != nil {
					t.Fatal("write browser restart result")
				}
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("API35 browser acceptance did not finish")
		}
	}
	management("DELETE", "/api/plugins/"+source, nil)
	request("GET", "/api/v1/plugin/"+source+"/updated", nil, "key", 404)
	request("GET", "/api/v2/plugin/"+source+"/anonymous", nil, "", 404)
	state(clone, "/state", "key")
	if raw := request("GET", "/api/v1/plugin/file/"+source+"/owned-api35-fixture", nil, "cookie", 200); !bytes.Equal(raw, v2.worker) {
		t.Fatal("primary uninstall lost retained code file stream")
	}
	management("DELETE", "/api/plugins/"+clone, nil)
	request("GET", "/api/v1/plugin/file/"+clone+"/owned-api35-fixture", nil, "cookie", 404)
	request("GET", "/api/v1/plugin/"+clone+"/state", nil, "key", 404)
	t.Logf("actual API35 Linux=%t: Source nonLatin1 filename500/Latin1 header bytes/pinned Go ABI200; v1/v2/default/anonymous/bear/key/native-session, path/query/body/header,204/error, no implicit reinit, config refresh, cloned isolation, update, cold restart and route removal verified; no old services", strings.HasPrefix(runtime.GOOS, "linux"))
}
