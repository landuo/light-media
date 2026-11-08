package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"light-media/internal/notice"
)

// Source basis: fixed8a ChainBase298-329/372-414/470-490 leaves an empty
// plugin+system dispatch as None. helper/message.py746-755 ignores that result;
// it only logs a real callback exception. ChainBase1665 saves history first.
// Exact fixed git blobs are sealed by 07071293 and actual selected-method
// original empty/ordinary-raise/raise_exception traces by 18cb8df5.
func sourceEmptyNoticeLogs(t *testing.T, a *App, wanted string) []map[string]any {
	t.Helper()
	a.wg.Wait()
	page, err := a.queryLogs(context.Background(), map[string]string{"from": "0001-01-01T00:00:00Z", "limit": "500"})
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, row := range page.Entries {
		if row["event"] == wanted {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestSourceEmptyNoticeActualSDKWebHistoryNoError(t *testing.T) {
	for _, c := range []struct {
		name    string
		enabled bool
		off     bool
	}{{"disabled-sender", false, false}, {"enabled-no-binding", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			a, cookie, object := originalNoticeFixture(t)
			raw, _ := json.Marshal(map[string]any{"enabled": c.enabled, "module_off": c.off})
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedNotice", string(raw), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			out := postOwnedNotice(t, object, context.Background(), `{"title":"owned empty Web reply","channel":"Web","userid":"owned-source-user"}`)
			rows := originalWebHistoryRead(t, a, cookie, "/api/message/web?count=100")
			if len(rows) != 1 || rows[0]["title"] != "owned empty Web reply" || rows[0]["channel"] != "Web" || rows[0]["userid"] != "owned-source-user" || rows[0]["action"] != float64(1) {
				t.Fatal("fixed history-before-dispatch changed", rows)
			}
			if out.Attempted != 1 || out.Queued != 0 || out.Failed != 0 || len(out.Errors) != 0 {
				t.Fatal("SOURCE_EMPTY_MODULE_ERROR: None became a sender failure", out)
			}
			if logs := sourceEmptyNoticeLogs(t, a, "notification_failed"); len(logs) != 0 {
				t.Fatal("SOURCE_EMPTY_MODULE_LOG: invented sender failure", logs)
			}
			if c.enabled {
				event, err := a.pluginStore.Data(context.Background(), "OwnedNotice", "notice_event")
				if err != nil || !strings.Contains(string(event.Value), "owned empty Web reply") {
					t.Fatal("ordinary NoticeMessage did not reach actual enabled SDK", err)
				}
			}
			t.Logf("actual SDK ordinary source no-op: history=%d attempted_callback=%d failed=%d errors=%v enabled=%v", len(rows), out.Attempted, out.Failed, out.Errors, c.enabled)
		})
	}
}

func TestSourceEmptyNoticeActualSDKQueuedNoProvider(t *testing.T) {
	a, cookie, object := originalNoticeFixture(t)
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "25:00", "end": "25:00"}})
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := postOwnedNotice(t, object, ctx, `{"title":"owned queued empty reply","channel":"Web"}`)
	cancel()
	if out.Queued != 1 || out.Attempted != 0 || out.Failed != 0 {
		t.Fatal("outside-window source call was not queued", out)
	}
	if rows := originalWebHistoryRead(t, a, cookie, "/api/message/web"); len(rows) != 1 || rows[0]["title"] != "owned queued empty reply" {
		t.Fatal("history did not precede deferred callback", rows)
	}
	noticePreferenceTest(t, a, "window", "", nil)
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	q, err := a.originalNoticeQueue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// One actual queue monitor iteration, explicitly triggered; no timer claim.
	attempts := q.Tick(context.Background())
	if len(attempts) != 1 || !attempts[0].Attempted || attempts[0].CallbackError != nil || len(q.Pending()) != 0 {
		t.Fatal("SOURCE_EMPTY_MODULE_QUEUED: empty callback must be consumed as None", attempts)
	}
	if logs := sourceEmptyNoticeLogs(t, a, "notification_failed"); len(logs) != 0 {
		t.Fatal("deferred empty callback logged failure", logs)
	}
	t.Log("actual App queue consumed 1 empty callback after caller cancellation; 1 saved history row; no failed log")
}

