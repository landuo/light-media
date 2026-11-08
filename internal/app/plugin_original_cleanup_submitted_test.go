package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func ownedCleanupAppFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_CLEANUP_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_CLEANUP_FIXTURE_BINARY must name actual owned SDK worker")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != "OwnedCleanup" || !slices.Contains(manifest.Capabilities, plugins.CleanupSubmitCapability) {
		t.Fatal("wrong actual fixture manifest")
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-resources/v1"}}}}}
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
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("actual artifact not loaded")
	}
	p := value.(*originalPluginInstance)
	if !p.external || p.hostContract != pluginResourcesSDKContract {
		t.Fatal("substituted builtin or wrong contract")
	}
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if client == nil || !client.SupportsCleanupContinuations() {
		t.Fatal("real fixture did not negotiate cleanup")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return a, cookie, p
}
func ownedCleanupConfigure(t *testing.T, p *originalPluginInstance, n int) {
	t.Helper()
	raw, _ := json.Marshal(map[string]int{"count": n})
	if _, err := p.callOriginalMethod(context.Background(), "configure", raw); err != nil {
		t.Fatal(err)
	}
}
func ownedCleanupRows(t *testing.T, a *App, instance string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		row, err := a.pluginStore.Data(context.Background(), instance, fmt.Sprintf("effect_%04d", i))
		var v struct{ N, PID int }
		if err != nil || json.Unmarshal(row.Value, &v) != nil || v.N != i || v.PID == 0 {
			t.Fatal("root returned before committed SQLite effect", instance, i, err)
		}
	}
	if _, err := a.pluginStore.Data(context.Background(), instance, fmt.Sprintf("effect_%04d", n+1)); err == nil {
		t.Fatal("extra/replayed side effect")
	}
}
func TestOriginalCleanupContinuationActualInstalledStopCloneAndCold(t *testing.T) {
	a, cookie, p := ownedCleanupAppFixture(t)
	// Neither a payload bit nor a lifecycle-shaped ordinary call grants cleanup.
	for _, method := range []string{"configure", "stop_service"} {
		out, err := p.callOriginalMethod(context.Background(), method, json.RawMessage(`{"probe":true,"cleanup_root":"forged"}`))
		var v struct{ Denied, Cleanup bool }
		if err != nil || json.Unmarshal(out.Result, &v) != nil || !v.Denied || v.Cleanup {
			t.Fatal("fake cleanup acquired authority", method, string(out.Result), err)
		}
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedCleanup/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("OwnedCleanuptwin")
	if !ok {
		t.Fatal("actual clone absent")
	}
	clone := value.(*originalPluginInstance)
	ownedCleanupConfigure(t, p, 180)
	ownedCleanupConfigure(t, clone, 130)
	if err := a.stopOriginalPlugin(context.Background(), p.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	ownedCleanupRows(t, a, p.installation.InstanceID, 180)
	if _, err := a.pluginStore.Data(context.Background(), clone.installation.InstanceID, "effect_0001"); err == nil {
		t.Fatal("source stop executed clone queue")
	}
	if err := a.stopOriginalPlugin(context.Background(), clone.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	ownedCleanupRows(t, a, clone.installation.InstanceID, 130)
	loaded, err := a.reloadOriginalPlugin(context.Background(), p.installation.InstanceID)
	if err != nil || loaded == nil {
		t.Fatal("cold object reload", err)
	}
	if err := a.stopOriginalPlugin(context.Background(), p.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	ownedCleanupRows(t, a, p.installation.InstanceID, 180)
	ownedConcurrentAppNoRuns(t, a)
	t.Log("actual local-index artifact -> App detached cleanup -> independent SDK worker -> SQLite180+130 effects; clone separate, cold volatile queue absent; not cmd/server main")
}
func TestOriginalCleanupContinuationActualGuardUnknownAndCancellation(t *testing.T) {
	for _, mode := range []string{"guard", "unknown", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a, _, p := ownedCleanupAppFixture(t)
			ownedCleanupConfigure(t, p, 100)
			p.sessionMu.Lock()
			client := p.session
			p.sessionMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			phaseCtx := context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
			original, revoke := p.originalBaseSessionHost(phaseCtx, "stop", nil)
			defer revoke()
			var attempts atomic.Int32
			entered := make(chan struct{})

			var wrapped plugins.HostHandler
			wrapped = func(callCtx context.Context, id plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method == plugins.BaseSaveData {
					attempts.Add(1)
					if mode == "guard" {
						return nil, errors.New("owned transient current scope revoked")
					}
					out, err := original(callCtx, id, method, raw)
					if err != nil {
						return nil, err
					}
					if mode == "cancel" {
						close(entered)
						<-callCtx.Done()
						return nil, callCtx.Err()
					}
					return out, errors.New("owned ACK lost after actual SQLite commit")
				}
				// Every successor receives a fresh real App closure. Interpose its finite
				// host resource only to inject guard/unknown/cancel at the first actual write.
				if method == plugins.CleanupSubmitCapability {
					var in struct {
						Method  string
						Payload json.RawMessage
					}
					if json.Unmarshal(raw, &in) != nil {
						return nil, errors.New("bad owned payload")
					}
					root, err := client.CleanupContext(callCtx)
					if err != nil {
						return nil, err
					}
					scope := originalBaseCallScope{object: p, phase: "stop", installation: p.installation, validate: func(c context.Context) error { return c.Err() }}
					if _, err := a.originalCleanupSubmitResource(context.Background(), scope, raw); err == nil {
						return nil, errors.New("unbound context acquired real App cleanup")
					}
					// For injection the same actual source/App binding is retained. The normal
					// App dispatcher path is independently exercised by the180+130 positive.
					_ = root
					pending, err := client.StartCleanupContinuationWithAdmission(callCtx, in.Method, in.Payload, wrapped)
					if err != nil {
						return nil, err
					}
					go func() { _, _ = pending.Wait() }()
					return json.RawMessage(`{"accepted":true}`), nil
				}
				return original(callCtx, id, method, raw)
			}
			if err := client.BeginShutdown(ctx); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := client.CallCleanup(phaseCtx, "stop_service", json.RawMessage(`{}`), wrapped)
				done <- err
			}()
			if mode == "cancel" {
				select {
				case <-entered:
					cancel()
				case <-ctx.Done():
					t.Fatal("real write not entered")
				}
			}
			if err := <-done; err == nil {
				t.Fatal("failed/unknown cleanup became success")
			}
			if attempts.Load() != 1 {
				t.Fatal("failed selected write replayed", attempts.Load())
			}
			_, err := a.pluginStore.Data(context.Background(), p.installation.InstanceID, "effect_0001")
			if mode == "guard" && err == nil {
				t.Fatal("guard wrote")
			}
			if mode != "guard" && err != nil {
				t.Fatal("confirmed write lost", err)
			}
			if _, err := a.pluginStore.Data(context.Background(), p.installation.InstanceID, "effect_0002"); err == nil {
				t.Fatal("continued after failed write")
			}
		})
	}
}
