package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
)

type cloudflareTestTransport func(*http.Request) (*http.Response, error)

func (f cloudflareTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func cloudflareTestOfficial(t *testing.T, a *App) *atomic.Int64 {
	t.Helper()
	withoutProxyEnv(t)
	t.Setenv("PROXY_HOST", "https://ghfast.top")
	folder := "../../testdata/plugins/cloudflare-native-official-v2.3.5"
	metadata, e := os.ReadFile(filepath.Join(folder, "release.json"))
	if e != nil {
		t.Fatal(e)
	}
	asset := "cfst_linux_amd64.tar.gz"
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		asset = "cfst_darwin_arm64.zip"
	} else if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("official native fixture missing for test platform")
	}
	data, e := os.ReadFile(filepath.Join(folder, asset))
	if e != nil {
		t.Fatal(e)
	}
	var release cloudflareOfficialRelease
	if e = json.Unmarshal(metadata, &release); e != nil {
		t.Fatal(e)
	}
	count := new(atomic.Int64)
	a.pluginCloudflare.client = &http.Client{Transport: cloudflareTestTransport(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.URL.String() == cf.ReleaseAPI {
			body = metadata
		} else if r.URL.String() == "https://ghfast.top/"+cf.ReleasePrefix+"/"+release.Tag+"/"+asset || r.URL.String() == cf.ReleasePrefix+"/"+release.Tag+"/"+asset {
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Error("asset role header missing")
			}
			count.Add(1)
			body = data
		} else {
			return nil, fmt.Errorf("test refused unexpected official URL")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	return count
}
func cloudflareTestClaim(t *testing.T, a *App, instance string) PluginRun {
	t.Helper()
	run, e := a.pluginStore.claimRunForInstance(context.Background(), instance)
	if e != nil {
		t.Fatal(e)
	}
	return run
}
func cloudflareTestExecute(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	a.executePluginRun(context.Background(), run)
	actual, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if actual.State != "succeeded" {
		state, _, err := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, run.InstanceID, run.Generation)
		t.Fatalf("actual SDK %s %s %s result=%s owner=%+v read=%v", actual.Action, actual.State, actual.ErrorCode, actual.Result, state.Runs[run.ID], err)
	}
	return actual
}
func cloudflareTestInstall(t *testing.T, a *App) (*http.Cookie, *atomic.Int64) {
	t.Helper()
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	a.pluginHostsFile = filepath.Join(t.TempDir(), "owned-hosts")
	if e := os.WriteFile(a.pluginHostsFile, []byte("# only synthetic hosts\n127.0.0.1 localhost\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, plugin := range []string{"CustomHosts", cf.PluginID} {
		w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"`+plugin+`"}`, cookie, "")
		if w.Code != 200 {
			t.Fatalf("install %s %d %s", plugin, w.Code, w.Body.String())
		}
		cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, plugin))
	}
	value, e := a.pluginStore.Config(context.Background(), "CustomHosts")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.SaveConfigAndQueue(context.Background(), "CustomHosts", value.Revision, map[string]any{"enabled": false, "hosts": "192.0.2.1 synthetic.example", "err_hosts": []string{}}); e != nil {
		t.Fatal(e)
	}
	cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, "CustomHosts"))
	return cookie, cloudflareTestOfficial(t, a)
}
func cloudflareTestConfigure(t *testing.T, a *App, cookie *http.Cookie, config map[string]any) {
	t.Helper()
	current, e := a.pluginStore.Config(context.Background(), cf.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	body := mustCleanSeedJSON(map[string]any{"revision": current.Revision, "config": config})
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/"+cf.PluginID+"/config", string(body), cookie, "")
	if w.Code != 200 {
		t.Fatalf("config %d %s", w.Code, w.Body.String())
	}
	cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, cf.PluginID))
}
func cloudflareTestConfig(args string) map[string]any {
	return map[string]any{"cf_ip": "192.0.2.1", "cron": "0 7 * * *", "ipv4": true, "ipv6": false, "notify": true, "check": false, "onlyonce": false, "re_install": false, "version": "", "additional_args": args}
}
func cloudflareTestLoopback(t *testing.T) int {
	t.Helper()
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestPluginCloudflareActualSDKOfficialCLIAndCustomHostsReload(t *testing.T) {
	a := newTestApp(t)
	cookie, downloads := cloudflareTestInstall(t, a)
	port := cloudflareTestLoopback(t)
	config := cloudflareTestConfig(fmt.Sprintf("-ip 127.0.0.1 -tp %d -dd -n 1 -t 1 -p 0", port))
	cloudflareTestConfigure(t, a, cookie, config)
	run, e := a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run = cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, cf.PluginID))
	var out cf.Outcome
	if json.Unmarshal(run.Result, &out) != nil || out.Status != "updated" || out.NewIP != "127.0.0.1" {
		t.Fatal("native CLI result missing", out)
	}
	if downloads.Load() != 1 {
		t.Fatal("official fixture asset not consumed once", downloads.Load())
	}
	state, _, e := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1)
	if e != nil || len(state.Artifacts) != 1 || !validCloudflareAsset(state.Artifacts[0]) {
		t.Fatal("verified executable receipt missing", e)
	}
	custom, e := a.pluginStore.Config(context.Background(), "CustomHosts")
	var host cf.Object
	_ = json.Unmarshal(custom.Value, &host)
	if e != nil || host["hosts"] != "127.0.0.1 synthetic.example\n" || host["enabled"] != true {
		t.Fatal("real CustomHosts config not saved", host, e)
	}
	cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, "CustomHosts"))
	actual, e := os.ReadFile(a.pluginHostsFile)
	if e != nil || !strings.Contains(string(actual), "127.0.0.1\tsynthetic.example") {
		t.Fatal("real reloader did not apply owned Hosts", string(actual), e)
	}
	var notices int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_cloudflare_notices`).Scan(&notices); e != nil || notices != 1 {
		t.Fatal("source notice producer missing", notices, e)
	}
	before := downloads.Load()
	if _, e = a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); e != nil {
		t.Fatal(e)
	}
	again := cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, cf.PluginID))
	if json.Unmarshal(again.Result, &out) != nil || out.Status != "unchanged" || downloads.Load() != before {
		t.Fatal("source unchanged/native cache behavior", out)
	}
}

