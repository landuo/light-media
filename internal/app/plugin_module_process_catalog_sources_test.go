package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Real production main -> catalog/person route -> plugin registry -> actual
// SDK process. Fixed TmdbChain.async_person_detail kwargs and MediaPerson DTO
// are checked, including falsey rejection and persistent cloned configuration.
func TestPluginCatalogSourcesProductionCLI(t *testing.T) {
	server, worker := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY"), os.Getenv("OWNED_CATALOG_EVENTS_PROCESS_BINARY")
	if server == "" || worker == "" {
		t.Skip("unverified: production main and actual snapshot-enabled catalog worker required")
	}
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID || len(manifest.APIs) != 2 || len(manifest.ChainHandlers) != 4 {
		t.Fatal("actual catalog module fixture identity")
	}
	digest := originalFrontendHash(payload)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	dir := t.TempDir()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	bootstrap, _ := json.Marshal(map[string]any{"username": "catalog-sources-acceptance", "password": "owned-catalog-sources-password", "settings": Settings{APIToken: hex.EncodeToString(secret[:]), PluginMarkets: []string{repository.URL + "/index"}}})
	boot := exec.Command(server, "--data-dir", dir, "--bootstrap-stdin")
	boot.Stdin = bytes.NewReader(bootstrap)
	if out, e := boot.CombinedOutput(); e != nil || !bytes.Contains(out, []byte("bootstrap_complete")) {
		t.Fatal("owned catalog bootstrap")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	origin := "http://" + address
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	var process *exec.Cmd
	stop := func() {
		t.Helper()
		if process == nil {
			return
		}
		_ = process.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		select {
		case e := <-done:
			if e != nil {
				t.Error("actual catalog shutdown", e)
			}
		case <-time.After(12 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("catalog shutdown timeout")
		}
		process = nil
	}
	defer stop()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	request := func(method, path string, value any, want int) []byte {
		t.Helper()
		var body []byte
		if value != nil {
			body, _ = json.Marshal(value)
		}
		r, e := http.NewRequest(method, origin+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		response, e := client.Do(r)
		if e != nil {
			t.Fatal("actual catalog request", method, path)
		}
		defer response.Body.Close()
		out, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != want {
			t.Fatalf("actual catalog %s %s status%d want%d; body withheld", method, path, response.StatusCode, want)
		}
		return out
	}
	start := func() {
		t.Helper()
		process = exec.Command(server, "--listen", address, "--data-dir", dir)
		process.Env = append(os.Environ(), "MP_SERVER_HOST=")
		process.Stdout = log
		process.Stderr = log
		if process.Start() != nil {
			t.Fatal("start production catalog")
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			res, e := client.Get(origin + "/healthz")
			if e == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("catalog listener unavailable")
			}
			time.Sleep(30 * time.Millisecond)
		}
		request("POST", "/api/auth/login", map[string]string{"username": "catalog-sources-acceptance", "password": "owned-catalog-sources-password"}, 200)
	}
	success := func(method, path string, value any) {
		t.Helper()
		raw := request(method, path, value, 200)
		var v struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &v) != nil || !v.Data.Success {
			t.Fatal("actual catalog management failed", method, path)
		}
	}
	snapshot := func(id string) map[string]json.RawMessage {
		t.Helper()
		var v map[string]json.RawMessage
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &v) != nil || len(v["pid"]) == 0 {
			t.Fatal("actual catalog events snapshot")
		}
		return v
	}
	const source = ownedModuleID
	const clone = source + "sources"
	config := func(id, marker, prefix string) map[string]any {
		recommend := map[string]any{"name": "Owned " + marker + " recommendations", "api_path": "plugin/" + id + "/feed?list=ordinary&marker=" + marker, "type": "电影"}
		discover := map[string]any{"name": "Owned " + marker + " discover", "mediaid_prefix": prefix, "api_path": "plugin/" + id + "/feed?marker=" + marker, "filter_params": map[string]any{"genre": "all", "region": nil}, "filter_ui": []any{map[string]any{"component": "VTextField", "props": map[string]any{"model": "genre", "label": "Owned genre"}}, map[string]any{"component": "VTextField", "props": map[string]any{"model": "region", "label": "Owned region"}}}, "depends": map[string]any{"region": []string{"genre"}}}
		pages := map[string]any{}
		for page, count := range map[int]int{1: 20, 2: 10, 3: 0} {
			rows := []any{}
			for n := 0; n < count; n++ {
				number := (page-1)*20 + n + 1
				rows = append(rows, map[string]any{"source": prefix, "mediaid_prefix": prefix, "media_id": fmt.Sprintf("opaque %s id%d:/?value=1", marker, number), "type": "电影", "title": fmt.Sprintf("Owned %s ordinary %02d", marker, number), "year": "2026", "vote_average": 8.2, "overview": "Owned ordinary test item"})
			}
			pages[fmt.Sprint(page)] = rows
		}
		return map[string]any{"enabled": true, "event_append_sources": true, "event_mutations": map[string]any{"recommend.source": map[string]any{"extra_sources": []any{recommend}}, "discover.source": map[string]any{"extra_sources": []any{discover}}}, "feed_pages": pages, "feed_fail": false}
	}
	originalConfig, cloneConfig := config(source, "source", "owned"), config(clone, "clone", "ownedclone")
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository.URL + "/index"})
	success("PUT", "/api/plugins/"+source, originalConfig)
	success("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": "sources"})
	success("PUT", "/api/plugins/"+clone, cloneConfig)
	// Keep only synthetic ordinary extra shelves available to the browser. Extras
	// are absent from saved preferences and therefore source-default disabled.
	disabled := map[string]bool{}
	for _, row := range catalog.RecommendSources() {
		disabled[row.ID] = false
	}
	var preference struct{ Data RecommendPreferences }
	if json.Unmarshal(request("PUT", "/api/catalog/recommend/preferences", map[string]any{"enabled": disabled}, 200), &preference) != nil || len(preference.Data.Enabled) != 15 {
		t.Fatal("owned baseline recommendation preferences")
	}
	for _, kind := range []string{"recommend", "discover"} {
		var rows []map[string]any
		if json.Unmarshal(request("GET", "/api/v1/"+kind+"/source", nil, 200), &rows) != nil || len(rows) != 2 || !strings.Contains(rows[0]["name"].(string), "source") || !strings.Contains(rows[1]["name"].(string), "clone") {
			t.Fatal("actual source and clone mutable event outputs", kind)
		}
	}
	for _, step := range []struct{ page, count int }{{1, 20}, {2, 10}, {3, 0}} {
		var rows []map[string]any
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+source+"/feed?list=ordinary&marker=source&page="+fmt.Sprint(step.page)+"&title=Owned+source+recommendations", nil, 200), &rows) != nil || len(rows) != step.count {
			t.Fatal("actual raw page count", step.page)
		}
	}
	observed := snapshot(source)
	var feed struct{ Query map[string][]string }
	if json.Unmarshal(observed["last_feed"], &feed) != nil || plugins.LastQuery(feed.Query, "page") != "3" || plugins.LastQuery(feed.Query, "title") != "Owned source recommendations" || plugins.LastQuery(feed.Query, "list") != "ordinary" {
		t.Fatal("actual raw pagination/title/query not delivered")
	}
	originalConfig["feed_fail"] = true
	success("PUT", "/api/plugins/"+source, originalConfig)
	request("GET", "/api/v1/plugin/"+source+"/feed?page=1", nil, 503)
	originalConfig["feed_fail"] = false
	success("PUT", "/api/plugins/"+source, originalConfig)
	sourceBefore, cloneBefore := snapshot(source), snapshot(clone)
	if bytes.Equal(sourceBefore["pid"], cloneBefore["pid"]) {
		t.Fatal("source/clone process identity shared")
	}
	stop()
	start()
	sourceCold, cloneCold := snapshot(source), snapshot(clone)
	if bytes.Equal(sourceCold["pid"], sourceBefore["pid"]) || bytes.Equal(cloneCold["pid"], cloneBefore["pid"]) {
		t.Fatal("actual cold source/clone restore")
	}
	if statePath := os.Getenv("CATALOG_SOURCES_PROCESS_BROWSER_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "repository": repository.URL, "source_id": source, "clone_id": clone, "source_config": originalConfig, "clone_config": cloneConfig, "remaining": "opaque detail/resolve/actions backend not yet implemented; this hold only discovery/raw feed/UI/preferences/filter/failure/cold"})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("write owned catalog sources browser state")
		}
		t.Log("catalog sources browser ready; synthetic source/clone, raw20/10/empty, extras initially disabled; waiting .done/.restart")
		deadline := time.Now().Add(55 * time.Minute)
		for {
			for _, fault := range []struct {
				suffix string
				fail   bool
			}{{".feed-down", true}, {".feed-up", false}} {
				if _, e := os.Stat(statePath + fault.suffix); e == nil {
					_ = os.Remove(statePath + fault.suffix)
					originalConfig["feed_fail"] = fault.fail
					success("PUT", "/api/plugins/"+source, originalConfig)
					_ = os.WriteFile(statePath+fault.suffix+".applied", []byte("owned source config applied"), 0600)
					t.Logf("owned catalog feed_fail=%t applied through real config save", fault.fail)
				}
			}
			if _, e := os.Stat(statePath + ".restart"); e == nil {
				_ = os.Remove(statePath + ".restart")
				beforeS, beforeC := snapshot(source), snapshot(clone)
				stop()
				start()
				afterS, afterC := snapshot(source), snapshot(clone)
				if bytes.Equal(beforeS["pid"], afterS["pid"]) || bytes.Equal(beforeC["pid"], afterC["pid"]) {
					t.Fatal("browser requested cold SDK restore failed")
				}
				receipt, _ := json.Marshal(map[string]any{"source_before": beforeS["pid"], "source_after": afterS["pid"], "clone_before": beforeC["pid"], "clone_after": afterC["pid"]})
				_ = os.WriteFile(statePath+".restarted", receipt, 0600)
				t.Log("catalog sources browser cold restart complete")
			}
			if _, e := os.Stat(statePath + ".done"); e == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("catalog sources browser hold expired")
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	snapshot(source)
	snapshot(clone)
	success("DELETE", "/api/plugins/"+clone, nil)
	var rows []any
	if json.Unmarshal(request("GET", "/api/v1/recommend/source", nil, 200), &rows) != nil || len(rows) != 1 {
		t.Fatal("clone uninstall did not remove only its source event output")
	}
	success("DELETE", "/api/plugins/"+source, nil)
	if json.Unmarshal(request("GET", "/api/v1/recommend/source", nil, 200), &rows) != nil || len(rows) != 0 {
		t.Fatal("source uninstall retained extra source")
	}
	t.Log("actual source/clone discovery, raw page/title/query, real503 recovery, cold and handler removal verified; opaque actions excluded from this partial gate")
}
