package sitemessageautoread

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestParseConfig(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`)} {
		cfg, err := ParseConfig(raw)
		if err != nil || cfg.Scheduled() || cfg.OnlyOnce || cfg.Cron != defaultCron || len(cfg.Titles()) != 0 {
			t.Fatalf("defaults: %+v, %v", cfg, err)
		}
	}
	cfg, err := ParseConfig(json.RawMessage(`{
		"enabled":true,"sites":[" first ","","second","first"],
		"titles":"   做种提醒  \r\n\n做种提醒\nBonus *\nbonus *\n站内 信","cron":""
	}`))
	if err != nil || !cfg.Scheduled() || !slices.Equal(cfg.Sites, []string{"first", "second"}) ||
		!slices.Equal(cfg.Titles(), []string{"做种提醒", "Bonus *", "bonus *", "站内 信"}) {
		t.Fatalf("rules: %+v, %v", cfg, err)
	}
	for _, raw := range []string{
		`{"enabled":true,"sites":[],"titles":"通知"}`,
		`{"onlyonce":true,"sites":["first"],"titles":"   \n "}`,
		`{"enabled":true,"sites":["first"],"titles":"通知","cron":"99 * * * *"}`,
		`{"onlyonce":"true","sites":["first"],"titles":"通知"}`,
		`{"enabled":true,"sites":[1],"titles":"通知"}`,
		`{"titles":"` + strings.Repeat("文", 342) + `"}`,
	} {
		if _, err := ParseConfig(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid configuration %q", raw)
		}
	}
}

type fixtureHost struct {
	t             *testing.T
	saveOrigin    bool
	saved         Config
	oncePayload   json.RawMessage
	markRequests  []sdk.SiteMessagesMarkReadRequestV1
	logs          []sdk.LogEntryV1
	markResults   map[string]sdk.SiteMessagesMarkReadResultV1
	inventoryRuns int
}

func (host *fixtureHost) Call(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	host.t.Helper()
	switch method {
	case sdk.BaseConfigSaveOrigin:
		return json.Marshal(host.saveOrigin)
	case sdk.BaseUpdateConfig:
		var input struct {
			Config Config `json:"config"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		host.saved = input.Config
	case sdk.BackgroundSubmitCapability:
		var input struct {
			Method  string          `json:"method"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		if input.Method != runOnce {
			host.t.Fatalf("unexpected background method %q", input.Method)
		}
		host.oncePayload = input.Payload
	case sdk.SitesReadCapability:
		host.inventoryRuns++
		return json.Marshal(sdk.SiteHostResultV1{SchemaVersion: 1, Items: []sdk.SiteHostRowV1{
			{ID: "first", SourceID: 1, Name: "站点一", Enabled: true},
			{ID: "second", SourceID: 2, Name: "站点二", Enabled: true},
			{ID: "disabled", SourceID: 3, Name: "停用站点"},
		}})
	case sdk.SitesMessagesMarkReadCapability:
		var input sdk.SiteMessagesMarkReadRequestV1
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		host.markRequests = append(host.markRequests, input)
		if result, exists := host.markResults[input.SiteID]; exists {
			return json.Marshal(result)
		}
		return nil, errors.New("different account error")
	case sdk.LogWriteCapability:
		var entry sdk.LogEntryV1
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, err
		}
		host.logs = append(host.logs, entry)
	default:
		host.t.Fatalf("unexpected host method %q", method)
	}
	return json.RawMessage(`null`), nil
}

func invoke(t *testing.T, plugin *Plugin, host *fixtureHost, method string, raw json.RawMessage) json.RawMessage {
	t.Helper()
	result, err := plugin.Handle(t.Context(), method, raw, host.Call)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return result
}

func TestOneShotRequiresConfigurationSaveAndRunsOnce(t *testing.T) {
	plugin := New()
	host := &fixtureHost{t: t, markResults: map[string]sdk.SiteMessagesMarkReadResultV1{
		"first": {SchemaVersion: 1, Scanned: 3, Matched: 1, Marked: 1},
	}}
	config := json.RawMessage(`{"onlyonce":true,"sites":["first"],"titles":" 自定义规则 \n 自定义规则 "}`)
	invoke(t, plugin, host, "init", config)
	if len(host.oncePayload) != 0 || host.inventoryRuns != 0 {
		t.Fatal("startup replayed a pending one-shot")
	}
	host.saveOrigin = true
	invoke(t, plugin, host, "init", config)
	if len(host.oncePayload) == 0 || host.saved.OnlyOnce || host.saved.Enabled || !slices.Equal(host.saved.Sites, []string{"first"}) ||
		string(invoke(t, plugin, host, "get_state", nil)) != "false" {
		t.Fatalf("manual-only save was not consumed: %s, %+v", host.oncePayload, host.saved)
	}
	invoke(t, plugin, host, runOnce, host.oncePayload)
	invoke(t, plugin, host, runOnce, host.oncePayload)
	if len(host.markRequests) != 1 || host.markRequests[0].SiteID != "first" ||
		!slices.Equal(host.markRequests[0].Titles, []string{"自定义规则"}) {
		t.Fatalf("wrong one-shot requests: %+v", host.markRequests)
	}
	for _, log := range host.logs {
		if strings.Contains(log.Message, "自定义规则") {
			t.Fatalf("log included a title: %+v", log)
		}
	}
}

func TestScheduledRunUsesSelectedActiveSitesAndReportsPartialFailure(t *testing.T) {
	plugin := New()
	host := &fixtureHost{t: t, markResults: map[string]sdk.SiteMessagesMarkReadResultV1{
		"first":  {SchemaVersion: 1, Scanned: 5, Matched: 2, Marked: 1, Failed: 1},
		"second": {SchemaVersion: 1, ErrorReason: "收件箱读取或已读确认失败"},
	}}
	if raw := invoke(t, plugin, host, "get_api", nil); string(raw) != "[]" {
		t.Fatalf("expected an empty API declaration, got %s", raw)
	}
	config := json.RawMessage(`{"enabled":true,"sites":["first","disabled","second","first"],"titles":"通知","cron":"*/15 * * * *"}`)
	invoke(t, plugin, host, "init", config)
	var jobs []struct {
		ID      string          `json:"id"`
		Handler string          `json:"handler"`
		Input   json.RawMessage `json:"input"`
		Trigger struct {
			Cron          string `json:"cron"`
			LocalTimezone bool   `json:"local_timezone"`
		} `json:"trigger"`
	}
	raw := invoke(t, plugin, host, "get_service", nil)
	if err := json.Unmarshal(raw, &jobs); err != nil || len(jobs) != 1 {
		t.Fatalf("services %s: %v", raw, err)
	}
	job := jobs[0]
	if job.ID != ID || job.Handler != run || job.Trigger.Cron != "*/15 * * * *" || !job.Trigger.LocalTimezone {
		t.Fatalf("wrong service %+v", job)
	}
	result := string(invoke(t, plugin, host, job.Handler, job.Input))
	if len(host.markRequests) != 2 || host.markRequests[0].SiteID != "first" || host.markRequests[1].SiteID != "second" ||
		!strings.Contains(result, `[false,`) || !strings.Contains(result, "检查 5 条，匹配 2 条，已读 1 条，失败 1 条") ||
		!strings.Contains(result, "跳过 1 个站点，2 个站点处理失败") {
		t.Fatalf("wrong run: %s, requests %+v", result, host.markRequests)
	}
	invoke(t, plugin, host, "init", json.RawMessage(`{}`))
	invoke(t, plugin, host, job.Handler, job.Input)
	if len(host.markRequests) != 2 {
		t.Fatal("stale scheduled invocation bypassed the current configuration")
	}
}
