package app

import (
	"archive/zip"
	"bufio"
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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// This gate is independent of the shared lifecycle test: a real production
// cmd/server owns the manager, SDK callback, disk logger, HTTP and SSE routes.
// Only the loopback repository and owned Go worker are synthetic. The pinned
// source basis is log.py:494, system.py:360/398/953, PluginLogDialog.vue:38 and
// LoggingView.vue:398/537; JSON storage/resume are existing host adaptations.
func TestPluginLogsProductionCLI(t *testing.T) {
	serverBinary, workerBinary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY"), os.Getenv("OWNED_INSTANCE_LOG_PROCESS_BINARY")
	if serverBinary == "" {
		t.Skip("unverified: actual production server required")
	}
	if workerBinary == "" {
		t.Fatal("actual separately built process-log SDK worker required")
	}
	for _, key := range []string{"LOG_MAX_FILE_SIZE", "LOG_BACKUP_COUNT"} {
		invalid := exec.Command(serverBinary, "--data-dir", t.TempDir())
		invalid.Env = append(os.Environ(), key+"=owned-invalid-integer")
		output, err := invalid.CombinedOutput()
		if err == nil || !bytes.Contains(output, []byte("invalid_plugin_log_rotation")) {
			t.Fatal("actual CLI accepted invalid source integer setting", key)
		}
	}
	payload, err := os.ReadFile(workerBinary)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := exec.Command(workerBinary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(manifestRaw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedInstanceLog" || len(manifest.APIs) != 3 {
		t.Fatal("wrong actual log SDK variant")
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
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
	token := hex.EncodeToString(secret[:])
	bootstrap, _ := json.Marshal(map[string]any{"username": "plugin-log-acceptance", "password": "owned-log-browser-password", "settings": Settings{APIToken: token, PluginMarkets: []string{repository.URL + "/index"}}})
	bootstrapCommand := exec.Command(serverBinary, "--data-dir", dir, "--bootstrap-stdin")
	bootstrapCommand.Stdin = bytes.NewReader(bootstrap)
	if output, err := bootstrapCommand.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("bootstrap_complete")) {
		t.Fatal("owned log bootstrap failed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := "http://" + address
	logFile, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 12 * time.Second, Jar: jar}
	var process *exec.Cmd
	var rotationEnvironment []string
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
				t.Error("owned log shutdown failed", err)
			}
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("owned log shutdown timed out")
		}
		process = nil
	}
	defer stop()
	request := func(method, path string, body any, want int) ([]byte, http.Header) {
		t.Helper()
		var input io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			input = bytes.NewReader(raw)
		}
		req, _ := http.NewRequest(method, origin+path, input)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-KEY", token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("owned log actual request", method, path, err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err != nil || response.StatusCode != want {
			t.Fatalf("owned log %s %s status=%d expected=%d; body withheld", method, path, response.StatusCode, want)
		}
		return raw, response.Header
	}
	start := func() {
		t.Helper()
		process = exec.Command(serverBinary, "--listen", address, "--data-dir", dir)
		process.Env = append(os.Environ(), rotationEnvironment...)
		process.Stdout, process.Stderr = logFile, logFile
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(12 * time.Second)
		for {
			response, err := client.Get(origin + "/api/auth/status")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("owned production log listener unavailable")
			}
			time.Sleep(25 * time.Millisecond)
		}
		request("POST", "/api/auth/login", map[string]string{"username": "plugin-log-acceptance", "password": "owned-log-browser-password"}, 200)
	}
	success := func(method, path string, body any) {
		t.Helper()
		raw, _ := request(method, path, body, 200)
		var result struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &result) != nil || !result.Data.Success {
			t.Fatal("owned log management effect rejected", method, path)
		}
	}
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": "OwnedInstanceLog", "repo_url": repository.URL + "/index"})
	probe := func(id string) int {
		t.Helper()
		raw, _ := request("GET", "/api/v1/plugin/"+id+"/probe", nil, 200)
		var value struct {
			PID           int
			InitCount     int  `json:"init_count"`
			StartupDenied bool `json:"startup_denied"`
		}
		if json.Unmarshal(raw, &value) != nil || value.PID <= 0 || value.PID == os.Getpid() || value.InitCount <= 0 || value.StartupDenied {
			t.Fatal("actual log SDK PID/callback missing")
		}
		return value.PID
	}
	sourcePID := probe("OwnedInstanceLog")
	success("POST", "/api/plugins/OwnedInstanceLog/clone", map[string]string{"suffix": "twin", "name": "Owned log twin"})
	clonePID := probe("OwnedInstanceLogtwin")
	if sourcePID == clonePID {
		t.Fatal("log clone did not own independent worker")
	}
	emit := func(id, level, message string) {
		t.Helper()
		raw, _ := request("POST", "/api/v1/plugin/"+id+"/write", plugins.LogEntryV1{Level: plugins.LogLevel(level), Message: message}, 200)
		var value struct{ PID int }
		if json.Unmarshal(raw, &value) != nil || value.PID <= 0 {
			t.Fatal("write did not reach actual SDK")
		}
	}
	emit("OwnedInstanceLog", "INFO", "owned source first\nTraceback (owned):\n  continuation <literal>")
	emit("OwnedInstanceLog", "ERROR", "owned source error")
	emit("OwnedInstanceLog", "WARNING", "owned source warning")
	emit("OwnedInstanceLogtwin", "INFO", "owned twin private")
	history := func(id string) ([]map[string]any, string) {
		t.Helper()
		raw, _ := request("GET", "/api/plugins/"+id+"/logs", nil, 200)
		var page struct {
			Data struct {
				History struct{ Entries []map[string]any }
				Resume  string `json:"resume_id"`
			}
		}
		if json.Unmarshal(raw, &page) != nil || page.Data.Resume == "" {
			t.Fatal("actual production log history missing")
		}
		return page.Data.History.Entries, page.Data.Resume
	}
	for _, instance := range []string{"OwnedInstanceLog", "OwnedInstanceLogtwin"} {
		raw, _ := request("POST", "/api/v1/plugin/"+instance+"/notice", map[string]any{"title": "owned host scope failure", "channel": "Telegram", "immediately": true, "plugin_instance_id": "forged"}, 200)
		var dispatch plugins.NotificationDispatch
		if json.Unmarshal(raw, &dispatch) != nil || dispatch.Attempted != 1 || dispatch.Failed != 0 || len(dispatch.Errors) != 0 || dispatch.Queued != 0 {
			t.Fatal("source empty module callback became a sender failure")
		}
		canonical, _ := request("GET", "/api/logs?plugin_instance_id="+instance+"&from=0001-01-01T00:00:00Z&limit=100", nil, 200)
		var canonicalPage struct{ Data logPage }
		if json.Unmarshal(canonical, &canonicalPage) != nil {
			t.Fatal("actual canonical scope history invalid")
		}
		entries := canonicalPage.Data.Entries
		count := 0
		for _, entry := range entries {
			if entry["event"] == "notification_failed" {
				count++
				if entry["plugin_instance_id"] != instance || entry["plugin_call_id"] == "" || entry["operation"] != "http" {
					t.Fatal("actual synchronous host log lost concrete caller")
				}
			}
		}
		if count != 0 {
			t.Fatal("empty source module callback invented a host error", instance, count)
		}
	}
	rows, boundary := history("OwnedInstanceLog")
	hasFirst := false
	for _, row := range rows {
		if row["plugin_instance_id"] != "OwnedInstanceLog" || strings.Contains(row["message"].(string), "twin private") {
			t.Fatal("actual source log leaked sibling")
		}
		hasFirst = hasFirst || strings.Contains(row["message"].(string), "owned source first")
	}
	if !hasFirst {
		t.Fatal("real SDK callback not written to disk history")
	}
	emit("OwnedInstanceLogtwin", "INFO", "owned twin after boundary")
	emit("OwnedInstanceLog", "INFO", "owned source after boundary")
	streamRequest, _ := http.NewRequest("GET", origin+"/api/plugins/OwnedInstanceLog/logs/stream?last_event_id="+url.QueryEscape(boundary), nil)
	stream, err := client.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != 200 || !strings.Contains(stream.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("actual SSE unavailable")
	}
	reader := bufio.NewReader(stream.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("actual SDK SSE missing", err)
		}
		if strings.HasPrefix(line, "data:") {
			if !strings.Contains(line, "owned source after boundary") || strings.Contains(line, "twin after boundary") {
				t.Fatal("actual SDK SSE identity changed")
			}
			break
		}
	}
	stream.Body.Close()
	plain, headers := request("GET", "/api/plugins/OwnedInstanceLog/logs/file", nil, 200)
	if !strings.Contains(headers.Get("Content-Type"), "text/plain") || !bytes.Contains(plain, []byte("owned source first")) || bytes.Contains(plain, []byte("twin private")) {
		t.Fatal("actual full text not isolated")
	}
	archive, headers := request("GET", "/api/plugins/OwnedInstanceLog/logs/download", nil, 200)
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || headers.Get("Content-Type") != "application/zip" || len(z.File) != 1 || !strings.HasSuffix(z.File[0].Name, "/ownedinstancelog.log") {
		t.Fatal("actual archive layout/type invalid")
	}
	f, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil || !bytes.Contains(raw, []byte("owned source first")) || bytes.Contains(raw, []byte("twin private")) {
		t.Fatal("actual archive mixed instance rows")
	}
	// Preseed only this disposable source file to the fixed threshold, then
	// make the real SDK writer perform a real 5MiB rollover. This is synthetic
	// size preparation plus actual production behavior, not a real outage.
	projection := filepath.Join(dir, "logs", "plugins", "ownedinstancelog.log")
	seed, err := os.OpenFile(projection, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = seed.Truncate(pluginProjectionMaxBytes - 1); err != nil {
		t.Fatal(err)
	}
	seed.Close()
	emit("OwnedInstanceLog", "INFO", "owned actual rollover")
	plain, _ = request("GET", "/api/plugins/OwnedInstanceLog/logs/file", nil, 200)
	if !bytes.Contains(plain, []byte("owned actual rollover")) || bytes.Contains(plain, []byte("owned source first")) {
		t.Fatal("actual full-text did not select current rotated file")
	}
	rotated, _ := request("GET", "/api/plugins/OwnedInstanceLog/logs/download", nil, 200)
	retained, err := zip.NewReader(bytes.NewReader(rotated), int64(len(rotated)))
	if err != nil || len(retained.File) != 2 || !strings.HasSuffix(retained.File[0].Name, "/ownedinstancelog.log") || !strings.HasSuffix(retained.File[1].Name, "/ownedinstancelog.log.1") {
		t.Fatal("actual rollover did not keep independent archived files")
	}
	if probe("OwnedInstanceLog") != sourcePID || probe("OwnedInstanceLogtwin") != clonePID {
		t.Fatal("log reading restarted actual workers")
	}
	stop()
	smallCurrent, err := os.ReadFile(projection)
	if err != nil {
		t.Fatal(err)
	}
	beforeBackup, err := os.ReadFile(projection + ".1")
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []struct{ name, max, backups string }{
		{"max-zero", "0", "3"}, {"backup-zero", "1", "0"}, {"max-negative", "-1", "3"},
	} {
		// Only this owned file is size-seeded; source .1 and the actual SDK
		// writer prove that zero/negative settings append without discarding it.
		if err = os.WriteFile(projection, smallCurrent, 0600); err != nil {
			t.Fatal(err)
		}
		seed, err := os.OpenFile(projection, os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = seed.Truncate((1 << 20) - 1)
		seed.Close()
		if err != nil {
			t.Fatal(err)
		}
		rotationEnvironment = []string{"LOG_MAX_FILE_SIZE=" + configured.max, "LOG_BACKUP_COUNT=" + configured.backups}
		start()
		emit("OwnedInstanceLog", "INFO", "owned configured "+configured.name)
		plain, _ = request("GET", "/api/plugins/OwnedInstanceLog/logs/file", nil, 200)
		if !bytes.Contains(plain, []byte("owned actual rollover")) || !bytes.Contains(plain, []byte("owned configured "+configured.name)) {
			t.Fatal("actual configured append lost current lines", configured.name)
		}
		backup, err := os.ReadFile(projection + ".1")
		if err != nil || !bytes.Equal(beforeBackup, backup) {
			t.Fatal("actual zero/negative configuration replaced existing backup", configured.name)
		}
		stop()
	}
	rotationEnvironment = nil
	// Remove only synthetic sparse padding before the browser-ready phase.
	if err = os.WriteFile(projection, smallCurrent, 0600); err != nil {
		t.Fatal(err)
	}
	start()
	nextSource, nextClone := probe("OwnedInstanceLog"), probe("OwnedInstanceLogtwin")
	if nextSource == sourcePID || nextClone == clonePID || nextSource == nextClone {
		t.Fatal("actual cold worker recovery missing")
	}
	plain, _ = request("GET", "/api/plugins/OwnedInstanceLog/logs/file", nil, 200)
	if !bytes.Contains(plain, []byte("owned actual rollover")) || !bytes.Contains(plain, []byte("owned close")) {
		t.Fatal("cold process lost actual retained/shutdown log")
	}
	anonymous := &http.Client{Timeout: 3 * time.Second}
	for _, suffix := range []string{"", "/file", "/download", "/stream"} {
		response, err := anonymous.Get(origin + "/api/plugins/OwnedInstanceLog/logs" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("unauthenticated log access accepted", suffix, response.StatusCode)
		}
	}
	emit("OwnedInstanceLog", "INFO", "owned source first\nTraceback (owned):\n  continuation <literal>")
	emit("OwnedInstanceLog", "ERROR", "owned source error")
	emit("OwnedInstanceLog", "WARNING", "owned source warning")
	t.Logf("actual production log chain verified: source PID%d→%d, clone PID%d→%d, SDK write/synchronous host error→disk/history/SSE/current text/independent ZIP/actual5MiB rollover, zero/negative CLI append and existing backup retention, invalid integer startup rejection, cold retention, scope and unauthenticated rejection; no public service", sourcePID, nextSource, clonePID, nextClone)
	if statePath := os.Getenv("PLUGIN_PROCESS_LOG_BROWSER_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "repository": repository.URL, "data_dir": dir})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("write owned log browser state")
		}
		t.Log("production log browser ready; waiting for owned completion file")
		deadline := time.Now().Add(35 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".emit"); err == nil {
				emit("OwnedInstanceLogtwin", "INFO", "owned twin live private")
				emit("OwnedInstanceLog", "INFO", "owned browser live\nowned browser continuation")
				_ = os.Remove(statePath + ".emit")
				_ = os.WriteFile(statePath+".emit.done", []byte("actual SDK emitted"), 0600)
			}
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				before := process.Process.Pid
				stop()
				start()
				result, _ := json.Marshal(map[string]int{"previous_pid": before, "current_pid": process.Process.Pid})
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", result, 0600)
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("owned log browser did not complete")
		}
	}
	success("DELETE", "/api/plugins/OwnedInstanceLogtwin", nil)
	success("DELETE", "/api/plugins/OwnedInstanceLog", nil)
	t.Log("owned log fixture uninstalled; isolated server shuts down")
}
