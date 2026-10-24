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
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

const ownedCommandFixtureID = "OwnedCommandIngressFixture"

func ownedCommandSDKFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_COMMAND_INGRESS_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_COMMAND_INGRESS_FIXTURE_BINARY must name the actual compiled owned Base SDK worker")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil || manifest.PluginID != ownedCommandFixtureID || manifest.Version != "1.0" || !slices.Equal(manifest.Capabilities, plugins.BaseMethods()) || slices.Contains(manifest.Methods, "not_a_method") {
		t.Fatal("wrong actual command artifact", manifest, err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-base/v1"}}}}}
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(ownedCommandFixtureID)
	if !ok || !value.(*originalPluginInstance).external {
		t.Fatal("real external command worker not loaded")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
		t.Fatal("fixture acquisition escaped its owned boundary", indexHits.Load(), artifactHits.Load(), unexpected.Load())
	}
	return a, cookie, value.(*originalPluginInstance)
}

type ownedCommandSnapshot struct {
	Nonce   int               `json:"nonce"`
	Calls   map[string]int    `json:"calls"`
	Events  []json.RawMessage `json:"events"`
	Ingress []json.RawMessage `json:"ingress"`
}

func ownedCommandRead(t *testing.T, object *originalPluginInstance) ownedCommandSnapshot {
	t.Helper()
	var result ownedCommandSnapshot
	if err := object.readSession(context.Background(), "snapshot", &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestOriginalWebCommandActualSDKIngress(t *testing.T) {
	a, cookie, object := ownedCommandSDKFixture(t)
	h := a.Handler()
	first := ownedCommandRead(t, object)
	setEnabled := func(enabled bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]bool{"enabled": enabled})
		response := callTest(t, h, "PUT", "/api/plugins/"+ownedCommandFixtureID, string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
	}
	setEnabled(true)
	for _, test := range []struct {
		name, path, body, origin, fetchSite string
		cookie                              *http.Cookie
		status                              int
	}{
		{"unauthenticated", "/api/v1/message/web", `{"text":"/owned_command"}`, "", "", nil, 401},
		{"forged session", "/api/v1/message/web", `{"text":"/owned_command"}`, "", "", &http.Cookie{Name: cookie.Name, Value: cookie.Value + "changed"}, 401},
		{"query key is not Web authentication", "/api/v1/message/web?apikey=owned-only&token=owned-only", `{"text":"/owned_command"}`, "", "", nil, 401},
		{"cross-origin", "/api/v1/message/web", `{"text":"/owned_command"}`, "https://elsewhere.invalid", "", cookie, 403},
		{"cross-site", "/api/message/web", `{"text":"/owned_command"}`, "", "cross-site", cookie, 403},
		{"non-command not fabricated", "/api/message/web", `{"text":"hello"}`, "", "", cookie, 501},
		{"AI branch not fabricated", "/api/message/web", `{"text":"/AI question"}`, "", "", cookie, 501},
		{"preset not fabricated", "/api/message/web", `{"text":"/restart"}`, "", "", cookie, 501},
		{"arbitrary event not accepted", "/api/message/web", `{"event":"owned.command","data":{"action":"not_a_method"}}`, "", "", cookie, 501},
		{"invalid text", "/api/message/web", `{"text":{"cmd":"/owned_command"}}`, "", "", cookie, 400},
		{"multiple bodies", "/api/message/web", `{"text":"/owned_command"} {}`, "", "", cookie, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			if test.cookie != nil {
				request.AddCookie(test.cookie)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
	a.wg.Wait()
	if got := ownedCommandRead(t, object); len(got.Events) != 0 || len(got.Ingress) != 0 || got.Calls["not_a_method"] != 0 {
		t.Fatal("rejected ingress reached an actual handler", got)
	}
	requestBody := `{"text":"/owned_command \u001c甲 \u0085乙\n丙","user":"forged","source":"forged","channel":"飞书","event":"forged","data":{"action":"forged"}}`
	response := callTest(t, h, "POST", "/api/v1/message/web?user=forged&source=forged", requestBody, cookie, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"accepted":true`) {
		t.Fatal("real Web command request failed", response.Code, response.Body.String())
	}
	a.wg.Wait()
	got := ownedCommandRead(t, object)
	if got.Nonce != first.Nonce || got.Calls["event"] != 2 || got.Calls["not_a_method"] != 0 || len(got.Events) != 1 || len(got.Ingress) != 1 {
		t.Fatal("command did not reach actual retained event object", got)
	}
	var event struct {
		Type string                     `json:"type"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(got.Events[0], &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "owned.command" || string(event.Data["action"]) != `"not_a_method"` || string(event.Data["channel"]) != `"Web"` || string(event.Data["source"]) != `"admin"` || string(event.Data["user"]) != `"admin"` || string(event.Data["arg_str"]) != `"甲 乙 丙"` || string(event.Data["big"]) != "9007199254740993" || string(event.Data["nested"]) != `{"values":["kept",false,null]}` {
		t.Fatal("declared event/data or trusted identity changed", string(got.Events[0]))
	}
	var ingress struct {
		Type string                     `json:"type"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(got.Ingress[0], &ingress); err != nil || ingress.Type != "command.excute" || string(ingress.Data["channel"]) != `"Web"` || string(ingress.Data["source"]) != `"admin"` || string(ingress.Data["user"]) != `"admin"` || string(ingress.Data["processing_status"]) != "null" || !strings.Contains(string(ingress.Data["cmd"]), "/owned_command") {
		t.Fatal("actual CommandExcute event was missing or forged", string(got.Ingress[0]), err)
	}
	stored, err := a.pluginStore.Data(context.Background(), ownedCommandFixtureID, "last_command_event")
	if err != nil || !strings.Contains(string(stored.Value), `"arg_str":"甲 乙 丙"`) {
		t.Fatal("actual SDK Base side effect missing", err, string(stored.Value))
	}
	a.pluginOriginalCommandsMu.Lock()
	declared, _ := json.Marshal(a.pluginOriginalCommands["/owned_command"]["data"])
	a.pluginOriginalCommandsMu.Unlock()
	for _, forbidden := range []string{`"arg_str"`, `"channel"`, `"source"`, `"user"`} {
		if strings.Contains(string(declared), forbidden) {
			t.Fatal("declaration was mutated", string(declared))
		}
	}
	for _, body := range []string{
		`{"text":"/owned_noaction"}`,
		`{"text":"/owned_nodata"}`,
		`{"text":"/owned_hidden"}`,
		`{"text":" /NOAI： /owned_command second"}`,
	} {
		response := callTest(t, h, "POST", "/api/message/web", body, cookie, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"accepted":true`) {
			t.Fatal(response.Code, response.Body.String())
		}
		a.wg.Wait()
	}
	got = ownedCommandRead(t, object)
	if len(got.Events) != 5 || len(got.Ingress) != 5 || got.Calls["event"] != 10 {
		t.Fatal("non-action/None/hidden/escaped command lost", got)
	}
	if !strings.Contains(string(got.Events[1]), `"arg_str":"preset"`) {
		t.Fatal("empty args erased declared arg_str", string(got.Events[1]))
	}
	query := callTest(t, h, "POST", "/api/v1/message/web?text=%2Fmissing&text=%2Fowned_command+query+argument", "", cookie, "")
	if query.Code != 200 {
		t.Fatal(query.Code, query.Body.String())
	}
	a.wg.Wait()
	if got := ownedCommandRead(t, object); len(got.Events) != 6 || !strings.Contains(string(got.Events[5]), `"arg_str":"query argument"`) {
		t.Fatal("source query text path missing", got)
	}
	for _, command := range []string{"/not_registered", "/OWNED_COMMAND"} {
		body, _ := json.Marshal(map[string]string{"text": command})
		unknown := callTest(t, h, "POST", "/api/message/web", string(body), cookie, "")
		if unknown.Code != 200 || !strings.Contains(unknown.Body.String(), `"accepted":false`) {
			t.Fatal("unknown command did not no-op", unknown.Code, unknown.Body.String())
		}
	}
	setEnabled(false)
	disabled := callTest(t, h, "POST", "/api/message/web", `{"text":"/owned_command"}`, cookie, "")
	if disabled.Code != 200 || !strings.Contains(disabled.Body.String(), `"accepted":false`) {
		t.Fatal(disabled.Code, disabled.Body.String())
	}
	a.wg.Wait()
	if got := ownedCommandRead(t, object); len(got.Events) != 6 {
		t.Fatal("disabled/unknown commands executed", got)
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("command restored retired queue", runs, err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=?`, time.Now().Add(-time.Second).Unix(), tokenHash(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	expired := callTest(t, h, "POST", "/api/message/web", `{"text":"/owned_command"}`, cookie, "")
	if expired.Code != 401 {
		t.Fatal("expired session accepted", expired.Code)
	}
}

func TestOriginalWebCommandTextSourceRules(t *testing.T) {
	for _, test := range []struct {
		input, text string
		command     bool
	}{
		{"/owned x", "/owned x", true},
		{" /owned x", " /owned x", false},
		{" /NOAI： /owned x \n", "/owned x", true},
		{"/noai\u001c/owned", "/owned", true},
		{"/noai", "", false},
		{"/noaix", "/noaix", true},
		{"/AI", "/AI", false},
		{"/airport", "/airport", false},
	} {
		text, command := originalWebCommandText(test.input)
		if text != test.text || command != test.command {
			t.Fatal(test, text, command)
		}
	}
}
