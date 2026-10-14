package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"light-media/internal/integrations"
)

func TestCLITraceChild(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_TRACE_CLI_CHILD") != "1" {
		return
	}
	main()
}

func TestRealCLIErrorConnectsReportAndHTTPLogs(t *testing.T) {
	for _, kind := range []string{"http400", "redirect", "transport"} {
		t.Run(kind, func(t *testing.T) {
			const secret = "sensitive_alias_123"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "http400":
					w.WriteHeader(400)
					w.Write([]byte(secret + " raw private response"))
				case "redirect":
					w.Header().Set("Location", "https://evil.invalid/"+secret)
					w.WriteHeader(302)
				case "transport":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						conn.Close()
					}
				}
			}))
			defer server.Close()
			input := integrations.Input{Downloaders: []integrations.Downloader{{ID: secret, Kind: "qbittorrent", BaseURL: server.URL + "/" + secret, Username: "private_username", Password: secret}}}
			body, _ := json.Marshal(input)
			cmd := exec.Command(os.Args[0], "-test.run=^TestCLITraceChild$")
			cmd.Env = append(os.Environ(), "LIGHT_MEDIA_TRACE_CLI_CHILD=1")
			cmd.Stdin = bytes.NewReader(body)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			exitErr, ok := err.(*exec.ExitError)
			if !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("expected CLI business failure: %v\n%s", err, stdout.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), secret) || strings.Contains(stderr.String(), "private_username") || strings.Contains(stderr.String(), server.URL) || strings.Contains(stderr.String(), "evil.invalid") {
				t.Fatal("CLI secret or URL leaked")
			}
			var report map[string]any
			if err = json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			runID, _ := report["run_id"].(string)
			if runID == "" {
				t.Fatal("report run ID absent")
			}
			foundHTTP, foundFailedRun := false, false
			for _, line := range bytes.Split(bytes.TrimSpace(stderr.Bytes()), []byte{'\n'}) {
				var event map[string]any
				if err = json.Unmarshal(line, &event); err != nil {
					t.Fatalf("stderr is not JSONL: %s", line)
				}
				if event["run_id"] != runID {
					t.Fatal("CLI log/run summary correlation missing")
				}
				if event["event"] == "http_completed" {
					foundHTTP = true
					if event["request_id"] == nil || event["target_id"] == nil {
						t.Fatal("request/target IDs absent")
					}
				}
				if event["event"] == "run_completed" && event["status"] == "failed" {
					foundFailedRun = true
				}
			}
			if !foundHTTP || !foundFailedRun {
				t.Fatal("CLI failure trace incomplete")
			}
		})
	}
}
