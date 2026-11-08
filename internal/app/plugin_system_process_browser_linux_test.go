package app

import (
	"context"
	"encoding/json"
	"errors"
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
	"light-media/web"
)

// The native Linux browser accepts the canonical ZIP, not a repackaged
// executable. The observed tables are the responses used by the actual UI.
func TestSystemProcessLinuxAMD64BrowserPackage(t *testing.T) {
	bundle := os.Getenv("SOURCE_SYSTEM_PROCESS_PACKAGE_DIR")
	evidence := os.Getenv("SOURCE_SYSTEM_PROCESS_BROWSER_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" || evidence == "" {
		t.Skip("requires the owned SystemProcess Linux amd64 browser fixture")
	}
	const pluginID = "SystemProcess"
	indexRaw, err := os.ReadFile(filepath.Join(bundle, artifacts.IndexName))
	if err != nil {
		t.Fatal(err)
	}
	var index artifacts.RepositoryIndex
	if err := json.Unmarshal(indexRaw, &index); err != nil || index.SchemaVersion != 1 || len(index.Plugins) != 1 {
		t.Fatal("canonical standard SystemProcess package index required", err)
	}
	declared := index.Plugins[0]
	if declared.PluginID != pluginID || declared.CurrentVersion != "1.1" || len(declared.Releases) != 1 || len(declared.Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected SystemProcess candidate")
	}
	artifact := &index.Plugins[0].Releases[0].Artifacts[0]
	if artifact.Format != "zip" || artifact.Platform != "linux-amd64" || artifact.HostContract != pluginResourcesSDKContract {
		t.Fatal("expected the canonical Linux amd64 resource ZIP")
	}
	archive := filepath.Join(bundle, "SystemProcess-1.1-linux-amd64.zip")
	info, err := os.Stat(archive)
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Size {
		t.Fatal("canonical SystemProcess ZIP does not match its index", err)
	}
	var downloads atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/go-plugins.v1.json":
			_ = json.NewEncoder(w).Encode(index)
		case "/SystemProcess-1.1-linux-amd64.zip":
			downloads.Add(1)
			http.ServeFile(w, r, archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	artifact.URL = repository.URL + "/SystemProcess-1.1-linux-amd64.zip"

	pid, name := systemProcessBrowserChild(t)
	t.Setenv("MP_SERVER_HOST", "")
	markets := []string{}
	a, err := New(Options{DataDir: t.TempDir(), Web: web.Handler(), PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	cookie := setupTestAdmin(t, a)
	handler := a.Handler()
	payload, err := json.Marshal(map[string]string{"plugin_id": pluginID, "repo_url": repository.URL + "/go-plugins.v1.json"})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, handler, http.MethodPost, "/api/plugins/install", string(payload), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), pluginID, validatePluginArtifactContract)
	if err != nil || installed.Manifest.Version != "1.1" || installed.SHA256 != artifact.ExecutableSHA256 || downloads.Load() != 1 {
		t.Fatal("host did not install exactly the indexed SystemProcess worker from one ZIP", err)
	}
	object := originalBaseSDKLoaded(t, a, pluginID)
	if enabled, err := object.getState(t.Context()); err != nil || enabled {
		t.Fatal("new SystemProcess worker did not start disabled", enabled, err)
	}
	// The dashboard display preference is independent of the plugin's source
	// enable switch. The browser still performs the actual plugin configuration.
	response = callTest(t, handler, http.MethodPost, "/api/plugins/dashboard/settings/enabled", `{"SystemProcess":true}`, cookie, "")
	var preference struct{ Data struct{ Saved bool } }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &preference) != nil || !preference.Data.Saved {
		t.Fatal("could not select the owned dashboard display preference")
	}

	var mu sync.Mutex
	observed := map[string][]byte{}
	const detailPath = "/api/plugins/SystemProcess/page"
	const dashboardPath = "/api/plugins/SystemProcess/dashboard"
	browser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != detailPath && r.URL.Path != dashboardPath {
			handler.ServeHTTP(w, r)
			return
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, r)
		if result.Code == http.StatusOK {
			mu.Lock()
			observed[r.URL.Path] = append([]byte(nil), result.Body.Bytes()...)
			mu.Unlock()
		}
		for key, values := range result.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(result.Code)
		_, _ = w.Write(result.Body.Bytes())
	})
	systemProcessBrowserGate(t, evidence, browser, pid, name)

	stored, err := a.pluginStore.Config(t.Context(), pluginID)
	var config struct {
		Enable  bool
		Refresh json.RawMessage `json:"dashboard_widget_refresh"`
	}
	if err != nil || json.Unmarshal(stored.Value, &config) != nil || !config.Enable ||
		string(config.Refresh) != "9" && string(config.Refresh) != `"9"` {
		t.Fatal("browser did not persist enabled SystemProcess with refresh 9", err)
	}
	var detail struct {
		RenderMode string `json:"render_mode"`
		Page       []systemProcessOwnedNode
	}
	var dashboard struct {
		ID         string
		RenderMode string `json:"render_mode"`
		Attrs      struct {
			Title   string
			Refresh json.RawMessage
		}
		Elements []systemProcessOwnedNode
	}
	decode := func(path string, out any) {
		t.Helper()
		mu.Lock()
		raw := observed[path]
		mu.Unlock()
		var envelope struct{ Data json.RawMessage }
		if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Data, out) != nil {
			t.Fatal("browser did not obtain the actual SystemProcess view", path)
		}
	}
	decode(detailPath, &detail)
	decode(dashboardPath, &dashboard)
	if detail.RenderMode != "vuetify" || dashboard.RenderMode != "vuetify" || dashboard.ID != pluginID ||
		dashboard.Attrs.Title != "系统进程" || string(dashboard.Attrs.Refresh) != string(config.Refresh) {
		t.Fatal("browser view mode, title or live refresh differs from the installed source")
	}
	detailRows := assertSystemProcessOwnedTable(t, detail.Page, pid, name, "")
	dashboardRows := assertSystemProcessOwnedTable(t, dashboard.Elements, pid, name, "242px")
	t.Logf("native Linux amd64 standard ZIP and actual browser verified: PID %d %s in detail/dashboard (%d/%d rows), enabled and refresh 9 persisted", pid, name, detailRows, dashboardRows)
}

