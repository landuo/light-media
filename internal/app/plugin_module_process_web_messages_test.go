package app

import (
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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// A real cmd/server owns HTTP/auth/history/SQLite/ordinary command dispatch and
// the SDK callback. Only executable plugins, repository and HTTP recipient are
// owned fixtures. No UI echo, user queue, external channel or AI route is used.
func TestPluginWebMessagesProductionCLI(t *testing.T) {
	serverBinary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY")
	if serverBinary == "" {
		t.Skip("unverified: independent production server required")
	}
	type fixture struct {
		payload []byte
		index   artifacts.Index
	}
	fixtures := map[string]*fixture{}
	for _, sample := range []struct{ id, env string }{{"OwnedInput", "OWNED_WEB_INPUT_BINARY"}, {"OwnedNotice", "OWNED_WEB_NOTICE_BINARY"}, {"OwnedInstanceLog", "OWNED_WEB_CALLER_BINARY"}} {
		path := os.Getenv(sample.env)
		if path == "" {
			t.Fatal("actual separately built worker required", sample.env)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := exec.Command(path, "--manifest").Output()
		if err != nil {
			t.Fatal(err)
		}
		var m plugins.Manifest
		if json.Unmarshal(raw, &m) != nil || m.Validate() != nil || m.PluginID != sample.id {
			t.Fatal("wrong actual message fixture", sample.id)
		}
		sum := sha256.Sum256(payload)
		digest := hex.EncodeToString(sum[:])
		fixtures[sample.id] = &fixture{payload: payload, index: artifacts.Index{SchemaVersion: 1, PluginID: m.PluginID, CurrentVersion: m.Version, Releases: []artifacts.Release{{Version: m.Version, Manifest: m, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}}
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		f := fixtures[parts[0]]
		if f == nil {
			http.NotFound(w, r)
			return
		}
		if parts[1] == "index" {
			json.NewEncoder(w).Encode(f.index)
			return
		}
		if parts[1] == "worker" {
			w.Write(f.payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer repository.Close()
	for id, f := range fixtures {
		f.index.Releases[0].Artifacts[0].URL = repository.URL + "/" + id + "/worker"
	}
	var recipientMu sync.Mutex
	received := []string{}
	recipient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Kwargs struct {
				Message struct {
					Title string `json:"title"`
				} `json:"message"`
			} `json:"kwargs"`
		}
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&call) != nil {
			t.Error("actual sender request missing")
			w.WriteHeader(400)
			return
		}
		recipientMu.Lock()
		received = append(received, call.Kwargs.Message.Title)
		recipientMu.Unlock()
		w.WriteHeader(204)
	}))
	defer recipient.Close()
	countReceived := func() int { recipientMu.Lock(); defer recipientMu.Unlock(); return len(received) }
	dir := t.TempDir()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(random[:])
	bootstrap, _ := json.Marshal(map[string]any{"username": "web-message-acceptance", "password": "owned-web-message-password", "settings": Settings{APIToken: token, PluginMarkets: []string{repository.URL + "/OwnedInput/index", repository.URL + "/OwnedNotice/index", repository.URL + "/OwnedInstanceLog/index"}}})
	boot := exec.Command(serverBinary, "--data-dir", dir, "--bootstrap-stdin")
	boot.Stdin = bytes.NewReader(bootstrap)
	if output, err := boot.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("bootstrap_complete")) {
		t.Fatal("owned web bootstrap failed")
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
	logFile, err := os.Create(filepath.Join(dir, "owned-web-server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	var process *exec.Cmd
	stop := func() {
		t.Helper()
		if process == nil {
			return
		}
		process.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Error("owned web shutdown failed", err)
			}
		case <-time.After(10 * time.Second):
			process.Process.Kill()
			<-done
			t.Error("owned web shutdown timed out")
		}
		process = nil
	}
	defer stop()
	request := func(method, path string, value any, want int) []byte {
		t.Helper()
		var body io.Reader
		if value != nil {
			raw, _ := json.Marshal(value)
			body = bytes.NewReader(raw)
		}
		r, _ := http.NewRequest(method, origin+path, body)
		r.Header.Set("Content-Type", "application/json")
		if strings.HasPrefix(path, "/api/v1/plugin/") {
			r.Header.Set("X-API-KEY", token)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("owned web actual request", method, path, err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err != nil || response.StatusCode != want {
			t.Fatalf("owned web %s %s status=%d expected=%d; body withheld", method, path, response.StatusCode, want)
		}
		return raw
	}
	start := func() {
		t.Helper()
		process = exec.Command(serverBinary, "--listen", address, "--data-dir", dir)
		process.Stdout, process.Stderr = logFile, logFile
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(12 * time.Second)
		for {
			r, err := client.Get(origin + "/healthz")
			if err == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("owned web listener unavailable")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	success := func(method, path string, value any) {
		t.Helper()
		raw := request(method, path, value, 200)
		var result struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &result) != nil || !result.Data.Success {
			t.Fatal("actual web management effect rejected", method, path)
		}
	}
	history := func() []map[string]any {
		t.Helper()
		raw := request("GET", "/api/message/web?size=1", nil, 200)
		var out struct{ Data []map[string]any }
		if json.Unmarshal(raw, &out) != nil {
			t.Fatal("actual web history invalid")
		}
		return out.Data
	}
	inputEvents := func() (int, int) {
		t.Helper()
		raw := request("GET", "/api/v1/plugin/OwnedInput/events", nil, 200)
		var out struct {
			PID    int
			Events []json.RawMessage
		}
		if json.Unmarshal(raw, &out) != nil || out.PID <= 0 {
			t.Fatal("actual input process missing")
		}
		return out.PID, len(out.Events)
	}
	createInput := func() {
		request("POST", "/api/v1/plugin/OwnedInput/input", map[string]any{"operation": "create", "input": map[string]any{"user_id": "web-message-acceptance", "channel": "Web", "source": "web-message-acceptance", "payload": map[string]any{"owned": true}, "timeout_seconds": 120}}, 200)
	}
	post := func(title string, save bool, failed bool) {
		t.Helper()
		raw := request("POST", "/api/v1/plugin/OwnedInstanceLog/notice", map[string]any{"channel": "Web", "title": title, "text": "owned ordinary body", "userid": "web-message-acceptance", "save_history": save}, 200)
		var out plugins.NotificationDispatch
		if json.Unmarshal(raw, &out) != nil || out.Attempted != 1 || (out.Failed > 0) != failed {
			t.Fatal("actual SDK notice dispatch result differs", out)
		}
	}
	start()
	mainPID := process.Process.Pid
	request("POST", "/api/auth/login", map[string]string{"username": "web-message-acceptance", "password": "owned-web-message-password"}, 200)
	if len(history()) != 0 {
		t.Fatal("fresh web history was not empty")
	}
	for _, id := range []string{"OwnedInput", "OwnedNotice", "OwnedInstanceLog"} {
		success("POST", "/api/plugins/install", map[string]string{"plugin_id": id, "repo_url": repository.URL + "/" + id + "/index"})
	}
	success("PUT", "/api/plugins/OwnedInput", map[string]any{"enabled": true})
	success("PUT", "/api/plugins/OwnedNotice", map[string]any{"enabled": true, "endpoint": recipient.URL})
	inputPID, _ := inputEvents()
	request("POST", "/api/message/web", map[string]any{"text": "/owned", "user": "forged", "source": "forged"}, 200)
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, events := inputEvents()
		if events >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Web command never reached actual SDK")
		}
		time.Sleep(25 * time.Millisecond)
	}
	rows := history()
	if len(rows) != 1 || rows[0]["action"] != float64(0) || rows[0]["text"] != "/owned" || rows[0]["userid"] != "web-message-acceptance" {
		t.Fatal("actual source action0/account history missing")
	}
	firstID := rows[0]["id"]
	createInput()
	request("POST", "/api/message/web", map[string]string{"text": "owned consumed answer"}, 200)
	if len(history()) != 1 {
		t.Fatal("pending input reply was stored unlike source")
	}
	createInput()
	beforeCancel := countReceived()
	request("POST", "/api/message/web", map[string]string{"text": "quit"}, 200)
	if len(history()) != 1 || countReceived() != beforeCancel+1 {
		t.Fatal("source cancel feedback was not delivered or created fake history")
	}
	post("owned outbound stored", true, false)
	if len(history()) != 2 {
		t.Fatal("actual SDK outbound history absent")
	}
	beforeHidden := countReceived()
	post("owned hidden prompt", false, false)
	if len(history()) != 2 || countReceived() != beforeHidden+1 {
		t.Fatal("save_history=false was not delivered/invisible as source")
	}
	success("PUT", "/api/plugins/OwnedNotice", map[string]any{"enabled": true, "endpoint": recipient.URL, "fail": true})
	beforeFailed := countReceived()
	// The source dispatcher consumes sender exceptions; prove the failed call
	// still executes and is logged, in addition to checking saved history below.
	beforeLog, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	beforeFailures := bytes.Count(beforeLog, []byte(`"event":"module_execution_failed"`))
	post("owned failed delivery stored", true, false)
	afterLog, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(afterLog, []byte(`"event":"module_execution_failed"`)) != beforeFailures+1 {
		t.Fatal("failed SDK sender was not invoked and logged exactly once")
	}
	if len(history()) != 3 || countReceived() != beforeFailed {
		t.Fatal("source history-before-failed-delivery order changed")
	}
	// One controlled client proves the source role parameter is ignored. No UI
	// consumer is running; this is not a second production/MessageView drain.
	r, _ := http.NewRequest("GET", origin+"/api/system/message?role=user", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	stream, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != 200 || !strings.Contains(stream.Header.Get("Content-Type"), "text/event-stream") {
		stream.Body.Close()
		t.Fatal("actual shared FIFO unavailable", stream.StatusCode)
	}
	reader := bufio.NewReader(stream.Body)
	found := false
	for i := 0; i < 12; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			stream.Body.Close()
			t.Fatal("shared FIFO frame missing")
		}
		if strings.HasPrefix(line, "data: ") && strings.TrimSpace(strings.TrimPrefix(line, "data: ")) != "" {
			var value map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &value) != nil || value["type"] != "plugin" {
				stream.Body.Close()
				t.Fatal("role=user became a separate/forged channel")
			}
			found = true
			break
		}
	}
	stream.Body.Close()
	if !found {
		t.Fatal("actual module error did not reach shared FIFO")
	}
	success("PUT", "/api/plugins/OwnedNotice", map[string]any{"enabled": true, "endpoint": recipient.URL})
	post("owned recovered invisible", false, false)
	beforeCold := countReceived()
	stop()
	start()
	nextMain := process.Process.Pid
	nextInput, _ := inputEvents()
	rows = history()
	if len(rows) != 3 || nextMain == mainPID || nextInput == inputPID {
		t.Fatal("cold actual process/history recovery failed")
	}
	present := false
	for _, row := range rows {
		present = present || row["id"] == firstID
	}
	if !present || countReceived() != beforeCold {
		t.Fatal("cold restart changed message identity or resent completed notice")
	}
	anonymous := &http.Client{Timeout: 3 * time.Second}
	response, err := anonymous.Get(origin + "/api/message/web")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("anonymous history disclosure")
	}
	t.Logf("actual production web chain: main PID%d→%d, input SDK PID%d→%d; command/input/cancel, action0 and encrypted source-column history, stored/hidden/failed SDK notices to real owned recipient, shared role=user FIFO, cold stable IDs/no resend, anonymous401 verified; no AI or user queue", mainPID, nextMain, inputPID, nextInput)
	if statePath := os.Getenv("PLUGIN_PROCESS_WEB_BROWSER_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "repository": repository.URL})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("write owned web browser state")
		}
		t.Log("ordinary web browser ready; waiting for owned completion file")
		deadline := time.Now().Add(35 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".down"); err == nil {
				stop()
				os.Remove(statePath + ".down")
				os.WriteFile(statePath+".down.done", []byte("owned actual server stopped"), 0600)
			}
			if _, err := os.Stat(statePath + ".up"); err == nil {
				if process == nil {
					start()
				}
				os.Remove(statePath + ".up")
				os.WriteFile(statePath+".up.done", []byte("owned actual server started"), 0600)
			}
			if _, err := os.Stat(statePath + ".input"); err == nil {
				createInput()
				os.Remove(statePath + ".input")
				os.WriteFile(statePath+".input.done", []byte("actual SDK input created"), 0600)
			}
			if _, err := os.Stat(statePath + ".emit"); err == nil {
				payload := map[string]any{"title": "owned browser outgoing", "text": "owned ordinary body"}
				if raw, err := os.ReadFile(statePath + ".emit"); err != nil {
					t.Fatal(err)
				} else if len(bytes.TrimSpace(raw)) > 0 {
					if json.Unmarshal(raw, &payload) != nil || payload == nil {
						t.Fatal("owned browser notice JSON invalid")
					}
				}
				payload["channel"], payload["userid"], payload["save_history"] = "Web", "web-message-acceptance", true
				var delivered plugins.NotificationDispatch
				if json.Unmarshal(request("POST", "/api/v1/plugin/OwnedInstanceLog/notice", payload, 200), &delivered) != nil || delivered.Attempted != 1 || delivered.Failed != 0 {
					t.Fatal("owned browser SDK notice was not actually delivered")
				}
				os.Remove(statePath + ".emit")
				os.WriteFile(statePath+".emit.done", []byte("actual SDK stored notice delivered"), 0600)
			}
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				previous := 0
				if process != nil {
					previous = process.Process.Pid
				}
				stop()
				start()
				result, _ := json.Marshal(map[string]int{"previous_pid": previous, "current_pid": process.Process.Pid})
				os.Remove(statePath + ".restart")
				os.WriteFile(statePath+".restart.done", result, 0600)
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("owned web browser did not complete")
		}
	}
	if process == nil {
		start()
	}
	for _, id := range []string{"OwnedInstanceLog", "OwnedNotice", "OwnedInput"} {
		success("DELETE", "/api/plugins/"+id, nil)
	}
	t.Log("owned web fixtures uninstalled; isolated production server stops")
}
