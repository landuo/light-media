package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

type orderObservation struct {
	Label  string          `json:"label"`
	Method string          `json:"method"`
	PID    int             `json:"pid"`
	Input  json.RawMessage `json:"input"`
}

func readOrderTrace(t *testing.T, path string) []orderObservation {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows := []orderObservation{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var row orderObservation
		if err = json.Unmarshal(scan.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err = scan.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}
func assertOrderTrace(t *testing.T, path string, want []string) {
	t.Helper()
	got := []string{}
	for _, row := range readOrderTrace(t, path) {
		got = append(got, row.Label+":"+row.Method)
		if row.PID <= 0 {
			t.Fatal("no real worker pid")
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("actual process invocation order got %v want %v", got, want)
	}
}
func installOrderWorker(t *testing.T, a *App, cookie *http.Cookie, binary string) plugins.Manifest {
	t.Helper()
	if binary == "" {
		t.Fatal("OWNED_ORDER_ALPHA/BETA/DEFAULT must name built actual SDK workers")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var m plugins.Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if err = m.Validate(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: m.PluginID, CurrentVersion: m.Version, Releases: []artifacts.Release{{Version: m.Version, Manifest: m, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginBaseSDKContract}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	request, _ := json.Marshal(map[string]any{"plugin_id": m.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(request), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	if _, ok := a.pluginRunningInstances.Load(m.PluginID); !ok {
		t.Fatalf("actual worker %s not loaded", m.PluginID)
	}
	return m
}
func TestOriginalManifestStartupAndResetActualWorkers(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	alpha := installOrderWorker(t, a, cookie, os.Getenv("OWNED_ORDER_ALPHA"))
	beta := installOrderWorker(t, a, cookie, os.Getenv("OWNED_ORDER_BETA"))
	def := installOrderWorker(t, a, cookie, os.Getenv("OWNED_ORDER_DEFAULT"))
	if alpha.PluginOrder != 20 || beta.PluginOrder != -10 || def.PluginOrder != 0 {
		t.Fatal("wrong actual manifest variants")
	}
	trace := filepath.Join(t.TempDir(), "actual-order.jsonl")
	configure := func(id, label string, enabled, failInit bool, failMethod string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": enabled, "trace_path": trace, "label": label, "fail_init": failInit, "fail_method": failMethod})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		if !failInit {
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		} else if r.Code < 400 {
			t.Fatal("expected actual failed initializer")
		}
		a.wg.Wait()
	}
	clear := func() {
		t.Helper()
		if err := os.WriteFile(trace, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	configure(alpha.PluginID, "alpha", true, false, "")
	configure(beta.PluginID, "beta", true, false, "")
	configure(def.PluginID, "default", true, false, "")
	clone := alpha.PluginID + "isolated"
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+alpha.PluginID+"/clone", `{"suffix":"Isolated"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	configure(clone, "clone", true, false, "")
	clear()
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	assertOrderTrace(t, trace, []string{"beta:init", "default:init", "alpha:init", "clone:init"})
	sourceObject := originalBaseSDKLoaded(t, a, alpha.PluginID)
	cloneObject := originalBaseSDKLoaded(t, a, clone)
	if sourceObject == cloneObject || cloneObject.manifest.PluginOrder != 20 {
		t.Fatal("clone identity/order not inherited")
	}
	clear()
	if !a.sendOriginalPluginDataReset(ctx, alpha.PluginID) {
		t.Fatal("actual registered chain returned false")
	}
	first := []string{"alpha:reset_first", "alpha:reset_second", "clone:reset_first", "clone:reset_second", "default:event", "beta:reset_first", "beta:reset_second"}
	assertOrderTrace(t, trace, first)
	for _, row := range readOrderTrace(t, trace) {
		var e struct {
			Type string                       `json:"type"`
			Data originalPluginDataResetEvent `json:"data"`
		}
		if err := json.Unmarshal(row.Input, &e); err != nil || e.Type != "plugin.data.reset" || e.Data.PluginID != alpha.PluginID || !e.Data.ResetConfig || !e.Data.ResetData {
			t.Fatalf("actual reset envelope %+v %v", e, err)
		}
	}
	// Same-priority re-registration moves the replacement behind existing clone.
	if _, err := a.reloadOriginalPlugin(ctx, alpha.PluginID); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	clear()
	a.sendOriginalPluginDataReset(ctx, clone)
	assertOrderTrace(t, trace, []string{"clone:reset_first", "clone:reset_second", "alpha:reset_first", "alpha:reset_second", "default:event", "beta:reset_first", "beta:reset_second"})
	// Failing the first real method does not prevent this object's next method,
	// other instances, or the lower-priority subscriber from running.
	configure(alpha.PluginID, "alpha", true, false, "reset_first")
	clear()
	a.sendOriginalPluginDataReset(ctx, clone)
	assertOrderTrace(t, trace, []string{"clone:reset_first", "clone:reset_second", "alpha:reset_first", "alpha:reset_second", "default:event", "beta:reset_first", "beta:reset_second"})
	configure(beta.PluginID, "beta", false, false, "")
	clear()
	a.sendOriginalPluginDataReset(ctx, clone)
	assertOrderTrace(t, trace, []string{"clone:reset_first", "clone:reset_second", "alpha:reset_first", "alpha:reset_second", "default:event"})
	if err := a.stopOriginalPlugin(ctx, clone); err != nil {
		t.Fatal(err)
	}
	clear()
	a.sendOriginalPluginDataReset(ctx, alpha.PluginID)
	assertOrderTrace(t, trace, []string{"alpha:reset_first", "alpha:reset_second", "default:event"})
	a.pluginOriginalEventsMu.Lock()
	for _, h := range a.pluginOriginalChainHandlers["plugin.data.reset"] {
		if h.Instance == clone {
			t.Error("stopped clone handler retained")
		}
	}
	a.pluginOriginalEventsMu.Unlock()
	// Failed init is isolated during a complete manager restart; no running
	// object or reset registration is inferred from the installed flag.
	configure(beta.PluginID, "beta", true, true, "")
	clear()
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	assertOrderTrace(t, trace, []string{"beta:init", "default:init", "alpha:init", "clone:init"})
	if _, ok := a.pluginRunningInstances.Load(beta.PluginID); ok {
		t.Fatal("failed init left running object")
	}
	clear()
	a.sendOriginalPluginDataReset(ctx, alpha.PluginID)
	assertOrderTrace(t, trace, []string{"alpha:reset_first", "alpha:reset_second", "clone:reset_first", "clone:reset_second", "default:event"})
	for _, id := range []string{alpha.PluginID, beta.PluginID, def.PluginID, clone} {
		if err := a.stopOriginalPlugin(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	clear()
	if a.sendOriginalPluginDataReset(ctx, alpha.PluginID) {
		t.Fatal("empty/unloaded chain returned true")
	}
	assertOrderTrace(t, trace, nil)
}
