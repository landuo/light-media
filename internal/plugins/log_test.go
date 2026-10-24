package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Serialization only. Actual process/callback and App acceptance are tested by
// the independently built owned-instance-log-fixture command, not this closure.
func TestLoggerFinitePayloadAndContext(t *testing.T) {
	calls := 0
	failure := errors.New("owned host failure")
	logger := Logger{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		var payload map[string]any
		if method != LogWriteCapability || json.Unmarshal(raw, &payload) != nil || len(payload) != 2 || payload["message"] != "owned text" {
			t.Fatal("logger invented identity or changed exact payload")
		}
		return nil, failure
	}}
	for _, level := range []LogLevel{LogDebug, LogInfo, LogWarning, LogWarn, LogError, LogCritical} {
		if err := logger.Log(context.Background(), level, "owned text"); !errors.Is(err, failure) {
			t.Fatal("host failure lost", err)
		}
	}
	if calls != 6 {
		t.Fatal(calls)
	}
	for _, entry := range []LogEntryV1{{Level: "TRACE"}, {Level: "info"}, {Level: LogInfo, Message: strings.Repeat("a", LogMessageLimit+1)}, {Level: LogInfo, Message: string([]byte{0xff})}} {
		if logger.Log(context.Background(), entry.Level, entry.Message) == nil {
			t.Fatal("invalid log accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(logger.Log(ctx, LogInfo, "owned text"), context.Canceled) {
		t.Fatal("canceled context accepted")
	}
	if logger.Log(nil, LogInfo, "owned text") == nil || (Logger{}).Log(context.Background(), LogInfo, "owned text") == nil || calls != 6 {
		t.Fatal("inactive logger made host call")
	}
	if (LogEntryV1{Level: LogInfo, Message: strings.Repeat("界", LogMessageLimit/3)}).Validate() != nil || (LogEntryV1{Level: LogInfo, Message: ""}).Validate() != nil {
		t.Fatal("bounded Unicode or explicit empty message rejected")
	}
}
