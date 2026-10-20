package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
)

func installCustomHostsFixture(t *testing.T, a *App) {
	t.Helper()
	useBuiltInPluginProcess(t, a)
	manifest := builtin.CustomHostsManifest()
	raw, _ := json.Marshal(manifest)
	if err := a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
}

func executeCustomHostsConfig(t *testing.T, a *App, revision int64, enabled bool, hosts any) PluginRun {
	t.Helper()
	_, _, err := a.pluginStore.SaveConfigAndQueue(context.Background(), "CustomHosts", revision, map[string]any{"enabled": enabled, "hosts": hosts, "err_hosts": ""})
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	run, err = a.pluginStore.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestPluginCustomHostsRealProcessWritesNormalizesAndRestoresOwnedFile(t *testing.T) {
	a := newTestApp(t)
	a.pluginHostsFile = filepath.Join(t.TempDir(), "hosts")
	base := "# owned fixture\n127.0.0.1\tlocalhost\n"
	if err := os.WriteFile(a.pluginHostsFile, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	installCustomHostsFixture(t, a)
	run := executeCustomHostsConfig(t, a, 0, true, "127.0.0.1 Fixture.One\n2001:db8::1 IPv6.One\nbad-ip invalid.example")
	if run.State != "succeeded" {
		t.Fatal(run.State, run.ErrorCode)
	}
	raw, _ := os.ReadFile(a.pluginHostsFile)
	if !strings.HasPrefix(string(raw), base) || !strings.Contains(string(raw), "127.0.0.1\tfixture.one") || !strings.Contains(string(raw), "2001:db8::1\tipv6.one") || strings.Contains(string(raw), "bad-ip") {
		t.Fatal("actual hosts write missing", string(raw))
	}
	stored, err := a.pluginStore.Config(context.Background(), "CustomHosts")
	var config struct {
		Enabled  bool
		Hosts    string
		ErrHosts []string `json:"err_hosts"`
	}
	if err != nil || json.Unmarshal(stored.Value, &config) != nil || !config.Enabled || len(config.ErrHosts) != 1 || !strings.HasSuffix(config.Hosts, "\n") {
		t.Fatal("normalized config/errors missing", err)
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification' AND id LIKE ?`, "plugin_notice_"+run.ID+"_invalid_%").Scan(&count); err != nil || count != 1 {
		t.Fatal("original invalid-row system notification missing", count, err)
	}
	run = executeCustomHostsConfig(t, a, stored.Revision, false, config.Hosts)
	if run.State != "succeeded" {
		t.Fatal(run.State, run.ErrorCode)
	}
	raw, _ = os.ReadFile(a.pluginHostsFile)
	if string(raw) != base {
		t.Fatal("disable did not restore system prefix", string(raw))
	}
}

func TestPluginCustomHostsFileWriteFailureDisablesAndDoesNotReportSuccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires an unprivileged test user")
	}
	a := newTestApp(t)
	a.pluginHostsFile = filepath.Join(t.TempDir(), "hosts")
	base := "127.0.0.1\tlocalhost\n"
	if err := os.WriteFile(a.pluginHostsFile, []byte(base), 0400); err != nil {
		t.Fatal(err)
	}
	installCustomHostsFixture(t, a)
	run := executeCustomHostsConfig(t, a, 0, true, "127.0.0.1 fixture.invalid")
	if run.State != "failed" || run.ErrorCode != "HOSTS_WRITE_FAILED" {
		t.Fatal("permission failure reported success", run.State, run.ErrorCode)
	}
	stored, err := a.pluginStore.Config(context.Background(), "CustomHosts")
	var state struct{ Enabled bool }
	if err != nil || json.Unmarshal(stored.Value, &state) != nil || state.Enabled {
		t.Fatal("write failure did not disable plugin", err)
	}
	raw, _ := os.ReadFile(a.pluginHostsFile)
	if string(raw) != base {
		t.Fatal("permission failure modified original hosts")
	}
}

func TestPluginCustomHostsRejectsChangedSnapshotAndOutsideScope(t *testing.T) {
	a := newTestApp(t)
	a.pluginHostsFile = filepath.Join(t.TempDir(), "hosts")
	base := "127.0.0.1\tlocalhost\n"
	if err := os.WriteFile(a.pluginHostsFile, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]string{"content": base + builtin.CustomHostsMarker + "\n127.0.0.1 fixture.invalid\n", "expected_digest": "wrong"})
	if _, err := a.replaceContainerHosts(context.Background(), request); pluginErrorCode(err) != "HOSTS_CHANGED" {
		t.Fatal("stale file was overwritten", err)
	}
	request, _ = json.Marshal(map[string]string{"content": "127.0.0.2 localhost\n", "expected_digest": hostsDigest([]byte(base))})
	if _, err := a.replaceContainerHosts(context.Background(), request); pluginErrorCode(err) != "HOSTS_SCOPE_DENIED" {
		t.Fatal("system prefix changed", err)
	}
	original := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(original, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a.pluginHostsFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, a.pluginHostsFile); err != nil {
		t.Fatal(err)
	}
	if _, err := a.readContainerHosts(); pluginErrorCode(err) != "HOSTS_READ_FAILED" {
		t.Fatal("system hosts symlink followed", err)
	}
}

