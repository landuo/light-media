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

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

const ownedEventTargetFixtureID = "OwnedEventTargetFixture"

func installOwnedEventTargetFixture(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	path := os.Getenv("OWNED_EVENT_TARGET_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("OWNED_EVENT_TARGET_FIXTURE_BINARY must name the actual compiled standard Base SDK worker")
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
	var m plugins.Manifest
	if json.Unmarshal(raw, &m) != nil || m.PluginID != ownedEventTargetFixtureID || m.Validate() != nil {
		t.Fatal("wrong actual SDK artifact")
	}
	digest := sha256.Sum256(binary)
	hash := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: m.PluginID, CurrentVersion: m.Version, Releases: []artifacts.Release{{Version: m.Version, Manifest: m, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hash, ExecutableSHA256: hash, Size: int64(len(binary)), HostContract: "light-media.sdk-base/v1"}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(binary)
		default:
			t.Error("unexpected artifact request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": m.PluginID, "repo_url": server.URL + "/index"})
	installed := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, installed.Code, installed.Body.Bytes())
	a.wg.Wait()
	t.Cleanup(a.closeOriginalSessions)
	return cookie
}

func ownedEventTargetConfig(t *testing.T, a *App, cookie *http.Cookie, instance string, enabled, fail, command bool, event string, data json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"enabled": enabled, "fail_event": fail, "command": command, "command_event": event, "command_data": data})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance, string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
}

