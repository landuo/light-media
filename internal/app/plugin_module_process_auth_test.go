package app

import (
	"archive/zip"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Fixed plugin.py:1098, core/auth.py:17-116 and endpoints/auth.py:54-84.
// This is an actual production CLI and independent concurrent Go SDK. Its HMAC
// identity is synthetic and authenticates no real external account/service.
func TestPluginAuthProductionCLI(t *testing.T) {
	serverBinary, workerBinary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY"), os.Getenv("OWNED_AUTH_PROCESS_BINARY")
	if serverBinary == "" {
		t.Skip("unverified: actual production cmd/server required")
	}
	if workerBinary == "" {
		t.Fatal("actual frontend-enabled owned auth SDK binary required")
	}
	worker, err := os.ReadFile(workerBinary)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := exec.Command(workerBinary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(manifestRaw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedAuth" || manifest.Frontend == nil {
		t.Fatal("wrong actual authentication SDK variant")
	}
	files := map[string][]byte{manifest.Entry: worker}
	for _, name := range []string{"remoteEntry.js", "auth.js", "style.css"} {
		files["ui/"+name], err = os.ReadFile(filepath.Join(os.Getenv("PLUGIN_PROCESS_AUTH_UI_ROOT"), name))
		if err != nil {
			t.Fatal("owned source UI asset required", name, err)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	inventory := []artifacts.PackageFile{}
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0600)
		if name == manifest.Entry {
			header.SetMode(0700)
		}
		target, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = target.Write(files[name]); err != nil {
			t.Fatal(err)
		}
		inventory = append(inventory, artifacts.PackageFile{Path: name, Size: int64(len(files[name])), SHA256: originalFrontendHash(files[name])})
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	payload := archive.Bytes()
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", Size: int64(len(payload)), SHA256: originalFrontendHash(payload), ExecutableSHA256: originalFrontendHash(worker), HostContract: pluginResourcesSDKContract, Files: inventory}}}}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/package.zip":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/package.zip"
	dir := t.TempDir()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	apiKey := hex.EncodeToString(secret[:])
	bootstrap, _ := json.Marshal(map[string]any{"username": "plugin-auth-acceptance", "password": "owned-auth-browser-password", "settings": Settings{APIToken: apiKey, PluginMarkets: []string{repository.URL + "/index"}}})
	command := exec.Command(serverBinary, "--data-dir", dir, "--bootstrap-stdin")
	command.Stdin = bytes.NewReader(bootstrap)
	if output, err := command.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("bootstrap_complete")) {
		t.Fatal("owned authentication bootstrap failed")
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
	newClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Timeout: 12 * time.Second, Jar: jar}
	}
	admin, anonymous := newClient(), newClient()
	var process *exec.Cmd
	request := func(client *http.Client, method, path string, input any, want int) ([]byte, http.Header) {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = bytes.NewReader(raw)
		}
		r, _ := http.NewRequest(method, origin+path, body)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("owned actual auth request", method, path, err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err != nil || response.StatusCode != want {
			t.Fatalf("owned auth %s %s status%d want%d; body withheld", method, path, response.StatusCode, want)
		}
		return raw, response.Header
	}
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
				t.Error("owned auth shutdown failed", err)
			}
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("owned auth shutdown timeout")
		}
		process = nil
	}
	defer stop()
	start := func() {
		t.Helper()
		process = exec.Command(serverBinary, "--listen", address, "--data-dir", dir)
		process.Stdout, process.Stderr = log, log
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(12 * time.Second)
		for {
			response, err := admin.Get(origin + "/api/auth/status")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("owned authentication listener unavailable")
			}
			time.Sleep(25 * time.Millisecond)
		}
		request(admin, "POST", "/api/auth/login", map[string]string{"username": "plugin-auth-acceptance", "password": "owned-auth-browser-password"}, 200)
	}
	success := func(method, path string, body any) {
		t.Helper()
		raw, _ := request(admin, method, path, body, 200)
		var out struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &out) != nil || !out.Data.Success {
			t.Fatal("actual authentication management rejected", method, path)
		}
	}
	providers := func() []map[string]json.RawMessage {
		t.Helper()
		raw, _ := request(anonymous, "GET", "/api/auth/providers", nil, 200)
		var out struct{ Data []map[string]json.RawMessage }
		if json.Unmarshal(raw, &out) != nil {
			t.Fatal("provider response invalid")
		}
		return out.Data
	}
	configure := func(id string, enabled bool) {
		t.Helper()
		name := "来源认证验收"
		if id != "OwnedAuth" {
			name = "分身认证验收"
		}
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": enabled, "providers": []any{map[string]any{"name": name}}})
	}
	authenticate := func(id, provider string, valid bool, want int) (string, int) {
		t.Helper()
		var nonceBytes [16]byte
		if _, err := rand.Read(nonceBytes[:]); err != nil {
			t.Fatal(err)
		}
		nonce := hex.EncodeToString(nonceBytes[:])
		mac := hmac.New(sha256.New, []byte("owned-auth-proof-key"))
		_, _ = mac.Write([]byte(nonce))
		proof := hex.EncodeToString(mac.Sum(nil))
		if !valid {
			proof = "00"
		}
		raw, _ := request(anonymous, "POST", "/api/plugins/"+id+"/api/authenticate", map[string]any{"nonce": nonce, "proof": proof, "user_id": 1, "provider_id": provider}, want)
		if want != 200 {
			return "", 0
		}
		var out struct {
			Ticket string
			PID    int
		}
		if json.Unmarshal(raw, &out) != nil || len(out.Ticket) != 43 || out.PID <= 0 || out.PID == os.Getpid() {
			t.Fatal("actual SDK did not mint a host ticket")
		}
		return out.Ticket, out.PID
	}
	exchange := func(client *http.Client, ticket string, want int) {
		t.Helper()
		raw, headers := request(client, "POST", "/api/auth/exchange", map[string]string{"ticket": ticket}, want)
		if want != 200 {
			return
		}
		var out struct{ Data struct{ Authenticated bool } }
		if json.Unmarshal(raw, &out) != nil || !out.Data.Authenticated || !strings.Contains(headers.Get("Set-Cookie"), "HttpOnly") {
			t.Fatal("ticket exchange did not establish actual protected session")
		}
		request(client, "GET", "/api/plugins?state=installed", nil, 200)
	}
	frontend := func(id string, want int) originalFrontendDescriptor {
		t.Helper()
		raw, _ := request(anonymous, "GET", "/api/plugins/"+id+"/frontend", nil, want)
		if want != 200 {
			return originalFrontendDescriptor{}
		}
		var out struct{ Data originalFrontendDescriptor }
		if json.Unmarshal(raw, &out) != nil || out.Data.InstanceID != id || out.Data.PackageSHA256 == "" {
			t.Fatal("anonymous descriptor not bound to actual package")
		}
		base := strings.TrimSuffix("/api"+out.Data.EntryPath, "remoteEntry.js")
		for _, name := range []string{"remoteEntry.js", "auth.js", "style.css"} {
			asset, _ := request(anonymous, "GET", base+name, nil, 200)
			if len(asset) == 0 {
				t.Fatal("anonymous graph asset empty")
			}
		}
		return out.Data
	}
	start()
	if len(providers()) != 0 {
		t.Fatal("uninstalled provider advertised")
	}
	success("POST", "/api/plugins/install", map[string]string{"plugin_id": "OwnedAuth", "repo_url": repository.URL + "/index"})
	configure("OwnedAuth", true)
	// A real installed SDK that never writes a plugin log has no physical
	// current file. Match the fixed source404 instead of an empty200 archive.
	for _, suffix := range []string{"", "/file", "/download", "/stream"} {
		request(admin, "GET", "/api/plugins/OwnedAuth/logs"+suffix, nil, 404)
	}
	if len(providers()) != 1 {
		t.Fatal("actual enabled provider missing")
	}
	sourceDescriptor := frontend("OwnedAuth", 200)
	request(anonymous, "GET", "/api/plugins?state=installed", nil, 401)
	authenticate("OwnedAuth", "plugin:OwnedAuth", false, 401)
	ticket, sourcePID := authenticate("OwnedAuth", "plugin:OwnedAuth", true, 200)
	session := newClient()
	exchange(session, ticket, 200)
	exchange(newClient(), ticket, 401)
	// The pinned exchange consumes a previously minted ticket without checking
	// provider state again. Anonymous assets do follow the current plugin state.
	ticket, _ = authenticate("OwnedAuth", "plugin:OwnedAuth", true, 200)
	configure("OwnedAuth", false)
	if len(providers()) != 0 {
		t.Fatal("disabled provider still advertised")
	}
	frontend("OwnedAuth", 401)
	exchange(newClient(), ticket, 200)
	authenticate("OwnedAuth", "plugin:OwnedAuth", true, 403)
	configure("OwnedAuth", true)
	success("POST", "/api/plugins/OwnedAuth/clone", map[string]string{"suffix": "login", "name": "Owned authentication clone"})
	configure("OwnedAuthlogin", true)
	ps := providers()
	if len(ps) != 2 || string(ps[0]["plugin_id"]) != `"OwnedAuth"` || string(ps[1]["plugin_id"]) != `"OwnedAuthlogin"` {
		t.Fatal("source/clone provider identity or insertion order changed")
	}
	cloneDescriptor := frontend("OwnedAuthlogin", 200)
	if cloneDescriptor.EntryPath == sourceDescriptor.EntryPath || cloneDescriptor.PluginID != "OwnedAuth" {
		t.Fatal("clone auth graph did not preserve copied source identity")
	}
	cloneTicket, clonePID := authenticate("OwnedAuthlogin", "plugin:OwnedAuthlogin", true, 200)
	if clonePID == sourcePID {
		t.Fatal("auth clone shares source worker")
	}
	exchange(newClient(), cloneTicket, 200)
	authenticate("OwnedAuthlogin", "plugin:OwnedAuth", true, 403)
	concurrentTicket, _ := authenticate("OwnedAuthlogin", "plugin:OwnedAuthlogin", true, 200)
	var accepted, rejected atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			body, _ := json.Marshal(map[string]string{"ticket": concurrentTicket})
			response, err := newClient().Post(origin+"/api/auth/exchange", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Error("actual concurrent exchange request failed")
				return
			}
			response.Body.Close()
			switch response.StatusCode {
			case 200:
				accepted.Add(1)
			case 401:
				rejected.Add(1)
			default:
				t.Error("actual exchange unexpected status", response.StatusCode)
			}
		}()
	}
	group.Wait()
	if accepted.Load() != 1 || rejected.Load() != 7 {
		t.Fatal("actual ticket was not consumed exactly once")
	}
	if os.Getenv("PLUGIN_PROCESS_AUTH_REAL_TTL") == "true" {
		expired, _ := authenticate("OwnedAuth", "plugin:OwnedAuth", true, 200)
		t.Log("actual TTL gate waiting121 seconds; synthetic ticket withheld")
		time.Sleep(121 * time.Second)
		exchange(newClient(), expired, 401)
		t.Log("actual production wall-clock121s expiry rejected")
	}
	pending, _ := authenticate("OwnedAuth", "plugin:OwnedAuth", true, 200)
	stop()
	start()
	exchange(newClient(), pending, 401)
	request(session, "GET", "/api/plugins?state=installed", nil, 200)
	_, nextSource := authenticate("OwnedAuth", "plugin:OwnedAuth", true, 200)
	_, nextClone := authenticate("OwnedAuthlogin", "plugin:OwnedAuthlogin", true, 200)
	if nextSource == sourcePID || nextClone == clonePID || nextSource == nextClone {
		t.Fatal("actual auth SDK cold recovery missing")
	}
	if frontend("OwnedAuth", 200).EntryPath != sourceDescriptor.EntryPath || len(providers()) != 2 {
		t.Fatal("cold provider/config/asset recovery failed")
	}
	configure("OwnedAuth", false)
	if len(providers()) != 1 {
		t.Fatal("source disable affected clone")
	}
	frontend("OwnedAuth", 401)
	frontend("OwnedAuthlogin", 200)
	cloneTicket, _ = authenticate("OwnedAuthlogin", "plugin:OwnedAuthlogin", true, 200)
	exchange(newClient(), cloneTicket, 200)
	configure("OwnedAuth", true)
	t.Logf("actual auth chain: HMAC reject/accept→host ticket→protected Cookie; replay/concurrent once/provider-disable semantics, source PID%d→%d/clone PID%d→%d, anonymous graph/isolation/cold ticket loss/session retention verified; no real identity service", sourcePID, nextSource, clonePID, nextClone)
	if statePath := os.Getenv("PLUGIN_PROCESS_AUTH_BROWSER_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "repository": repository.URL, "data_dir": dir})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("write owned auth browser state")
		}
		t.Log("production auth browser ready; waiting for owned completion file")
		deadline := time.Now().Add(35 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				before := process.Process.Pid
				stop()
				start()
				result, _ := json.Marshal(map[string]int{"previous_pid": before, "current_pid": process.Process.Pid})
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", result, 0600)
			}
			if _, err := os.Stat(statePath + ".disable-source"); err == nil {
				configure("OwnedAuth", false)
				_ = os.Remove(statePath + ".disable-source")
				_ = os.WriteFile(statePath+".disable-source.done", []byte("source disabled through actual config API"), 0600)
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("owned authentication browser did not finish")
		}
	}
	success("DELETE", "/api/plugins/OwnedAuthlogin", nil)
	success("DELETE", "/api/plugins/OwnedAuth", nil)
	if len(providers()) != 0 {
		t.Fatal("uninstalled auth provider remained")
	}
	frontend("OwnedAuth", 401)
	t.Log("owned auth fixtures uninstalled; isolated production server stops")
}
