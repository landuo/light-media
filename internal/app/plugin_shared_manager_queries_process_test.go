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
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSharedManagerQueriesProductionProcessCold(t *testing.T) {
	binary := os.Getenv("OWNED_MANAGER_SERVER_BINARY")
	if binary == "" {
		t.Fatal("actual server binary required")
	}
	dir := t.TempDir()
	markets := []string{}
	a, err := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "BETA")
	managerInstall(t, a, cookie, "ALPHA")
	managerInstall(t, a, cookie, "BROKEN")
	res := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAlpha", `{"enabled":true,"ordinary":"persisted production process","target_id":"OwnedBeta"}`, cookie, "")
	assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
	if err = a.Store.Put(context.Background(), "control", "global", map[string]string{"state": "paused"}); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	if root := os.Getenv("OWNED_MANAGER_PROCESS_EVIDENCE"); root != "" {
		if err = os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		evidence, err = os.MkdirTemp(root, "process-")
		if err != nil {
			t.Fatal(err)
		}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	var process *exec.Cmd
	var done chan error
	var baseURL string
	run := 0
	stop := func() {
		if process == nil {
			return
		}
		if err := process.Process.Signal(syscall.SIGTERM); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error("actual server stop", err)
			}
		case <-time.After(20 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("actual server stop timeout")
		}
		process = nil
	}
	defer stop()
	request := func(method, path string, input any, auth bool) (int, json.RawMessage) {
		t.Helper()
		var body io.Reader
		if input != nil {
			body = bytes.NewReader(mustCleanSeedJSON(input))
		}
		req, err := http.NewRequest(method, baseURL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.AddCookie(cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, out
	}
	start := func() {
		t.Helper()
		run++
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		_ = listener.Close()
		baseURL = "http://" + addr
		logFile, err := os.Create(filepath.Join(evidence, fmt.Sprintf("server-%d.log", run)))
		if err != nil {
			t.Fatal(err)
		}
		process = exec.Command(binary, "--listen", addr, "--data-dir", dir)
		process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + os.TempDir(), "LANG=C.UTF-8", "TZ=UTC", "PLUGIN_MARKET="}
		process.Stdout = logFile
		process.Stderr = logFile
		if err = process.Start(); err != nil {
			_ = logFile.Close()
			t.Fatal(err)
		}
		done = make(chan error, 1)
		go func(cmd *exec.Cmd) { done <- cmd.Wait(); _ = logFile.Close() }(process)
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			req, _ := http.NewRequest("GET", baseURL+"/api/v1/plugin/OwnedAlpha/snapshot", nil)
			req.AddCookie(cookie)
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == 200 {
					return
				}
			}
			select {
			case err := <-done:
				process = nil
				t.Fatal("server exited before ready", err)
			default:
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("server readiness timeout")
	}
	query := func(id, method string, target any) json.RawMessage {
		t.Helper()
		status, out := request("POST", "/api/v1/plugin/"+id+"/query", map[string]any{"method": method, "target": target}, true)
		if status != 200 {
			t.Fatal("production query", status, string(out))
		}
		return out
	}
	snapshot := func(id string) managerWorkerSnapshot {
		t.Helper()
		status, raw := request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, true)
		var out managerWorkerSnapshot
		if status != 200 || json.Unmarshal(raw, &out) != nil || !out.StaleDenied {
			t.Fatal("production snapshot", status, string(raw))
		}
		return out
	}
	start()
	hostBefore := process.Process.Pid
	before := snapshot("OwnedAlpha")
	beta := snapshot("OwnedBeta")
	managerEqual(t, query("OwnedAlpha", "ids", nil), []string{"OwnedBeta", "OwnedAlpha", "OwnedBroken"})
	managerEqual(t, query("OwnedBeta", "running", nil), []string{"OwnedBeta", "OwnedAlpha"})
	managerEqual(t, query("OwnedBeta", "config", "OwnedAlpha"), map[string]any{"enabled": true, "ordinary": "persisted production process", "target_id": "OwnedBeta"})
	if status, _ := request("POST", "/api/v1/plugin/OwnedAlpha/query", map[string]any{"method": "ids"}, false); status != 401 {
		t.Fatal("anonymous actual query", status)
	}
	status, raw := request("GET", "/api/plugins/OwnedAlpha/page", nil, true)
	if status != 200 || !bytes.Contains(raw, []byte("persisted production process")) || !bytes.Contains(raw, []byte("OwnedBroken")) {
		t.Fatal("actual plugin page missed live query", status, string(raw))
	}
	status, raw = request("POST", "/api/plugins/OwnedAlpha/clone", map[string]any{"suffix": "Copy"}, true)
	assertOriginalRegistrySuccess(t, status, raw)
	clone := snapshot("OwnedAlphacopy")
	if clone.PID == before.PID || beta.PID == before.PID || clone.PID == hostBefore {
		t.Fatal("process/clone not isolated")
	}
	managerEqual(t, query("OwnedAlphacopy", "ids", nil), []string{"OwnedBeta", "OwnedAlpha", "OwnedBroken", "OwnedAlphacopy"})
	status, raw = request("POST", "/api/plugins/OwnedBeta/reload", map[string]any{}, true)
	assertOriginalRegistrySuccess(t, status, raw)
	managerEqual(t, query("OwnedAlpha", "ids", nil), []string{"OwnedAlpha", "OwnedBroken", "OwnedAlphacopy", "OwnedBeta"})
	if syscall.Kill(beta.PID, 0) == nil {
		t.Fatal("reloaded beta left prior process")
	}
	beforeStop := snapshot("OwnedAlpha")
	stop()
	if syscall.Kill(beforeStop.PID, 0) == nil || syscall.Kill(clone.PID, 0) == nil {
		t.Fatal("host close leaked SDK process")
	}
	start()
	after := snapshot("OwnedAlpha")
	if after.PID == before.PID || process.Process.Pid == hostBefore {
		t.Fatal("not cold processes")
	}
	managerEqual(t, query("OwnedAlphacopy", "ids", nil), []string{"OwnedBeta", "OwnedAlpha", "OwnedAlphacopy", "OwnedBroken"})
	managerEqual(t, query("OwnedAlphacopy", "running", nil), []string{"OwnedBeta", "OwnedAlpha", "OwnedAlphacopy"})
	managerEqual(t, query("OwnedBeta", "config", "OwnedAlpha"), map[string]any{"enabled": true, "ordinary": "persisted production process", "target_id": "OwnedBeta"})
	if after.InitCount != 1 {
		t.Fatal("read/cold replayed config init", after.InitCount)
	}
	observed := map[string]any{"host_before": hostBefore, "host_after": process.Process.Pid, "worker_before": before.PID, "worker_after": after.PID, "clone_pid": clone.PID, "init_after_cold": after.InitCount, "cold_ids": json.RawMessage(query("OwnedAlpha", "ids", nil)), "cold_running": json.RawMessage(query("OwnedAlpha", "running", nil)), "ordinary_config": json.RawMessage(query("OwnedBeta", "config", "OwnedAlpha"))}
	data, _ := json.MarshalIndent(observed, "", "  ")
	if err = os.WriteFile(filepath.Join(evidence, "observations.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("actual process observations %s", data)
	stop()
	for n := 1; n <= run; n++ {
		log, err := os.ReadFile(filepath.Join(evidence, fmt.Sprintf("server-%d.log", n)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(log), "WARNING: DATA RACE") {
			t.Fatal("production process race")
		}
	}
}