func TestOriginalEventTargetActualSDKCommandsClonesAndIsolation(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventTargetFixture(t, a)
	ids := []string{ownedEventTargetFixtureID, ownedEventTargetFixtureID + "twin", ownedEventTargetFixtureID + "other"}
	for _, suffix := range []string{"Twin", "Other"} {
		body, _ := json.Marshal(map[string]string{"suffix": suffix})
		response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ids[0]+"/clone", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
	}
	pids := map[int]bool{}
	for _, id := range ids {
		ownedEventTargetConfig(t, a, cookie, id, true, false, false, "", nil)
		loaded, ok := a.pluginRunningInstances.Load(id)
		if !ok || !loaded.(*originalPluginInstance).external {
			t.Fatal("fixture/clone is not an actual loaded SDK object", id)
		}
		object := loaded.(*originalPluginInstance)
		if object.installation.PluginID != ownedEventTargetFixtureID || object.installation.InstanceID != id {
			t.Fatal("clone class identity is not independent from shared executable", id, object.installation)
		}
		var state struct {
			PID int `json:"pid"`
		}
		if err := object.readSession(context.Background(), "snapshot", &state); err != nil || state.PID == 0 || pids[state.PID] {
			t.Fatal("three actual worker processes were not independently loaded", id, state, err)
		}
		pids[state.PID] = true
	}
	counts := map[string]map[string]int{}
	for _, id := range ids {
		counts[id] = map[string]int{}
	}
	for _, test := range []struct {
		name, event, target string
		want                []string
	}{
		{"source class", "message.action", `"OwnedEventTargetFixture"`, ids[:1]},
		{"clone renamed class", "message.action", `"OwnedEventTargetFixturetwin"`, ids[1:2]},
		{"second clone", "message.action", `"OwnedEventTargetFixtureother"`, ids[2:]},
		{"unknown target", "message.action", `"UnknownOwnedTarget"`, nil},
		{"case mismatch", "message.action", `"ownedeventtargetfixture"`, nil},
		{"absent target", "message.action", "", ids},
		{"empty target", "message.action", `""`, ids},
		{"null target", "message.action", `null`, ids},
		{"false target", "message.action", `false`, ids},
		{"zero target", "message.action", `0`, ids},
		{"empty array", "message.action", `[]`, ids},
		{"empty object", "message.action", `{}`, ids},
		{"true target does not broaden", "message.action", `true`, nil},
		{"large integer target does not broaden", "message.action", `9007199254740993`, nil},
		{"nonempty array does not broaden", "message.action", `["OwnedEventTargetFixture"]`, nil},
		{"other event strips without filtering", "plugin.action", `"OwnedEventTargetFixturetwin"`, ids},
		{"unknown unsubscribed event", "owned.unsubscribed", `"OwnedEventTargetFixturetwin"`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := `{"big":9007199254740993,"nested":{"__mp_target_plugin_id":"keep","value":9007199254740995},"false":false,"null":null`
			if test.target != "" {
				data += `,"__mp_target_plugin_id":` + test.target
			}
			data += `}`
			ownedEventTargetConfig(t, a, cookie, ids[0], true, false, true, test.event, json.RawMessage(data))
			a.pluginOriginalCommandsMu.Lock()
			before, err := json.Marshal(a.pluginOriginalCommands["/owned_target"]["data"])
			a.pluginOriginalCommandsMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			response := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/owned_target argument","user":"forged","channel":"forged","source":"forged"}`, cookie, "")
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"accepted":true`) {
				t.Fatal("actual authenticated command was not accepted", response.Code, response.Body.String())
			}
			a.wg.Wait()
			a.pluginOriginalCommandsMu.Lock()
			after, err := json.Marshal(a.pluginOriginalCommands["/owned_target"]["data"])
			a.pluginOriginalCommandsMu.Unlock()
			if err != nil || string(before) != string(after) {
				t.Fatal("event filtering mutated original declaration", string(before), string(after), err)
			}
			for _, id := range test.want {
				counts[id][test.event]++
			}
			for _, id := range ids {
				counts[id]["command.excute"]++
				requireBusinessEventCount(t, a, id, "command.excute", counts[id]["command.excute"])
				events := requireBusinessEventCount(t, a, id, test.event, counts[id][test.event])
				if len(events) == 0 {
					continue
				}
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(events[len(events)-1].Data, &payload); err != nil {
					t.Fatal(err)
				}
				if _, leaked := payload["__mp_target_plugin_id"]; leaked || string(payload["big"]) != "9007199254740993" || !strings.Contains(string(payload["nested"]), `"__mp_target_plugin_id":"keep"`) || !strings.Contains(string(payload["nested"]), "9007199254740995") || string(payload["false"]) != "false" || string(payload["null"]) != "null" || string(payload["channel"]) != `"Web"` || string(payload["source"]) != `"admin"` || string(payload["user"]) != `"admin"` || string(payload["arg_str"]) != `"argument"` {
					t.Fatal("payload or actual authenticated host identity lost", id, string(events[len(events)-1].Data))
				}
			}
		})
	}
	// Disabled, failing and stopped subscribers retain their existing independent
	// behavior. A handler error cannot suppress another loaded enabled worker.
	ownedEventTargetConfig(t, a, cookie, ids[1], false, false, false, "", nil)
	ownedEventTargetConfig(t, a, cookie, ids[0], true, true, false, "", nil)
	raw := json.RawMessage(`{"__mp_target_plugin_id":"OwnedEventTargetFixturetwin","marker":"disabled"}`)
	a.broadcastOriginalPluginEvent(context.Background(), "message.action", raw)
	a.wg.Wait()
	for _, id := range ids {
		requireBusinessEventCount(t, a, id, "message.action", counts[id]["message.action"])
	}
	a.broadcastOriginalPluginEvent(context.Background(), "plugin.action", raw)
	a.wg.Wait()
	for _, id := range []string{ids[0], ids[2]} {
		counts[id]["plugin.action"]++
	}
	for _, id := range ids {
		requireBusinessEventCount(t, a, id, "plugin.action", counts[id]["plugin.action"])
	}
	if err := a.stopOriginalPlugin(context.Background(), ids[2]); err != nil {
		t.Fatal(err)
	}
	a.broadcastOriginalPluginEvent(context.Background(), "plugin.action", raw)
	a.wg.Wait()
	counts[ids[0]]["plugin.action"]++
	for _, id := range ids {
		requireBusinessEventCount(t, a, id, "plugin.action", counts[id]["plugin.action"])
	}
}
