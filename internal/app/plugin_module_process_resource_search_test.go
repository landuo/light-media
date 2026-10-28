package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/priorityrules"
	"light-media/internal/sites"
)

// A separate production main owns discovery, the authenticated numeric site,
// module invocation, real site HTTP fallback and cold recovery. The repository,
// external executable and NexusPHP HTML server are explicitly owned fixtures.
func TestPluginSiteResourceModulesProductionCLI(t *testing.T) {
	mainBinary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY")
	if mainBinary == "" {
		t.Skip("unverified: independent production main required")
	}
	worker := os.Getenv("OWNED_RESOURCE_MODULE_PROCESS_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal("separately compiled actual module worker required", err)
	}
	manifestBytes, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(manifestBytes, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID || len(manifest.APIs) != 1 {
		t.Fatal("actual snapshot-enabled resource worker manifest", err)
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
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
	htmlPath := os.Getenv("PLUGIN_RESOURCE_HTML_FIXTURE")
	if htmlPath == "" {
		htmlPath = "../sites/testdata/ubits-search.sanitized.html"
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	var nativeCalls atomic.Int32
	var browserHolding atomic.Bool
	var streamHolding atomic.Bool
	var mediaHolding atomic.Bool
	var mediaProgressHolding atomic.Bool
	mediaProgressRelease := make(chan struct{})
	var mediaProgressReleaseOnce sync.Once
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls.Add(1)
		pageOK := r.URL.Query().Get("page") == "1" || browserHolding.Load() && r.URL.Query().Get("page") == "0" || streamHolding.Load() && r.URL.Query().Get("page") == "2" || mediaHolding.Load() && r.URL.Query().Get("page") == "0"
		wantKeyword := ""
		if streamHolding.Load() || mediaHolding.Load() {
			wantKeyword = "Owned keyword"
		}
		if r.URL.Path != "/torrents.php" || !pageOK || r.URL.Query().Get("search") != wantKeyword || r.Header.Get("Cookie") != "owned-site-cookie" || r.UserAgent() != "Owned-UA" {
			t.Error("actual native page/auth fallback contract")
		}
		if mediaProgressHolding.Load() && r.URL.Query().Get("page") == "1" {
			select {
			case <-mediaProgressRelease:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write(html)
	}))
	defer native.Close()
	defer mediaProgressReleaseOnce.Do(func() { close(mediaProgressRelease) })
	sourcePath := os.Getenv("PLUGIN_RESOURCE_SOURCE_ORACLE")
	if sourcePath == "" {
		sourcePath = "../../testdata/standard-resource-modules/source-oracle.json"
	}
	oracleBytes, err := os.ReadFile(sourcePath)
	var oracle resourceBrowseSourceOracle
	if err != nil || json.Unmarshal(oracleBytes, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("fixed source oracle required", err)
	}
	var result json.RawMessage
	for _, sample := range oracle.Cases {
		if sample.Name == "plugin_list" {
			result = json.RawMessage(strings.ReplaceAll(string(sample.Plugin), oracle.Site["url"].(string), native.URL))
		}
	}
	if len(result) == 0 {
		t.Fatal("fixed source plugin list absent")
	}
	dir := t.TempDir()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	key := hex.EncodeToString(random[:])
	site := SiteConfig{Config: sites.Config{ID: "owned-resource-site", Kind: "ubits", Name: "Owned resource site", BaseURL: native.URL, Cookie: "owned-site-cookie", UserAgent: "Owned-UA", Timeout: 15}, SiteOptions: SiteOptions{Domain: "127.0.0.1", Priority: 1}, Enabled: true}
	bootstrapSettings := Settings{APIToken: key, Sites: []SiteConfig{site}, PluginMarkets: []string{repository.URL + "/index"}}
	if os.Getenv("PLUGIN_MEDIA_SUBSCRIPTION_SCOPE_GATE") == "true" {
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/torrents.php" {
				t.Error("terminal owned batch unexpectedly searched second native site")
			}
			// Native site.updated legitimately attempts icon/userdata HTTP.
			// This owned boundary supplies no such data; preserve its 404 and
			// actual core diagnostics rather than confusing it with a search.
			http.NotFound(w, r)
		}))
		defer other.Close()
		second := site
		second.ID, second.Name, second.BaseURL = "owned-resource-zsecond", "Owned resource second", other.URL
		second.Enabled = false
		bootstrapSettings.Sites = append(bootstrapSettings.Sites, second)
		bootstrapSettings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-resource-qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}}}
	}
	bootstrap, _ := json.Marshal(map[string]any{"username": "resource-module-acceptance", "password": "owned-resource-password", "settings": bootstrapSettings})
	boot := exec.Command(mainBinary, "--data-dir", dir, "--bootstrap-stdin")
	boot.Stdin = bytes.NewReader(bootstrap)
	if raw, err := boot.CombinedOutput(); err != nil || !bytes.Contains(raw, []byte("bootstrap_complete")) {
		t.Fatal("actual resource bootstrap failed; sensitive output withheld", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := "http://" + address
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 12 * time.Second}
	logPath := filepath.Join(dir, "resource-production.log")
	log, err := os.Create(logPath)
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
		case err := <-done:
			if err != nil {
				t.Error("actual resource shutdown", err)
			}
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("actual resource shutdown timeout")
		}
		process = nil
	}
	defer stop()
	defer func() {
		if evidence := os.Getenv("PLUGIN_RESOURCE_EVIDENCE_DIR"); evidence != "" {
			_ = os.MkdirAll(evidence, 0700)
			if raw, err := os.ReadFile(logPath); err == nil {
				_ = os.WriteFile(filepath.Join(evidence, "resources43-production-main.log"), raw, 0600)
			}
		}
	}()
	start := func() {
		t.Helper()
		process = exec.Command(mainBinary, "--listen", address, "--data-dir", dir)
		process.Stdout, process.Stderr = log, log
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		until := time.Now().Add(12 * time.Second)
		for {
			r, err := client.Get(origin + "/api/auth/status")
			if err == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return
				}
			}
			if time.Now().After(until) {
				t.Fatal("actual resource listener unavailable")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	request := func(method, path string, value any) []byte {
		t.Helper()
		var body io.Reader
		if value != nil {
			raw, _ := json.Marshal(value)
			body = bytes.NewReader(raw)
		}
		r, _ := http.NewRequest(method, origin+path, body)
		r.Header.Set("Content-Type", "application/json")
		if strings.HasPrefix(path, "/api/v1/plugin/") {
			r.Header.Set("X-API-KEY", key)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("actual resource request", method, path, err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err != nil || response.StatusCode != 200 {
			t.Fatal("actual resource response; sensitive body withheld", method, path, response.StatusCode, err)
		}
		return raw
	}
	success := func(method, path string, value any) {
		t.Helper()
		var out struct{ Data struct{ Success bool } }
		if json.Unmarshal(request(method, path, value), &out) != nil || !out.Data.Success {
			t.Fatal("actual resource management rejected", method, path)
		}
	}
	type snapshot struct {
		PID    int
		Calls  map[string]int
		Inputs map[string]json.RawMessage
	}
	read := func(id string) snapshot {
		t.Helper()
		var out snapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil), &out) != nil || out.PID <= 0 {
			t.Fatal("actual external SDK snapshot absent", id)
		}
		return out
	}
	configure := func(id, handler string, value json.RawMessage, enabled bool) {
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": enabled, "modules": map[string]string{"async_refresh_torrents": handler, "refresh_torrents": "module_fail"}, "result": value})
	}
	check := func(wantTitles []string, wantCore int32) {
		t.Helper()
		before := nativeCalls.Load()
		raw := request("POST", "/api/sites/manage/"+site.ID+"/resources", map[string]any{"query": "", "page": 2})
		var out struct{ Data SearchOutput }
		wantCount := len(wantTitles) + 2*int(wantCore)
		wantStatus := "ok"
		if wantCount == 0 {
			wantStatus = "empty"
		}
		if json.Unmarshal(raw, &out) != nil || len(out.Data.Candidates) != wantCount || len(out.Data.Sites) != 1 || out.Data.Sites[0].Status != wantStatus || nativeCalls.Load()-before != wantCore {
			t.Fatal("actual source/core accumulation or falsey cutoff differs", len(out.Data.Candidates), nativeCalls.Load()-before)
		}
		for i, want := range wantTitles {
			if out.Data.Candidates[i].Resource.Title != want {
				t.Fatal("actual source/clone/core order differs", i)
			}
		}
		for _, secret := range []string{"owned-plugin-secret", "owned-private-material", "owned-site-cookie", "site_cookie", "enclosure"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("actual public resource response leaked private source input")
			}
		}
	}
	start()
	mainPID := process.Process.Pid
	request("POST", "/api/auth/login", map[string]string{"username": "resource-module-acceptance", "password": "owned-resource-password"})
	success("POST", "/api/plugins/install", map[string]string{"plugin_id": ownedModuleID, "repo_url": repository.URL + "/index"})
	configure(ownedModuleID, "module_execute", result, true)
	source := read(ownedModuleID)
	check([]string{"Owned plugin resource"}, 1)
	after := read(ownedModuleID)
	if after.Calls["get_state"]-source.Calls["get_state"] != 1 || after.Calls["get_module"]-source.Calls["get_module"] != 1 || after.Calls["module_execute"]-source.Calls["module_execute"] != 1 {
		t.Fatal("actual source discovery/invocation frame count differs")
	}
	var input struct{ Args, Kwargs json.RawMessage }
	if json.Unmarshal(after.Inputs["module_execute"], &input) != nil || string(input.Args) != "[]" {
		t.Fatal("actual async override input missing")
	}
	want := bytes.ReplaceAll(oracle.Cases[0].Observations[0].Kwargs, []byte(oracle.Site["url"].(string)), []byte(native.URL))
	if !originalBaseJSONEqual(input.Kwargs, want) {
		t.Fatal("actual fixed async_browse kwargs/site/page differ")
	}
	if os.Getenv("PLUGIN_RESOURCE_STREAM_GATE") == "true" {
		var streamOracle resourceStreamOracle
		raw, err := os.ReadFile(os.Getenv("PLUGIN_RESOURCE_STREAM_SOURCE_ORACLE"))
		if err != nil || json.Unmarshal(raw, &streamOracle) != nil || streamOracle.Production.Pages != 2 || os.Getenv("SEARCH_RESOURCE_PAGES") != "2" {
			t.Fatal("actual ordinary stream oracle and two-page main configuration required", err)
		}
		success("PUT", "/api/plugins/"+ownedModuleID, map[string]any{"enabled": true, "modules": map[string]string{"async_refresh_torrents": "module_fail", "async_worker_search_site": "module_execute", "get_search_page_size": "module_page_size"}, "result": result, "page_size": streamOracle.Production.PageSize})
		beforeStream := read(ownedModuleID)
		beforeCore := nativeCalls.Load()
		r, _ := http.NewRequest("POST", origin+"/api/search/resources", strings.NewReader(`{"query":"Owned keyword","page":2}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "text/event-stream")
		streamHolding.Store(true)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("actual ordinary resource stream request", err)
		}
		raw, err = io.ReadAll(io.LimitReader(response.Body, 4<<20))
		response.Body.Close()
		streamHolding.Store(false)
		if err != nil || response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatal("actual ordinary resource stream response", err)
		}
		var complete SearchOutput
		event := ""
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			}
			if event == "error" {
				t.Fatal("actual resource stream emitted error; body withheld")
			}
			if event == "complete" && strings.HasPrefix(line, "data: ") {
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &complete) != nil {
					t.Fatal("actual stream final result invalid")
				}
			}
		}
		wantRows, wantPages, wantPlugin := 0, 0, 0
		for _, frame := range streamOracle.Production.Frames {
			if frame.Type == "append" {
				wantPages++
				wantRows += len(frame.Items)
				for _, row := range frame.Items {
					if row["title"] == "Owned plugin resource" {
						wantPlugin++
					}
				}
			}
		}
		pluginRows := 0
		for _, row := range complete.Candidates {
			if row.Resource.Title == "Owned plugin resource" {
				pluginRows++
			}
		}
		if wantRows == 0 || len(complete.Candidates) != wantRows || pluginRows != wantPlugin || int(nativeCalls.Load()-beforeCore) != wantPages || len(complete.Sites) != 1 || complete.Sites[0].Status != "ok" {
			t.Fatal("actual resource SSE worker/capacity pages differ from source", len(complete.Candidates), nativeCalls.Load()-beforeCore)
		}
		afterStream := read(ownedModuleID)
		if afterStream.Calls["module_execute"]-beforeStream.Calls["module_execute"] != wantPages || afterStream.Calls["module_page_size"]-beforeStream.Calls["module_page_size"] != wantPages || afterStream.Calls["get_state"]-beforeStream.Calls["get_state"] != wantPages*2 {
			t.Fatal("actual main skipped source worker/capacity getter frame")
		}
		var workerInput, sizeInput struct{ Args, Kwargs json.RawMessage }
		_ = json.Unmarshal(afterStream.Inputs["module_execute"], &workerInput)
		_ = json.Unmarshal(afterStream.Inputs["module_page_size"], &sizeInput)
		for _, row := range streamOracle.Production.Observations {
			if row.Kind != "plugin" {
				continue
			}
			want := bytes.ReplaceAll(row.Kwargs, []byte(oracle.Site["url"].(string)), []byte(native.URL))
			if row.Method == "get_search_page_size" && !originalBaseJSONEqual(sizeInput.Kwargs, want) {
				t.Fatal("actual source capacity two kwargs differ")
			}
			// Snapshot retains the final call; check that last source page only.
			var page struct{ Page int }
			_ = json.Unmarshal(row.Kwargs, &page)
			if row.Method == "async_worker_search_site" && page.Page == 2 && !originalBaseJSONEqual(workerInput.Kwargs, want) {
				t.Fatal("actual source worker five kwargs differ")
			}
		}
		for _, secret := range []string{"owned-plugin-secret", "owned-private-material", "enclosure"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("actual SSE leaked source private material")
			}
		}
		t.Logf("actual main resource SSE SDK%d: %d pages/%d rows, worker+page-size wrappers and independent source kwargs each page; source AST core result boundary2 matches owned native HTTP2", afterStream.PID, wantPages, wantRows)
		configure(ownedModuleID, "module_execute", result, true)
	}
	if os.Getenv("PLUGIN_RESOURCE_BATCH_GATE") == "true" {
		var batchOracle resourceBatchOracle
		raw, err := os.ReadFile(os.Getenv("PLUGIN_RESOURCE_BATCH_SOURCE_ORACLE"))
		if err != nil || json.Unmarshal(raw, &batchOracle) != nil || len(batchOracle.Cases) != 8 {
			t.Fatal("actual fixed batch source replay required", err)
		}
		for _, sample := range batchOracle.Cases {
			if sample.Name != "plugin_dict" && sample.Name != "empty_list" {
				continue
			}
			plugin := bytes.ReplaceAll(sample.Plugin, []byte(oracle.Site["url"].(string)), []byte(native.URL))
			success("PUT", "/api/plugins/"+ownedModuleID, map[string]any{"enabled": true, "modules": map[string]string{"async_batch_search_torrents": "module_execute", "async_worker_search_site": "module_fail"}, "result": json.RawMessage(plugin)})
			beforeSDK, beforeCore := read(ownedModuleID), nativeCalls.Load()
			streamHolding.Store(true) // same real keyword/source page, ordinary JSON response
			raw = request("POST", "/api/search/resources", map[string]any{"query": "Owned keyword", "page": 2})
			streamHolding.Store(false)
			var output struct{ Data SearchOutput }
			if json.Unmarshal(raw, &output) != nil || len(output.Data.Sites) != 1 || output.Data.Sites[0].Status != "ok" {
				t.Fatal("actual non-stream batch response missing")
			}
			wantRows, wantCore := len(sample.Returned), 0
			if sample.Name == "empty_list" {
				wantRows = len(batchOracle.Production.Returned)
				for _, row := range batchOracle.Production.Observations {
					if row.Kind == "core" && (row.Method == "async_batch_search_torrents" || row.Method == "async_search_torrents") {
						wantCore++
					}
				}
			}
			if len(output.Data.Candidates) != wantRows || int(nativeCalls.Load()-beforeCore) != wantCore {
				t.Fatal("actual dict bypass/list repeated native/reset differs", sample.Name, len(output.Data.Candidates), nativeCalls.Load()-beforeCore)
			}
			afterSDK := read(ownedModuleID)
			if afterSDK.Calls["module_execute"]-beforeSDK.Calls["module_execute"] != 1 || afterSDK.Calls["module_fail"] != beforeSDK.Calls["module_fail"] {
				t.Fatal("actual non-stream entry used wrong source extension")
			}
			var input struct{ Args, Kwargs json.RawMessage }
			_ = json.Unmarshal(afterSDK.Inputs["module_execute"], &input)
			want := bytes.ReplaceAll(sample.Observations[0].Kwargs, []byte(oracle.Site["url"].(string)), []byte(native.URL))
			if string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, want) {
				t.Fatal("actual source batch four kwargs differ")
			}
			for _, secret := range []string{"owned-plugin-secret", "owned-private-material", "enclosure"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("actual non-stream batch private data exposed")
				}
			}
			t.Logf("actual main non-stream batch %s SDK%d: source map/list branch, %d output rows/%d native HTTP calls and exact ordered sites/keyword/mtype/page", sample.Name, afterSDK.PID, wantRows, wantCore)
		}
		configure(ownedModuleID, "module_execute", result, true)
	}
	if os.Getenv("PLUGIN_RESOURCE_FILTER_GATE") == "true" {
		raw, err := os.ReadFile(os.Getenv("PLUGIN_RESOURCE_FILTER_SOURCE_ORACLE"))
		var reference titleFilterSourceOracle
		if err != nil || json.Unmarshal(raw, &reference) != nil || len(reference.Production) != 3 {
			t.Fatal("actual source two-model title-filter replay required", err)
		}
		setRules := func(configuration priorityrules.Configuration) {
			t.Helper()
			for _, section := range []string{"custom", "groups", "defaults"} {
				var current struct {
					Data struct{ Revisions map[string]int64 }
				}
				if json.Unmarshal(request("GET", "/api/settings/rules", nil), &current) != nil {
					t.Fatal("actual rule revision read failed")
				}
				value := ruleSection(configuration, section)
				var saved struct {
					Data struct{ Value json.RawMessage }
				}
				want, _ := json.Marshal(value)
				if json.Unmarshal(request("PUT", "/api/settings/rules/"+section, map[string]any{"revision": current.Data.Revisions[section], "value": value}), &saved) != nil || !originalBaseJSONEqual(saved.Data.Value, want) {
					t.Fatal("actual rule settings save/readback differs", section)
				}
			}
		}
		expression := "OwnedAny"
		for _, sample := range reference.Production {
			configuration := priorityrules.Configuration{SearchGroups: []string{"owned-rule"}, Mode: priorityrules.Rust}
			if sample.Name != "mutate_then_fail" {
				configuration.Groups = []priorityrules.Group{{Name: "owned-rule", RuleString: &expression}}
				configuration.CustomRules = []priorityrules.Definition{{"id": "OwnedAny", "name": "Owned any", "include": ".*"}}
			}
			setRules(configuration)
			success("PUT", "/api/plugins/"+ownedModuleID, map[string]any{"enabled": true, "modules": map[string]string{"filter_torrents": "module_filter"}, "filter_mode": sample.Name})
			beforeSDK, beforeCore := read(ownedModuleID), nativeCalls.Load()
			streamHolding.Store(true)
			raw = request("POST", "/api/search/resources", map[string]any{"query": "Owned keyword", "page": 2})
			streamHolding.Store(false)
			var output struct{ Data SearchOutput }
			if json.Unmarshal(raw, &output) != nil || nativeCalls.Load()-beforeCore != 1 {
				t.Fatal("actual filter host source query did not execute")
			}
			want, got := map[int]int{}, map[int]int{}
			for _, row := range sample.Returned {
				want[int(row["pri_order"].(float64))]++
			}
			for _, row := range output.Data.Candidates {
				got[row.Resource.PriorityOrder]++
			}
			if len(output.Data.Candidates) != len(sample.Returned) || len(want) != len(got) {
				t.Fatal("actual filter copy/alias/failed mutation output differs", sample.Name, got)
			}
			for priority, count := range want {
				if got[priority] != count {
					t.Fatal("actual model alias conflated with an equal copy", sample.Name, got)
				}
			}
			afterSDK := read(ownedModuleID)
			var input struct {
				Args, Kwargs json.RawMessage
				Refs         []int `json:"title_torrent_refs"`
			}
			_ = json.Unmarshal(afterSDK.Inputs["module_filter"], &input)
			var kwargs map[string]json.RawMessage
			_ = json.Unmarshal(input.Kwargs, &kwargs)
			if afterSDK.Calls["module_filter"]-beforeSDK.Calls["module_filter"] != 1 || string(input.Args) != "[]" || len(input.Refs) != 2 || input.Refs[0] != 0 || input.Refs[1] != 1 || len(kwargs) != 3 || string(kwargs["mediainfo"]) != "null" {
				t.Fatal("actual source title-filter DTO/independent row identity missing")
			}
			for _, secret := range []string{"owned-site-cookie", "site_cookie", "title_torrent_refs", "originalFilterModel"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("actual filter response exposed private models/refs")
				}
			}
			t.Logf("actual main title filter %s SDK%d rows%d priorities%v: real settings revisions/source HTTP, explicit independent input refs, existing native core and failure-before-mutation propagation", sample.Name, afterSDK.PID, len(output.Data.Candidates), got)
		}
		setRules(priorityrules.Configuration{Mode: priorityrules.Rust})
		configure(ownedModuleID, "module_execute", result, true)
	}
	if os.Getenv("PLUGIN_RESOURCE_BYTES_GATE") == "true" {
		reference := readResourceBytesOracle(t, os.Getenv("PLUGIN_RESOURCE_BYTES_SOURCE_ORACLE"))
		for _, sample := range reference.Cases {
			method := map[string]string{"browse": "async_refresh_torrents", "batch": "async_batch_search_torrents", "title": "filter_torrents"}[sample.Entry]
			handler := "module_execute"
			if sample.Entry == "title" {
				handler = "module_filter"
				var settings struct {
					Data struct{ Revisions map[string]int64 }
				}
				_ = json.Unmarshal(request("GET", "/api/settings/rules", nil), &settings)
				request("PUT", "/api/settings/rules/defaults", map[string]any{"revision": settings.Data.Revisions["defaults"], "value": ruleSection(priorityrules.Configuration{Mode: priorityrules.Rust, SearchGroups: []string{"owned-rule"}}, "defaults")})
			}
			success("PUT", "/api/plugins/"+ownedModuleID, map[string]any{"enabled": true, "modules": map[string]string{method: handler}, "result": sample.Plugin})
			beforeSDK, beforeCore := read(ownedModuleID), nativeCalls.Load()
			var raw json.RawMessage
			if sample.Entry == "browse" {
				raw = request("POST", "/api/sites/manage/"+site.ID+"/resources", map[string]any{"query": "", "page": 2})
			} else {
				streamHolding.Store(true)
				if sample.Entry == "title" && sample.Name == "full_bytes" {
					// Source title caller keeps truthy bytes unchanged. The native
					// typed public resource boundary rejects these, not fake models.
					body := strings.NewReader(`{"query":"Owned keyword","page":2}`)
					r, _ := http.NewRequest("POST", origin+"/api/search/resources", body)
					r.Header.Set("Content-Type", "application/json")
					response, err := client.Do(r)
					if err != nil {
						t.Fatal("actual truthy bytes public projection", err)
					}
					raw, _ = io.ReadAll(response.Body)
					response.Body.Close()
					if response.StatusCode != 400 || !bytes.Contains(raw, []byte("PLUGIN_MODULE_RESULT_INVALID")) {
						t.Fatal("truthy title bytes forged typed resources", response.StatusCode)
					}
				} else {
					raw = request("POST", "/api/search/resources", map[string]any{"query": "Owned keyword", "page": 2})
				}
				streamHolding.Store(false)
			}
			var output struct{ Data SearchOutput }
			_ = json.Unmarshal(raw, &output)
			wantRows, wantCore := 0, int32(0)
			if sample.Entry == "batch" {
				var returned []json.RawMessage
				_ = json.Unmarshal(sample.Returned, &returned)
				wantRows, wantCore = len(returned), 1
			}
			if sample.Entry == "title" {
				wantCore = 1 // Real site HTTP precedes the terminal filter bytes.
			}
			if len(output.Data.Candidates) != wantRows || nativeCalls.Load()-beforeCore != wantCore {
				t.Fatal("actual bytes falsey/type/fallback differs", sample.Entry, sample.Name, len(output.Data.Candidates), nativeCalls.Load()-beforeCore)
			}
			if sample.Entry == "browse" && (len(output.Data.Sites) != 1 || (output.Data.Sites[0].Status == "failed") != (sample.Error != nil)) {
				t.Fatal("browse bytes source endpoint failure lost")
			}
			afterSDK := read(ownedModuleID)
			if afterSDK.Calls[handler]-beforeSDK.Calls[handler] != 1 {
				t.Fatal("actual typed bytes SDK callback not invoked once")
			}
			t.Logf("actual main typed bytes %s/%s SDK%d rows%d nativeHTTP%d: terminal scalar identity, consumer falsey/type and source legacy fallback", sample.Entry, sample.Name, afterSDK.PID, wantRows, wantCore)
		}
		var settings struct {
			Data struct{ Revisions map[string]int64 }
		}
		_ = json.Unmarshal(request("GET", "/api/settings/rules", nil), &settings)
		request("PUT", "/api/settings/rules/defaults", map[string]any{"revision": settings.Data.Revisions["defaults"], "value": ruleSection(priorityrules.Configuration{Mode: priorityrules.Rust}, "defaults")})
		configure(ownedModuleID, "module_execute", result, true)
	}
	if os.Getenv("PLUGIN_MEDIA_RESOURCE_GATE") == "true" {
		raw, err := os.ReadFile(os.Getenv("PLUGIN_MEDIA_FILTER_SOURCE_ORACLE"))
		var reference struct {
			Production struct {
				Input json.RawMessage `json:"module_input"`
			} `json:"production_model"`
		}
		if err != nil || json.Unmarshal(raw, &reference) != nil || len(reference.Production.Input) == 0 {
			t.Fatal("actual fixed complete MediaInfo sample required", err)
		}
		var sourceMedia map[string]json.RawMessage
		_ = json.Unmarshal(reference.Production.Input, &sourceMedia)
		if string(sourceMedia["title"]) != `"Owned keyword"` || string(sourceMedia["type"]) != `"电影"` {
			t.Fatal("owned main media sample differs from source model")
		}
		for _, entry := range []string{"web", "stream"} {
			method := "async_batch_search_torrents"
			value := result
			if entry == "stream" {
				method = "async_worker_search_site"
			} else {
				value, _ = json.Marshal(map[string]json.RawMessage{"1": result})
			}
			success("PUT", "/api/plugins/"+ownedModuleID, map[string]any{"enabled": true, "modules": map[string]string{method: "module_execute", "async_recognize_media": "module_page_size", "recognize_media": "module_fail"}, "result": value, "page_size": reference.Production.Input})
			beforeSDK, beforeCore := read(ownedModuleID), nativeCalls.Load()
			mediaHolding.Store(true)
			body := map[string]any{"media_type": "movie", "tmdb_id": 900043}
			var output SearchOutput
			if entry == "web" {
				var response struct{ Data SearchOutput }
				if json.Unmarshal(request("POST", "/api/search", body), &response) != nil {
					t.Fatal("actual main media HTTP response")
				}
				output = response.Data
			} else {
				encoded, _ := json.Marshal(body)
				r, _ := http.NewRequest("POST", origin+"/api/search", bytes.NewReader(encoded))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Accept", "text/event-stream")
				response, err := client.Do(r)
				if err != nil {
					t.Fatal("actual main media SSE", err)
				}
				raw, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode != 200 || !bytes.Contains(raw, []byte("event: complete\n")) || bytes.Contains(raw, []byte("event: error\n")) {
					t.Fatal("actual main media SSE did not complete", response.StatusCode)
				}
				for _, frame := range strings.Split(string(raw), "\n\n") {
					if strings.HasPrefix(frame, "event: complete\ndata: ") {
						_ = json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: complete\ndata: ")), &output)
					}
				}
			}
			mediaHolding.Store(false)
			wantRows, wantCore := 1, int32(0)
			if entry == "stream" {
				wantRows, wantCore = 3, 1
			}
			if len(output.Candidates) != wantRows || nativeCalls.Load()-beforeCore != wantCore {
				t.Fatal("actual main media module/native results differ", entry, len(output.Candidates), nativeCalls.Load()-beforeCore)
			}
			afterSDK := read(ownedModuleID)
			if afterSDK.Calls["module_execute"]-beforeSDK.Calls["module_execute"] != 1 || afterSDK.Calls["module_page_size"]-beforeSDK.Calls["module_page_size"] != 1 || afterSDK.Calls["module_fail"] != beforeSDK.Calls["module_fail"] {
				t.Fatal("actual main media module caller name/order/fill differs")
			}
			var call struct{ Kwargs map[string]json.RawMessage }
			_ = json.Unmarshal(afterSDK.Inputs["module_execute"], &call)
			if !originalBaseJSONEqual(call.Kwargs["mtype"], sourceMedia["type"]) || !originalBaseJSONEqual(call.Kwargs["keyword"], sourceMedia["title"]) || string(call.Kwargs["page"]) != "0" {
				t.Fatal("actual main media source type/title/page kwargs differ")
			}
			call.Kwargs = nil // json.Unmarshal merges into a non-nil map; this is a different real RPC frame.
			_ = json.Unmarshal(afterSDK.Inputs["module_page_size"], &call)
			if len(call.Kwargs) != 10 || string(call.Kwargs["cache"]) != "true" || string(call.Kwargs["meta"]) != "null" || string(call.Kwargs["mediaid"]) != "null" {
				t.Fatal("actual main Web search used subscription-add recognition contract")
			}
			t.Logf("actual main media %s SDK%d rows%d/nativeHTTP%d: complete actual source MediaInfo, async recognition cachetrue/metaNone/requestedIDNone, type-aware batch/worker and raw-result stop", entry, afterSDK.PID, wantRows, wantCore)
		}
		if os.Getenv("PLUGIN_MEDIA_PROGRESS_GATE") == "true" {
			// Actual first-page native rows must cross the separate production
			// HTTP listener while the second owned response is still blocked.
			success("PUT", "/api/plugins/"+ownedModuleID, map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_execute", "get_search_page_size": "module_page_size"}, "search_results": map[string]json.RawMessage{"": reference.Production.Input}, "page_size": 2})
			beforeSDK, beforeCore := read(ownedModuleID), nativeCalls.Load()
			mediaHolding.Store(true)
			mediaProgressHolding.Store(true)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, _ := http.NewRequestWithContext(ctx, "POST", origin+"/api/search", strings.NewReader(`{"media_type":"movie","tmdb_id":900043}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "text/event-stream")
			response, err := client.Do(r)
			if err != nil {
				t.Fatal("actual production progressive media SSE", err)
			}
			first, complete, event := false, false, ""
			scanner := bufio.NewScanner(response.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "event: ") {
					event = strings.TrimPrefix(line, "event: ")
				}
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var output SearchOutput
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &output) != nil {
					t.Fatal("actual production media SSE frame")
				}
				if !first && len(output.Candidates) == 2 && event != "complete" {
					select {
					case <-mediaProgressRelease:
						t.Fatal("owned next page was already released")
					default:
					}
					first = true
					mediaProgressReleaseOnce.Do(func() { close(mediaProgressRelease) })
				}
				if event == "complete" {
					complete = len(output.Candidates) == 2
				}
			}
			response.Body.Close()
			cancel()
			mediaProgressHolding.Store(false)
			mediaHolding.Store(false)
			if scanner.Err() != nil || !first || !complete || nativeCalls.Load()-beforeCore != 2 {
				t.Fatal("actual production first-page SSE waited for next response", scanner.Err(), first, complete, nativeCalls.Load()-beforeCore)
			}
			afterSDK := read(ownedModuleID)
			if afterSDK.Calls["module_execute"]-beforeSDK.Calls["module_execute"] != 1 || afterSDK.Calls["module_page_size"]-beforeSDK.Calls["module_page_size"] != 2 {
				t.Fatal("actual production media page getters/recognition repeated")
			}
			t.Log("actual production progressive media SSE first two rows precede blocked next response; final source dedup two rows/nativeHTTP2/capacity2calls")
		}
		if os.Getenv("PLUGIN_MEDIA_SUBSCRIPTION_SCOPE_GATE") == "true" {
			verifyOwnedResourceSubscriptionScope(t, dir, bootstrapSettings.Sites[1], request, success, stop, start, func() ownedModuleSnapshot {
				v := read(ownedModuleID)
				return ownedModuleSnapshot{PID: v.PID, Calls: v.Calls, Inputs: v.Inputs}
			}, &nativeCalls)
		}
		configure(ownedModuleID, "module_execute", result, true)
	}
	success("POST", "/api/plugins/"+ownedModuleID+"/clone", map[string]string{"suffix": "Resource"})
	cloneID := ownedModuleID + "resource"
	cloneResult := json.RawMessage(strings.ReplaceAll(string(result), "Owned plugin resource", "Owned clone resource"))
	configure(cloneID, "module_execute", cloneResult, true)
	clone := read(cloneID)
	if clone.PID == after.PID {
		t.Fatal("source and clone share actual process")
	}
	check([]string{"Owned plugin resource", "Owned clone resource"}, 1)
	if os.Getenv("PLUGIN_RESOURCE_DISPLAY_ORDER_GATE") == "true" {
		// Source user PluginOrder controls cards; PluginManager's already-loaded
		// object order still controls ordinary module execution. Verify both
		// persisted order and actual source/clone output on the same main.
		var organization struct{ Data PluginOrganization }
		if json.Unmarshal(request("GET", "/api/plugins/organization", nil), &organization) != nil {
			t.Fatal("actual card organization missing")
		}
		beforeSource, beforeClone := read(ownedModuleID), read(cloneID)
		reversed := []map[string]any{{"id": cloneID, "type": "plugin", "order": 0}, {"id": ownedModuleID, "type": "plugin", "order": 1}}
		var saved struct{ Data PluginOrganization }
		if json.Unmarshal(request("PUT", "/api/plugins/organization", map[string]any{"revision": organization.Data.Revision, "folders": organization.Data.Folders, "order": reversed}), &saved) != nil || saved.Data.Revision <= organization.Data.Revision {
			t.Fatal("actual reversed card order was not persisted")
		}
		want, _ := json.Marshal(reversed)
		if !originalBaseJSONEqual(saved.Data.Order, want) {
			t.Fatal("actual saved card order differs")
		}
		check([]string{"Owned plugin resource", "Owned clone resource"}, 1)
		afterSource, afterClone := read(ownedModuleID), read(cloneID)
		if beforeSource.PID != afterSource.PID || beforeClone.PID != afterClone.PID || beforeSource.Calls["init"] != afterSource.Calls["init"] || beforeClone.Calls["init"] != afterClone.Calls["init"] {
			t.Fatal("display reorder reinitialized executable object")
		}
		t.Log("actual reversed durable card order leaves same source/clone PIDs/init and source-first ordinary module execution")
	}
	configure(ownedModuleID, "module_execute", json.RawMessage(`false`), true)
	clone = read(cloneID)
	check(nil, 0)
	if read(cloneID).Calls["module_execute"] != clone.Calls["module_execute"] {
		t.Fatal("false scalar did not stop subsequent clone")
	}
	configure(ownedModuleID, "module_fail", result, true)
	check([]string{"Owned clone resource"}, 1)
	configure(ownedModuleID, "module_execute", result, false)
	check([]string{"Owned clone resource"}, 1)
	beforeCold := read(cloneID)
	stop()
	start()
	afterCold := read(cloneID)
	if afterCold.PID == beforeCold.PID || process.Process.Pid == mainPID {
		t.Fatal("actual cold SDK/main did not change process")
	}
	check([]string{"Owned clone resource"}, 1)
	if statePath := os.Getenv("PLUGIN_RESOURCE_BROWSER_STATE"); statePath != "" {
		browserHolding.Store(true)
		state, _ := json.Marshal(map[string]any{"origin": origin, "username": "resource-module-acceptance", "password": "owned-resource-password", "data_dir": dir, "site_id": site.ID, "source_id": ownedModuleID, "clone_id": cloneID, "clone_pid": afterCold.PID, "native_requests_before_hold": nativeCalls.Load(), "page": 2, "entry": "/#sites", "scope": "owned source remains disabled; real enabled clone override precedes two owned NexusPHP rows; .done resumes uninstall and shutdown"})
		if err := os.WriteFile(statePath, state, 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("owned resource browser ready", statePath)
		until := time.Now().Add(12 * time.Minute)
		for {
			if _, err := os.Stat(statePath + ".done"); err == nil {
				break
			}
			if time.Now().After(until) {
				t.Fatal("owned resource browser hold expired")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	success("DELETE", "/api/plugins/"+cloneID, nil)
	check(nil, 1)
	success("DELETE", "/api/plugins/"+ownedModuleID, nil)
	check(nil, 1)
	t.Logf("actual Linux main %d→%d, source SDK%d and clone SDK%d→%d: async source kwargs/getters, list accumulation, falsey scalar cutoff, source failure/clone continuation, disabled source, cold recovery, uninstall/native fallback and public credential isolation", mainPID, process.Process.Pid, after.PID, beforeCold.PID, afterCold.PID)
	stop()
}
