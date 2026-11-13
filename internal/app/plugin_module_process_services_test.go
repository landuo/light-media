package app

import (
	"bytes"
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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Fixed Scheduler.update_plugin_job/list/start + system.runscheduler. The real
// server performs every registration/invocation. Only the SDK and its HTTP side
// effect recipient are owned fixtures; no MoviePilot/user service is touched.
func TestPluginServicesProductionCLI(t *testing.T) {
	serverBinary, workerBinary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY"), os.Getenv("OWNED_INPUT_PROCESS_FIXTURE_BINARY")
	if serverBinary == "" {
		t.Skip("unverified: actual cmd/server required")
	}
	if workerBinary == "" {
		t.Fatal("actual owned-input SDK executable required")
	}
	payload, err := os.ReadFile(workerBinary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(workerBinary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedInput" {
		t.Fatal("invalid owned-input artifact")
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	reached, release := make(chan struct{}), make(chan struct{})
	var heldOnce sync.Once
	var browserHold atomic.Bool
	var peerMu sync.Mutex
	counts := map[string]int{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/owned/observations" {
			peerMu.Lock()
			defer peerMu.Unlock()
			_ = json.NewEncoder(w).Encode(counts)
			return
		}
		peerMu.Lock()
		counts[r.URL.Path]++
		peerMu.Unlock()
		if r.URL.Path == "/held" {
			heldOnce.Do(func() { close(reached) })
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		for browserHold.Load() && r.URL.Path == "/manual" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		w.WriteHeader(204)
	}))
	defer peer.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
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
	var entropy [32]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(entropy[:])
	bootstrap, _ := json.Marshal(map[string]any{"username": "service-acceptance", "password": "owned-service-browser-password", "settings": Settings{APIToken: token, PluginMarkets: []string{repository.URL + "/index"}}})
	setup := exec.Command(serverBinary, "--data-dir", dir, "--bootstrap-stdin")
	setup.Stdin = bytes.NewReader(bootstrap)
	if output, err := setup.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("bootstrap_complete")) {
		t.Fatal("owned service bootstrap failed")
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
	defer copyOwnedEventsProcessEvidence(t, "events36-service-process.log", filepath.Join(dir, "process.log"))
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
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
				t.Error("owned service shutdown", err)
			}
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("owned service shutdown timed out")
		}
		process = nil
	}
	defer stop()
	start := func() {
		t.Helper()
		process = exec.Command(serverBinary, "--listen", address, "--data-dir", dir)
		process.Stdout, process.Stderr = log, log
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(12 * time.Second)
		for {
			response, err := client.Get(origin + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("owned service listener unavailable")
			}
			time.Sleep(20 * time.Millisecond)
		}
		credentials, _ := json.Marshal(map[string]string{"username": "service-acceptance", "password": "owned-service-browser-password"})
		response, err := client.Post(origin+"/api/auth/login", "application/json", bytes.NewReader(credentials))
		if err != nil {
			t.Fatal("owned service login unavailable")
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("owned service login rejected")
		}
	}
	request := func(method, path string, input any, want int) []byte {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(method, origin+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-KEY", token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("owned service request failed", method, path)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil || response.StatusCode != want {
			t.Fatalf("owned service %s %s status %d expected%d", method, path, response.StatusCode, want)
		}
		return raw
	}
	success := func(method, path string, input any) {
		t.Helper()
		var response struct{ Data struct{ Success bool } }
		if json.Unmarshal(request(method, path, input, 200), &response) != nil || !response.Data.Success {
			t.Fatal("owned service response not successful", method, path)
		}
	}
	progress := func(id string) map[string]json.RawMessage {
		t.Helper()
		var response struct {
			Data struct {
				Success bool
				Data    map[string]json.RawMessage
			}
		}
		if json.Unmarshal(request("GET", "/api/v1/dashboard/schedule/"+id+"/progress", nil, 200), &response) != nil || !response.Data.Success {
			t.Fatal("actual progress not available", id)
		}
		return response.Data.Data
	}
	list := func() []PluginServiceInfo {
		t.Helper()
		var response struct{ Data []PluginServiceInfo }
		if json.Unmarshal(request("GET", "/api/plugins/services", nil, 200), &response) != nil {
			t.Fatal("actual service list invalid")
		}
		return response.Data
	}
	pid := func(id string) int {
		t.Helper()
		var response struct{ PID int }
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/events", nil, 200), &response) != nil || response.PID <= 0 {
			t.Fatal("actual SDK PID missing", id)
		}
		return response.PID
	}
	definition := func(id string, seconds int, mode, path string) map[string]any {
		return map[string]any{"id": id, "name": "Owned " + id, "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": seconds * 1000}, "input": map[string]any{"mode": mode, "gate": peer.URL + path, "progress": 37, "text": "owned actual service progress"}}
	}
	configure := func(id string, defs []map[string]any) {
		t.Helper()
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "services": defs})
	}
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": "OwnedInput", "repo_url": repository.URL + "/index"})
	defs := []map[string]any{definition("a-slow", 7200, "tuple-fail", "/manual"), definition("z-fast", 3600, "list-fail", "/manual"), definition("held", 10800, "tuple-fail", "/held")}
	configure("OwnedInput", defs)
	items := list()
	if len(items) != 4 || items[0].ID != "scheduler_job" || items[1].ServiceID != "z-fast" || items[2].ServiceID != "a-slow" {
		t.Fatal("actual waiting list is not next-time sorted")
	}
	sourcePID := pid("OwnedInput")
	heldDone := make(chan bool, 1)
	go func() {
		response, err := client.Get(origin + "/api/v1/system/runscheduler?jobid=OwnedInput_held")
		if err != nil {
			heldDone <- false
			return
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		heldDone <- response.StatusCode == 200 && bytes.Contains(raw, []byte(`"success":true`))
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("manual actual SDK did not reach held side effect")
	}
	select {
	case <-heldDone:
		t.Fatal("synchronous source API returned before actual worker")
	default:
	}
	active := progress("OwnedInput_held")
	if string(active["status"]) != `"running"` || string(active["value"]) != "37" {
		t.Fatal("actual service progress not consumed")
	}
	items = list()
	if items[0].ServiceID != "held" || items[0].NextRun != nil || items[0].CanRun {
		t.Fatal("actual running row order/next-time mismatch")
	}
	request("GET", "/api/v1/system/runscheduler?jobid=OwnedInput_held", nil, 200)
	request("POST", "/api/plugins/services/run", map[string]any{"instance_id": "OwnedInput", "service_id": "held"}, 400)
	close(release)
	select {
	case ok := <-heldDone:
		if !ok {
			t.Fatal("manual completion rejected")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("manual result not returned")
	}
	done := progress("OwnedInput_held")
	if string(done["status"]) != `"failed"` || string(done["value"]) != "37" || string(done["success"]) != "false" {
		t.Fatal("tuple failure hidden by HTTP success")
	}
	peerMu.Lock()
	heldCalls := counts["/held"]
	peerMu.Unlock()
	if heldCalls != 1 {
		t.Fatal("busy no-op repeated actual callback")
	}
	// One real date-trigger tick, then remove its declaration before cold restart.
	clock := definition("clock", 3600, "tuple-fail", "/clock")
	clock["trigger"] = map[string]any{"kind": "date", "timezone": "UTC", "run_at": time.Now().Add(300 * time.Millisecond).Format(time.RFC3339Nano)}
	configure("OwnedInput", append(defs, clock))
	deadline := time.Now().Add(10 * time.Second)
	for {
		p := progress("OwnedInput_clock")
		if string(p["status"]) == `"failed"` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real process clock did not invoke SDK")
		}
		time.Sleep(20 * time.Millisecond)
	}
	peerMu.Lock()
	clockCalls := counts["/clock"]
	peerMu.Unlock()
	if clockCalls != 1 {
		t.Fatal("actual date tick side effect count differs")
	}
	configure("OwnedInput", defs)
	success("POST", "/api/plugins/OwnedInput/clone", map[string]any{"suffix": "service", "name": "Owned service clone"})
	configure("OwnedInputservice", []map[string]any{definition("clone", 3600, "tuple-fail", "/clone")})
	clonePID := pid("OwnedInputservice")
	if sourcePID == clonePID {
		t.Fatal("source and clone share worker")
	}
	request("GET", "/api/v1/system/runscheduler?jobid=OwnedInputservice_clone", nil, 200)
	if string(progress("OwnedInputservice_clone")["status"]) != `"failed"` {
		t.Fatal("clone method not invoked")
	}
	sourceBefore := progress("OwnedInput_held")
	stop()
	start()
	if pid("OwnedInput") == sourcePID || pid("OwnedInputservice") == clonePID {
		t.Fatal("cold startup retained old SDK process")
	}
	items = list()
	if len(items) != 5 {
		t.Fatal("cold startup did not rebuild actual declarations")
	}
	if string(progress("OwnedInput_held")["status"]) != `"waiting"` || string(sourceBefore["status"]) != `"failed"` {
		t.Fatal("in-memory progress did not reset while service config recovered")
	}
	request("GET", "/api/v1/system/runscheduler?jobid=missing", nil, 200)
	if os.Getenv("PLUGIN_PROCESS_SERVICE_INSTANCES_FIXTURE") == "true" {
		verifyOwnedProductionServiceInstances(t, request, success, configure, defs)
	}
	if os.Getenv("PLUGIN_PROCESS_SERVICE_ASYNC_FIXTURE") == "true" {
		verifyOwnedProductionAsyncServices(t, request, success, configure, pid, defs)
	}
	if os.Getenv("PLUGIN_PROCESS_INPUT_COMMAND_FIXTURE") == "true" {
		verifyOwnedProductionInputCommands(t, request, stop, start, pid)
	}
	if os.Getenv("PLUGIN_PROCESS_SUBSCRIPTION_ORDINARY_FIXTURE") == "true" {
		verifyOwnedProductionSubscriptionEpisodesReset(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_USER_MESSAGE_FIXTURE") == "true" {
		verifyOwnedProductionUserMessages(t, request, success)
	}
	if os.Getenv("PLUGIN_PROCESS_DOWNLOADER_CONTROL_FIXTURE") == "true" {
		verifyOwnedProductionDownloaderControl(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_DOWNLOADER_ADD_FIXTURE") == "true" {
		verifyOwnedProductionDownloaderAdd(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_DOWNLOAD_FILE_DELETED_FIXTURE") == "true" {
		verifyOwnedProductionDownloadFileDeleted(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_SITE_UPDATED_FIXTURE") == "true" {
		verifyOwnedProductionCoreSiteUpdated(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_TRANSFER_FIXTURE") == "true" {
		verifyOwnedProductionTransfer(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_DOWNLOAD_CONTEXT_FIXTURE") == "true" {
		verifyOwnedProductionDownloadContext(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_METADATA_FIXTURE") == "true" {
		verifyOwnedProductionMetadata(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_CORE_EVENT_CONSUMERS_FIXTURE") == "true" {
		verifyOwnedProductionCoreEventConsumers(t, dir, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_TRANSFER_MODULE_FIXTURE") == "true" {
		verifyOwnedProductionTransferModule(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_DIRECTORY_FIXTURE") == "true" {
		verifyOwnedProductionDirectoryCollector(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_COMPLETED_FIXTURE") == "true" {
		verifyOwnedProductionTransferCompleted(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_BLURAY_FIXTURE") == "true" {
		verifyOwnedProductionBluray(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_TRANSFER_TYPES_FIXTURE") == "true" {
		verifyOwnedProductionTransferTypes(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_CATALOG_SEASONS_FIXTURE") == "true" {
		verifyOwnedProductionCatalogSeasons(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_CATALOG_RELATED_FIXTURE") == "true" {
		verifyOwnedProductionCatalogRelated(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_BROWSER_DOWNLOAD_FIXTURE") == "true" {
		verifyOwnedProductionBrowserDownload(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_COMMON_CATEGORY_FIXTURE") == "true" {
		verifyOwnedProductionCommonCategories(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_MEDIA_EXISTS_FIXTURE") == "true" {
		verifyOwnedProductionMediaExists(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_SCRAPE_BATCH_FIXTURE") == "true" {
		verifyOwnedProductionScrapeBatch(t, dir, origin, request, success, stop, start)
	}
	if os.Getenv("PLUGIN_PROCESS_BROWSER_STORAGE_FIXTURE") == "true" {
		verifyOwnedProductionBrowserStorage(t, dir, origin, request, success, stop, start)
	}
	t.Logf("actual Linux service host: manual synchronous wait, busy no-op, 37%% progress/false tuple, real date clock count=%d, list order/running next=null, clone independent PID, cold registry/config and cleared in-memory progress", clockCalls)
	if statePath := os.Getenv("PLUGIN_PROCESS_SERVICES_BROWSER_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "peer": peer.URL, "source": "OwnedInput", "clone": "OwnedInputservice"})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("write service browser state")
		}
		t.Log("production services browser ready")
		deadline := time.Now().Add(45 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			_, err := os.Stat(statePath + ".hold")
			browserHold.Store(err == nil)
			if _, err = os.Stat(statePath + ".restart"); err == nil {
				before := process.Process.Pid
				stop()
				start()
				state, _ := json.Marshal(map[string]int{"previous_pid": before, "current_pid": process.Process.Pid})
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", state, 0600)
			}
			if _, err = os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("services browser not completed")
		}
	}
	browserHold.Store(false)
	if os.Getenv("PLUGIN_PROCESS_DOWNLOAD_CONTEXT_FIXTURE") == "true" && os.Getenv("PLUGIN_PROCESS_SERVICES_BROWSER_STATE") != "" {
		success("DELETE", "/api/plugins/OwnedTransferEvents", nil)
		request("GET", "/api/v1/plugin/OwnedTransferEvents/state", nil, 404)
	}
	if os.Getenv("PLUGIN_PROCESS_TRANSFER_FIXTURE") == "true" && os.Getenv("PLUGIN_PROCESS_SERVICES_BROWSER_STATE") != "" {
		for _, id := range []string{"OwnedTransferEventsbrowser", "OwnedTransferEvents"} {
			success("DELETE", "/api/plugins/"+id, nil)
			request("GET", "/api/v1/plugin/"+id+"/state", nil, 404)
		}
	}
	for _, id := range []string{"OwnedInputservice", "OwnedInput"} {
		success("DELETE", "/api/plugins/"+id, nil)
		request("GET", "/api/v1/plugin/"+id+"/events", nil, 404)
	}
	if items := list(); len(items) != 1 || items[0].ID != "scheduler_job" {
		t.Fatal("uninstall left real services registered")
	}
	t.Log("actual services cleanup: source+clone uninstalled, API404 and only the common system service remains")
}
