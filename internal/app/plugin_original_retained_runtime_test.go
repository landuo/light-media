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
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Exactly two pre-existing owned programs; no hidden compile, arbitrary URL,
// external fixture or environment/proxy rewriting. Unlike their old helper,
// sharing is explicitly disabled BEFORE install or worker initialization.
func retainedRuntimeFixture(t *testing.T, submitted bool) (*App, *originalPluginInstance) {
	t.Helper()
	env, id, contract := "OWNED_APP_CONCURRENT_FIXTURE_BINARY", ownedConcurrentAppID, pluginBaseSDKContract
	if submitted {
		env, id, contract = "OWNED_SUBMITTED_FIXTURE_BINARY", ownedSubmittedAppID, pluginResourcesSDKContract
	}
	executable := os.Getenv(env)
	if executable == "" {
		t.Fatal(env + " must bind a worker rebuilt with the current SDK")
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, err := exec.CommandContext(ctx, executable, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != id {
		t.Fatal("wrong owned manifest")
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: id, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hash, ExecutableSHA256: hash, Size: int64(len(payload)), HostContract: contract}}}}}
	var indexHits, artifactHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("owned artifact non-GET")
			w.WriteHeader(502)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			t.Error("unexpected owned route")
			w.WriteHeader(502)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": id, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatal("actual owned worker absent")
	}
	object := value.(*originalPluginInstance)
	client := ownedSubmittedClient(object)
	if !object.external || object.hostContract != contract || client == nil || !client.SupportsConcurrent() || !client.SupportsTransportClose() {
		t.Fatal("actual concurrent/transport-close negotiation missing")
	}
	t.Cleanup(func() {
		if indexHits.Load() != 1 || artifactHits.Load() != 1 {
			t.Error("unexpected artifact reacquisition")
		}
	})
	return a, object
}

func retainedRuntimeAwait(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for " + what)
	}
}

func TestOriginalRetainedActualCancelledCallAndForcedClose(t *testing.T) {
	a, object := retainedRuntimeFixture(t, false)
	client := ownedSubmittedClient(object)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := object.callOriginalMethod(ctx, "event", json.RawMessage(`{"type":"owned.concurrent","data":{"token":"retained-cancel"}}`))
		result <- err
	}()
	ownedConcurrentAppWaitData(t, a, ownedConcurrentAppID, "entered_retained-cancel")
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled caller succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("caller did not return on cancellation")
	}
	life := object.originalRetainedState()
	life.mu.Lock()
	pending := 0
	for lease := range life.owners {
		if lease.kind == originalRetainedCall && lease.appReturned && !lease.sdkDrained && !lease.phaseActive {
			pending++
		}
	}
	claimed := life.retirementClaimed
	life.mu.Unlock()
	if pending != 1 || claimed {
		t.Fatalf("cancellation was mistaken for actual worker drain: pending=%d claimed=%v", pending, claimed)
	}
	if _, ok := a.pluginRetainedObjects.Load(object); !ok {
		t.Fatal("undrained object missing from shutdown enumeration")
	}
	// This existing owned event deliberately ignores ctx cancellation and only
	// exits after source close releases it. App.Close must initiate close/kill,
	// not first wait for its retained invocation to drain.
	closed := make(chan struct{})
	var closeErr error
	go func() { closeErr = a.Close(); close(closed) }()
	retainedRuntimeAwait(t, closed, "actual App.Close with an undrained worker")
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	retainedRuntimeAwait(t, life.retired(), "actual retirement notification")
	if _, ok := a.pluginRetainedObjects.Load(object); ok {
		t.Fatal("reaped object remains in cleanup enumeration")
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := client.Stop(stopCtx); err != nil {
		t.Fatal("actual worker final join was not clean", err)
	}
	t.Log("actual cancellation returned before SDK drain; App.Close initiated cleanup and observed real worker exit")
}

func TestOriginalRetainedActualUnpublishedSubmission(t *testing.T) {
	for _, ending := range []string{"complete", "app-close"} {
		t.Run(ending, func(t *testing.T) {
			a, prior := retainedRuntimeFixture(t, true)
			if _, err := prior.callOriginalMethod(context.Background(), "configure", json.RawMessage(`{"submit_init":true,"fail_init":true}`)); err != nil {
				t.Fatal(err)
			}
			loaded, err := a.reloadOriginalPlugin(context.Background(), ownedSubmittedAppID)
			if err != nil || loaded != nil {
				t.Fatal("failing initializer published", loaded, err)
			}
			if _, ok := a.pluginRunningInstances.Load(ownedSubmittedAppID); ok {
				t.Fatal("unpublished object exposed as running")
			}
			var held *originalPluginInstance
			a.pluginSubmittedObjects.Range(func(key, _ any) bool {
				candidate := key.(*originalPluginInstance)
				if candidate.installation.InstanceID == ownedSubmittedAppID {
					if held != nil {
						t.Error("duplicate held objects")
					}
					held = candidate
				}
				return true
			})
			if held == nil || held == prior {
				t.Fatal("accepted task lost exact failed-initializer object")
			}
			client := ownedSubmittedClient(held)
			if client == nil {
				t.Fatal("failed initializer killed child")
			}
			life := held.originalRetainedState()
			life.mu.Lock()
			managerOwned, owners := life.managerOwned, len(life.owners)
			life.mu.Unlock()
			if managerOwned || owners < 2 {
				t.Fatalf("submission/call ownership not distinct: manager=%v owners=%d", managerOwned, owners)
			}
			row, err := a.pluginStore.Data(context.Background(), ownedSubmittedAppID, "entered_init")
			var entered struct {
				PID int `json:"pid"`
			}
			if err != nil || json.Unmarshal(row.Value, &entered) != nil || entered.PID <= 0 {
				t.Fatal("actual child entry missing", err)
			}
			if ending == "complete" {
				if err := a.pluginStore.originalBaseSaveData(context.Background(), ownedSubmittedAppID, "release_init", json.RawMessage(`true`)); err != nil {
					t.Fatal(err)
				}
				ownedSubmittedDone(t, a, ownedSubmittedAppID, "init", entered.PID)
				retainedRuntimeAwait(t, life.retired(), "natural retained child termination")
				a.wg.Wait()
			} else {
				closed := make(chan struct{})
				var closeErr error
				go func() { closeErr = a.Close(); close(closed) }()
				retainedRuntimeAwait(t, closed, "forced unpublished object cleanup")
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := client.Stop(stopCtx); err != nil {
				t.Fatal("actual unpublished transport did not reap cleanly", err)
			}
			if _, ok := a.pluginRetainedObjects.Load(held); ok {
				t.Fatal("retained cleanup reference leaked")
			}
			count := 0
			a.pluginSubmittedObjects.Range(func(_, _ any) bool { count++; return true })
			if count != 0 {
				t.Fatal("accepted submission reference leaked")
			}
			store, pluginsStore := a.Store, a.pluginStore
			if ending == "app-close" {
				store, err = OpenStore(a.Dir)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				pluginsStore, err = NewPluginStore(context.Background(), store)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, hook := range []string{"close", "stop_service", "stop"} {
				row, err := pluginsStore.Data(context.Background(), ownedSubmittedAppID, "cleanup_"+hook)
				var marker struct {
					PID int `json:"pid"`
				}
				if err == nil && json.Unmarshal(row.Value, &marker) == nil && marker.PID == entered.PID {
					t.Fatal("unpublished retirement added a business hook", hook)
				}
			}
			t.Logf("actual failed initializer PID %d retained accepted child through %s, transport-only reap", entered.PID, ending)
		})
	}
}
