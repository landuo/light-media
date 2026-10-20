package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
	serverstatus "light-media/internal/plugins/serverstatus"
)

type pluginStatusHostFixture struct {
	mu                 sync.Mutex
	clock              float64
	status, dns, tls   int
	entered, release   chan struct{}
	observedViewBudget time.Duration
}

func (f *pluginStatusHostFixture) now() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}
func (f *pluginStatusHostFixture) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.dns, f.tls
}
func (f *pluginStatusHostFixture) Status(ctx context.Context, target string, timeout time.Duration) (*serverstatus.Response, error) {
	if target != serverstatus.ServerURL || timeout != 20*time.Second {
		return nil, errors.New("non-source status request")
	}
	f.mu.Lock()
	f.status++
	f.clock++
	count := f.status
	entered, release := f.entered, f.release
	f.entered, f.release = nil, nil
	if deadline, ok := ctx.Deadline(); ok {
		f.observedViewBudget = time.Until(deadline)
	}
	f.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	body := fmt.Sprintf("Active connections: 12\nserver accepts handled requests\n1000 995 %d\nReading: 2 Writing: 3 Waiting: 7\n", count*100)
	return &serverstatus.Response{Status: 200, Text: body, Content: []byte(body), Headers: map[string]string{"Content-Type": "text/plain"}, Protocol: 11}, nil
}
func (f *pluginStatusHostFixture) DNS(_ context.Context, host string, port int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if host != "movie-pilot.org" || port != 443 {
		return nil, errors.New("non-source DNS request")
	}
	f.dns++
	return []string{"192.0.2.1", "2001:db8::1"}, nil
}
func (f *pluginStatusHostFixture) TLS(_ context.Context, host string, port int, timeout time.Duration) (serverstatus.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if host != "movie-pilot.org" || port != 443 || timeout != 5*time.Second {
		return serverstatus.Certificate{}, errors.New("non-source TLS request")
	}
	f.tls++
	return serverstatus.Certificate{Version: "TLSv1.3", NotAfter: "Jan  1 00:00:00 2030 GMT", Subject: [][][2]string{{{"commonName", host}}}, Issuer: [][][2]string{{{"commonName", "Synthetic CA"}}}}, nil
}

func serverStatusTestApp(t *testing.T) (*App, *pluginStatusHostFixture) {
	t.Helper()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	f := &pluginStatusHostFixture{clock: 1700000000}
	a.pluginServerStatus.network, a.pluginServerStatus.clock = f, f.now
	return a, f
}
func executeServerStatusPending(t *testing.T, a *App) PluginRun {
	t.Helper()
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	run, err = a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || run.State != "succeeded" {
		t.Fatal("real status worker failed", run.State, run.ErrorCode, err)
	}
	return run
}

