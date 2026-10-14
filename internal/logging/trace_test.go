package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type traceRoundTrip func(*http.Request) (*http.Response, error)

func (f traceRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func traceLines(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		records = append(records, value)
	}
	return records
}

func TestHTTPTraceHasGeneratedCorrelationAndNeverLogsRequestData(t *testing.T) {
	for _, status := range []int{400, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var logs, summary bytes.Buffer
			r, err := NewRun(&logs)
			if err != nil {
				t.Fatal(err)
			}
			ctx, targetID := Target(r.Context(context.Background()), "material")
			const secret = "private_test_secret"
			req, err := http.NewRequestWithContext(ctx, "POST", "https://example.invalid/"+secret+"?api_key="+secret, strings.NewReader(secret))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Cookie", secret)
			req.Header.Set("Authorization", "Bearer "+secret)
			client := http.Client{Transport: Transport(traceRoundTrip(func(received *http.Request) (*http.Response, error) {
				if received.Header.Get("Cookie") != secret {
					t.Fatal("tracing changed HTTP request")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://evil.invalid/" + secret}}, Body: io.NopCloser(strings.NewReader(secret))}, nil
			})), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != secret {
				t.Fatal("tracing changed response payload")
			}
			response.Body.Close()
			if err = r.Report(&summary, map[string]any{"status": "incomplete", "id": secret}, "incomplete", secret); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String()+summary.String(), secret) || strings.Contains(logs.String(), "example.invalid") || strings.Contains(logs.String(), "evil.invalid") {
				t.Fatal("secret/URL leaked")
			}
			records := traceLines(t, logs.Bytes())
			var startID, endID string
			for _, record := range records {
				if record["run_id"] != r.ID {
					t.Fatal("run correlation missing")
				}
				switch record["event"] {
				case "http_started":
					startID, _ = record["request_id"].(string)
				case "http_completed":
					endID, _ = record["request_id"].(string)
					if record["target_id"] != targetID || record["http_status"] != float64(status) || record["method"] != "POST" {
						t.Fatalf("bad completion: %+v", record)
					}
				}
			}
			if startID == "" || startID != endID {
				t.Fatal("request start/completion correlation missing")
			}
			var out map[string]any
			if err = json.Unmarshal(summary.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out["run_id"] != r.ID || out["id"] != "redacted_target" {
				t.Fatalf("bad summary: %+v", out)
			}
		})
	}
}

func TestTransportErrorUsesFixedCode(t *testing.T) {
	var logs, summary bytes.Buffer
	r, err := NewRun(&logs)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(r.Context(context.Background()), "GET", "https://example.invalid/secret?token=secret", nil)
	wireErr := errors.New("raw transport https://example.invalid/secret?token=secret")
	_, actualErr := Transport(traceRoundTrip(func(*http.Request) (*http.Response, error) { return nil, wireErr })).RoundTrip(req)
	if actualErr != wireErr {
		t.Fatal("tracing replaced caller error")
	}
	if err = r.Report(&summary, map[string]any{"status": "transport_error"}, "transport_error"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "example.invalid") || strings.Contains(logs.String(), "token=secret") {
		t.Fatal("raw transport error leaked")
	}
	found := false
	for _, record := range traceLines(t, logs.Bytes()) {
		if record["event"] == "http_completed" {
			found = true
			if record["error_code"] != "transport_error" {
				t.Fatal("missing fixed code")
			}
		}
	}
	if !found {
		t.Fatal("missing completion")
	}
}

func TestSemanticOperationAndFixedBusinessCode(t *testing.T) {
	var logs, summary bytes.Buffer
	r, _ := NewRun(&logs)
	ctx, targetID := Target(r.Context(context.Background()), "fnos_experiment")
	ctx = EndpointContext(ctx, "fnos", "/item/private-guid-not-to-log")
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.invalid/?token=not_to_log", nil)
	resp, err := Transport(traceRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("body"))}, nil
	})).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err = r.Report(&summary, map[string]any{"status": "failed", "code": "ITEM_PARENT_MISMATCH", "trace_target_id": targetID}, "failed"); err != nil {
		t.Fatal(err)
	}
	foundHTTP, foundBusiness := false, false
	for _, record := range traceLines(t, logs.Bytes()) {
		if record["event"] == "http_completed" {
			foundHTTP = true
			if record["operation"] != "fnos_item_detail" {
				t.Fatal("semantic endpoint category missing")
			}
		}
		if record["event"] == "run_completed" {
			foundBusiness = true
			if record["error_code"] != "ITEM_PARENT_MISMATCH" {
				t.Fatal("HTTP200 business error missing from log")
			}
		}
	}
	if !foundHTTP || !foundBusiness || strings.Contains(logs.String(), "private-guid") || strings.Contains(logs.String(), "not_to_log") {
		t.Fatal("operation/code trace missing or raw data leaked")
	}
	for _, unknown := range []string{"untrusted_secret_code", "HTTP_123456", "credentials:private_value"} {
		if fixedSummaryCode(unknown) != "" {
			t.Fatal("unknown free-text code admitted")
		}
	}
}

func TestAcceptedOrPreparedActionsAreNotMisreportedAsFailed(t *testing.T) {
	for status, want := range map[string]string{"prepared": "prepared", "scan_accepted": "accepted", "diagnosed": "diagnosed"} {
		var logs, summary bytes.Buffer
		r, _ := NewRun(&logs)
		if err := r.Report(&summary, map[string]any{"status": status}, status); err != nil {
			t.Fatal(err)
		}
		for _, record := range traceLines(t, logs.Bytes()) {
			if record["event"] == "run_completed" && (record["status"] != want || record["error_code"] != "none") {
				t.Fatalf("%s misreported: %+v", status, record)
			}
		}
	}
}

func TestRunsDoNotShareSinkOrIDs(t *testing.T) {
	var left, right bytes.Buffer
	l, _ := NewRun(&left)
	r, _ := NewRun(&right)
	var wg sync.WaitGroup
	for _, run := range []*Run{l, r} {
		wg.Add(1)
		go func(run *Run) {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(run.Context(context.Background()), "GET", "https://example.invalid/", nil)
			response, _ := Transport(traceRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
			})).RoundTrip(req)
			response.Body.Close()
		}(run)
	}
	wg.Wait()
	if l.ID == r.ID || strings.Contains(left.String(), r.ID) || strings.Contains(right.String(), l.ID) {
		t.Fatal("concurrent runs contaminated one another")
	}
}

type failedTraceWriter struct{}

func (failedTraceWriter) Write([]byte) (int, error) { return 0, errors.New("sink_failed") }

func TestTraceSinkFailureAndBudgetVisibleInSummary(t *testing.T) {
	r, err := NewRun(failedTraceWriter{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = r.Report(&out, map[string]any{"status": "passed"}, "passed"); err == nil {
		t.Fatal("failed sink reported as healthy")
	}
	if !strings.Contains(out.String(), `"failed":2`) || !strings.Contains(out.String(), `"status":"failed"`) {
		t.Fatal("logging failure absent from stdout")
	}
	var logs bytes.Buffer
	r, _ = NewRun(&logs)
	for i := 0; i < 2000; i++ {
		r.record(context.Background(), 0, "synthetic_event", nil)
	}
	out.Reset()
	if err = r.Report(&out, map[string]any{"status": "passed"}, "passed"); err == nil {
		t.Fatal("exhausted sink reported as healthy")
	}
	if logs.Len() > RunByteBudget || r.handler.Stats().Dropped == 0 {
		t.Fatal("trace budget not enforced")
	}
}