func TestPluginCustomHostsStartupRestoresActiveConfigAndSkipsMigrationStaging(t *testing.T) {
	a := newTestApp(t)
	a.pluginHostsFile = filepath.Join(t.TempDir(), "hosts")
	base := "127.0.0.1\tlocalhost\n"
	if err := os.WriteFile(a.pluginHostsFile, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	installCustomHostsFixture(t, a)
	if run := executeCustomHostsConfig(t, a, 0, true, "127.0.0.1 restored.invalid"); run.State != "succeeded" {
		t.Fatal(run.State, run.ErrorCode)
	}
	manifest := builtin.CustomHostsManifest()
	raw, _ := json.Marshal(manifest)
	if err := a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: "CustomHosts.staged", PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.SaveConfig(context.Background(), "CustomHosts.staged", 0, map[string]any{"enabled": true, "hosts": "127.0.0.1 must-not-apply.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.pluginHostsFile, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.initializeNativePlugins(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil || run.InstanceID != "CustomHosts" {
		t.Fatal("startup did not restore active instance", run.InstanceID, err)
	}
	a.executePluginRun(context.Background(), run)
	content, _ := os.ReadFile(a.pluginHostsFile)
	if !strings.Contains(string(content), "restored.invalid") || strings.Contains(string(content), "must-not-apply") {
		t.Fatal("migration staging was activated or saved config not restored")
	}
	runs, err := a.pluginStore.Runs(context.Background(), "CustomHosts.staged", 50)
	if err != nil || len(runs) != 0 {
		t.Fatal("staged import created startup runs", len(runs), err)
	}
	var pending int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE state='pending'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("startup initialization duplicated queued runs", pending, err)
	}
}

// Explicit opt-in, only inside a UUID-owned container fixture. It proves the
// real Go resolver and HTTP transport use /etc/hosts, without a real site,
// credentials, published port, or any change to the NAS host/production file.
func TestIsolatedCustomHostsNetwork(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_HOSTS_TEST") != "1" {
		t.Skip("requires owned Docker hosts fixture")
	}
	fixture := os.Getenv("LIGHT_MEDIA_HOSTS_TEST_ID")
	if len(fixture) != 36 || strings.Trim(fixture, "abcdef0123456789-") != "" || os.Geteuid() != 1000 {
		t.Fatal("invalid isolated hosts fixture")
	}
	raw, err := os.ReadFile("/etc/hosts")
	if err != nil || !strings.HasPrefix(string(raw), "# light-media-owned-hosts-fixture "+fixture+"\n") || strings.Contains(string(raw), builtin.CustomHostsMarker) {
		t.Fatal("owned hosts mount not confirmed")
	}
	base := string(raw)
	a := newTestApp(t)
	installCustomHostsFixture(t, a)
	hostname := "light-media-" + fixture + ".invalid"
	run := executeCustomHostsConfig(t, a, 0, true, "127.0.0.1 "+hostname)
	if run.State != "succeeded" {
		t.Fatal("container write failed", run.State, run.ErrorCode)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, hostname+":") {
			t.Error("request host authority changed")
		}
		io.WriteString(w, "owned-hosts-fixture-ok")
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	dialer := net.Dialer{Timeout: time.Second, Resolver: &net.Resolver{PreferGo: true}}
	transport := &http.Transport{DialContext: dialer.DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	response, err := client.Get("http://" + hostname + ":" + port + "/")
	if err != nil {
		t.Fatal("Go connection did not use the applied hosts entry", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "owned-hosts-fixture-ok" {
		t.Fatal("wrong connection target")
	}
	stored, _ := a.pluginStore.Config(context.Background(), "CustomHosts")
	run = executeCustomHostsConfig(t, a, stored.Revision, false, "")
	restored, _ := os.ReadFile("/etc/hosts")
	if run.State != "succeeded" || string(restored) != base {
		t.Fatal("container hosts not restored", run.State, run.ErrorCode)
	}
	t.Log("owned_container_hosts_write_go_http_and_restore_verified")
}

func TestPluginCustomHostsSourceFullReplacementAndCanonicalPrefix(t *testing.T) {
	a := newTestApp(t)
	a.pluginHostsFile = filepath.Join(t.TempDir(), "hosts")
	before := "# owned fixture\n127.0.0.1 localhost # base comment\n"
	if e := os.WriteFile(a.pluginHostsFile, []byte(before), 0600); e != nil {
		t.Fatal(e)
	}
	installCustomHostsFixture(t, a)
	if _, _, e := a.pluginStore.SaveConfigAndQueue(context.Background(), "CustomHosts", 0, map[string]any{"enabled": "source-truthy", "hosts": "192.0.2.9 名称.测试", "err_hosts": map[string]any{"ignored": true}, "unknown_source_field": 42}); e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.ClaimRun(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	a.executePluginRun(context.Background(), run)
	done, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil || done.State != "succeeded" {
		t.Fatal(done.State, done.ErrorCode, e)
	}
	value, e := a.pluginStore.Config(context.Background(), "CustomHosts")
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(value.Value, &fields) != nil || len(fields) != 3 || string(fields["enabled"]) != "true" {
		t.Fatal("source whole config replacement not preserved")
	}
	content, e := os.ReadFile(a.pluginHostsFile)
	if e != nil || !strings.HasPrefix(string(content), "# owned fixture\n127.0.0.1\tlocalhost # base comment\n") || !strings.Contains(string(content), "192.0.2.9\txn--") {
		t.Fatal("source parser/IDNA/canonical prefix differs", e)
	}
	request, _ := json.Marshal(map[string]string{"content": "# owned fixture\n127.0.0.2\tlocalhost # base comment\n", "expected_digest": hostsDigest(content)})
	if _, e = a.replaceContainerHosts(context.Background(), request); pluginErrorCode(e) != "HOSTS_SCOPE_DENIED" {
		t.Fatal("canonicalization allowed changing system addresses", e)
	}
}
