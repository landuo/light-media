package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

const ownedStandardFixtureID = "OwnedStandardFixture"

func ownedStandardSDKFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_STANDARD_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_STANDARD_FIXTURE_BINARY must name the actual owned compiled Base SDK worker")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatalf("actual Base manifest %v", err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != ownedStandardFixtureID || manifest.Version != "1.0" || !slices.Equal(manifest.Capabilities, plugins.BaseMethods()) {
		t.Fatalf("wrong executable/capabilities %+v", manifest)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
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
		case "/owned-standard-fixture":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/owned-standard-fixture"
	a := newTestApp(t)
	settings := a.Settings()
	noShare, noServer := false, ""
	settings.PluginStatisticShare, settings.PluginServerURL = &noShare, &noServer
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(context.Background(), ownedStandardFixtureID)
	if err != nil || !in.Installed || in.Version != "1.0" {
		t.Fatalf("actual Base installation %+v %v", in, err)
	}
	value, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok {
		t.Fatal("real Base worker not loaded")
	}
	object := value.(*originalPluginInstance)
	if !object.external {
		t.Fatal("Base fixture substituted builtin")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("fixture acquisition %d %d %d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
	}
	return a, cookie, object
}

type ownedStandardSnapshot struct {
	Nonce       int                        `json:"nonce"`
	InitCount   int                        `json:"init_count"`
	InitInput   json.RawMessage            `json:"init_input"`
	Config      json.RawMessage            `json:"config"`
	Calls       map[string]int             `json:"calls"`
	LastPayload map[string]json.RawMessage `json:"lastpayload"`
	StaleDenied bool                       `json:"stale_denied"`
}

func ownedStandardSnapshotRead(t *testing.T, p *originalPluginInstance) ownedStandardSnapshot {
	t.Helper()
	var s ownedStandardSnapshot
	if err := p.readSession(context.Background(), "snapshot", &s); err != nil {
		t.Fatal(err)
	}
	if !s.StaleDenied {
		t.Fatal("completed Base callback retained authority")
	}
	return s
}

