package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	cf "light-media/internal/plugins/cloudflarespeedtest"
)

const cloudflareFixturePrefix = "# Cloudflare owned fixture\n127.0.0.1 localhost\n"

type cloudflareOfficialFixtureTransport func(*http.Request) (*http.Response, error)

func (f cloudflareOfficialFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestCloudflareSpeedTestInstalledLinuxAMD64WebAndRestart(t *testing.T) {
	testCloudflareSpeedTestInstalledLinux(t, "web")
}

func TestCloudflareSpeedTestInstalledLinuxAMD64OnceAndAPI(t *testing.T) {
	testCloudflareSpeedTestInstalledLinux(t, "once")
}

func TestCloudflareSpeedTestInstalledLinuxAMD64UnavailableDependencies(t *testing.T) {
	testCloudflareSpeedTestInstalledLinux(t, "unavailable")
}

func TestCustomHostsInstalledLinuxAMD64WithCloudflare(t *testing.T) {
	testCloudflareSpeedTestInstalledLinux(t, "custom-hosts")
}

func testCloudflareSpeedTestInstalledLinux(t *testing.T, mode string) {
	t.Helper()
	onceAndAPI, unavailable := mode == "once", mode == "unavailable"
	paired := mode == "custom-hosts"
	bundle := os.Getenv("SOURCE_CLOUDFLARE_PACKAGE_DIR")
	customWorker := os.Getenv("SOURCE_CLOUDFLARE_CUSTOM_HOSTS_WORKER")
	customBundle := os.Getenv("SOURCE_CUSTOM_HOSTS_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || paired && customBundle == "" || !paired && customWorker == "" {
		t.Skip("requires the indexed Linux amd64 Cloudflare package and the selected CustomHosts fixture")
	}
	withoutProxyEnv(t)
	t.Setenv("PROXY_HOST", "https://ghfast.top")
	t.Setenv("PLUGIN_MARKET", "")
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("TZ", "Asia/Shanghai")

	fixture := os.Getenv("SOURCE_CLOUDFLARE_OFFICIAL_FIXTURE_DIR")
	if fixture == "" {
		fixture = "../../testdata/plugins/cloudflare-native-official-v2.3.5"
	}
	metadata, err := os.ReadFile(filepath.Join(fixture, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	const archiveName = "cfst_linux_amd64.tar.gz"
	archive, err := os.ReadFile(filepath.Join(fixture, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	const archiveSHA = "c4c8fc76b4e1bf2bdb5ced8b765956d82dda7bc4eb59df5c04053f0f7db98d90"
	if fmt.Sprintf("%x", sha256.Sum256(archive)) != archiveSHA {
		t.Fatal("Cloudflare archive does not match the reviewed official fixture")
	}
	const assetURL = cf.ReleasePrefix + "/v2.3.5/" + archiveName
	var archiveRequests, latestRequests, fallbackRequests atomic.Int32
	official := &http.Client{Transport: cloudflareOfficialFixtureTransport(func(r *http.Request) (*http.Response, error) {
		var body []byte
		status := http.StatusOK
		switch r.URL.String() {
		case cf.ReleaseAPI:
			latestRequests.Add(1)
			body = metadata
			if unavailable {
				status, body = http.StatusNotFound, nil
			}
		case "https://api.github.com/repos/XIU2/CloudflareSpeedTest/releases/tags/v2.2.2":
			fallbackRequests.Add(1)
			body = []byte(`{"tag_name":"v2.2.2","assets":[{"name":"CloudflareST_linux_amd64.tar.gz"}]}`)
		case "https://api.github.com/repos/XIU2/CloudflareSpeedTest/releases/tags/v2.3.5":
			body = metadata
		case assetURL, "https://ghfast.top/" + assetURL, "https://api.github.com/repos/XIU2/CloudflareSpeedTest/releases/assets/491252222":
			if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/octet-stream" {
				t.Error("official archive request did not use its fixed read contract")
			}
			archiveRequests.Add(1)
			body = archive
		default:
			t.Error("unexpected official fixture transport request", r.URL.Scheme, r.URL.Host, r.URL.Path)
			return nil, fmt.Errorf("fixture refused external request")
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}

	peer, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	var probeConnections atomic.Int32
	go func() {
		for {
			connection, err := peer.Accept()
			if err != nil {
				return
			}
			probeConnections.Add(1)
			_ = connection.Close()
		}
	}()
	ownedHosts := filepath.Join(t.TempDir(), "owned-cloudflare-hosts")
	if err := os.WriteFile(ownedHosts, []byte(cloudflareFixturePrefix), 0600); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	start := func() *App {
		app, err := New(Options{DataDir: dataDir})
		if err != nil {
			t.Fatal(err)
		}
		app.cloudflareTestClient = official
		app.Start(t.Context())
		return app
	}
	a := start()
	t.Cleanup(func() {
		if a != nil {
			_ = a.Close()
		}
	})
	admin := setupTestAdmin(t, a)
	const ownedKey = "owned-cloudflare-api-key"
	const route = "/api/plugins/CloudflareSpeedTest/api/cloudflare_speedtest?apikey="
	if onceAndAPI || unavailable {
		settings := a.Settings()
		settings.APIToken = ownedKey
		if err := a.saveSettings(t.Context(), settings); err != nil {
			t.Fatal(err)
		}
	}
	if paired {
		installMergeSiteSwitchPackage(t, a, admin, customBundle, "CustomHosts")
	} else {
		installCloudflareCustomHostsPeer(t, a, admin, customWorker)
	}
	installMergeSiteSwitchPackage(t, a, admin, bundle, cf.PluginID)
	configure := func(instance string, config map[string]any) {
		t.Helper()
		body, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		w := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/"+instance, string(body), admin, "")
		assertOriginalRegistrySuccess(t, w.Code, w.Body.Bytes())
	}
	const oldIP, newIP = "192.0.2.1", "127.0.0.1"
	const originalHosts = oldIP + " owned-cf-a.invalid\n" + oldIP + " owned-cf-b.invalid\n"
	const updatedHosts = newIP + " owned-cf-a.invalid\n" + newIP + " owned-cf-b.invalid\n"
	if !paired {
		configure("CustomHosts", map[string]any{"fixture_path": ownedHosts, "hosts": originalHosts, "enabled": true, "err_hosts": []string{}})
	}
	configure(cf.PluginID, map[string]any{
		"cf_ip": oldIP, "cron": "0 3 29 2 *", "ipv4": true, "ipv6": false, "notify": true, "check": false,
		"onlyonce": onceAndAPI, "re_install": false, "version": "",
		"additional_args": fmt.Sprintf("-ip 127.0.0.1 -tp %d -dd -n 1 -t 1 -p 0", peer.Addr().(*net.TCPAddr).Port),
	})

	send := func() {
		t.Helper()
		w := callTest(t, a.Handler(), http.MethodPost, "/api/message/web", `{"text":"/cloudflare_speedtest"}`, admin, "")
		var result struct {
			Data struct{ Success, Accepted bool }
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Data.Success || !result.Data.Accepted {
			t.Fatalf("source Web command did not admit Cloudflare: %d %s", w.Code, w.Body.String())
		}
	}
	readConfig := func(instance string) (map[string]any, int64) {
		t.Helper()
		stored, err := a.pluginStore.Config(t.Context(), instance)
		var value map[string]any
		if err != nil || json.Unmarshal(stored.Value, &value) != nil || value == nil {
			t.Fatalf("could not read sealed %s config: %v", instance, err)
		}
		return value, stored.Revision
	}
	if paired {
		// The runner binds an owned fixture directly to /etc/hosts. Check its
		// exact starting bytes before an enabled configuration can write.
		const hostsPath = "/etc/hosts"
		const prefix = "# CustomHosts isolated mounted fixture\n127.0.0.1\tlocalhost\n"
		const marker = "# CustomHostsPlugin\n"
		const invalid = "not-an-ip malformed.invalid"
		const ipv6 = "2001:db8::1 owned-v6-a.invalid\n2001:db8::1 owned-v6-b.invalid\n"
		const ipv6Rendered = "2001:db8::1\towned-v6-a.invalid\n2001:db8::1\towned-v6-b.invalid\n"
		const aliasCollision = "198.51.100.8 owned-cf-a.invalid ignored-alias.invalid\n"
		const initial = prefix + marker + "198.51.100.6\tstale.invalid\n"
		assertFile := func(want string) {
			t.Helper()
			if actual, err := os.ReadFile(hostsPath); err != nil || string(actual) != want {
				t.Fatalf("unexpected mounted Hosts result: %q, %v", actual, err)
			}
		}
		assertFile(initial)
		formResponse := callTest(t, a.Handler(), http.MethodGet, "/api/plugins/CustomHosts/form", "", admin, "")
		var form struct {
			Data struct {
				Conf  []any
				Model map[string]any
			}
		}
		if formResponse.Code != http.StatusOK || json.Unmarshal(formResponse.Body.Bytes(), &form) != nil {
			t.Fatalf("could not open the installed CustomHosts form: %d %s", formResponse.Code, formResponse.Body.String())
		}
		canonical, err := json.Marshal(form.Data.Conf)
		// Sorted JSON from the pinned Python get_form, identical to the previously browser-verified 1.2.1 form.
		const formSHA = "e05a0da921b4ec05292b84727e8390651caad56d284adbd1991891cb511744a0"
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(canonical)) != formSHA || len(form.Data.Model) != 3 ||
			form.Data.Model["enabled"] != false || form.Data.Model["hosts"] != "" || form.Data.Model["err_hosts"] != "" {
			t.Fatal("the installed form did not match the pinned source")
		}
		configure("CustomHosts", map[string]any{"enabled": true, "hosts": ""})
		assertFile(initial)
		empty, _ := readConfig("CustomHosts")
		if errors, ok := empty["err_hosts"].([]any); empty["enabled"] != true || empty["hosts"] != "" || !ok || len(errors) != 0 {
			t.Fatal("source empty-text normalization should not clear the existing marker")
		}
		configure("CustomHosts", map[string]any{"enabled": false, "hosts": ""})
		assertFile(prefix)
		input := originalHosts + ipv6 + aliasCollision + invalid + "\n"
		configure("CustomHosts", map[string]any{"enabled": true, "hosts": input})
		assertFile(prefix + marker + strings.ReplaceAll(originalHosts, " ", "\t") + ipv6Rendered)
		assertConfig := func(wantHosts string) {
			t.Helper()
			config, _ := readConfig("CustomHosts")
			badRows, ok := config["err_hosts"].([]any)
			if len(config) != 3 || config["enabled"] != true || config["hosts"] != wantHosts || !ok || len(badRows) != 1 || badRows[0] != invalid+"\n" {
				t.Fatalf("source normalization or invalid row differed: %#v", config)
			}
		}
		assertConfig(input)
		custom := originalBaseSDKLoaded(t, a, "CustomHosts")
		send()
		expected := prefix + marker + strings.ReplaceAll(updatedHosts, " ", "\t") + ipv6Rendered
		privateAwait(t, "actual CustomHosts received Cloudflare reload and replaced both owned IPs", func() bool {
			content, err := os.ReadFile(hostsPath)
			if err != nil || string(content) != expected {
				return false
			}
			// Wait for the reload callback to finish its config save before
			// the independent uninstall step revokes the installation.
			custom.submitted.mu.Lock()
			defer custom.submitted.mu.Unlock()
			return len(custom.submitted.leases) == 0
		})
		logs, err := a.readLogs(map[string]string{"module": "plugins", "limit": "100"})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range logs {
			if row["event"] == "event_failed" {
				t.Fatal("plugin event failed before the paired reload completed")
			}
		}
		assertConfig(updatedHosts + ipv6 + aliasCollision + invalid + "\n")
		cloudflare, _ := readConfig(cf.PluginID)
		if cloudflare["cf_ip"] != newIP || archiveRequests.Load() != 1 || probeConnections.Load() == 0 {
			t.Fatal("paired reload was not driven by the official Cloudflare child and persisted IP")
		}
		removed := callTest(t, a.Handler(), http.MethodDelete, "/api/plugins/CustomHosts", "", admin, "")
		assertOriginalRegistrySuccess(t, removed.Code, removed.Body.Bytes())
		assertFile(expected)
		t.Log("source form, disabled cleanup, duplicate-address rows, invalid row, actual Cloudflare reload and uninstall retention verified on mounted Hosts")
		return
	}
	if unavailable {
		_, originalPeerRevision := readConfig("CustomHosts")
		if err := a.stopOriginalPlugin(t.Context(), "CustomHosts"); err != nil {
			t.Fatal(err)
		}
		verifyRun := func(wantLog string) {
			t.Helper()
			reply := callTest(t, a.Handler(), http.MethodGet, route+ownedKey, "", nil, "")
			var result struct{ Success bool }
			if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &result) != nil || !result.Success {
				t.Fatalf("source handled failure lost its response: %d %s", reply.Code, reply.Body.String())
			}
			logs, err := a.readLogs(map[string]string{"module": "plugins", "limit": "100"})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range logs {
				if row["message"] == wantLog {
					return
				}
			}
			t.Fatalf("missing source log %q", wantLog)
		}
		verifyRun("Cloudflare CDN优选依赖于自定义Hosts，请先维护hosts")
		if latestRequests.Load() != 0 {
			t.Fatal("a stopped CustomHosts must not admit a speed test")
		}
		if _, err := a.reloadOriginalPlugin(t.Context(), "CustomHosts"); err != nil {
			t.Fatal(err)
		}
		a.registerOriginalPlugin(t.Context(), "CustomHosts")
		verifyRun("获取到最优ip格式错误，请重试")
		custom, revision := readConfig("CustomHosts")
		config, _ := readConfig(cf.PluginID)
		if custom["hosts"] != originalHosts || revision != originalPeerRevision || config["cf_ip"] != oldIP ||
			config["onlyonce"] != false || latestRequests.Load() != 2 || fallbackRequests.Load() != 1 ||
			archiveRequests.Load() != 0 || probeConnections.Load() != 0 {
			t.Fatal("unavailable dependencies changed the saved IP or bypassed the verified asset gate")
		}
		t.Log("stopped CustomHosts refused before metadata; legacy asset mismatch followed the source failure path without downloading or changing Hosts")
		return
	}
	countNotices := func() (starts, completes int) {
		for _, row := range originalWebHistoryRead(t, a, admin, "/api/message/web?count=50") {
			switch row["title"] {
			case "开始Cloudflare IP优选 ...":
				if row["channel"] != "Web" || row["userid"] != "admin" {
					t.Error("Cloudflare command start notice lost its real Web sender")
				}
				starts++
			case "【Cloudflare优选任务完成】":
				if row["text"] != "原ip："+oldIP+"\n新ip："+newIP {
					t.Error("Cloudflare completion notice lost original/result IPs")
				}
				completes++
			}
		}
		return starts, completes
	}
	reloads := func() int {
		t.Helper()
		key := "cloudflare_owned_reloads"
		raw, err := a.pluginStore.originalBaseGetData(t.Context(), "CustomHosts", &key)
		var count int
		if err != nil || json.Unmarshal(raw, &count) != nil {
			t.Fatalf("could not read CustomHosts reload evidence: %v", err)
		}
		return count
	}
	expectedStarts := 0
	if !onceAndAPI {
		send()
		expectedStarts = 1
	}
	privateAwait(t, "Cloudflare notices and actual CustomHosts reload", func() bool {
		starts, completes := countNotices()
		return starts == expectedStarts && completes == 1 && reloads() == 1
	})
	expectedFile := cloudflareFixturePrefix + "# CustomHostsPlugin\n" + newIP + "\towned-cf-a.invalid\n" + newIP + "\towned-cf-b.invalid\n"
	if content, err := os.ReadFile(ownedHosts); err != nil || string(content) != expectedFile {
		t.Fatalf("CustomHosts did not apply exactly the two owned domains: %v", err)
	}
	cloudflare, cloudflareRevision := readConfig(cf.PluginID)
	custom, customRevision := readConfig("CustomHosts")
	if cloudflare["cf_ip"] != newIP || cloudflare["version"] != "v2.3.5" || custom["hosts"] != updatedHosts || custom["enabled"] != true ||
		archiveRequests.Load() != 1 || probeConnections.Load() == 0 {
		t.Fatal("real official engine did not persist the expected IP, version and CustomHosts replacement")
	}
	if onceAndAPI {
		if cloudflare["onlyonce"] != false {
			t.Fatal("the private once job did not persist its cleared switch")
		}
		services, err := a.originalPublicServicesResult(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, service := range services {
			if service.InstanceID == cf.PluginID && service.ServiceID == cf.PluginID && service.CanRun && service.NextRun != nil {
				found = true
			}
		}
		if !found {
			t.Fatal("the ordinary cron was not registered separately from the private once job")
		}
		before := probeConnections.Load()
		refused := callTest(t, a.Handler(), http.MethodGet, route+"wrong", "", nil, "")
		if refused.Code != http.StatusUnauthorized || probeConnections.Load() != before {
			t.Fatal("invalid API key admitted Cloudflare work")
		}
		reply := callTest(t, a.Handler(), http.MethodGet, route+ownedKey, "", nil, "")
		var result struct {
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
		}
		if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &result) != nil || !result.Success ||
			string(result.Data) != "{}" || probeConnections.Load() <= before || archiveRequests.Load() != 1 || reloads() != 1 {
			t.Fatalf("authenticated API did not complete one unchanged run: %d %s", reply.Code, reply.Body.String())
		}
		t.Log("private once job cleared its switch; public cron remained registered; bad API key was denied and valid API used the cached engine")
		return
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a = start()
	originalBaseSDKLoaded(t, a, cf.PluginID)
	originalBaseSDKLoaded(t, a, "CustomHosts")
	previousConnections := probeConnections.Load()
	send()
	privateAwait(t, "reopened Cloudflare reused the official engine and observed unchanged IP", func() bool {
		logs, err := a.readLogs(map[string]string{"module": "plugins", "limit": "100"})
		if err != nil || probeConnections.Load() <= previousConnections {
			return false
		}
		for _, row := range logs {
			if msg, _ := row["message"].(string); strings.Contains(msg, "CloudflareSpeedTest CDN优选ip未变，不做处理") {
				return true
			}
		}
		return false
	})
	_, actualCloudflareRevision := readConfig(cf.PluginID)
	_, actualCustomRevision := readConfig("CustomHosts")
	starts, completes := countNotices()
	if starts != 2 || completes != 1 || reloads() != 1 || archiveRequests.Load() != 1 ||
		actualCloudflareRevision != cloudflareRevision || actualCustomRevision != customRevision {
		t.Fatal("unchanged result after restart repeated a write, reload, completion, or binary download")
	}
	t.Logf("Cloudflare source Web commands ran the exact Linux CFST archive %s on an owned loopback; reload and changed/unchanged effects confirmed", archiveSHA)
}

func installCloudflareCustomHostsPeer(t *testing.T, a *App, admin *http.Cookie, binary string) {
	t.Helper()
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	raw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.PluginID != "CustomHosts" || manifest.Version != "1.2.2" || manifest.Validate() != nil {
		t.Fatal("unexpected test-only CustomHosts peer manifest", err)
	}
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version,
		Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{
			Platform: "linux-amd64", Format: "executable", Size: int64(len(data)), HostContract: pluginBaseSDKContract,
			SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), ExecutableSHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
		}}}},
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	payload, err := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": repository.URL + "/index"})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(payload), admin, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	originalBaseSDKLoaded(t, a, manifest.PluginID)
}