func TestSourceEmptyNoticeActualSDKRealModuleFailureRetained(t *testing.T) {
	a, cookie, object := originalNoticeFixture(t)
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedNotice", `{"enabled":true,"fail":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	out := postOwnedNotice(t, object, context.Background(), `{"title":"owned real sender exception","channel":"Web","userid":"owned-source-user"}`)
	if out.Attempted != 1 || out.Failed != 0 || len(out.Errors) != 0 {
		t.Fatal("CONSUMED_MODULE_EXCEPTION_RETHROWN", out)
	}
	if rows := originalWebHistoryRead(t, a, cookie, "/api/message/web"); len(rows) != 1 || rows[0]["title"] != "owned real sender exception" {
		t.Fatal("source prior history lost", rows)
	}
	if logs := sourceEmptyNoticeLogs(t, a, "notification_failed"); len(logs) != 0 {
		t.Fatal("DUPLICATE_QUEUE_ERROR", logs)
	}
	logs := sourceEmptyNoticeLogs(t, a, "module_execution_failed")
	if len(logs) != 1 || logs[0]["plugin_instance_id"] != "OwnedNotice" || logs[0]["operation"] != "post_notice" || logs[0]["plugin_call_id"] == "" {
		t.Fatal("source module ERROR caller/count lost", logs)
	}
	var fifo map[string]any
	if json.Unmarshal(a.systemMessages.get("plugin"), &fifo) != nil || fifo["type"] != "plugin" || !strings.Contains(fifo["title"].(string), "插件模块执行失败") || a.systemMessages.get("plugin") != nil {
		t.Fatal("source module FIFO count/type lost", fifo)
	}
	count, err := a.pluginStore.Data(context.Background(), "OwnedNotice", "system_error_count")
	if err != nil || string(count.Value) != "1" {
		t.Fatal("actual SystemError count differs", err, string(count.Value))
	}
	event, err := a.pluginStore.Data(context.Background(), "OwnedNotice", "system_error_event")
	if err != nil || !strings.Contains(string(event.Value), `"type":"system.error"`) || !strings.Contains(string(event.Value), `"plugin_method":"post_message"`) {
		t.Fatal("actual module SystemError missing", err, string(event.Value))
	}
	t.Log("actual module failure: module ERROR/FIFO/SystemError once each; history=1; queue ERROR=0; no sender HTTP")
}

func TestSourceEmptyNoticeActualSDKPropagatedQueueFailure(t *testing.T) {
	a, cookie, _ := originalNoticeFixture(t)
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedNotice", `{"enabled":true,"fail":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	q, err := a.originalNoticeQueue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	periods, err := notice.ParseSchedule([]any{map[string]any{"start": "25:00", "end": "25:00"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SetPeriods(periods); err != nil {
		t.Fatal(err)
	}
	attempt, err := q.Send(context.Background(), notice.Call{Args: []any{"post_message"}, Kwargs: notice.Object{"message": notice.Object{"title": "owned propagated"}, "raise_exception": true}})
	if err != nil || !attempt.Queued {
		t.Fatal("propagated probe not queued", attempt, err)
	}
	if err = q.SetPeriods(nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.runOriginalNoticeQueue(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(12 * time.Second)
	for {
		logs := sourceEmptyNoticeLogs(t, a, "notification_failed")
		if len(logs) == 1 {
			break
		}
		if len(logs) > 1 || time.Now().After(deadline) {
			t.Fatal("PROPAGATED_QUEUE_ERROR_MISSING", logs)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(q.Pending()) != 0 || len(sourceEmptyNoticeLogs(t, a, "module_execution_failed")) != 0 || a.systemMessages.get("plugin") != nil {
		t.Fatal("raise_exception duplicated consumed-error side effects")
	}
	if count, err := a.pluginStore.Data(context.Background(), "OwnedNotice", "system_error_count"); err == nil && len(count.Value) > 0 && string(count.Value) != "null" {
		t.Fatal("raise_exception emitted consumed SystemError", string(count.Value))
	}
	t.Log("actual 10-second App monitor: real SDK raise_exception recorded one queue ERROR; no duplicate module/FIFO/SystemError")
}

func TestSourceEmptyNoticeNativeCountDiagnosticUnchanged(t *testing.T) {
	a := newTestApp(t)
	call := notice.Call{Args: []any{"post_message"}, Kwargs: notice.Object{"message": notice.Object{"title": "owned no module"}}}
	for _, f := range []struct {
		name string
		call func() (int, error)
	}{{"count", func() (int, error) { return a.sendOriginalNoticeModulesCount(context.Background(), call) }}, {"native-delivery", func() (int, error) {
		return a.sendPluginMessageModules(context.Background(), pluginNoticeQueueEntry{Call: call})
	}}} {
		t.Run(f.name, func(t *testing.T) {
			n, err := f.call()
			if n != 0 || pluginErrorCodeInternal(err) != "PLUGIN_NOTIFICATION_PROVIDER_UNAVAILABLE" {
				t.Fatal("NATIVE_DIAGNOSTIC_ERASED", n, err)
			}
		})
	}
}

func TestSourceEmptyNoticeWrapperKeepsOtherErrors(t *testing.T) {
	a := newTestApp(t)
	if err := a.sendOriginalNoticeModules(context.Background(), notice.Call{}); pluginErrorCodeInternal(err) != "PLUGIN_NOTIFICATION_INVALID" {
		t.Fatal("invalid source call was swallowed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.sendOriginalNoticeModules(ctx, notice.Call{Args: []any{"post_message"}, Kwargs: notice.Object{}}); !errors.Is(err, context.Canceled) {
		t.Fatal("source context failure was swallowed", err)
	}
	if err := a.sendOriginalNoticeModules(context.Background(), notice.Call{Args: []any{"post_message"}, Kwargs: notice.Object{"owned": func() {}}}); err == nil {
		t.Fatal("source marshaling failure was swallowed")
	}
}
