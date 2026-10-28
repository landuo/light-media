package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

// Real cold startup, held HTTP repository, actual SDK and actual scheduler.
// Source lifecycle.py initializes existing objects before background sync;
// plugin.sync force-installs registered missing/older candidates only.
func verifyOwnedProductionStartupRepair(t *testing.T, dir string, index artifacts.Index, v1, v2 []byte, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func(), setSafe func(bool)) {
	t.Helper()
	declaration, _ := json.Marshal(index)
	var privateIndex artifacts.Index
	if json.Unmarshal(declaration, &privateIndex) != nil {
		t.Fatal("copy owned startup declaration")
	}
	index = privateIndex
	var phase, metadataHits, artifactHits atomic.Int32
	gate := make(chan struct{})
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			metadataHits.Add(1)
			current := phase.Load()
			if current == 1 {
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
			copy := index
			copy.Releases = append([]artifacts.Release(nil), index.Releases...)
			for i := range copy.Releases {
				copy.Releases[i].Artifacts = append([]artifacts.Artifact(nil), index.Releases[i].Artifacts...)
			}
			copy.CurrentVersion = "1.0"
			if current != 0 {
				copy.CurrentVersion = "2.0"
			}
			if current == 2 {
				copy.Releases[1].Artifacts[0].SHA256 = originalFrontendHash([]byte("different owned bytes"))
			}
			_ = json.NewEncoder(w).Encode(copy)
		case "/v1":
			artifactHits.Add(1)
			_, _ = w.Write(v1)
		case "/v2":
			artifactHits.Add(1)
			_, _ = w.Write(v2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/v1"
	index.Releases[1].Artifacts[0].URL = server.URL + "/v2"
	const id = ownedStandardFixtureID
	clone := id + "startup"
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{server.URL + "/index"}, "plugin_local_repo_paths": []string{}, "plugin_auto_reload": false})
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": server.URL + "/index", "force": true})
	success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "key": "startup-source"})
	success("POST", "/api/plugins/"+id+"/clone", map[string]any{"suffix": "Startup"})
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "key": "startup-clone"})
	type snapshot struct {
		Nonce         int
		InitCount     int `json:"init_count"`
		Version       string
		Config, Value json.RawMessage
		Ticks         int
	}
	read := func(identity, version, marker string) snapshot {
		t.Helper()
		var value snapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+identity+"/echo", nil, 200), &value) != nil || value.Nonce <= 0 || value.Version != version || !bytes.Contains(value.Config, []byte(marker)) || !bytes.Equal(value.Value, []byte(`"`+marker+`"`)) {
			t.Fatal("actual startup SDK/config/KV mismatch", identity, version)
		}
		return value
	}
	waitInstance := func(identity, version, marker string, previous int) snapshot {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for {
			var value snapshot
			_ = json.Unmarshal(request("GET", "/api/v1/plugin/"+identity+"/echo", nil, 0), &value)
			if value.Nonce > 0 && value.Nonce != previous && value.Version == version {
				return read(identity, version, marker)
			}
			if time.Now().After(deadline) {
				t.Fatal("real startup did not recover expected actual SDK", version)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	prior := read(id, "1.0", "startup-source")
	stop()
	phase.Store(1)
	metadataBefore := metadataHits.Load()
	start()
	deadlineMetadata := time.Now().Add(8 * time.Second)
	for metadataHits.Load() == metadataBefore {
		if time.Now().After(deadlineMetadata) {
			t.Fatal("background startup did not actually reach held repository")
		}
		time.Sleep(10 * time.Millisecond)
	}
	loaded := read(id, "1.0", "startup-source")
	if loaded.Nonce == prior.Nonce || loaded.InitCount != 1 {
		t.Fatal("initial existing-code startup did not precede held sync")
	}
	cloneInitial := read(clone, "1.0", "startup-clone")
	// Listener and both APIs remain usable while background metadata is held.
	request("GET", "/api/plugins/"+id+"/form", nil, 200)
	close(gate)
	updated := waitInstance(id, "2.0", "startup-source", loaded.Nonce)
	cloneUpdated := waitInstance(clone, "1.0", "startup-clone", cloneInitial.Nonce)
	if updated.InitCount != 1 || cloneUpdated.Nonce == cloneInitial.Nonce || cloneUpdated.InitCount != 1 {
		t.Fatal("successful startup sync did not reinitialize whole manager")
	}
	t.Logf("held startup: existing sourcePID=%d clonePID=%d usable before sync; source2PID=%d clone1PID=%d after whole-manager reinit", loaded.Nonce, cloneInitial.Nonce, updated.Nonce, cloneUpdated.Nonce)
	stop()
	start()
	current := waitInstance(id, "2.0", "startup-source", updated.Nonce)
	hits := artifactHits.Load()
	time.Sleep(250 * time.Millisecond)
	if artifactHits.Load() != hits || read(id, "2.0", "startup-source").Nonce != current.Nonce {
		t.Fatal("current version was reinstalled after restart")
	}
	stop()
	// Only the test-owned exact package is removed; config/data/clone stay.
	if err := os.RemoveAll(filepath.Join(dir, "plugin-packages", id)); err != nil {
		t.Fatal(err)
	}
	phase.Store(2)
	start()
	deadline := time.Now().Add(10 * time.Second)
	for !bytes.Contains(request("GET", "/api/logs?module=plugins&limit=100", nil, 200), []byte("sync_failed")) {
		if time.Now().After(deadline) {
			t.Fatal("failed real startup repair was not reported")
		}
		time.Sleep(50 * time.Millisecond)
	}
	read(clone, "1.0", "startup-clone")
	stop()
	setSafe(true)
	beforeSafe := metadataHits.Load()
	start()
	request("GET", "/api/plugins/installed", nil, 200)
	request("GET", "/api/plugins/"+id+"/api/echo", nil, 404)
	time.Sleep(250 * time.Millisecond)
	if metadataHits.Load() != beforeSafe {
		t.Fatal("safe startup performed plugin synchronization")
	}
	stop()
	setSafe(false)
	phase.Store(3)
	start()
	repaired := waitInstance(id, "2.0", "startup-source", current.Nonce)
	if repaired.InitCount != 1 {
		t.Fatal("repaired worker was not actually initialized once")
	}
	waitInstance(clone, "1.0", "startup-clone", cloneUpdated.Nonce)
	success("DELETE", "/api/plugins/"+id, nil)
	success("DELETE", "/api/plugins/"+clone, nil)
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{}})
	t.Log("real cold startup: existing-before-sync, manager reinit, no-op, missing-code bad repair, safe mode and successful repair verified")
}
