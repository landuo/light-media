package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugininput"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func installOwnedInputFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	path := os.Getenv("OWNED_INPUT_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("OWNED_INPUT_FIXTURE_BINARY must name the compiled actual SDK worker")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, path, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedInput" {
		t.Fatal("wrong input fixture manifest")
	}
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(binary)), HostContract: pluginResourcesSDKContract}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(binary)
		default:
			t.Error("unexpected owned request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.APIToken = "owned-input-synthetic-api-key"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.closeOriginalSessions)
	body, _ := json.Marshal(map[string]string{"plugin_id": "OwnedInput", "repo_url": server.URL + "/index"})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	return a, cookie
}
func ownedInputRequest(t *testing.T, a *App, cookie *http.Cookie, instance, operation, input string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"operation":"` + operation + `","input":` + input + `}`
	return ownedInputAPI(t, a, "POST", "/api/v1/plugin/"+instance+"/input", body)
}
func ownedInputAPI(t *testing.T, a *App, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-KEY", a.Settings().APIToken)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func ownedInputCreate(t *testing.T, a *App, cookie *http.Cookie, instance string) plugins.PendingInputInteraction {
	t.Helper()
	r := ownedInputRequest(t, a, cookie, instance, "create", `{"user_id":"admin","channel":"Web","source":"admin","payload":{"large":9007199254740993},"prompt_id":"owned"}`)
	var result plugins.PendingInputInteraction
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || len(result.RequestID) != 12 || result.PluginID != instance {
		t.Fatal("SDK create", r.Code, r.Body.String())
	}
	return result
}
func ownedInputEvents(t *testing.T, a *App, cookie *http.Cookie, instance string) []json.RawMessage {
	t.Helper()
	a.wg.Wait()
	r := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+instance+"/events", "")
	var result struct {
		PID    int               `json:"pid"`
		Events []json.RawMessage `json:"events"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.PID == 0 || result.PID == os.Getpid() {
		t.Fatal("actual worker events", r.Code, r.Body.String())
	}
	return result.Events
}
func TestOriginalInputActualSDKWebConsumptionAndCancellation(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	first := ownedInputCreate(t, a, cookie, "OwnedInput")
	second := ownedInputCreate(t, a, cookie, "OwnedInput")
	if first.RequestID == second.RequestID {
		t.Fatal("replacement id")
	}
	r := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/owned keep literal","user":"forged","channel":"Telegram"}`, cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	events := ownedInputEvents(t, a, cookie, "OwnedInput")
	if len(events) != 1 || !strings.Contains(string(events[0]), `"input_text":"/owned keep literal"`) || !strings.Contains(string(events[0]), second.RequestID) || strings.Contains(string(events[0]), "forged") || strings.Contains(string(events[0]), "__mp_target") || !strings.Contains(string(events[0]), "9007199254740993") {
		t.Fatal("input did not precede command with trusted context", string(events[0]))
	}
	r = callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/owned"}`, cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	events = ownedInputEvents(t, a, cookie, "OwnedInput")
	if len(events) != 2 || !strings.Contains(string(events[1]), `"type":"plugin.action"`) {
		t.Fatal("consumed request remained active", len(events))
	}
	ownedInputCreate(t, a, cookie, "OwnedInput")
	r = callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":" QuIt "}`, cookie, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "已取消插件输入") {
		t.Fatal("cancel feedback", r.Code, r.Body.String())
	}
	events = ownedInputEvents(t, a, cookie, "OwnedInput")
	if len(events) != 3 || !strings.Contains(string(events[2]), `"cancelled":true`) {
		t.Fatal("cancel event", len(events))
	}
}
func TestOriginalInputActualSDKCloneScopeAndStaleCapability(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInputtwin", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	request := ownedInputCreate(t, a, cookie, "OwnedInputtwin")
	query := `{"user_id":"admin","channel":"Web","source":"admin"}`
	r = ownedInputRequest(t, a, cookie, "OwnedInput", "pop", query)
	if r.Code != 200 || strings.TrimSpace(r.Body.String()) != "null" {
		t.Fatal("foreign read/consume", r.Code, r.Body.String())
	}
	r = ownedInputRequest(t, a, cookie, "OwnedInput", "remove", `{"request_id":"`+request.RequestID+`"}`)
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	r = callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"owned answer"}`, cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	if events := ownedInputEvents(t, a, cookie, "OwnedInput"); len(events) != 0 {
		t.Fatal("clone event reached source")
	}
	if events := ownedInputEvents(t, a, cookie, "OwnedInputtwin"); len(events) != 1 || !strings.Contains(string(events[0]), `"plugin_id":"OwnedInputtwin"`) {
		t.Fatal("clone event missing", len(events))
	}
	r = ownedInputAPI(t, a, "POST", "/api/v1/plugin/OwnedInput/input", `{"operation":"create","stale":true,"input":`+query+`}`)
	if r.Code != 403 {
		t.Fatal("closed host scope reused", r.Code, r.Body.String())
	}
	r = ownedInputRequest(t, a, cookie, "OwnedInput", "create", `{"user_id":"admin","plugin_id":"OwnedInputtwin"}`)
	if r.Code != 403 {
		t.Fatal("forged target accepted", r.Code)
	}
}
func TestOriginalInputActualSDKMediaExpiryAndShutdown(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	request := ownedInputCreate(t, a, cookie, "OwnedInput")
	for _, sample := range []struct {
		body   string
		status int
	}{{`{"text":"answer","images":["owned-no-network"]}`, 200}, {`{"text":"CALLBACK:owned"}`, 501}, {`{"text":"  "}`, 501}} {
		r := callTest(t, a.Handler(), "POST", "/api/message/web", sample.body, cookie, "")
		if r.Code != sample.status {
			t.Fatal("unimplemented media/callback route swallowed", r.Code, r.Body.String())
		}
	}
	r := ownedInputRequest(t, a, cookie, "OwnedInput", "get", `{"user_id":"admin","channel":"Web","source":"admin"}`)
	if r.Code != 200 || !strings.Contains(r.Body.String(), request.RequestID) {
		t.Fatal("media consumed input")
	}
	// Shift only the owned in-memory fixture clock; no production or sleep.
	request.TimeoutSeconds = 1
	_, err := a.pluginOriginalInput.Create(request, time.Now().Add(-2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	r = callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/owned"}`, cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	events := ownedInputEvents(t, a, cookie, "OwnedInput")
	if len(events) != 2 {
		t.Fatal("expiry must notify and allow slash command", len(events))
	}
	foundExpired, foundCommand := false, false
	for _, event := range events {
		foundExpired = foundExpired || strings.Contains(string(event), `"expired":true`)
		foundCommand = foundCommand || strings.Contains(string(event), `"type":"plugin.action"`)
	}
	if !foundExpired || !foundCommand {
		t.Fatal("expiry/command events missing")
	}
	ownedInputCreate(t, a, cookie, "OwnedInput")
	a.Close()
	web, admin := "Web", "admin"
	if got := a.pluginOriginalInput.Lookup(plugininput.NewKey(admin, &web, &admin, nil), "OwnedInput", false, time.Now()); got != nil {
		t.Fatal("input survives app close")
	}
}
