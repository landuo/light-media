package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Unlike in-process handler tests, this gate runs the embedded production UI,
// App.Start, scheduler, shutdown and startup recovery in a real server process.
// Expected transitions come from fixed plugin.py:72,121,156,641 and plugin API
// endpoints:576,739,933,975,992. Only the repository and plugin are synthetic.
func TestPluginModuleProductionProcessLifecycle(t *testing.T) {
	serverBinary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY")
	v1Binary, v2Binary := os.Getenv("OWNED_STANDARD_FIXTURE_BINARY"), os.Getenv("OWNED_STANDARD_FIXTURE_V2_BINARY")
	if serverBinary == "" {
		t.Skip("unverified: PLUGIN_PROCESS_SERVER_BINARY must name the actual cmd/server executable")
	}
	if v1Binary == "" || v2Binary == "" {
		t.Fatal("two independently compiled actual SDK artifacts are required")
	}
	type release struct {
		declaration artifacts.Release
		payload     []byte
	}
	load := func(path, version string) release {
		t.Helper()
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := exec.Command(path, "--manifest").Output()
		if err != nil {
			t.Fatal("actual SDK manifest", err)
		}
		var manifest plugins.Manifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedStandardFixtureID || manifest.Version != version {
			t.Fatal("incorrect actual artifact identity/version")
		}
		sum := sha256.Sum256(payload)
		digest := hex.EncodeToString(sum[:])
		return release{artifacts.Release{Version: version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginBaseSDKContract}}}, payload}
	}
	v1, v2 := load(v1Binary, "1.0"), load(v2Binary, "2.0")
	if bytes.Equal(v1.payload, v2.payload) {
		t.Fatal("version update requires different executable bytes")
	}
	var promoted atomic.Bool
	var metadataUnavailable atomic.Bool
	var artifactHits atomic.Int64
	var metadataHits atomic.Int64
	index := artifacts.Index{SchemaVersion: 1, PluginID: ownedStandardFixtureID, CurrentVersion: "1.0", Releases: []artifacts.Release{v1.declaration, v2.declaration}, History: map[string]string{"1.0": "Owned initial version", "2.0": "Owned executable update", "0.1": "### Author notes only\n\n**Source history**\n\n- Configuration retained\n- Clone isolated\n\n[Reference](https://example.org/plugin)\n\n`0.1` has no downloadable artifact."}}
	// Keep author order distinct from lexical version order for the real UI.
	index.HistoryJSON = json.RawMessage(`{"2.0":"Owned executable update","1.0":"Owned initial version","0.1":"### Author notes only\n\n**Source history**\n\n- Configuration retained\n- Clone isolated\n\n[Reference](https://example.org/plugin)\n\n` + "`0.1`" + ` has no downloadable artifact."}`)
	var frontend artifacts.Index
	var frontendPayload []byte
	if binary := os.Getenv("OWNED_FRONTEND_FIXTURE_BINARY"); binary != "" {
		frontend, frontendPayload = ownedProcessFrontendArchive(t, binary)
	}
	var renderIndex artifacts.Index
	var renderPayload []byte
	if os.Getenv("OWNED_RENDER_PROCESS_FIXTURE_BINARY") != "" {
		renderIndex, renderPayload = originalMarketOwnedIndex(t, "OWNED_RENDER_PROCESS_FIXTURE_BINARY")
	}
	var base artifacts.Index
	var basePayload []byte
	if os.Getenv("OWNED_BASE_PROCESS_FIXTURE_BINARY") != "" {
		base, basePayload = originalMarketOwnedIndex(t, "OWNED_BASE_PROCESS_FIXTURE_BINARY")
	}
	var exact, legacy artifacts.Index
	var exactPayload, legacyPayload []byte
	var eventIndex artifacts.Index
	var eventPayload []byte
	if os.Getenv("OWNED_EVENT_PROCESS_FIXTURE_BINARY") != "" {
		eventIndex, eventPayload = originalMarketOwnedIndex(t, "OWNED_EVENT_PROCESS_FIXTURE_BINARY")
	}
	if os.Getenv("OWNED_EXACT_PROCESS_FIXTURE_BINARY") != "" {
		exact, exactPayload = originalMarketOwnedIndex(t, "OWNED_EXACT_PROCESS_FIXTURE_BINARY")
	}
	if os.Getenv("OWNED_LEGACY_ID_FIXTURE_BINARY") != "" {
		legacy, legacyPayload = originalCatalogFixture(t, "OWNED_LEGACY_ID_FIXTURE_BINARY")
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index", "/version-one/index", "/version-two/index", "/same-version/index", "/incompatible/index":
			metadataHits.Add(1)
			if metadataUnavailable.Load() {
				http.Error(w, "owned repository temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			copy := index
			if promoted.Load() {
				copy.CurrentVersion = "2.0"
			}
			switch r.URL.Path {
			case "/incompatible/index":
				copy.CurrentVersion = "2.0"
				copy.SystemVersion = json.RawMessage(`">=3"`)
			case "/version-one/index":
				copy.CurrentVersion, copy.Label = "1.0", "Owned version one"
			case "/version-two/index":
				copy.CurrentVersion, copy.Label = "2.0", "Owned version two"
			case "/same-version/index":
				copy.CurrentVersion, copy.Label = "2.0", "Owned equal version"
			}
			if frontendPayload == nil && basePayload == nil && exactPayload == nil && renderPayload == nil {
				_ = json.NewEncoder(w).Encode(copy)
			} else {
				items := []artifacts.Index{copy}
				if renderPayload != nil {
					items = append(items, renderIndex)
				}
				if frontendPayload != nil {
					items = append(items, frontend)
				}
				if basePayload != nil {
					items = append(items, base)
				}
				if exactPayload != nil {
					items = append(items, exact)
				}
				_ = json.NewEncoder(w).Encode(artifacts.RepositoryIndex{SchemaVersion: 1, Plugins: items})
			}
		case "/market-metadata":
			if os.Getenv("PLUGIN_PROCESS_BROWSER_MARKET") != "true" {
				http.NotFound(w, r)
				return
			}
			metadataHits.Add(1)
			if metadataUnavailable.Load() {
				http.Error(w, "owned metadata unavailable", http.StatusServiceUnavailable)
				return
			}
			// Explicitly display-only Python metadata, never fake executable cards.
			values := map[string]any{}
			for i := 0; i < 23; i++ {
				author, label := "Owned author A", "owned-media,owned-tools"
				if i%2 != 0 {
					author, label = "Owned author B", "owned-timer"
				}
				description := fmt.Sprintf("Owned description %02d", i)
				if i == 0 {
					description = "Boundary description"
				}
				values[fmt.Sprintf("OwnedMarket%02d", i)] = map[string]any{"v2": true, "name": fmt.Sprintf("Owned filter %02d", i), "version": "1.0", "description": description, "author": author, "labels": label}
			}
			_ = json.NewEncoder(w).Encode(values)
		case "/plugin/statistic":
			if os.Getenv("PLUGIN_PROCESS_BROWSER_MARKET") != "true" {
				http.NotFound(w, r)
				return
			}
			values := map[string]int{ownedStandardFixtureID: 30, "OwnedBaseFixture": 40}
			for i := 0; i < 23; i++ {
				values[fmt.Sprintf("OwnedMarket%02d", i)] = i
			}
			_ = json.NewEncoder(w).Encode(values)
		case "/legacy-index":
			_ = json.NewEncoder(w).Encode(legacy)
		case "/render-worker":
			_, _ = w.Write(renderPayload)
		case "/events-index":
			_ = json.NewEncoder(w).Encode(eventIndex)
		case "/events-worker":
			_, _ = w.Write(eventPayload)
		case "/legacy-worker":
			_, _ = w.Write(legacyPayload)
		case "/exact-worker":
			_, _ = w.Write(exactPayload)
		case "/base-worker":
			_, _ = w.Write(basePayload)
		case "/frontend.zip":
			_, _ = w.Write(frontendPayload)
		case "/worker1":
			artifactHits.Add(1)
			_, _ = w.Write(v1.payload)
		case "/worker2":
			artifactHits.Add(1)
			_, _ = w.Write(v2.payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker1"
	index.Releases[1].Artifacts[0].URL = repository.URL + "/worker2"
	if frontendPayload != nil {
		frontend.Releases[0].Artifacts[0].URL = repository.URL + "/frontend.zip"
	}
	if renderPayload != nil {
		renderIndex.Releases[0].Artifacts[0].URL = repository.URL + "/render-worker"
	}
	if basePayload != nil {
		base.Releases[0].Artifacts[0].URL = repository.URL + "/base-worker"
	}
	if exactPayload != nil {
		exact.Releases[0].Artifacts[0].URL = repository.URL + "/exact-worker"
	}
	if legacyPayload != nil {
		legacy.Releases[0].Artifacts[0].URL = repository.URL + "/legacy-worker"
	}
	if eventPayload != nil {
		eventIndex.Releases[0].Artifacts[0].URL = repository.URL + "/events-worker"
	}
	dir := t.TempDir()
	var githubFixture *ownedGitHubProcessFixture
	var centerFixture *ownedPluginCenter
	if os.Getenv("PLUGIN_PROCESS_CENTER_FIXTURE") == "true" {
		centerFixture = newOwnedPluginCenter(t)
	}
	if os.Getenv("PLUGIN_PROCESS_GITHUB_FIXTURE") == "true" {
		if os.Getenv("PLUGIN_PROCESS_EDITABLE_MARKET") != "true" {
			t.Fatal("owned GitHub fixture requires editable test sources")
		}
		githubFixture = newOwnedGitHubProcessFixture(t, dir, repository.URL, index, v1.payload, v2.payload)
	}
	if os.Getenv("PLUGIN_PROCESS_DEV_WATCH_FIXTURE") == "true" {
		invalidContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		invalid := exec.CommandContext(invalidContext, serverBinary, "--listen", "127.0.0.1:0", "--data-dir", filepath.Join(dir, "invalid-dev-startup"))
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "DEV=") && !strings.HasPrefix(entry, "LIGHT_MEDIA_SAFE_MODE=") && !strings.HasPrefix(entry, "PLUGIN_MARKET=") {
				invalid.Env = append(invalid.Env, entry)
			}
		}
		invalid.Env = append(invalid.Env, "DEV=owned-invalid-boolean", "LIGHT_MEDIA_SAFE_MODE=true", "PLUGIN_MARKET=")
		result, err := invalid.CombinedOutput()
		timedOut := invalidContext.Err() != nil
		cancel()
		if err == nil || timedOut || string(result) != "invalid_plugin_developer_mode\n" {
			t.Fatal("actual CLI did not reject invalid DEV before application startup")
		}
	}
	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listen.Addr().String()
	_ = listen.Close()
	origin := "http://" + address
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	apiKey := hex.EncodeToString(secret[:])
	// These are synthetic local test credentials, never a user account.
	const username, password = "plugin-acceptance", "owned-browser-test-password"
	bootstrap, _ := json.Marshal(map[string]any{"username": username, "password": password, "settings": Settings{APIToken: apiKey, PluginMarkets: []string{repository.URL + "/index"}}})
	command := exec.Command(serverBinary, "--data-dir", dir, "--bootstrap-stdin")
	command.Stdin = bytes.NewReader(bootstrap)
	if err := command.Run(); err != nil {
		t.Fatal("isolated production bootstrap", err)
	}
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	var process *exec.Cmd
	developerMode := false
	safeMode := false
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
				t.Error("production shutdown failed", err)
			}
		case <-time.After(15 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("production shutdown failed to join")
		}
		process = nil
	}
	defer stop()
	start := func() {
		t.Helper()
		process = exec.Command(serverBinary, "--listen", address, "--data-dir", dir)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "PLUGIN_MARKET=") && !strings.HasPrefix(entry, "DEV=") {
				process.Env = append(process.Env, entry)
			}
		}
		if os.Getenv("PLUGIN_PROCESS_EDITABLE_MARKET") != "true" {
			process.Env = append(process.Env, "PLUGIN_MARKET="+repository.URL+"/index")
		}
		process.Env = append(process.Env, fmt.Sprintf("LIGHT_MEDIA_SAFE_MODE=%t", safeMode), "MOVIEPILOT_DOCKER=false")
		if developerMode {
			process.Env = append(process.Env, "DEV=true")
		} else {
			process.Env = append(process.Env, "DEV=false")
		}
		if githubFixture != nil {
			keep := []string{}
			for _, entry := range process.Env {
				key, _, _ := strings.Cut(entry, "=")
				switch strings.ToLower(key) {
				case "ssl_cert_file", "https_proxy", "http_proxy", "all_proxy", "no_proxy":
					continue
				}
				keep = append(keep, entry)
			}
			process.Env = append(keep, githubFixture.childEnvironment()...)
		}
		process.Stdout, process.Stderr = log, log
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			response, err := client.Get(origin + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("production listener did not become ready; inspect isolated process.log")
	}
	request := func(method, path string, input any, want int) []byte {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(raw)
		}
		r, err := http.NewRequest(method, origin+path, body)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-API-KEY", apiKey)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("production request failed", method, path, err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil || (want != 0 && response.StatusCode != want) {
			var failure struct{ Error struct{ Code string } }
			_ = json.Unmarshal(raw, &failure)
			code := failure.Error.Code
			if code == "" || len(code) > 96 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") != "" {
				code = "UNCLASSIFIED"
			}
			t.Fatalf("production %s %s: status %d, expected %d; error_code=%s; body withheld", method, path, response.StatusCode, want, code)
		}
		return raw
	}
	success := func(method, path string, body any) {
		t.Helper()
		raw := request(method, path, body, 200)
		var result struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &result) != nil || !result.Data.Success {
			t.Fatalf("production %s %s returned HTTP 200 without business success", method, path)
		}
	}
	type snapshot struct {
		DataPath  string          `json:"data_path"`
		Version   string          `json:"version"`
		Nonce     int             `json:"nonce"`
		InitCount int             `json:"init_count"`
		Config    json.RawMessage `json:"config"`
		Value     json.RawMessage `json:"value"`
		Calls     map[string]int  `json:"calls"`
	}
	read := func(id string) snapshot {
		t.Helper()
		raw := request("GET", "/api/v1/plugin/"+id+"/echo", nil, 200)
		var value snapshot
		if json.Unmarshal(raw, &value) != nil || value.Nonce <= 0 || value.Nonce == os.Getpid() {
			t.Fatal("missing actual SDK child process snapshot")
		}
		return value
	}
	install := func(version string, force bool) {
		t.Helper()
		success("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository.URL + "/index", "release_version": version, "force": force})
	}
	checkConfig := func(id, key, version string) snapshot {
		t.Helper()
		value := read(id)
		var config map[string]any
		if value.Version != version || json.Unmarshal(value.Config, &config) != nil || config["key"] != key || string(value.Value) != fmt.Sprintf("%q", key) {
			t.Fatal("actual code/config/data transition is incorrect", id, version)
		}
		return value
	}
	start()
	unauthenticated, err := (&http.Client{Timeout: 10 * time.Second}).Get(origin + "/api/plugins/market")
	if err != nil {
		t.Fatal("production login boundary request failed", err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatal("application login protection changed while official authorization is excluded", unauthenticated.StatusCode)
	}
	request("POST", "/api/auth/login", map[string]string{"username": username, "password": password}, 200)
	install("1.0", false)
	initial := read(ownedStandardFixtureID)
	if initial.Version != "1.0" || initial.InitCount != 1 || string(initial.Config) != "null" {
		t.Fatal("installation did not create exactly one unconfigured persistent instance")
	}
	success("PUT", "/api/plugins/"+ownedStandardFixtureID, map[string]any{"enabled": true, "key": "source"})
	saved := checkConfig(ownedStandardFixtureID, "source", "1.0")
	if saved.Nonce != initial.Nonce || saved.InitCount != initial.InitCount+1 {
		t.Fatal("configuration save replaced the object instead of reinitializing it")
	}
	// Manual run and actual clock both invoke tick on the same object.
	request("POST", "/api/plugins/services/run", map[string]string{"instance_id": ownedStandardFixtureID, "service_id": "owned_standard_tick"}, 200)
	deadline := time.Now().Add(5 * time.Second)
	for read(ownedStandardFixtureID).Calls["tick"] < 2 {
		if time.Now().After(deadline) {
			t.Fatal("manual and timed service did not both execute")
		}
		time.Sleep(50 * time.Millisecond)
	}
	clone := ownedStandardFixtureID + "process"
	success("POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", map[string]string{"suffix": "Process"})
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": false, "key": "clone"})
	cloned := checkConfig(clone, "clone", "1.0")
	if cloned.Nonce == saved.Nonce {
		t.Fatal("clone shares source runtime")
	}
	var cloneFile string
	if cloned.DataPath != "" {
		if cloned.DataPath != filepath.Join(dir, "plugin-data", clone) || saved.DataPath != filepath.Join(dir, "plugin-data", ownedStandardFixtureID) {
			t.Fatal("worker received wrong instance data directory")
		}
		cloneFile = filepath.Join(cloned.DataPath, "owned-file.txt")
		if err := os.WriteFile(cloneFile, []byte("owned file survives reset and uninstall"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	success("POST", "/api/plugins/"+ownedStandardFixtureID+"/reload", nil)
	reloaded := checkConfig(ownedStandardFixtureID, "source", "1.0")
	if reloaded.Nonce == saved.Nonce || reloaded.InitCount != 1 {
		t.Fatal("reload reused the old worker")
	}
	promoted.Store(true)
	var all struct{ Data []pluginCard }
	if json.Unmarshal(request("GET", "/api/plugins?force=true", nil, 200), &all) != nil {
		t.Fatal("default all-plugin list unavailable")
	}
	versions := []string{}
	for _, card := range all.Data {
		if card.InstanceID == ownedStandardFixtureID {
			versions = append(versions, card.Version)
			if card.Version == "1.0" && (card.HasUpdate || card.RepositoryURL != "") || card.Version == "2.0" && !card.HasUpdate {
				t.Fatal("all-list mixed local class and remote update metadata")
			}
		}
	}
	if len(versions) != 2 || versions[0] != "1.0" || versions[1] != "2.0" {
		t.Fatal("default all-list did not retain local and upgrade entries in source order", versions)
	}
	install("2.0", true)
	updated := checkConfig(ownedStandardFixtureID, "source", "2.0")
	if updated.Nonce == reloaded.Nonce || checkConfig(clone, "clone", "1.0").Nonce != cloned.Nonce {
		t.Fatal("update failed to replace only source code/runtime")
	}
	// A failed init persists the saved config and mutates the existing object;
	// it must not silently roll back or turn a failed request into success.
	request("PUT", "/api/plugins/"+ownedStandardFixtureID, map[string]any{"enabled": false, "key": "failed-init", "fail_init": true}, 500)
	failed := checkConfig(ownedStandardFixtureID, "failed-init", "2.0")
	if failed.Nonce != updated.Nonce {
		t.Fatal("init failure replaced the existing object")
	}
	success("POST", "/api/plugins/"+ownedStandardFixtureID+"/reload", nil)
	request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 404)
	var failedClass struct{ Data originalPluginHistory }
	if json.Unmarshal(request("GET", "/api/plugins/history/"+ownedStandardFixtureID, nil, 200), &failedClass) != nil || failedClass.Data.CurrentVersion != "2.0" {
		t.Fatal("failed init class lost its source version")
	}
	var failedLocal struct{ Data []pluginCard }
	if json.Unmarshal(request("GET", "/api/plugins?state=installed", nil, 200), &failedLocal) != nil {
		t.Fatal("failed init local-class list unavailable")
	}
	foundFailedClass := false
	for _, card := range failedLocal.Data {
		if card.InstanceID == ownedStandardFixtureID {
			foundFailedClass = true
			if card.Version != "2.0" || card.State || card.RepositoryURL != "" || card.HasUpdate || !card.Available {
				t.Fatal("failed initializer did not retain its imported local class", card)
			}
		}
	}
	if !foundFailedClass {
		t.Fatal("failed initializer class was hidden from local list")
	}
	success("PUT", "/api/plugins/"+ownedStandardFixtureID, map[string]any{"enabled": false, "key": "source"})
	success("POST", "/api/plugins/"+ownedStandardFixtureID+"/reload", nil)
	stop()
	start()
	restarted := checkConfig(ownedStandardFixtureID, "source", "2.0")
	if restarted.Nonce == updated.Nonce || restarted.InitCount != 1 {
		t.Fatal("restart did not recover code/config/data into a fresh object")
	}
	checkConfig(clone, "clone", "1.0")
	success("POST", "/api/plugins/"+clone+"/reset", nil)
	reset := read(clone)
	if reset.Version != "1.0" || string(reset.Config) != "null" || string(reset.Value) != "null" {
		t.Fatal("reset did not clear only clone configuration and database data")
	}
	if cloneFile != "" {
		content, err := os.ReadFile(cloneFile)
		if err != nil || string(content) != "owned file survives reset and uninstall" || reset.DataPath != cloned.DataPath {
			t.Fatal("reset removed instance-owned files", err)
		}
	}
	checkConfig(ownedStandardFixtureID, "source", "2.0")
	success("DELETE", "/api/plugins/"+ownedStandardFixtureID, nil)
	request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 404)
	read(clone)
	// Primary uninstall retains config/data/code in the fixed source. Reinstall
	// without force may acquire again, but it must preserve that same namespace.
	install("2.0", true)
	checkConfig(ownedStandardFixtureID, "source", "2.0")
	success("DELETE", "/api/plugins/"+clone, nil)
	request("GET", "/api/v1/plugin/"+clone+"/echo", nil, 404)
	checkConfig(ownedStandardFixtureID, "source", "2.0")
	if cloneFile != "" {
		content, err := os.ReadFile(cloneFile)
		if err != nil || string(content) != "owned file survives reset and uninstall" {
			t.Fatal("clone uninstall removed data files instead of only code", err)
		}
	}
	for _, path := range []string{"/api/workflow/plugin/actions", "/api/agent/tools"} {
		request("GET", path, nil, 404)
	}
	if basePayload != nil {
		verifyOwnedBaseProductionProcess(t, dir, base.PluginID, repository.URL+"/index", request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_EDITABLE_MARKET") == "true" {
		verifyOwnedProductionMarketMetadata(t, repository.URL, &metadataUnavailable, &artifactHits, &metadataHits, request, success, stop, start)
	}
	if exactPayload != nil && legacyPayload != nil {
		verifyOwnedProductionCatalogIdentities(t, repository.URL, request, success, stop, start)
	}
	if eventPayload != nil {
		verifyOwnedSubscriptionProductionEvents(t, dir, eventIndex.PluginID, repository.URL+"/events-index", request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_SYSTEM_VERSION_FIXTURE") == "true" {
		if os.Getenv("PLUGIN_PROCESS_EDITABLE_MARKET") != "true" {
			t.Fatal("compatibility fixture requires editable source settings")
		}
		verifyOwnedProductionSystemVersion(t, repository.URL, &artifactHits, request, success, stop, start)
	}
	if githubFixture != nil {
		verifyOwnedGitHubProductionNetwork(t, githubFixture, repository.URL, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_LOCAL_WATCH_FIXTURE") == "true" {
		if os.Getenv("PLUGIN_PROCESS_EDITABLE_MARKET") != "true" {
			t.Fatal("real local watcher requires editable sources")
		}
		verifyOwnedProductionLocalWatcher(t, dir, index, v1.payload, v2.payload, request, success, stop, start, func(value bool) { developerMode = value })
	}
	if os.Getenv("PLUGIN_PROCESS_RESOURCE_WATCH_FIXTURE") == "true" {
		verifyOwnedProductionResourceWatcher(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_STARTUP_FIXTURE") == "true" {
		verifyOwnedProductionStartupRepair(t, dir, index, v1.payload, v2.payload, request, success, stop, start, func(value bool) { safeMode = value })
	}
	if os.Getenv("PLUGIN_PROCESS_DOCKER_BACKUP_FIXTURE") == "true" {
		verifyOwnedProductionDockerCodeRecovery(t, dir, repository.URL, request, success, stop, start)
	}
	if centerFixture != nil {
		verifyOwnedProductionPluginCenter(t, centerFixture, repository.URL, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_ORGANIZATION_FIXTURE") == "true" {
		verifyOwnedProductionOrganization(t, dir, repository.URL+"/index", request, success, stop, start)
	}
	t.Logf("production lifecycle verified: os=%s arch=%s actual artifact fetches=%d; restart, failure and clone isolation verified", runtime.GOOS, runtime.GOARCH, artifactHits.Load())
	if statePath := os.Getenv("PLUGIN_PROCESS_BROWSER_STATE"); statePath != "" {
		// Leave a clean market for browser installation. Test credentials are
		// declared above; no API key or Cookie is written to the state file.
		success("DELETE", "/api/plugins/"+ownedStandardFixtureID, nil)
		promoted.Store(false)
		browserState := map[string]any{"origin": origin, "repository": repository.URL, "data_dir": dir, "os": runtime.GOOS, "arch": runtime.GOARCH}
		if os.Getenv("PLUGIN_PROCESS_LOCAL_WATCH_FIXTURE") == "true" {
			browserState["local_repository_root"] = filepath.Join(dir, "owned-local-publications")
		}
		if os.Getenv("PLUGIN_PROCESS_RESOURCE_WATCH_FIXTURE") == "true" {
			browserState["resource_repository_root"] = filepath.Join(dir, "owned-resource-publications")
			browserState["resource_versions_root"] = filepath.Join(dir, "owned-resource-versions")
		}
		if centerFixture != nil {
			browserState["plugin_center_origin"] = centerFixture.server.URL
		}
		state, _ := json.Marshal(browserState)
		if err := os.WriteFile(statePath, state, 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("production browser ready; waiting for explicit completion file")
		renderFailure := false
		organizationFailure := false
		deadline := time.Now().Add(45 * time.Minute)
		for time.Now().Before(deadline) {
			if renderPayload != nil {
				_, err := os.Stat(statePath + ".render-config-fail")
				fail := err == nil
				if fail != renderFailure {
					setOwnedRenderConfigurationFailure(t, dir, fail)
					renderFailure = fail
					_ = os.WriteFile(statePath+".render-mode", []byte(fmt.Sprint(fail)), 0600)
				}
			}
			if os.Getenv("PLUGIN_PROCESS_ORGANIZATION_FIXTURE") == "true" {
				_, err := os.Stat(statePath + ".organization-fail")
				fail := err == nil
				if fail != organizationFailure {
					setOwnedOrganizationFailure(t, dir, fail)
					organizationFailure = fail
					_ = os.WriteFile(statePath+".organization-mode", []byte(fmt.Sprint(fail)), 0600)
				}
			}
			if centerFixture != nil {
				_, err := os.Stat(statePath + ".center-fail")
				centerFixture.fail.Store(err == nil)
			}
			_, metadataErr := os.Stat(statePath + ".fail")
			metadataUnavailable.Store(metadataErr == nil)
			if _, err := os.Stat(statePath + ".promote"); err == nil {
				promoted.Store(true)
			}
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				before := process.Process.Pid
				stop()
				start()
				result, _ := json.Marshal(map[string]int{"previous_pid": before, "current_pid": process.Process.Pid})
				if err = os.Remove(statePath + ".restart"); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(statePath+".restart.done", result, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("browser verification did not signal completion")
	}
}