func TestPluginServerStatusActualSDKViewsKeepVolatileSampleAndSourceConfig(t *testing.T) {
	a, f := serverStatusTestApp(t)
	h, cookie := a.Handler(), setupTestAdmin(t, a)
	install := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"MPServerStatus"}`, cookie, "")
	if install.Code != 200 {
		t.Fatal(install.Code, install.Body.String())
	}
	executeServerStatusPending(t, a)
	page := callTest(t, h, "GET", "/api/plugins/MPServerStatus/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "插件未启用") {
		t.Fatal("disabled source page changed", page.Code, page.Body.String())
	}
	if n, _, _ := f.counts(); n != 0 {
		t.Fatal("disabled page contacted network")
	}
	dashboard := callTest(t, h, "GET", "/api/plugins/MPServerStatus/dashboard", "", cookie, "")
	if dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), `"refresh":10`) || !strings.Contains(dashboard.Body.String(), `"md":8`) {
		t.Fatal("direct disabled source dashboard missing", dashboard.Code, dashboard.Body.String())
	}
	if n, dns, tls := f.counts(); n != 1 || dns != 0 || tls != 0 {
		t.Fatal("dashboard added detail probes", n, dns, tls)
	}
	save := callTest(t, h, "PUT", "/api/plugins/MPServerStatus/config", `{"revision":1,"config":{"enable":"source-truthy","unused":{"value":"synthetic-private-setting"}}}`, cookie, "")
	if save.Code != 200 {
		t.Fatal(save.Code, save.Body.String())
	}
	executeServerStatusPending(t, a)
	form := callTest(t, h, "GET", "/api/plugins/MPServerStatus/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), `"enable":true`) || strings.Contains(form.Body.String(), "synthetic-private-setting") {
		t.Fatal("source normalized form/private config projection changed", form.Code, form.Body.String())
	}
	stored, _ := a.pluginStore.Config(context.Background(), "MPServerStatus")
	if !strings.Contains(string(stored.Value), "source-truthy") || !strings.Contains(string(stored.Value), "synthetic-private-setting") {
		t.Fatal("config was normalized destructively")
	}
	page = callTest(t, h, "GET", "/api/plugins/MPServerStatus/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "等待下一次采样") || !strings.Contains(page.Body.String(), "服务探测") {
		t.Fatal("first page did not reset/init source sample", page.Code, page.Body.String())
	}
	dashboard = callTest(t, h, "GET", "/api/plugins/MPServerStatus/dashboard", "", cookie, "")
	if dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), "100.00 次/秒") {
		t.Fatal("second worker lost host sample", dashboard.Code, dashboard.Body.String())
	}
	page = callTest(t, h, "GET", "/api/plugins/MPServerStatus/page", "", cookie, "")
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body.String())
	}
	if n, dns, tls := f.counts(); n != 4 || dns != 1 || tls != 1 {
		t.Fatal("detail cache did not survive worker exit", n, dns, tls)
	}
	f.mu.Lock()
	budget := f.observedViewBudget
	f.mu.Unlock()
	if budget < 50*time.Second {
		t.Fatal("source HTTP20/TLS5 squeezed into generic 10s budget", budget)
	}
	if got := callTest(t, h, "POST", "/api/plugins/MPServerStatus/reload", `{}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	executeServerStatusPending(t, a)
	page = callTest(t, h, "GET", "/api/plugins/MPServerStatus/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "等待下一次采样") {
		t.Fatal("explicit reload did not clear volatile samples", page.Body.String())
	}
	if _, dns, tls := f.counts(); dns != 2 || tls != 2 {
		t.Fatal("reload did not clear positive/negative network caches", dns, tls)
	}
	meta := callTest(t, h, "GET", "/api/plugins/dashboard/meta", "", cookie, "")
	if meta.Code != 200 || !strings.Contains(meta.Body.String(), "MPServerStatus") {
		t.Fatal("truthy source state missing from dashboard metadata", meta.Body.String())
	}
	for _, path := range []string{"page", "dashboard", "form"} {
		if got := callTest(t, h, "GET", "/api/plugins/MPServerStatus/"+path, "", nil, ""); got.Code != 401 {
			t.Fatal("anonymous status access", path, got.Code)
		}
	}
}

func TestPluginServerStatusRejectsWorkerSelectedProbeAndStaleRun(t *testing.T) {
	a, f := serverStatusTestApp(t)
	manifest := serverstatus.Manifest()
	raw, _ := json.Marshal(manifest)
	_, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: "MPServerStatus", PluginID: "MPServerStatus", Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw, DesiredEnabled: true}, map[string]any{"enable": true})
	if err != nil {
		t.Fatal(err)
	}
	executeServerStatusPending(t, a)
	installation, _ := a.pluginStore.Installation(context.Background(), "MPServerStatus")
	config, _ := a.pluginStore.Config(context.Background(), "MPServerStatus")
	for _, input := range []string{`{}`, `{"include_network_details":null}`, `{"include_network_details":false}`, `{"include_network_details":true,"url":"http://127.0.0.1/"}`, `{"include_network_details":"true"}`} {
		if _, err := a.pluginServerStatusSnapshot(context.Background(), installation, config.Revision, config.Value, "page", json.RawMessage(input)); err == nil {
			t.Fatal("worker selected request semantics", input)
		}
	}
	identity := plugins.Identity{PluginID: installation.PluginID, InstanceID: installation.InstanceID, Version: installation.Version}
	if _, err := a.pluginHostCall(context.Background(), installation, nil, identity, "server.status.configure", json.RawMessage(`{}`)); err == nil {
		t.Fatal("read-only caller reset cache without real init run")
	}
	if n, _, _ := f.counts(); n != 0 {
		t.Fatal("invalid request probed network")
	}
	if err := a.pluginStore.Reset(context.Background(), installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginServerStatusSnapshot(context.Background(), installation, config.Revision, config.Value, "dashboard", json.RawMessage(`{"include_network_details":false}`)); err == nil {
		t.Fatal("old generation published a sample")
	}
}

