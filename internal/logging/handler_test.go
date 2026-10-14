package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRedactionAndStableContext(t *testing.T) {
	var buf bytes.Buffer
	h, e := New(&buf, Options{Secrets: []string{"opaque-bare-secret", "s p a c e"}})
	if e != nil {
		t.Fatal(e)
	}
	logger := slog.New(h).With("module", "probe", "password", "attribute-secret")
	ctx := WithIDs(context.Background(), IDs{RequestID: "req-1", WorkflowID: "flow-7", JobID: "job-9"})
	logger.InfoContext(ctx, "Cookie: SID=cookie-secret; other=also-secret\npassword=pass-secret api_key=api-secret\nhttps://user:pass@host/private?token=query-secret\nBearer bearer-secret opaque-bare-secret s+p+a+c+e\nforged_record=true", slog.String("request_id", "spoofed"), slog.Any("response", map[string]string{"token": "body-secret"}), slog.Int("count", 1))
	if bytes.Count(buf.Bytes(), []byte{'\n'}) != 1 {
		t.Fatal("JSONL newline injection")
	}
	for _, secret := range []string{"cookie-secret", "also-secret", "pass-secret", "api-secret", "query-secret", "bearer-secret", "opaque-bare-secret", "s+p+a+c+e", "attribute-secret", "body-secret", "https://", "user:pass"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	var record map[string]any
	if json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record) != nil {
		t.Fatal("invalid JSON")
	}
	if record["request_id"] != "req-1" || record["workflow_id"] != "flow-7" || record["job_id"] != "job-9" {
		t.Fatal(record)
	}
	if _, ok := record["response"]; ok {
		t.Fatal("body field retained")
	}
}
func TestBudgetAndOversizedMessage(t *testing.T) {
	var buf bytes.Buffer
	h, _ := New(&buf, Options{MaxBytes: 512})
	r := slog.NewRecord(time.Now(), slog.LevelInfo, strings.Repeat("secret", 2000), 0)
	if e := h.Handle(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Fatal("oversize partial disclosure")
	}
	for i := 0; i < 20; i++ {
		_ = h.Handle(context.Background(), r)
	}
	stats := h.Stats()
	if stats.Dropped == 0 || stats.Bytes > 512 || buf.Len() > 512 {
		t.Fatal(stats)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk unavailable") }
func TestSinkFailureVisible(t *testing.T) {
	h, _ := New(failingWriter{}, Options{})
	e := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelError, "failure", 0))
	if e == nil || h.Stats().Failed != 1 {
		t.Fatal("failure hidden")
	}
}

type dangerousValue struct{ called *bool }

func (v dangerousValue) LogValue() slog.Value { *v.called = true; return slog.StringValue("secret") }
func TestUnknownValuesNotResolvedAndGroupsOmitted(t *testing.T) {
	var b bytes.Buffer
	h, _ := New(&b, Options{})
	called := false
	l := slog.New(h).With("http_body", dangerousValue{&called})
	l.WithGroup("http").Info("event", slog.String("status", "secret"))
	if called || strings.Contains(b.String(), "secret") {
		t.Fatal("evaluated unsafe data")
	}
}
func TestConcurrentRecordsAndLevel(t *testing.T) {
	var b bytes.Buffer
	h, _ := New(&b, Options{MaxBytes: 1 << 20})
	l := slog.New(h)
	l.Debug("not recorded")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.Info("event", slog.String("event", "probe.completed")) }()
	}
	wg.Wait()
	if h.Stats().Written != 40 {
		t.Fatal(h.Stats())
	}
	d := json.NewDecoder(&b)
	for i := 0; i < 40; i++ {
		var v map[string]any
		if e := d.Decode(&v); e != nil {
			t.Fatal(e)
		}
	}
	var v any
	if e := d.Decode(&v); e != io.EOF {
		t.Fatal(e)
	}
}
func TestQuotedSecretsAndInjection(t *testing.T) {
	var b bytes.Buffer
	h, _ := New(&b, Options{})
	slog.New(h).Info(`{"password":"secret with spaces","api-key":"key-value"} https://host/path?passkey=token`)
	for _, secret := range []string{"secret with spaces", "key-value", "passkey=token"} {
		if strings.Contains(b.String(), secret) {
			t.Fatal("quoted secret leaked")
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestShortWriteAndOverlappingSecrets(t *testing.T) {
	h, _ := New(shortWriter{}, Options{})
	err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "event", 0))
	if !errors.Is(err, io.ErrShortWrite) || h.Stats().Failed != 1 {
		t.Fatal("short write hidden")
	}
	var b bytes.Buffer
	h, _ = New(&b, Options{Secrets: []string{"prefix", "prefix-private-suffix"}})
	slog.New(h).Info("prefix-private-suffix")
	if strings.Contains(b.String(), "private-suffix") {
		t.Fatal("overlap leaked suffix")
	}
}