func TestOriginalStandardDirectModuleEndToEnd(t *testing.T) {
	ctx := context.Background()
	a, cookie, object := ownedStandardSDKFixture(t)
	first := ownedStandardSnapshotRead(t, object)
	if first.InitCount != 1 || string(first.InitInput) != "{}" || string(first.Config) != "null" {
		t.Fatal("manager {} and Base None were conflated", first.InitCount, string(first.InitInput), string(first.Config))
	}
	if _, err := a.pluginStore.Config(ctx, ownedStandardFixtureID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("start wrote default config", err)
	}
	put := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":true,"key":"source"}`, cookie, "")
	assertOriginalRegistrySuccess(t, put.Code, put.Body.Bytes())
	a.wg.Wait()
	loaded, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok || loaded != object {
		t.Fatal("save replaced actual instance")
	}
	saved := ownedStandardSnapshotRead(t, object)
	if saved.InitCount != 2 || saved.Nonce != first.Nonce {
		t.Fatal("save did not reuse same persistent object")
	}
	for _, method := range []string{"get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav"} {
		var raw json.RawMessage
		if err := object.readSession(ctx, method, &raw); err != nil || !json.Valid(raw) {
			t.Fatal("standard method", method, err)
		}
	}
	for _, view := range []string{"form", "page", "dashboard"} {
		response := callTest(t, a.Handler(), "GET", "/api/plugins/"+ownedStandardFixtureID+"/"+view, "", cookie, "")
		if response.Code != http.StatusOK {
			t.Fatal("actual standard HTTP view", view, response.Code, response.Body.String())
		}
	}
	manual := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"OwnedStandardFixture","service_id":"owned_standard_tick"}`, cookie, "")
	if manual.Code != http.StatusOK {
		t.Fatal("registered manual service", manual.Code, manual.Body.String())
	}
	a.wg.Wait()
	// Observe the registered trigger's real deadline; never advance or edit it.
	a.pluginOriginalDirectJobs.mu.Lock()
	job := a.pluginOriginalDirectJobs.jobs[originalServiceKey{ownedStandardFixtureID, "owned_standard_tick"}]
	if job == nil {
		a.pluginOriginalDirectJobs.mu.Unlock()
		t.Fatal("actual interval registration disappeared")
	}
	due := job.due
	a.pluginOriginalDirectJobs.mu.Unlock()
	if due.IsZero() {
		t.Fatal("actual interval registration has no deadline")
	}
	if delay := time.Until(due); delay > 0 {
		time.Sleep(delay)
	}
	a.dispatchOriginalPluginJobs(ctx, time.Now())
	a.wg.Wait()
	_, instance, found := a.lookupOriginalPluginCommand("/owned_standard")
	if !found || instance != ownedStandardFixtureID {
		t.Fatal("actual command not registered")
	}
	commandResponse := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/owned_standard owned argument"}`, cookie, "")
	if commandResponse.Code != http.StatusOK {
		t.Fatal("authenticated original Web command", commandResponse.Code, commandResponse.Body.String())
	}
	a.broadcastOriginalPluginEvent(ctx, "owned.standard", json.RawMessage(`{"marker":"event"}`))
	a.wg.Wait()
	settings := a.Settings()
	settings.APIToken = "owned-standard-only-key"
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	api := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo?apikey="+settings.APIToken, "", nil, "")
	if api.Code != http.StatusOK {
		t.Fatal("actual direct http method", api.Code, api.Body.String())
	}
	executed := ownedStandardSnapshotRead(t, object)
	if executed.InitCount != 2 || executed.Nonce != first.Nonce || executed.Calls["tick"] < 2 || executed.Calls["command"] != 1 || executed.Calls["event"] != 2 || executed.Calls["http"] != 1 {
		t.Fatal("handlers did not execute on retained instance", executed.Calls)
	}
	var commandData map[string]any
	if err := json.Unmarshal(executed.LastPayload["command"], &commandData); err != nil || commandData["channel"] != "Web" || commandData["source"] != "admin" || commandData["user"] != "admin" || commandData["arg_str"] != "owned argument" || commandData["tag"] != "command" {
		t.Fatal("actual command event context", commandData, err)
	}
	if string(executed.LastPayload["tick"]) != `{"tag":"service"}` {
		t.Fatal("service input was wrapped", string(executed.LastPayload["tick"]))
	}
	cloneResponse := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloneResponse.Code, cloneResponse.Body.Bytes())
	cloneID := ownedStandardFixtureID + "twin"
	cloneValue, ok := a.pluginRunningInstances.Load(cloneID)
	if !ok || cloneValue == object {
		t.Fatal("clone did not create independent actual object")
	}
	clone := cloneValue.(*originalPluginInstance)
	cloneSnapshot := ownedStandardSnapshotRead(t, clone)
	if cloneSnapshot.InitCount != 1 || cloneSnapshot.Nonce == first.Nonce || cloneSnapshot.Calls["http"] != 0 {
		t.Fatal("clone inherited live member execution")
	}
	changed := callTest(t, a.Handler(), "PUT", "/api/plugins/"+cloneID, `{"enabled":false,"key":"sibling"}`, cookie, "")
	assertOriginalRegistrySuccess(t, changed.Code, changed.Body.Bytes())
	sourceData, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "shared")
	if err != nil || string(sourceData.Value) != `"source"` {
		t.Fatal("sibling changed source Base namespace", err, string(sourceData.Value))
	}
	siblingData, err := a.pluginStore.Data(ctx, cloneID, "shared")
	if err != nil || string(siblingData.Value) != `"sibling"` {
		t.Fatal("sibling Base namespace missing", err)
	}
	if err := a.stopOriginalPlugin(ctx, cloneID); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"owned_close", "owned_stop_service", "owned_stop"} {
		if _, err := a.pluginStore.Data(ctx, cloneID, key); err != nil {
			t.Fatal("actual stop chain missing", key, err)
		}
	}
	if current, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID); !ok || current != object {
		t.Fatal("sibling stop changed source")
	}
	stopData, err := a.pluginStore.Data(ctx, cloneID, "owned_stop")
	var stopped struct {
		Order []string `json:"order"`
	}
	if err != nil || json.Unmarshal(stopData.Value, &stopped) != nil || !slices.Equal(stopped.Order, []string{"close", "stop_service", "stop"}) {
		t.Fatal("source close/stop_service/transport order", err, stopped.Order)
	}
	reset := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/reset", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, reset.Code, reset.Body.Bytes())
	resetValue, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok || resetValue == object {
		t.Fatal("reset did not create a fresh actual object")
	}
	resetSnapshot := ownedStandardSnapshotRead(t, resetValue.(*originalPluginInstance))
	if resetSnapshot.InitCount != 1 || string(resetSnapshot.InitInput) != "{}" || string(resetSnapshot.Config) != "null" {
		t.Fatal("reset did not restore manager/Base absence separation")
	}
	if _, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "shared"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset retained own Base data", err)
	}
	uninstall := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/uninstall", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, uninstall.Code, uninstall.Body.Bytes())
	if _, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID); ok {
		t.Fatal("uninstall retained actual object")
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("standard module entered retired queue", runs, err)
	}
}
