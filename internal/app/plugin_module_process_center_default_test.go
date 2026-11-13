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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Fixed config.py:426 default MP_SERVER_HOST. Safe mode prevents installation
// reports or plugin execution; the optional live probe performs GET reads only.
func TestPluginCenterDefaultProductionCLI(t *testing.T) {
	binary := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY")
	if binary == "" {
		t.Skip("unverified: actual cmd/server binary required")
	}
	dir := t.TempDir()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(random[:])
	bootstrap, _ := json.Marshal(map[string]any{"username": "plugin-default-acceptance", "password": "owned-default-read-password", "settings": Settings{APIToken: token}})
	command := exec.Command(binary, "--data-dir", dir, "--bootstrap-stdin")
	command.Stdin = bytes.NewReader(bootstrap)
	if result, err := command.CombinedOutput(); err != nil || !bytes.Contains(result, []byte("bootstrap_complete")) {
		t.Fatal("owned default bootstrap failed")
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
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 25 * time.Second, Jar: jar}
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
				t.Error("default production shutdown failed", err)
			}
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-done
			t.Error("default production shutdown timed out")
		}
		process = nil
	}
	defer stop()
	start := func() {
		t.Helper()
		process = exec.Command(binary, "--listen", address, "--data-dir", dir, "--safe")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "MP_SERVER_HOST=") {
				process.Env = append(process.Env, entry)
			}
		}
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
					credentials, _ := json.Marshal(map[string]string{"username": "plugin-default-acceptance", "password": "owned-default-read-password"})
					login, _ := http.NewRequest("POST", origin+"/api/auth/login", bytes.NewReader(credentials))
					login.Header.Set("Content-Type", "application/json")
					logged, err := client.Do(login)
					if err != nil {
						t.Fatal("owned default login failed")
					}
					logged.Body.Close()
					if logged.StatusCode != 200 {
						t.Fatal("owned default login rejected")
					}
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("default production listener unavailable")
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	request := func(method, path string, input any) []byte {
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
		req.Header.Set("X-API-KEY", token)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("owned default request failed", method, path)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil || response.StatusCode != 200 {
			t.Fatal("owned default response failed", method, path, response.StatusCode)
		}
		return raw
	}
	readAddress := func() string {
		t.Helper()
		var response struct {
			Data struct {
				Server string `json:"plugin_server_url"`
			}
		}
		if json.Unmarshal(request("GET", "/api/plugins/markets", nil), &response) != nil {
			t.Fatal("default address response invalid")
		}
		return response.Data.Server
	}
	start()
	if readAddress() != "https://movie-pilot.org" {
		t.Fatal("actual CLI did not apply fixed default without environment override")
	}
	if os.Getenv("PLUGIN_PROCESS_PUBLIC_CENTER_READ") == "true" {
		var statistics struct{ Data map[string]int64 }
		if json.Unmarshal(request("GET", "/api/plugins/statistic", nil), &statistics) != nil || len(statistics.Data) == 0 {
			t.Fatal("actual public center statistics unavailable; empty fallback is not successful live data")
		}
		positive := 0
		for _, count := range statistics.Data {
			if count > 0 {
				positive++
			}
		}
		if positive == 0 {
			t.Fatal("actual public center has no positive observed counters")
		}
		t.Logf("actual public center read through cmd/server: counters=%d positive=%d; no installation/vote POST; UID withheld", len(statistics.Data), positive)
	}
	if statePath := os.Getenv("PLUGIN_PROCESS_DEFAULT_BROWSER_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "default_center": "https://movie-pilot.org"})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("write default browser state")
		}
		t.Log("production default browser ready; waiting for owned completion file")
		deadline := time.Now().Add(30 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				before := process.Process.Pid
				stop()
				start()
				data, _ := json.Marshal(map[string]int{"previous_pid": before, "current_pid": process.Process.Pid})
				_ = os.Remove(statePath + ".restart")
				if os.WriteFile(statePath+".restart.done", data, 0600) != nil {
					t.Fatal("write actual default restart result")
				}
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("default browser did not complete")
		}
		if readAddress() != "" {
			t.Fatal("browser explicit empty center did not survive actual cold startup")
		}
	} else {
		request("PUT", "/api/plugins/markets", map[string]any{"plugin_server_url": ""})
		stop()
		start()
		if readAddress() != "" {
			t.Fatal("explicit empty center reverted to default on actual cold startup")
		}
	}
	t.Log("actual default CLI: fixed public default, explicit empty and cold settings verified; safe mode no plugin startup/reporting")
}
