package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Actual main/HTTP/module/event/image chain, with owned plugin callbacks.
func TestPluginCatalogOpaqueProductionCLI(t *testing.T) {
	server, worker := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY"), os.Getenv("OWNED_CATALOG_OPAQUE_PROCESS_BINARY")
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
	searchSources := "owned"
	bootstrap, _ := json.Marshal(map[string]any{"username": "catalog-opaque-acceptance", "password": "owned-catalog-opaque-password", "settings": Settings{APIToken: hex.EncodeToString(secret[:]), PluginMarkets: []string{repository.URL + "/index"}, SearchSource: &searchSources}})
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
			res, e := client.Get(origin + "/api/auth/status")
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
		request("POST", "/api/auth/login", map[string]string{"username": "catalog-opaque-acceptance", "password": "owned-catalog-opaque-password"}, 200)
	}
	success := func(method, path string, value any) {
		t.Helper()
		raw := request(method, path, value, 200)
		var v struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &v) != nil || !v.Data.Success {
			t.Fatal("actual catalog management failed", method, path)
		}
	}
	const source = ownedModuleID
	const clone = source + "opaque"
	model := func(id int, title string) map[string]any {
		return map[string]any{"source": "themoviedb", "tmdb_id": id, "media_id": strconv.Itoa(id), "type": "电影", "title": title, "year": "2026", "poster_path": "owned-poster", "backdrop_path": "owned-backdrop", "logo_path": "owned-logo", "tmdb_info": map[string]any{"owned_private": id}}
	}
	config := func(prefix, id string, number int, primary bool) map[string]any {
		modules := map[string]string{"async_recognize_media": "module_execute"}
		if primary {
			modules["async_obtain_images"] = "module_images"
			modules["async_search_medias"] = "module_search_media"
			modules["async_tmdb_episodes"] = "module_episodes"
		}
		mutations := map[string]any{"discover.source": map[string]any{"extra_sources": []any{map[string]any{"name": "Owned " + prefix + " opaque", "mediaid_prefix": prefix, "api_path": "plugin/" + id + "/feed", "filter_params": map[string]any{}, "filter_ui": []any{}}}}}
		if primary {
			mutations["media.recognize.convert"] = map[string]any{"mediaid": "owned:convert", "convert_type": "themoviedb", "media_dict": map[string]any{"id": 120}}
			mutations["name.recognize"] = map[string]any{"name": "Other.Film", "year": "2026", "season": 0}
		}
		results := map[string]any{prefix + ":opaque /?:id=1": model(number, "Owned "+prefix+" metadata")}
		if primary {
			results["themoviedb:120"] = model(120, "Owned converted metadata")
			results["name:Other Film"] = model(120, "Owned auxiliary metadata")
		}
		return map[string]any{"enabled": true, "modules": modules, "recognize_results": results, "recognize_missing": nil, "image_mode": "false", "event_mutations": mutations, "event_append_sources": true, "media_result": []any{model(number, "Owned platform search")}, "episode_result": []any{map[string]any{"episode_number": 2, "season_number": 0, "name": "Owned platform episode"}}, "feed": []any{map[string]any{"source": prefix, "mediaid_prefix": prefix, "media_id": "opaque /?:id=1", "type": "电影", "title": "Owned " + prefix + " opaque card", "year": "2026", "overview": "Owned plugin-platform ordinary metadata acceptance"}}}
	}
	snapshot := func(id string) ownedModuleSnapshot {
		t.Helper()
		var value ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &value) != nil || value.PID <= 0 {
			t.Fatal("actual opaque snapshot")
		}
		return value
	}
	detail := func(action, source, id string, want int) map[string]json.RawMessage {
		t.Helper()
		query := url.Values{"source": {source}, "kind": {"movie"}, "source_id": {id}, "title": {"Original Film"}, "year": {"2020"}}
		var value struct{ Data map[string]json.RawMessage }
		if json.Unmarshal(request("GET", "/api/catalog/"+action+"?"+query.Encode(), nil, want), &value) != nil {
			t.Fatal("actual opaque response")
		}
		return value.Data
	}
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository.URL + "/index"})
	sourceConfig := config("owned", source, 120, true)
	cloneConfig := config("ownedclone", clone, 121, false)
	success("PUT", "/api/plugins/"+source, sourceConfig)
	success("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": "opaque"})
	success("PUT", "/api/plugins/"+clone, cloneConfig)
	initialSource, initialClone := snapshot(source), snapshot(clone)
	if initialSource.PID == initialClone.PID {
		t.Fatal("source and clone share process")
	}
	for _, row := range []struct {
		prefix string
		id     int
	}{{"owned", 120}, {"ownedclone", 121}} {
		got := detail("explore/detail", row.prefix, "opaque /?:id=1", 200)
		if string(got["id"]) != strconv.Itoa(row.id) || string(got["poster_path"]) != `"plugin-poster"` {
			t.Fatal("actual source/clone metadata/image consumption", string(got["id"]))
		}
		var resolved struct{ ID int }
		resolution := detail("explore/resolve", row.prefix, "opaque /?:id=1", 200)
		if json.Unmarshal(resolution["media"], &resolved) != nil || resolved.ID != row.id || string(resolution["match_method"]) != `"tmdb"` {
			t.Fatal("opaque ID treated as native before plugin resolve")
		}
	}
	got := detail("explore/detail", "owned", "convert", 200)
	if string(got["title"]) != `"Owned converted metadata"` {
		t.Fatal("ordinary conversion not consumed")
	}
	raw = request("GET", "/api/v1/media/owned:convert?"+url.Values{"type_name": {"电影"}}.Encode(), nil, 200)
	var fixed map[string]json.RawMessage
	if json.Unmarshal(raw, &fixed) != nil || len(fixed) != 58 || string(fixed["tmdb_id"]) != `120` || fixed["data"] != nil || fixed["tmdb_info"] != nil {
		t.Fatal("source response schema/raw wire")
	}
	// Force the primary conversion output to None so the actual title/name
	// helper is reached, with existing module callbacks controlling its result.
	delete(sourceConfig["event_mutations"].(map[string]any), "media.recognize.convert")
	success("PUT", "/api/plugins/"+source, sourceConfig)
	got = detail("explore/detail", "owned", "auxiliary", 200)
	if string(got["title"]) != `"Owned auxiliary metadata"` {
		t.Fatal("name recognition not consumed")
	}
	beforeSource, beforeClone := snapshot(source), snapshot(clone)
	var searchRows []map[string]json.RawMessage
	if json.Unmarshal(request("GET", "/api/v1/media/search?title=Owned&count=8&page=1", nil, 200), &searchRows) != nil || len(searchRows) != 1 || string(searchRows[0]["title"]) != `"Owned platform search"` {
		t.Fatal("real source search result not consumed")
	}
	var episodes []map[string]json.RawMessage
	if json.Unmarshal(request("GET", "/api/v1/tmdb/120/0", nil, 200), &episodes) != nil || len(episodes) != 1 || string(episodes[0]["episode_number"]) != `2` || len(episodes[0]) != 11 {
		t.Fatal("real source episode result not consumed")
	}
	sourceConfig["episode_result"] = false
	success("PUT", "/api/plugins/"+source, sourceConfig)
	request("GET", "/api/v1/tmdb/120/0", nil, 500)
	sourceConfig["episode_result"] = []any{map[string]any{"episode_number": 2, "season_number": 0, "name": "Owned platform episode"}}
	success("PUT", "/api/plugins/"+source, sourceConfig)
	stop()
	start()
	coldSource, coldClone := snapshot(source), snapshot(clone)
	if coldSource.PID == beforeSource.PID || coldClone.PID == beforeClone.PID || coldSource.PID == coldClone.PID || coldSource.Calls["init"] != 1 || coldClone.Calls["init"] != 1 {
		t.Fatal("cold source/clone identity")
	}
	if string(detail("explore/detail", "ownedclone", "opaque /?:id=1", 200)["id"]) != `121` {
		t.Fatal("clone config lost on cold")
	}
	observations := map[string]any{"source_before": beforeSource, "clone_before": beforeClone, "source_cold": coldSource, "clone_cold": coldClone, "main_pid": process.Process.Pid, "origin": origin, "data_dir": dir}
	if destination := os.Getenv("CATALOG_OPAQUE_OBSERVATIONS"); destination != "" {
		bytes, _ := json.MarshalIndent(observations, "", "  ")
		if os.WriteFile(destination, bytes, 0600) != nil {
			t.Fatal("write observations")
		}
	}
	if state := os.Getenv("CATALOG_OPAQUE_BROWSER_STATE"); state != "" {
		observations["username"], observations["password"] = "catalog-opaque-acceptance", "owned-catalog-opaque-password"
		observations["source"], observations["clone"] = source, clone
		write := func() {
			t.Helper()
			bytes, _ := json.MarshalIndent(observations, "", "  ")
			if os.WriteFile(state, bytes, 0600) != nil {
				t.Fatal("write browser hold")
			}
		}
		write()
		deadline := time.Now().Add(45 * time.Minute)
		for {
			if _, e := os.Stat(state + ".done"); e == nil {
				break
			}
			if _, e := os.Stat(state + ".restart"); e == nil {
				_ = os.Remove(state + ".restart")
				stop()
				start()
				observations["main_pid"] = process.Process.Pid
				observations["source_cold"], observations["clone_cold"] = snapshot(source), snapshot(clone)
				write()
				_ = os.WriteFile(state+".restarted", []byte("cold"), 0600)
			}
			if time.Now().After(deadline) {
				t.Fatal("opaque browser hold expired")
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	// Source recognizer remains installed; absence of the clone now means no
	// canonical result for its opaque key (do not manufacture native ID121).
	delete(sourceConfig["event_mutations"].(map[string]any), "name.recognize")
	success("PUT", "/api/plugins/"+source, sourceConfig)
	detail("explore/detail", "ownedclone", "opaque /?:id=1", 404)
	success("DELETE", "/api/plugins/"+source, nil)
	var rows []any
	if json.Unmarshal(request("GET", "/api/v1/discover/source", nil, 200), &rows) != nil || len(rows) != 0 {
		t.Fatal("uninstall retained catalog handlers")
	}
	t.Logf("actual opaque detail/resolve, conversion/name/image consumption, raw58 DTO, source/clone PIDs%d/%d→%d/%d and uninstall verified", initialSource.PID, initialClone.PID, coldSource.PID, coldClone.PID)
}
