package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestSharedLocalStorageProductionServerCold(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "server")
	args := []string{"build", "-buildvcs=false", "-mod=readonly"}
	if os.Getenv("OWNED_LOCAL_STORAGE_RACE") == "1" {
		args = append(args, "-race")
	}
	args = append(args, "-o", binary, "./cmd/server")
	build := exec.Command("go", args...)
	build.Dir = "../.."
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("actual server build: %v %s", e, out)
	}
	t.Logf("actual server build go %q", args)
	a, cookie, _ := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	dir := a.Dir
	root := sharedLocalStorageRoot(t, a)
	path := filepath.Join(root, "process.mkv")
	if e := sharedLocalStorageFixtureWrite(t, path, []byte("owned process"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.Put(context.Background(), "control", "global", map[string]string{"state": "paused"}); e != nil {
		t.Fatal(e)
	}
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	evidence := t.TempDir()
	if base := os.Getenv("OWNED_LOCAL_STORAGE_PROCESS_EVIDENCE"); base != "" {
		if e := os.MkdirAll(base, 0700); e != nil {
			t.Fatal(e)
		}
		var e error
		evidence, e = os.MkdirTemp(base, "server-")
		if e != nil {
			t.Fatal(e)
		}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 20 * time.Second}
	defer client.CloseIdleConnections()
	var process *exec.Cmd
	var done chan error
	var base string
	starts := 0
	stop := func() {
		if process == nil {
			return
		}
		if e := process.Process.Signal(syscall.SIGTERM); e != nil {
			t.Error(e)
		}
		select {
		case e := <-done:
			if e != nil {
				t.Error("server normal shutdown", e)
			}
		case <-time.After(20 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("owned server failed to stop")
		}
		process = nil
	}
	defer stop()
	request := func(method, path string, input any, auth bool) (int, json.RawMessage, error) {
		var body io.Reader
		if input != nil {
			body = bytes.NewReader(mustCleanSeedJSON(input))
		}
		req, e := http.NewRequest(method, base+path, body)
		if e != nil {
			return 0, nil, e
		}
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.AddCookie(cookie)
		}
		resp, e := client.Do(req)
		if e != nil {
			return 0, nil, e
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(resp.Body)
		return resp.StatusCode, raw, e
	}
	query := func(id, method, path string, want string) {
		t.Helper()
		status, raw, e := request("POST", "/api/v1/plugin/"+id+"/storage", map[string]any{"method": method, "payload": plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": path})}}, true)
		var out struct {
			Result json.RawMessage `json:"result"`
		}
		if e != nil || status != 200 || json.Unmarshal(raw, &out) != nil || string(out.Result) != want {
			t.Fatal("actual server storage", e, status, string(raw), want)
		}
	}
	start := func() {
		t.Helper()
		starts++
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		address := listener.Addr().String()
		listener.Close()
		base = "http://" + address
		log, e := os.Create(filepath.Join(evidence, fmt.Sprintf("server-%d.log", starts)))
		if e != nil {
			t.Fatal(e)
		}
		process = exec.Command(binary, "--listen", address, "--data-dir", dir)
		process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + os.TempDir(), "LANG=C.UTF-8", "TZ=UTC", "PLUGIN_MARKET="}
		process.Stdout = log
		process.Stderr = log
		if e = process.Start(); e != nil {
			log.Close()
			t.Fatal(e)
		}
		done = make(chan error, 1)
		go func(cmd *exec.Cmd) { done <- cmd.Wait(); log.Close() }(process)
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			status, _, e := request("GET", "/api/settings", nil, true)
			if e == nil && status == 200 {
				return
			}
			select {
			case e := <-done:
				process = nil
				t.Fatal("server exited", e)
			default:
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("server readiness")
	}
	start()
	firstPID := process.Process.Pid
	query("OwnedLocalStorage", plugins.LocalStorageExistsCapability, path, "true")
	status, _, e := request("POST", "/api/v1/plugin/OwnedLocalStorage/storage", map[string]any{"method": plugins.LocalStorageExistsCapability, "payload": plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": path})}}, false)
	if e != nil || status != 401 {
		t.Fatal("actual anonymous route", e, status)
	}
	status, raw, e := request("POST", "/api/plugins/OwnedLocalStorage/clone", map[string]any{"suffix": "Process"}, true)
	if e != nil {
		t.Fatal(e)
	}
	assertOriginalRegistrySuccess(t, status, raw)
	query("OwnedLocalStorageprocess", plugins.LocalStorageExistsCapability, path, "true")
	query("OwnedLocalStorage", plugins.LocalStorageDeleteCapability, path, "true")
	query("OwnedLocalStorageprocess", plugins.LocalStorageExistsCapability, path, "false")
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("HTTP reported success without deletion", e)
	}
	stop()
	start()
	if process.Process.Pid == firstPID {
		t.Fatal("not a cold server")
	}
	query("OwnedLocalStorage", plugins.LocalStorageExistsCapability, path, "false")
	query("OwnedLocalStorageprocess", plugins.LocalStorageExistsCapability, path, "false")
	query("OwnedLocalStorageprocess", plugins.LocalStorageDeleteCapability, path, "true")
	stop()
	for i := 1; i <= starts; i++ {
		raw, e := os.ReadFile(filepath.Join(evidence, fmt.Sprintf("server-%d.log", i)))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(raw, []byte("WARNING: DATA RACE")) {
			t.Fatal("server race")
		}
	}
	observations := map[string]any{"starts": starts, "normal_stops": starts, "anonymous_status": 401, "removed_file_cold_absent": true, "source_missing_delete": true}
	encoded, _ := json.MarshalIndent(observations, "", "  ")
	if e := sharedLocalStorageFixtureWrite(t, filepath.Join(evidence, "observations.json"), encoded, 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("real process observations %s", encoded)
}
