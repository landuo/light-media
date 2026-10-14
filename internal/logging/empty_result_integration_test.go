package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"light-media/internal/integrations"
	"light-media/internal/logging"
)

// Exercise the real site's bounded HTTP parser and aggregate status, rather
// than manufacturing an empty_result report that the adapter never produces.
func TestSiteEmptyResultTracePreservesBusinessSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, searchBody, cookie, resultStatus, reportStatus, targetCode, runState string
		requests                                                                   int
	}{
		{"recognized_empty", "<table class='torrents'>没有找到资源</table>", "private_session", "empty_result", "ok", "none", "ok", 2},
		{"unrecognized_empty", "<table class='torrents'></table>", "private_session", "protocol_changed", "incomplete", "protocol_changed", "failed", 2},
		{"unverified_authentication", "", "", "unverified", "incomplete", "unverified", "failed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const authHTML = "<a href='logout.php'>退出</a><a href='userdetails.php?id=1'>用户</a>"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("Cookie") != "private_session" {
					t.Error("HTTP request did not preserve the caller's authentication")
				}
				switch req.URL.Path {
				case "/index.php":
					_, _ = io.WriteString(w, authHTML)
				case "/torrents.php":
					_, _ = io.WriteString(w, authHTML+tc.searchBody)
				default:
					http.Error(w, "unexpected path", http.StatusNotFound)
				}
			}))
			defer server.Close()
			var logs, summary bytes.Buffer
			run, err := logging.NewRun(&logs)
			if err != nil {
				t.Fatal(err)
			}
			report := integrations.Run(run.Context(context.Background()), integrations.Input{Sites: []integrations.Site{{ID: "empty-site", Kind: "ubits", BaseURL: server.URL, Cookie: tc.cookie, Query: "missing film"}}})
			if report.Status != tc.reportStatus || len(report.Results) != 1 {
				t.Fatalf("unexpected aggregate semantics: %+v", report)
			}
			result := report.Results[0]
			if result.Status != tc.resultStatus || result.SampleCount != 0 || result.Requests != tc.requests {
				t.Fatalf("unexpected site evidence: %+v", result)
			}
			if err = run.Report(&summary, report, report.Status, tc.cookie); err != nil {
				t.Fatal(err)
			}
			var out struct {
				RunID  string `json:"run_id"`
				Status string `json:"status"`
			}
			if err = json.Unmarshal(summary.Bytes(), &out); err != nil || out.RunID != run.ID || out.Status != tc.reportStatus {
				t.Fatalf("summary correlation/semantics changed: %v", err)
			}
			var starts, completions, targetCompletions, runCompletions int
			for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte{'\n'}) {
				var event map[string]any
				if err = json.Unmarshal(line, &event); err != nil || event["run_id"] != out.RunID {
					t.Fatalf("invalid or uncorrelated trace: %v", err)
				}
				switch event["event"] {
				case "http_started":
					starts++
				case "http_completed":
					completions++
					if event["http_status"] != float64(200) || event["target_id"] != result.TraceTargetID {
						t.Fatal("HTTP evidence was not correlated to the actual target")
					}
				case "target_completed":
					targetCompletions++
					if event["error_code"] != tc.targetCode || event["target_id"] != result.TraceTargetID {
						t.Fatalf("target semantics changed: %+v", event)
					}
				case "run_completed":
					runCompletions++
					if event["status"] != tc.runState {
						t.Fatalf("incomplete/unverified aggregate was reported as successful: %+v", event)
					}
				}
			}
			if starts != tc.requests || completions != tc.requests || targetCompletions != 1 || runCompletions != 1 {
				t.Fatalf("unexpected trace counts: %d/%d/%d/%d", starts, completions, targetCompletions, runCompletions)
			}
			if strings.Contains(logs.String(), "private_session") || strings.Contains(logs.String(), server.URL) || strings.Contains(logs.String(), "missing film") {
				t.Fatal("authentication, URL or search query leaked into HTTP traces")
			}
		})
	}
}
