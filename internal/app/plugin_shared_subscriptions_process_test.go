package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
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

func TestSharedSubscriptionsActualServerProcessCold(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	cookie := setupTestAdmin(t, a)
	installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
	seedSharedSubscription(t, a, "owned-process")
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedSharedHost/clone", `{"suffix":"Process"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	// No autonomous acquisition/network jobs run in this owned fixture. The
	// server remains in normal mode to exercise actual startup plugin loading.
	if e = a.Store.Put(ctx, "control", "global", map[string]string{"state": "paused"}); e != nil {
		t.Fatal(e)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(t.TempDir(), "server")
	args := []string{"build", "-mod=readonly"}
	if os.Getenv("OWNED_SHARED_HOST_RACE") == "1" {
		args = append(args, "-race")
	}
	args = append(args, "-o", binary, "./cmd/server")
	build := exec.Command("go", args...)
	build.Dir = "../.."
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("actual server build: %v %s", e, out)
	}
	program, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("actual cmd/server build %q SHA256=%x", args, sha256.Sum256(program))
	evidence := t.TempDir()
	if base := os.Getenv("SHARED_SUBSCRIPTIONS_PROCESS_EVIDENCE_DIR"); base != "" {
		if e = os.MkdirAll(base, 0700); e != nil {
			t.Fatal(e)
		}
		evidence, e = os.MkdirTemp(base, "process-")
		if e != nil {
			t.Fatal(e)
		}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	baseURL := ""
	run := 0
	var process *exec.Cmd
	var done chan error
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
				t.Errorf("actual server exit: %v", e)
			}
		case <-time.After(15 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("owned server failed graceful stop")
		}
		process = nil
	}
	defer stop()
	request := func(instance, endpoint, method string, body any, authenticated bool) (int, json.RawMessage) {
		t.Helper()
		var payload io.Reader
		if body != nil {
			payload = bytes.NewReader(mustCleanSeedJSON(body))
		}
		r, e := http.NewRequest(method, baseURL+"/api/v1/plugin/"+instance+"/"+endpoint, payload)
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		if authenticated {
			r.AddCookie(cookie)
		}
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, b
	}
	start := func() {
		t.Helper()
		run++
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		addr := listener.Addr().String()
		_ = listener.Close()
		baseURL = "http://" + addr
		logPath := filepath.Join(evidence, fmt.Sprintf("server-%02d.log", run))
		logFile, e := os.Create(logPath)
		if e != nil {
			t.Fatal(e)
		}
		process = exec.Command(binary, "-listen", addr, "-data-dir", dir)
		process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + os.TempDir(), "LANG=C.UTF-8", "TZ=UTC", "PLUGIN_MARKET="}
		process.Stdout = logFile
		process.Stderr = logFile
		if e = process.Start(); e != nil {
			logFile.Close()
			t.Fatal(e)
		}
		done = make(chan error, 1)
		go func(cmd *exec.Cmd) { done <- cmd.Wait(); _ = logFile.Close() }(process)
		t.Logf("actual server run=%d pid=%d target=%s log=%s", run, process.Process.Pid, baseURL, logPath)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			r, _ := http.NewRequest("GET", baseURL+"/api/v1/plugin/OwnedSharedHost/snapshot", nil)
			r.AddCookie(cookie)
			response, e := client.Do(r)
			if e == nil {
				b, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode == 200 {
					t.Logf("actual HTTP worker snapshot %s", b)
					return
				}
			}
			select {
			case e := <-done:
				process = nil
				t.Fatalf("server exited before ready: %v", e)
			default:
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("owned server readiness timeout")
	}
	start()
	if status, _ := request("OwnedSharedHost", "snapshot", "GET", nil, false); status != 401 {
		t.Fatalf("normal auth not enforced: %d", status)
	}
	call := func(instance, method string, in any, want int) json.RawMessage {
		t.Helper()
		status, b := request(instance, "shared", "POST", map[string]any{"method": method, "payload": in}, true)
		if status != want {
			t.Fatalf("actual HTTP status %d want%d: %s", status, want, b)
		}
		var envelope struct {
			Result json.RawMessage `json:"result"`
			PID    int             `json:"pid"`
		}
		if e = json.Unmarshal(b, &envelope); e != nil {
			t.Fatal(e)
		}
		t.Logf("actual HTTP %s instance=%s worker_pid=%d status=%d", method, instance, envelope.PID, status)
		return envelope.Result
	}
	before := decodeSharedSubscription(t, call("OwnedSharedHost", plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-process"}, 200))
	call("OwnedSharedHost", plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{State: json.RawMessage(`"N,R"`)}, 200)
	call("OwnedSharedHostprocess", plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionTMDBV1{TMDBID: json.RawMessage(`7001`), Season: json.RawMessage(`1`)}, 200)
	want := decodeSharedSubscription(t, call("OwnedSharedHost", plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-process", Patch: json.RawMessage(`{"keyword":"real cmd/server","note":{"process":true},"sites":[999999],"state":"R","custom_words":"owned private process word"}`), ExpectedRevision: &before.Revision}, 200))
	shared := decodeSharedSubscription(t, call("OwnedSharedHostprocess", plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-process"}, 200))
	assertSharedOracleJSON(t, shared.Row, want.Row)
	call("OwnedSharedHostprocess", plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-process", Patch: json.RawMessage(`{"keyword":"stale"}`), ExpectedRevision: &before.Revision}, 422)
	db, e := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var saved []byte
	if e = db.QueryRow(`SELECT payload FROM records WHERE kind='subscription' AND id='owned-process'`).Scan(&saved); e != nil {
		t.Fatal(e)
	}
	var native Subscription
	if e = json.Unmarshal(saved, &native); e != nil || native.Keyword != "real cmd/server" || native.Revision != want.Revision {
		t.Fatal("real SQLite native row not updated", e)
	}
	if bytes.Contains(saved, []byte("owned private process word")) {
		t.Fatal("private plaintext in ordinary native record")
	}
	if _, e = db.Exec(`CREATE TRIGGER owned_process_abort BEFORE UPDATE ON records WHEN OLD.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned process failure'); END`); e != nil {
		t.Fatal(e)
	}
	call("OwnedSharedHost", plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-process", Patch: json.RawMessage(`{"keyword":"SQL must rollback","custom_words":"must rollback"}`)}, 422)
	if _, e = db.Exec(`DROP TRIGGER owned_process_abort`); e != nil {
		t.Fatal(e)
	}
	stop()
	start()
	got := decodeSharedSubscription(t, call("OwnedSharedHostprocess", plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-process"}, 200))
	assertSharedOracleJSON(t, got.Row, want.Row)
	if got.Revision != want.Revision {
		t.Fatal("process cold lost revision or SQL rollback")
	}
	stop()
}