func TestPluginServerStatusConfigCanReplaceBlockedProbeWithoutOldSample(t *testing.T) {
	a, f := serverStatusTestApp(t)
	h, cookie := a.Handler(), setupTestAdmin(t, a)
	if got := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"MPServerStatus"}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	executeServerStatusPending(t, a)
	if got := callTest(t, h, "PUT", "/api/plugins/MPServerStatus/config", `{"revision":1,"config":{"enable":true}}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	executeServerStatusPending(t, a)
	installation, _ := a.pluginStore.Installation(context.Background(), "MPServerStatus")
	config, _ := a.pluginStore.Config(context.Background(), "MPServerStatus")
	entered, release := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.entered, f.release = entered, release
	f.mu.Unlock()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	probeDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go func() {
		_, err := a.pluginServerStatusSnapshot(ctx, installation, config.Revision, config.Value, "page", json.RawMessage(`{"include_network_details":true}`))
		probeDone <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("synthetic probe did not enter")
	}
	changed := make(chan error, 1)
	go func() {
		_, _, err := a.pluginStore.SaveConfigAndQueue(ctx, installation.InstanceID, config.Revision, map[string]any{"enable": true})
		if err == nil {
			var run PluginRun
			run, err = a.pluginStore.ClaimRun(ctx)
			if err == nil {
				_, err = a.pluginServerStatusConfigure(ctx, installation, &run, json.RawMessage(`{}`))
			}
		}
		changed <- err
	}()
	select {
	case err := <-changed:
		if err != nil {
			t.Fatal("new init failed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("network wait held config/registry/SQL lock")
	}
	unblock()
	if err := <-probeDone; err == nil {
		t.Fatal("old revision returned a successful stale probe")
	}
	a.pluginServerStatus.mu.Lock()
	current := a.pluginServerStatus.slots[installation.InstanceID]
	a.pluginServerStatus.mu.Unlock()
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.revision != config.Revision+1 || current.machine.Last != nil || current.machine.DNSCache != nil || current.machine.TLSCache != nil {
		t.Fatal("old result contaminated replacement volatile cache")
	}
}

func TestPluginServerStatusViewWaitsForInitializationWithoutHoldingProcess(t *testing.T) {
	a, f := serverStatusTestApp(t)
	m := serverstatus.Manifest()
	raw, _ := json.Marshal(m)
	if _, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: m.PluginID, PluginID: m.PluginID, Version: m.Version, ManifestSchemaVersion: m.SchemaVersion, Manifest: raw, DesiredEnabled: true}, map[string]any{"enable": true}); err != nil {
		t.Fatal(err)
	}
	installation, _ := a.pluginStore.Installation(context.Background(), m.PluginID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.pluginView(ctx, installation, "page", "")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatal("view bypassed pending initialization", err)
	case <-time.After(50 * time.Millisecond):
	}
	if n, _, _ := f.counts(); n != 0 {
		t.Fatal("pre-init view sampled the source")
	}
	executeServerStatusPending(t, a)
	if err := <-done; err != nil {
		t.Fatal("view did not resume after real init", err)
	}
	if n, dns, tls := f.counts(); n != 1 || dns != 1 || tls != 1 {
		t.Fatal("initialized view sampled incorrectly", n, dns, tls)
	}
}

func TestPluginServerStatusSavingConfigCancelsInFlightRealView(t *testing.T) {
	a, f := serverStatusTestApp(t)
	h, cookie := a.Handler(), setupTestAdmin(t, a)
	if got := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"MPServerStatus"}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	executeServerStatusPending(t, a)
	if got := callTest(t, h, "PUT", "/api/plugins/MPServerStatus/config", `{"revision":1,"config":{"enable":true}}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	executeServerStatusPending(t, a)
	installation, _ := a.pluginStore.Installation(context.Background(), "MPServerStatus")
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	f.mu.Lock()
	f.entered, f.release = entered, release
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.pluginView(ctx, installation, "page", "")
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("view did not enter the synthetic network")
	}
	if got := callTest(t, h, "PUT", "/api/plugins/MPServerStatus/config", `{"revision":2,"config":{"enable":false}}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled old view reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("old probe retained the only process slot after config changed")
	}
	executeServerStatusPending(t, a)
	page := callTest(t, h, "GET", "/api/plugins/MPServerStatus/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "插件未启用") {
		t.Fatal("replacement did not take effect", page.Body.String())
	}
}

func TestPluginServerStatusSourceSingleDashboardIdentity(t *testing.T) {
	a, _ := serverStatusTestApp(t)
	h, cookie := a.Handler(), setupTestAdmin(t, a)
	if got := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"MPServerStatus"}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Code)
	}
	executeServerStatusPending(t, a)
	if got := callTest(t, h, "PUT", "/api/plugins/MPServerStatus/config", `{"revision":1,"config":{"enable":true}}`, cookie, ""); got.Code != 200 {
		t.Fatal(got.Code)
	}
	executeServerStatusPending(t, a)
	meta := callTest(t, h, "GET", "/api/plugins/dashboard/meta", "", cookie, "")
	if meta.Code != 200 || !strings.Contains(meta.Body.String(), `"id":"MPServerStatus"`) || !strings.Contains(meta.Body.String(), `"key":""`) || strings.Contains(meta.Body.String(), `"key":"default"`) {
		t.Fatal("source singleton identity changed", meta.Code, meta.Body.String())
	}
	got := callTest(t, h, "GET", "/api/plugins/MPServerStatus/dashboard?key=", "", cookie, "")
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"refresh":10`) {
		t.Fatal("original empty key failed", got.Code, got.Body.String())
	}
}
