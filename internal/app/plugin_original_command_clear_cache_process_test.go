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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"light-media/internal/catalog"
)

// Real production CLI + real authenticated HTTP + separate compiled SDK worker
// + owned disk/SQLite. No transport handler stand-in and no public provider call.
func TestOriginalClearCacheProductionProcess(t *testing.T) {
	binary := os.Getenv("OWNED_CLEAR_CACHE_SERVER_BINARY")
	if binary == "" {
		t.Fatal("actual cmd/server binary required")
	}
	index, payload := originalMarketOwnedIndex(t, "OWNED_CLEAR_CACHE_BINARY")
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			t.Errorf("unexpected owned repository request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	var deliveries atomic.Int32
	recipient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Kwargs struct {
				Message struct {
					Title, Channel, Source, UserID string
					SaveHistory                    bool `json:"save_history"`
				}
			}
		}
		if e := json.NewDecoder(r.Body).Decode(&v); e != nil || v.Kwargs.Message.Title != "缓存清理完成！" || v.Kwargs.Message.Channel != "Web" || v.Kwargs.Message.Source != "owned-clear" || v.Kwargs.Message.UserID != "owned-clear" || v.Kwargs.Message.SaveHistory {
			t.Error("invalid real command notification")
		}
		deliveries.Add(1)
		w.WriteHeader(204)
	}))
	defer recipient.Close()
	dir := t.TempDir()
	cacheDir := t.TempDir()
	cacheFile := filepath.Join(cacheDir, "cache.value")
	keepFile := filepath.Join(cacheDir, "business.value")
	if e := os.WriteFile(cacheFile, []byte("owned disposable cache"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(keepFile, []byte("owned keep"), 0600); e != nil {
		t.Fatal(e)
	}
	var entropy [32]byte
	if _, e := rand.Read(entropy[:]); e != nil {
		t.Fatal(e)
	}
	token := hex.EncodeToString(entropy[:])
	raw, _ := json.Marshal(map[string]any{"username": "owned-clear", "password": "owned-clear-temporary-password", "settings": Settings{APIToken: token, PluginMarkets: []string{repo.URL + "/index"}}})
	env := append(os.Environ(), "PLUGIN_MARKET="+repo.URL+"/index")
	boot := exec.Command(binary, "--data-dir", dir, "--bootstrap-stdin")
	boot.Env = env
	boot.Stdin = bytes.NewReader(raw)
	if output, e := boot.CombinedOutput(); e != nil || !bytes.Contains(output, []byte("bootstrap_complete")) {
		t.Fatal("owned bootstrap failed", e)
	}
	seed, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	ownedClearSeed(t, seed)
	importCache(t, seed, "browse:movie:popular::1", catalog.Page{Results: []catalog.Media{{ID: 120, MediaType: "movie", Title: "owned cached browse"}}, Page: 1, TotalPages: 1, TotalResults: 1})
	importCache(t, seed, recommendCacheKey("tmdb_movies", 1, time.Now()), catalog.ExplorePage{Results: []catalog.ExploreMedia{{Media: catalog.Media{ID: 120, MediaType: "movie", Title: "owned kept recommendation"}, Source: "themoviedb", SourceID: "120"}}, Page: 1, TotalPages: 1, TotalResults: 1})
	if e = seed.Close(); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := "http://" + address
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	logFile, e := os.Create(filepath.Join(dir, "owned-process.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer logFile.Close()
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
				t.Error("owned graceful stop", e)
			}
		case <-time.After(15 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("owned shutdown timeout")
		}
		process = nil
	}
	defer stop()
	defer func() {
		stop()
		if destination := os.Getenv("OWNED_CLEAR_CACHE_OBSERVATIONS"); destination != "" {
			if err := os.MkdirAll(destination, 0700); err != nil {
				t.Error(err)
				return
			}
			_ = logFile.Sync()
			data, err := os.ReadFile(filepath.Join(dir, "owned-process.log"))
			if err != nil {
				t.Error(err)
				return
			}
			data = bytes.ReplaceAll(data, []byte(token), []byte("[owned-token-redacted]"))
			data = bytes.ReplaceAll(data, []byte("owned-clear-temporary-password"), []byte("[owned-password-redacted]"))
			if err = os.WriteFile(filepath.Join(destination, "server.log"), data, 0600); err != nil {
				t.Error(err)
			}
		}
	}()
	start := func() {
		t.Helper()
		process = exec.Command(binary, "--listen", address, "--data-dir", dir)
		process.Env = env
		process.Stdout, process.Stderr = logFile, logFile
		if e := process.Start(); e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			r, e := client.Get(origin + "/api/auth/status")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("actual server not ready")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	request := func(method, path string, value any, want int) []byte {
		t.Helper()
		var body io.Reader
		if value != nil {
			b, _ := json.Marshal(value)
			body = bytes.NewReader(b)
		}
		r, _ := http.NewRequest(method, origin+path, body)
		r.Header.Set("Content-Type", "application/json")
		if strings.HasPrefix(path, "/api/v1/plugin/") {
			r.Header.Set("X-API-KEY", token)
		}
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal("actual HTTP", method, path, e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if e != nil || resp.StatusCode != want {
			t.Fatalf("actual %s %s status=%d want=%d body withheld", method, path, resp.StatusCode, want)
		}
		return b
	}
	success := func(method, path string, value any) {
		t.Helper()
		raw := request(method, path, value, 200)
		var out struct{ Data struct{ Success bool } }
		if json.Unmarshal(raw, &out) != nil || !out.Data.Success {
			t.Fatal("management returned business failure", method, path)
		}
	}
	snapshot := func() ownedClearSnapshot {
		t.Helper()
		b := request("GET", "/api/v1/plugin/"+ownedClearID+"/snapshot", nil, 200)
		var out ownedClearSnapshot
		if json.Unmarshal(b, &out) != nil || out.PID == 0 {
			t.Fatal("actual worker snapshot missing")
		}
		return out
	}
	login := func() {
		request("POST", "/api/auth/login", map[string]string{"username": "owned-clear", "password": "owned-clear-temporary-password"}, 200)
	}
	start()
	hostPID := process.Process.Pid
	login()
	success("POST", "/api/plugins/install", map[string]string{"plugin_id": ownedClearID, "repo_url": repo.URL + "/index"})
	success("PUT", "/api/plugins/"+ownedClearID, map[string]any{"enabled": true, "directory": cacheDir, "endpoint": recipient.URL, "result": false})
	before := snapshot()
	if before.PID == hostPID || before.PID == os.Getpid() {
		t.Fatal("SDK is not independent process")
	}
	cached := request("GET", "/api/catalog/browse?kind=movie&list=popular", nil, 200)
	if !bytes.Contains(cached, []byte("owned cached browse")) {
		t.Fatal("real native cache consumer missed seed")
	}
	command := func() {
		raw := request("POST", "/api/v1/message/web", map[string]any{"text": "/clear_cache", "channel": "forged", "user": "forged"}, 200)
		if !bytes.Contains(raw, []byte(`"accepted":true`)) {
			t.Fatal("command not accepted")
		}
	}
	command()
	if _, e := os.Stat(cacheFile); !os.IsNotExist(e) {
		t.Fatal("actual worker did not clear its disk cache", e)
	}
	if deliveries.Load() != 1 {
		t.Fatal("actual recipient missing", deliveries.Load())
	}
	if s := snapshot(); s.Clears != 1 || s.Calls["clear"] != 1 {
		t.Fatal("actual module not called once", s)
	}
	request("GET", "/api/catalog/browse?kind=movie&list=popular", nil, 200) // false blocks native clear
	success("PUT", "/api/plugins/"+ownedClearID, map[string]any{"enabled": true, "directory": cacheDir, "endpoint": recipient.URL, "result": nil})
	command()
	// No TMDB key is configured: after real invalidation the native endpoint must
	// fail closed, not serve the old record and not contact a production API.
	miss := request("GET", "/api/catalog/browse?kind=movie&list=popular", nil, 502)
	if bytes.Contains(miss, []byte("owned cached browse")) || !bytes.Contains(miss, []byte(`"code":"NOT_CONFIGURED"`)) {
		t.Fatal("stale native cached response survived")
	}
	kept := request("GET", "/api/catalog/recommend?id=tmdb_movies", nil, 200)
	if !bytes.Contains(kept, []byte("owned kept recommendation")) {
		t.Fatal("outer recommendation cache was cleared")
	}
	if deliveries.Load() != 2 {
		t.Fatal("second actual recipient missing")
	}
	old := snapshot()
	stop()
	if e := syscall.Kill(before.PID, 0); e == nil {
		t.Fatal("old worker survived normal server stop")
	}
	check, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	ownedClearCheck(t, check, true)
	if e = check.Close(); e != nil {
		t.Fatal(e)
	}
	start()
	coldHostPID := process.Process.Pid
	login()
	next := snapshot()
	if process.Process.Pid == hostPID || next.PID == old.PID || next.Clears != old.Clears {
		t.Fatal("cold host/worker or durable count mismatch", old, next)
	}
	request("GET", "/api/catalog/browse?kind=movie&list=popular", nil, 502)
	if b, e := os.ReadFile(keepFile); e != nil || string(b) != "owned keep" {
		t.Fatal("unrelated disk content changed", e)
	}
	request("GET", "/api/catalog/recommend?id=tmdb_movies", nil, 200)
	command()
	if snapshot().Clears != 3 {
		t.Fatal("cold command chain did not run")
	}
	history := request("GET", "/api/message/web", nil, 200)
	var rows struct{ Data []map[string]any }
	if json.Unmarshal(history, &rows) != nil || len(rows.Data) != 3 {
		t.Fatal("incoming command history differs")
	}
	for _, row := range rows.Data {
		if row["action"] != float64(0) {
			t.Fatal("completion was saved despite save_history=false")
		}
	}
	stop()
	data, e := os.ReadFile(filepath.Join(dir, "owned-process.log"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(data, []byte("WARNING: DATA RACE")) {
		t.Fatal("production process race")
	}
	if destination := os.Getenv("OWNED_CLEAR_CACHE_OBSERVATIONS"); destination != "" {
		_ = os.MkdirAll(destination, 0700)
		observations, _ := json.MarshalIndent(map[string]any{"host_pid_before": hostPID, "host_pid_after": coldHostPID, "worker_pid_before": before.PID, "worker_pid_after": next.PID, "deliveries": deliveries.Load(), "commands_after_cold": 3, "native_browse_before": 200, "native_browse_after_false": 200, "native_browse_after_none": 502, "native_error": "NOT_CONFIGURED", "recommend_before_and_after": 200, "incoming_history_rows": len(rows.Data), "completion_history_rows": 0, "unrelated_file_preserved": true}, "", "  ")
		if e := os.WriteFile(filepath.Join(destination, "observations.json"), observations, 0600); e != nil {
			t.Error(e)
		}
	}
	t.Logf("actual host pid %d -> %d; worker pid %d -> %d; cache miss and kept recommendation across cold restart", hostPID, coldHostPID, before.PID, next.PID)
}