func TestPluginCloudflareSourceAPIUsesTrustedOwnRevisionAndRealSDK(t *testing.T) {
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	port := cloudflareTestLoopback(t)
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig(fmt.Sprintf("-ip 127.0.0.1 -tp %d -dd -n 1 -t 1 -p 0", port)))
	settings := a.Settings()
	settings.APIToken = "owned-cloudflare-api-fixture"
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	if w := callTest(t, a.Handler(), "GET", "/api/v1/plugin/CloudflareSpeedTest/cloudflare_speedtest?apikey=wrong", "", nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.pluginWorker(ctx) }()
	defer func() { cancel(); <-done }()
	w := callTest(t, a.Handler(), "GET", "/api/v1/plugin/CloudflareSpeedTest/cloudflare_speedtest?apikey="+settings.APIToken, "", nil, "")
	if w.Code != 200 {
		t.Fatalf("real API %d %s", w.Code, w.Body.String())
	}
	var response map[string]any
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response["success"] != true {
		t.Fatal(response)
	}
}

func TestPluginCloudflareOnceCronAndPublicManualServiceUseBoundRun(t *testing.T) {
	a := newTestApp(t)
	cookie, _ := cloudflareTestInstall(t, a)
	config := cloudflareTestConfig("-h")
	config["onlyonce"] = true
	cloudflareTestConfigure(t, a, cookie, config)
	current, e := a.pluginStore.Config(context.Background(), cf.PluginID)
	var value cf.Object
	_ = json.Unmarshal(current.Value, &value)
	if e != nil || value["onlyonce"] != false {
		t.Fatal("one-shot not consumed atomically", value, e)
	}
	w := callTest(t, a.Handler(), "GET", "/api/plugins/services", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Cloudflare IP优选服务") {
		t.Fatal(w.Code, w.Body.String())
	}
	var dueRaw string
	if e = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id='CloudflareSpeedTest' AND service_id='CloudflareSpeedTest.once'`).Scan(&dueRaw); e != nil {
		t.Fatal(e)
	}
	due, e := time.Parse(time.RFC3339Nano, dueRaw)
	if e != nil {
		t.Fatal(e)
	}
	if wait := time.Until(due) + 20*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}
	n, e := a.pluginStore.enqueueDuePluginSchedules(context.Background(), time.Now())
	if e != nil || n != 1 {
		t.Fatal("once dispatch", n, e)
	}
	once := cloudflareTestClaim(t, a, cf.PluginID)
	if once.Action != "once" {
		t.Fatal(once.Action)
	}
	cloudflareTestExecute(t, a, once)
	w = callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"CloudflareSpeedTest","service_id":"CloudflareSpeedTest"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, cf.PluginID))

}
