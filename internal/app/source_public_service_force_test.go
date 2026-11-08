package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Fixed manager reload stops/removes the old bound object without joining an
// already accepted public service. Same-version force commits new metadata
// first; the old callback must finish its source history after replacement.
func TestSourcePublicServiceForceUpdateKeepsOldBoundObject(t *testing.T) {
	var requests atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><a href="logout.php">退出</a><span>签到成功</span></html>`)
	}))
	defer func() { unblock(); peer.Close() }()
	binary := os.Getenv("INDEPENDENT_PUBLIC_ASI")
	if binary == "" {
		t.Fatal("owned actual ASI executable required")
	}
	payload, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := exec.Command(binary, "--manifest").Output()
	if e != nil {
		t.Fatal(e)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "AutoSignIn" || !manifest.ManagedPrivateScheduler {
		t.Fatal("actual managed ASI manifest required")
	}
	hash := sha256.Sum256(payload)
	sum := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			json.NewEncoder(w).Encode(index)
		case "/worker":
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	rows, ids := []any{}, []any{}
	const total = 64
	for n := 0; n < total; n++ {
		id := fmt.Sprint(91000 + n)
		ids = append(ids, id)
		rows = append(rows, map[string]any{"id": id, "name": fmt.Sprintf("IndependentGeneration%d", n), "url": peer.URL + fmt.Sprintf("/site-%d/", n), "cookie": "owned=synthetic", "ua": "IndependentGenerationUA", "render": false, "timeout": 10})
	}
	config := func(onlyonce bool) map[string]any {
		return map[string]any{"enabled": true, "notify": true, "cron": "25", "onlyonce": onlyonce, "queue_cnt": 1, "sign_sites": ids, "login_sites": []any{}, "retry_keyword": []any{}, "auto_cf": 0, "clean": false}
	}
	retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": rows})
	retainedStoreSaveConfig(t, a, "AutoSignIn", config(false))
	installBody, _ := json.Marshal(map[string]any{"plugin_id": "AutoSignIn", "repo_url": repo.URL + "/index"})
	installed := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(installBody), cookie, "")
	assertOriginalRegistrySuccess(t, installed.Code, installed.Body.Bytes())
	// Match the standard installer helper: initial PluginReload refreshes are
	// already accepted asynchronously. No service/business callback exists yet.
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("AutoSignIn")
	if !ok {
		t.Fatal("initial actual loaded object missing")
	}
	old := value.(*originalPluginInstance)
	oldGeneration := old.installation.Generation
	if e := a.runOriginalPublicService(context.Background(), "AutoSignIn", "AutoSignIn"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("actual +3 second public callback not in owned source HTTP")
	}
	forceBody, _ := json.Marshal(map[string]any{"plugin_id": "AutoSignIn", "repo_url": repo.URL + "/index", "force": true})
	type response struct {
		code int
		body []byte
	}
	updated := make(chan response, 1)
	go func() {
		r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(forceBody), cookie, "")
		updated <- response{r.Code, append([]byte(nil), r.Body.Bytes()...)}
	}()
	// The full force endpoint must finish its actual reload/new init while the
	// source callback's first real HTTP remains held. This is the nonjoin check.
	select {
	case r := <-updated:
		assertOriginalRegistrySuccess(t, r.code, r.body)
	case <-time.After(10 * time.Second):
		t.Fatal("force update joined held ordinary source public callback")
	}
	if current, present := a.pluginRunningInstances.Load("AutoSignIn"); !present || current == old {
		t.Fatal("force operation did not replace logical old object before release")
	}
	old.sessionMu.Lock()
	oldClient := old.session
	old.sessionMu.Unlock()
	if oldClient == nil {
		t.Fatal("held source client was prematurely detached/reaped")
	}
	unblock()
	privateAwait(t, "old bound public callback actual64 complete", func() bool { return requests.Load() == total && publicProgressStatusForASI(a) == "success" })
	in, e := a.pluginStore.Installation(context.Background(), "AutoSignIn")
	if e != nil || in.Generation <= oldGeneration || in.Version != manifest.Version {
		t.Error("same-version force update not actually committed", e, in.Generation, in.Version)
	}
	current, ok := a.pluginRunningInstances.Load("AutoSignIn")
	if !ok || current == old {
		t.Error("successful force update did not replace actual object")
	}
	if n := requests.Load(); n != total {
		t.Error("active old public owner lost real source HTTP after generation publication", n, total)
	}
	data, e := a.pluginStore.originalBaseGetData(context.Background(), "AutoSignIn", nil)
	if e != nil {
		t.Fatal(e)
	}
	var stored []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(data, &stored) != nil {
		t.Fatal("actual persisted data projection invalid")
	}
	matches := 0
	for _, row := range stored {
		if !strings.Contains(row.Key, "月") {
			continue
		}
		var history []struct {
			Success  bool `json:"success"`
			LoggedIn bool `json:"logged_in"`
		}
		if json.Unmarshal(row.Value, &history) != nil || len(history) != total {
			continue
		}
		matches++
		for _, item := range history {
			if !item.Success || !item.LoggedIn {
				t.Error("post-stop history is not actual successful parsed business", item)
			}
		}
	}
	if matches != 1 {
		t.Error("active old owner did not persist one complete successful64-row source history before replacement", matches)
	}
	t.Logf("observed same-version actual force generation %d→%d; owned source HTTP %d;64-row histories %d", oldGeneration, in.Generation, requests.Load(), matches)
}

func publicProgressStatusForASI(a *App) string {
	a.pluginOriginalDirectJobs.mu.Lock()
	defer a.pluginOriginalDirectJobs.mu.Unlock()
	out := ""
	if p := a.pluginOriginalDirectJobs.progress["AutoSignIn_AutoSignIn"]; p != nil {
		json.Unmarshal(p.data["status"], &out)
	}
	return out
}