func systemProcessBrowserChild(t *testing.T) (int, string) {
	t.Helper()
	const name = "owned-sp-probe"
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	t.Cleanup(cancel)
	child := exec.CommandContext(ctx, "/bin/sh", "-c", `printf '%s' "$1" > "/proc/$$/comm" || exit; printf 'ready\n'; read -r ignored || :`, "owned", name)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		input.Close()
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		input.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = child.Wait() })
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(output, ready); err != nil || string(ready) != "ready\n" {
		t.Fatal("owned process failed to set its name", err)
	}
	pid := child.Process.Pid
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil || strings.TrimSpace(string(comm)) != name {
		t.Fatal("owned kernel process identity was not independently observable", err)
	}
	return pid, name
}

func systemProcessBrowserGate(t *testing.T, dir string, handler http.Handler, pid int, name string) {
	t.Helper()
	for _, filename := range []string{"browser-ready.json", "browser-done"} {
		if _, err := os.Stat(filepath.Join(dir, filename)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("owned browser gate requires a fresh evidence directory", filename, err)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:5192")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	ready, err := json.Marshal(map[string]any{"address": listener.Addr().String(), "pid": pid, "name": name})
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(dir, "browser-ready.pending")
	if err := os.WriteFile(pending, append(ready, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pending, filepath.Join(dir, "browser-ready.json")); err != nil {
		t.Fatal(err)
	}
	t.Log("SystemProcess browser-ready: 127.0.0.1:5192")
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("SystemProcess browser handoff timed out")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(dir, "browser-done")); err == nil {
				return
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
	}
}
