package app

import (
	"archive/zip"
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
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// The source module is not mocked: an owned repository serves actual verified
// Vue/Vuetify ZIPs, production cmd/server loads independent SDK processes, and
// this gate observes manager routes, methods, files and persisted preferences.
func TestPluginUI3133ProductionCLI(t *testing.T) {
	serverBinary, first, second := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY"), os.Getenv("UI3133_PROCESS_FIXTURE_BINARY"), os.Getenv("UI3133_PROCESS_V2_FIXTURE_BINARY")
	if serverBinary == "" || first == "" || second == "" {
		t.Skip("unverified: actual production server and two owned shared-Vuetify artifacts required")
	}
	type release struct {
		declaration artifacts.Release
		payload     []byte
	}
	load := func(binary, version string) release {
		t.Helper()
		worker, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := exec.Command(binary, "--manifest").Output()
		if err != nil {
			t.Fatal(err)
		}
		var manifest plugins.Manifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedFrontendFixture" || manifest.Version != version || manifest.Frontend == nil || !slices.Contains(manifest.Frontend.Shared, "vuetify") || !slices.Contains(manifest.Methods, "get_dashboard_meta") {
			t.Fatal("wrong actual shared-Vuetify fixture")
		}
		ui := os.Getenv("UI3133_PROCESS_FRONTEND_DIR")
		if ui == "" {
			ui = "../../testdata/standard-navigation/ui"
		}
		files := map[string][]byte{manifest.Entry: worker}
		for _, name := range []string{"remoteEntry.js", "component.js", "fixture.css"} {
			data, err := os.ReadFile(filepath.Join(ui, name))
			if err != nil {
				t.Fatal(err)
			}
			files["frontend/assets/"+name] = []byte(strings.ReplaceAll(string(data), "__OWNED_UI_VERSION__", version))
		}
		names := []string{}
		for name := range files {
			names = append(names, name)
		}
		slices.Sort(names)
		var data bytes.Buffer
		archive := zip.NewWriter(&data)
		inventory := []artifacts.PackageFile{}
		for _, name := range names {
			file, err := archive.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = file.Write(files[name]); err != nil {
				t.Fatal(err)
			}
			inventory = append(inventory, artifacts.PackageFile{Path: name, SHA256: originalFrontendHash(files[name]), Size: int64(len(files[name]))})
		}
		if archive.Close() != nil {
			t.Fatal("close ZIP")
		}
		payload := data.Bytes()
		artifact := artifacts.Artifact{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", SHA256: originalFrontendHash(payload), ExecutableSHA256: originalFrontendHash(worker), Size: int64(len(payload)), HostContract: pluginPureSDKContract, Files: inventory}
		return release{artifacts.Release{Version: version, Manifest: manifest, Artifacts: []artifacts.Artifact{artifact}}, payload}
	}
	v1, v2 := load(first, "1.0"), load(second, "2.0")
	index := artifacts.Index{SchemaVersion: 1, PluginID: "OwnedFrontendFixture", CurrentVersion: "1.0", Releases: []artifacts.Release{v1.declaration, v2.declaration}}
	var promoted atomic.Bool
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
	bootstrap, _ := json.Marshal(map[string]any{"username": "ui3133-acceptance", "password": "owned-ui3133-password", "settings": Settings{APIToken: key, PluginMarkets: []string{repository.URL + "/index"}}})
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
				t.Error("actual shutdown", err)
			}
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("shutdown timeout")
		}
		process = nil
	}
	defer stop()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	plain := &http.Client{Timeout: 20 * time.Second}
	request := func(method, path string, value any, auth string, want int) []byte {
		t.Helper()
		var body []byte
		if value != nil {
			body, _ = json.Marshal(value)
		}
		req, err := http.NewRequest(method, origin+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "OwnedUI3133/actual-http")
		if auth == "key" {
			req.Header.Set("X-API-KEY", key)
		}
		transport := plain
		if auth == "cookie" {
			transport = client
		}
		res, err := transport.Do(req)
		if err != nil {
			t.Fatal("actual transport", method, strings.SplitN(path, "?", 2)[0])
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || want > 0 && res.StatusCode != want {
			t.Fatalf("actual %s %s status%d want%d response withheld", method, strings.SplitN(path, "?", 2)[0], res.StatusCode, want)
		}
		return raw
	}
	start := func() {
		t.Helper()
		process = exec.Command(serverBinary, "--listen", address, "--data-dir", dir)
		process.Env = append(os.Environ(), "MP_SERVER_HOST=")
		if os.Getenv("CLONE_FRONTEND_PROCESS_DEV") == "true" {
			process.Env = append(process.Env, "DEV=true")
		}
		process.Stdout = log
		process.Stderr = log
		if process.Start() != nil {
			t.Fatal("start actual server")
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			res, e := plain.Get(origin + "/api/auth/status")
			if e == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("actual listener unavailable")
			}
			time.Sleep(30 * time.Millisecond)
		}
		request("POST", "/api/auth/login", map[string]string{"username": "ui3133-acceptance", "password": "owned-ui3133-password"}, "cookie", 200)
	}
	management := func(method, path string, value any) {
		t.Helper()
		raw := request(method, path, value, "cookie", 200)
		var v struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &v) != nil || !v.Data.Success {
			t.Fatal("actual management failure", method, path)
		}
	}
	read := func(path string) json.RawMessage {
		t.Helper()
		raw := request("GET", path, nil, "cookie", 200)
		var v struct{ Data json.RawMessage }
		if json.Unmarshal(raw, &v) != nil {
			t.Fatal("invalid actual envelope")
		}
		return v.Data
	}
	list := func(path string) []map[string]any {
		t.Helper()
		var rows []map[string]any
		if json.Unmarshal(read(path), &rows) != nil {
			t.Fatal("invalid list", path)
		}
		return rows
	}
	status := func(id string) map[string]any {
		t.Helper()
		raw := request("GET", "/api/plugins/"+id+"/api/status", nil, "cookie", 200)
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil || value["nonce"] == nil {
			t.Fatal("actual worker not observed")
		}
		return value
	}
	const source = "OwnedFrontendFixture"
	const clone = source + "copy"
	configure := func(id, marker string, enabled bool, extra map[string]any) {
		t.Helper()
		value := map[string]any{"enabled": enabled, "marker": marker, "sidebar_nav": []map[string]any{{"nav_key": "details", "title": "Owned " + marker + " details", "section": "system", "order": 3}, {"nav_key": "my-tool", "title": "Owned " + marker + " tool", "section": "discovery", "order": "١_٢"}}}
		for k, v := range extra {
			value[k] = v
		}
		management("PUT", "/api/plugins/"+id, value)
	}
	start()
	if os.Getenv("UI3133_PROCESS_RESOURCE_WATCH_ONLY") == "true" {
		verifyOwnedProductionResourceWatcher(t, dir, func(method, path string, value any, want int) []byte {
			return request(method, path, value, "cookie", want)
		}, management, stop, start)
		return
	}
	management("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository.URL + "/index"})
	configure(source, "source", false, nil)
	for _, version := range []string{"v1", "v2"} {
		request("GET", "/api/"+version+"/plugin/remotes", nil, "", 422)
		request("GET", "/api/"+version+"/plugin/remotes?token=wrong", nil, "", 403)
		var rows []map[string]any
		if json.Unmarshal(request("GET", "/api/"+version+"/plugin/remotes?token=moviepilot", nil, "", 200), &rows) != nil || len(rows) != 1 {
			t.Fatal("disabled Vue absent from public discovery")
		}
		request("GET", "/api/"+version+"/plugin/sidebar_nav?token=moviepilot", nil, "", 401)
		request("GET", "/api/"+version+"/plugin/sidebar_nav", nil, "key", 200)
	}
	if len(list("/api/plugins/dashboard/meta")) != 0 || len(list("/api/plugins/navigation")) != 0 {
		t.Fatal("disabled metadata/sidebar included")
	}
	disabled := status(source)
	configure(source, "source", true, nil)
	management("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": "copy"})
	configure(clone, "clone", true, nil)
	before, copyBefore := status(source), status(clone)
	if before["nonce"] != disabled["nonce"] || before["nonce"] == copyBefore["nonce"] {
		t.Fatal("actual reinit/clone objects")
	}
	frontendPaths := map[string]string{}
	for _, id := range []string{source, clone} {
		var d originalFrontendDescriptor
		if json.Unmarshal(read("/api/plugins/"+id+"/frontend"), &d) != nil || d.InstanceID != id || !slices.Contains(d.Shared, "vuetify") {
			t.Fatal("shared descriptor", id)
		}
		frontendPaths[id] = "/api" + d.EntryPath
		entry := request("GET", "/api"+d.EntryPath, nil, "cookie", 200)
		if !bytes.Contains(entry, []byte("scope.vuetify")) || !bytes.Contains(entry, []byte("./component.js")) {
			t.Fatal("shared ESM not actual")
		}
		chunk := request("GET", strings.TrimSuffix("/api"+d.EntryPath, "remoteEntry.js")+"component.js", nil, "cookie", 200)
		if !bytes.Contains(chunk, []byte("u.useTheme()")) || !bytes.Contains(chunk, []byte("VDataTable")) {
			t.Fatal("complex shared controls absent")
		}
		request("GET", strings.TrimSuffix("/api"+d.EntryPath, "remoteEntry.js")+"fixture.css", nil, "cookie", 200)
	}
	if len(list("/api/plugins/dashboard/meta")) != 4 || len(list("/api/plugins/navigation")) != 4 {
		t.Fatal("source/clone declarations not independently included")
	}
	for _, id := range []string{source, clone} {
		var item map[string]any
		if json.Unmarshal(read("/api/plugins/"+id+"/dashboard?key=summary"), &item) != nil || item["id"] != id || item["key"] != "summary" || item["render_mode"] != "vue" {
			t.Fatal("actual dashboard route", id)
		}
		last := status(id)["last_dashboard"].(map[string]any)
		if last["key"] != "summary" || last["user_agent"] != "OwnedUI3133/actual-http" {
			t.Fatal("actual dashboard arguments", last)
		}
	}
	for key, value := range map[string]any{"enabled": map[string]bool{source + ":summary": true, clone + ":details": true}, "order": []map[string]string{{"id": clone, "key": "details"}, {"id": source, "key": "summary"}}, "desktop": map[string]any{"items": map[string]any{source + ":summary": map[string]int{"x": 0, "y": 0, "w": 8, "h": 22}, clone + ":details": map[string]int{"x": 0, "y": 22, "w": 6, "h": 22}}}, "tablet": map[string]any{"items": map[string]any{}}, "mobile": map[string]any{"items": map[string]any{}}} {
		raw := request("POST", "/api/plugins/dashboard/settings/"+key, value, "cookie", 200)
		if !bytes.Contains(raw, []byte(`"saved":true`)) {
			t.Fatal("actual preference save")
		}
	}
	configure(source, "source", true, map[string]any{"sidebar_nav": []map[string]any{{"nav_key": "bad-title", "title": []any{1}}}})
	request("GET", "/api/plugins/navigation", nil, "cookie", 500)
	request("GET", "/api/v1/plugin/sidebar_nav", nil, "key", 500)
	configure(source, "source", true, nil)
	for _, value := range []any{[]any{[]any{map[string]any{"md": 8}}, map[string]any{}, []any{}}, []any{map[string]any{}, map[string]any{}, []any{"invalid-node"}}} {
		configure(source, "source", true, map[string]any{"dashboard_value": value})
		request("GET", "/api/plugins/"+source+"/dashboard?key=summary", nil, "cookie", 500)
	}
	configure(source, "source", true, map[string]any{"dashboard_none": true})
	if string(read("/api/plugins/"+source+"/dashboard?key=summary")) != "null" {
		t.Fatal("actual None not retained")
	}
	configure(source, "source", true, map[string]any{"dashboard_fail": true})
	request("GET", "/api/plugins/"+source+"/dashboard?key=summary", nil, "cookie", 500)
	configure(source, "source", true, nil)
	promoted.Store(true)
	management("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository.URL + "/index", "force": true})
	updated := status(source)
	if updated["version"] != "2.0" || updated["nonce"] == before["nonce"] || updated["marker"] != "source" || status(clone)["version"] != "1.0" {
		t.Fatal("actual frontend update/clone isolation")
	}
	request("GET", frontendPaths[source], nil, "cookie", 409)
	request("GET", frontendPaths[clone], nil, "cookie", 200)
	var current originalFrontendDescriptor
	if json.Unmarshal(read("/api/plugins/"+source+"/frontend"), &current) != nil {
		t.Fatal("updated descriptor")
	}
	if raw := request("GET", strings.TrimSuffix("/api"+current.EntryPath, "remoteEntry.js")+"component.js", nil, "cookie", 200); !bytes.Contains(raw, []byte("2.0")) {
		t.Fatal("updated frontend content absent")
	}
	stop()
	start()
	cold, coldClone := status(source), status(clone)
	if cold["nonce"] == updated["nonce"] || coldClone["nonce"] == copyBefore["nonce"] || cold["marker"] != "source" || coldClone["marker"] != "clone" {
		t.Fatal("actual cold restore")
	}
	if len(list("/api/plugins/dashboard/meta")) != 4 || len(list("/api/plugins/sidebar_nav")) != 4 {
		t.Fatal("cold metadata/nav restore")
	}
	for _, key := range []string{"enabled", "order", "desktop", "tablet", "mobile"} {
		var v map[string]any
		if json.Unmarshal(read("/api/plugins/dashboard/settings/"+key), &v) != nil || v["value"] == nil {
			t.Fatal("cold preference absent", key)
		}
	}
	if statePath := os.Getenv("UI3133_PROCESS_BROWSER_STATE"); statePath != "" {
		encoded, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "repository": repository.URL, "source_id": source, "clone_id": clone})
		if os.WriteFile(statePath, encoded, 0600) != nil {
			t.Fatal("browser state")
		}
		t.Log("UI3133 browser ready: actual shared Vue/Vuetify source2/clone1, two metadata each, source/clone chosen; waiting for completion marker")
		deadline := time.Now().Add(60 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				old := process.Process.Pid
				stop()
				start()
				data, _ := json.Marshal(map[string]int{"previous_pid": old, "current_pid": process.Process.Pid})
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", data, 0600)
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("browser acceptance not completed")
		}
	}
	management("DELETE", "/api/plugins/"+source, nil)
	if len(list("/api/plugins/dashboard/meta")) != 2 || len(list("/api/plugins/navigation")) != 2 {
		t.Fatal("uninstalled source declarations remain")
	}
	management("DELETE", "/api/plugins/"+clone, nil)
	if len(list("/api/plugins/dashboard/meta")) != 0 || len(list("/api/plugins/navigation")) != 0 || len(list("/api/plugins/remotes")) != 0 {
		t.Fatal("uninstalled clone declarations remain")
	}
	t.Logf("actual UI3133 Linux=%t: verified shared Vue/Vuetify ZIP/ESM/CSS, discovery auth/state split, metadata/sidebar instances, dashboard arguments/None/failure, profile persistence, update/cold restore/uninstall verified; browser interaction separate", runtime.GOOS == "linux")
}
