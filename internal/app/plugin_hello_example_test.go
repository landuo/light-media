package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"light-media/internal/plugins/artifacts"
)

// Exercise the documented packager and the real installer, SDK, encrypted
// persistence and public routes. This does not substitute a mock host for Base.
func TestHelloExampleLocalInstallRunCloneRestartResetUninstall(t *testing.T) {
	project, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "work"), 0750); err != nil {
		t.Fatal(err)
	}
	owned, err := os.MkdirTemp(filepath.Join(project, "work"), "hello-example-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(owned) })
	t.Setenv("TMPDIR", owned) // Avoid macOS /var's symlink in strict artifact roots.
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("node", "examples/hello-test/build.mjs", repository, runtime.GOOS+"-"+runtime.GOARCH)
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal("documented package build", err, string(output))
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.PluginLocalRepoPaths = []string{repository}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	url, err := artifacts.LocalRepositoryURL("HelloTest", repository)
	if err != nil {
		t.Fatal(err)
	}
	installBody, _ := json.Marshal(map[string]string{"plugin_id": "HelloTest", "repo_url": url})
	install := func() {
		t.Helper()
		response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(installBody), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	configure := func(id, message string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"enabled": true, "message": message})
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	check := func(id string, count int, message string) {
		t.Helper()
		response := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+id+"/status", "", cookie, "")
		var body struct {
			Success bool
			Result  struct {
				Count   int
				Message string
			}
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.Success || body.Result.Count != count || body.Result.Message != message {
			t.Fatal("persisted example state", id, response.Code, response.Body.String())
		}
	}
	run := func(id string) {
		t.Helper()
		response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+id+"/run", "{}", cookie, "")
		if response.Code != 200 {
			t.Fatal("example run", response.Code, response.Body.String())
		}
	}
	install()
	check("HelloTest", 0, "")
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/HelloTest/run", "{}", cookie, "")
	if response.Code != 409 {
		t.Fatal("disabled example must not execute", response.Code, response.Body.String())
	}
	configure("HelloTest", "source message")
	run("HelloTest")
	run("HelloTest")
	check("HelloTest", 2, "source message")
	response = callTest(t, a.Handler(), "POST", "/api/plugins/HelloTest/clone", `{"suffix":"Copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	configure("HelloTestcopy", "clone message")
	run("HelloTestcopy")
	check("HelloTestcopy", 1, "clone message")
	check("HelloTest", 2, "source message")
	page := callTest(t, a.Handler(), "GET", "/api/plugins/HelloTestcopy/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "plugin/HelloTestcopy/run") {
		t.Fatal("clone page must address its own host identity", page.Body.String())
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if err := a.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("HelloTest", 2, "source message")
	check("HelloTestcopy", 1, "clone message")
	response = callTest(t, a.Handler(), "DELETE", "/api/plugins/HelloTest", "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "GET", "/api/v1/plugin/HelloTest/status", "", cookie, "")
	if response.Code != 404 {
		t.Fatal("uninstall removes API", response.Code)
	}
	install()
	check("HelloTest", 2, "source message") // Source uninstall preserves config/KV.
	response = callTest(t, a.Handler(), "POST", "/api/plugins/HelloTest/reset", "{}", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	check("HelloTest", 0, "")
	check("HelloTestcopy", 1, "clone message")
	for _, id := range []string{"HelloTestcopy", "HelloTest"} {
		response = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
}
