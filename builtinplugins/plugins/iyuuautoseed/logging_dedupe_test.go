package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

var errUnexpectedSourceLogCall = errors.New("unexpected host call")

func TestSourceLogDedupMergesRepeatedWarningsPerRun(t *testing.T) {
	var logs []sdk.LogEntryV1
	caller := sdk.HostCaller(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != sdk.LogWriteCapability {
			return nil, errUnexpectedSourceLogCall
		}
		var entry sdk.LogEntryV1
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, err
		}
		logs = append(logs, entry)
		return json.RawMessage(`null`), nil
	})
	bound := bindSourceLog(context.Background(), caller, "fixture-token")
	sink, ok := bound.Value(sourceLogKey{}).(SourceLogSink)
	if !ok || sink == nil {
		t.Fatal("source log sink was not bound")
	}
	warning := "站点 A 未获取站点地址，无法获取种子下载链接"
	for i := 0; i < 3; i++ {
		sink(bound, SourceLog{Level: "WARN", Method: "auto_seed", Message: warning})
	}
	sink(bound, SourceLog{Level: "WARN", Method: "auto_seed", Message: "另一条警告"})
	sink(bound, SourceLog{Level: "INFO", Method: "auto_seed", Message: "进度"})
	flushSourceLogRepeats(bound, caller)

	if len(logs) != 4 {
		t.Fatalf("repeated warning was not merged: %+v", logs)
	}
	if logs[0].Level != sdk.LogWarn || logs[0].Message != warning {
		t.Fatalf("first warning must keep the source text: %+v", logs[0])
	}
	if logs[1].Message != "另一条警告" {
		t.Fatalf("distinct warning must not be merged: %+v", logs[1])
	}
	if logs[2].Level != sdk.LogInfo || logs[2].Message != "进度" {
		t.Fatalf("ordinary progress must stay untouched: %+v", logs[2])
	}
	if logs[3].Level != sdk.LogWarn || !strings.Contains(logs[3].Message, "同类日志已合并 3 条") || !strings.Contains(logs[3].Message, warning) {
		t.Fatalf("merged count summary missing: %+v", logs[3])
	}
}

func TestSourceLogDedupIsPerInvocation(t *testing.T) {
	var logs []sdk.LogEntryV1
	caller := sdk.HostCaller(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		var entry sdk.LogEntryV1
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, err
		}
		logs = append(logs, entry)
		return json.RawMessage(`null`), nil
	})
	first := bindSourceLog(context.Background(), caller, "")
	sink, _ := first.Value(sourceLogKey{}).(SourceLogSink)
	sink(first, SourceLog{Level: "WARN", Message: "下载器 qB 未连接，请检查配置"})
	flushSourceLogRepeats(first, caller)

	second := bindSourceLog(context.Background(), caller, "")
	sink, _ = second.Value(sourceLogKey{}).(SourceLogSink)
	sink(second, SourceLog{Level: "WARN", Message: "下载器 qB 未连接，请检查配置"})
	flushSourceLogRepeats(second, caller)

	if len(logs) != 2 {
		t.Fatalf("a new invocation must log the warning again: %+v", logs)
	}
	for _, entry := range logs {
		if strings.Contains(entry.Message, "同类日志已合并") {
			t.Fatalf("single warnings must not get a merge summary: %+v", entry)
		}
	}
}
