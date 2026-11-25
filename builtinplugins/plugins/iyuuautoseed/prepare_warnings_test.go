package iyuuautoseed

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPreparationWarningsAggregateSiteStageAndSeeds(t *testing.T) {
	c := &Core{}
	c.beginPreparationWarnings()
	logs := []SourceLog{}
	ctx := WithSourceLogSink(context.Background(), func(_ context.Context, entry SourceLog) {
		logs = append(logs, entry)
	})
	reason := errors.New("invalid source indexer response")
	c.recordPreparationWarning("tr", &seedPreparationError{stage: "读取站点配置", site: "example.org", hash: "aaaaaaaaaaaaaaaaaaaa", cause: reason})
	c.recordPreparationWarning("tr", &seedPreparationError{stage: "读取站点配置", site: "example.org", hash: "bbbbbbbbbbbbbbbbbbbb", cause: reason})
	c.recordPreparationWarning("tr", &seedPreparationError{stage: "读取站点配置", site: "other.example", hash: "cccccccccccccccccccc", cause: reason})
	c.flushPreparationWarnings(ctx)

	if len(logs) != 2 {
		t.Fatalf("preparation warnings were not grouped: %+v", logs)
	}
	first := logs[0]
	if first.Level != "WARN" || !strings.Contains(first.Message, "下载器 tr") || !strings.Contains(first.Message, "站点 example.org") ||
		!strings.Contains(first.Message, "失败 2 颗") || !strings.Contains(first.Message, "aaaaaaaaaaaa") || !strings.Contains(first.Message, "bbbbbbbbbbbb") ||
		!strings.Contains(first.Message, "原因：站点索引返回格式无效") {
		t.Fatalf("grouped warning lost downloader, site, seeds or reason: %+v", first)
	}
	if strings.Contains(first.Message, "cccccccccccc") {
		t.Fatalf("different sites were merged: %+v", first)
	}
	if !strings.Contains(logs[1].Message, "other.example") {
		t.Fatalf("second site warning missing: %+v", logs[1])
	}
	c.flushPreparationWarnings(ctx)
	if len(logs) != 2 {
		t.Fatalf("flush must clear the run scope: %+v", logs)
	}
}

func TestPreparationWarningReasonHidesPrivatePayloads(t *testing.T) {
	reason := seedFailureReason(errors.New("source indexer call https://private.example/api?passkey=secret failed"))
	if strings.Contains(reason, "private.example") || strings.Contains(reason, "passkey") || strings.Contains(reason, "secret") {
		t.Fatalf("private payload leaked into the warning: %q", reason)
	}
	if reason != "宿主返回未识别错误（详情已隐藏）" {
		t.Fatalf("arbitrary error text must not be surfaced: %q", reason)
	}
	if got := seedFailureReason(errors.New("PLUGIN_CAPABILITY_DENIED: private detail")); got != "插件缺少站点读取权限" {
		t.Fatalf("known host code was not classified: %q", got)
	}
	if got := seedFailureReason(errors.New("E_HOST_BUDGET: x")); got != "宿主错误 E_HOST_BUDGET" {
		t.Fatalf("unknown host code was not extracted: %q", got)
	}
	if got := seedFailureReason(nil); got != "" {
		t.Fatalf("nil error must not add a reason: %q", got)
	}
}
