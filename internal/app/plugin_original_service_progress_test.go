package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func ownedProgressConfig(t *testing.T, a *App, cookie *http.Cookie, mode, gate string, interval int64) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"enabled": true, "services": []map[string]any{{"id": "owned_progress", "name": "Owned progress", "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": interval}, "input": map[string]any{"mode": mode, "gate": gate, "progress": 37, "text": "owned progress update"}}}})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
}
func ownedProgressRead(t *testing.T, a *App, cookie *http.Cookie) map[string]json.RawMessage {
	t.Helper()
	r := callTest(t, a.Handler(), "GET", "/api/v1/dashboard/schedule/OwnedInput_owned_progress/progress", "", cookie, "")
	var result struct {
		Data struct {
			Success bool                       `json:"success"`
			Data    map[string]json.RawMessage `json:"data"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || !result.Data.Success {
		t.Fatal("progress query", r.Code, r.Body.String())
	}
	return result.Data.Data
}
func TestOriginalServiceActualSDKProgressFailureAndNoop(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ownedProgressConfig(t, a, cookie, "tuple-fail", server.URL, 3600000)
	waiting := ownedProgressRead(t, a, cookie)
	if string(waiting["status"]) != `"waiting"` || string(waiting["success"]) != "null" {
		t.Fatal("new service not waiting", waiting)
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"OwnedInput","service_id":"owned_progress"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("actual service did not reach gate")
	}
	active := ownedProgressRead(t, a, cookie)
	if string(active["status"]) != `"running"` || string(active["value"]) != "37" || string(active["enable"]) != "true" || !strings.Contains(string(active["data"]), "9007199254740993") {
		t.Fatal("worker progress not consumed", active)
	}
	r = callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=OwnedInput_owned_progress", "", cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	r = callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"OwnedInput","service_id":"owned_progress"}`, cookie, "")
	if r.Code != 400 || !strings.Contains(r.Body.String(), "PLUGIN_BUSY") {
		t.Fatal("native busy semantics", r.Code, r.Body.String())
	}
	close(release)
	a.wg.Wait()
	finished := ownedProgressRead(t, a, cookie)
	if string(finished["status"]) != `"failed"` || string(finished["success"]) != "false" || string(finished["value"]) != "37" || string(finished["error"]) != `"owned service failure"` || string(finished["enable"]) != "false" || bytes.Equal(finished["finished_at"], []byte("null")) {
		t.Fatal("tuple failure not recorded", finished)
	}
	r = callTest(t, a.Handler(), "GET", "/api/plugins/services", "", cookie, "")
	var listed struct {
		Data []PluginServiceInfo `json:"data"`
	}
	if json.Unmarshal(r.Body.Bytes(), &listed) != nil || len(listed.Data) != 1 || listed.Data[0].Progress != 37 || listed.Data[0].ProgressDetail["status"] != "failed" {
		t.Fatal("UI does not receive progress", r.Body.String())
	}
	r = ownedInputRequest(t, a, cookie, "OwnedInput", "progress", `{"value":99}`)
	if r.Code != 403 {
		t.Fatal("API handler borrowed service callback", r.Code)
	}
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":false}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	if retained := ownedProgressRead(t, a, cookie); string(retained["status"]) != `"failed"` {
		t.Fatal("removed service lost progress cache")
	}
}
func TestOriginalServiceActualSDKResultTupleAndClock(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	for _, sample := range []struct{ mode, status, err string }{{"list-fail", "success", "null"}, {"tuple-empty", "failed", `"定时任务返回失败"`}, {"error", "failed", `"插件定时方法执行失败"`}} {
		ownedProgressConfig(t, a, cookie, sample.mode, "", 3600000)
		r := callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=OwnedInput_owned_progress", "", cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		progress := ownedProgressRead(t, a, cookie)
		if string(progress["status"]) != `"`+sample.status+`"` || string(progress["error"]) != sample.err {
			t.Fatal(sample.mode, progress)
		}
		if sample.status == "success" && string(progress["value"]) != "100" {
			t.Fatal("successful list must complete", progress)
		}
		if sample.mode == "error" {
			message := a.systemMessages.get("system")
			if !strings.Contains(string(message), "Go 插件执行失败") || strings.Contains(string(message), "owned callback error") {
				t.Fatal("safe system message missing", string(message))
			}
			events := ownedInputEvents(t, a, cookie, "OwnedInput")
			found := false
			for _, event := range events {
				if strings.Contains(string(event), `"type":"scheduler"`) && strings.Contains(string(event), `"diagnostic_source":"go-sdk-error-code/v1"`) && strings.Contains(string(event), `"traceback":""`) {
					found = true
				}
			}
			if !found {
				t.Fatal("actual scheduler error event not delivered", len(events))
			}
		}
	}
	// Start the actual 100ms scheduler loop with an owned one-shot date trigger.
	raw, _ := json.Marshal(map[string]any{"enabled": true, "services": []map[string]any{{"id": "owned_clock", "name": "Owned timer", "handler": "run_service", "trigger": map[string]any{"kind": "date", "timezone": "UTC", "run_at": time.Now().Add(300 * time.Millisecond).Format(time.RFC3339Nano)}, "input": map[string]any{"mode": "tuple-fail", "progress": 37}}}})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runOriginalPluginScheduler(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		progress := a.originalServiceProgressSnapshot("OwnedInput_owned_clock")
		status, _ := json.Marshal(progress["status"])
		if progress != nil && string(status) == `"failed"` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("clock did not execute worker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	a.wg.Wait()
}

func TestOriginalServiceActualSDKSuffixReplacementAndSyncWait(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	makeDefinition := func(id, mode, kind string) map[string]any {
		return map[string]any{"id": id, "name": id, "handler": "run_service", "trigger": map[string]any{"kind": kind, "timezone": "UTC", "interval_ms": 3600000}, "input": map[string]any{"mode": mode, "progress": 37}}
	}
	for _, invalid := range []bool{false, true} {
		kind := "interval"
		if invalid {
			kind = "invalid"
		}
		config, _ := json.Marshal(map[string]any{"enabled": true, "services": []any{makeDefinition("shared|first", "list-fail", "interval"), makeDefinition("shared|last", "tuple-fail", kind)}})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(config), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		services := a.originalPublicServices(time.Now())
		if !invalid && (len(services) != 1 || services[0].ID != "OwnedInput_shared" || services[0].Name != "shared|last") {
			t.Fatal("suffix not replaced", services)
		}
		if invalid && len(services) != 0 {
			t.Fatal("invalid last declaration retained old clock", services)
		}
		r = callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=OwnedInput_shared", "", cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		progress := a.originalServiceProgressSnapshot("OwnedInput_shared")
		status, _ := json.Marshal(progress["status"])
		if string(status) != `"failed"` {
			t.Fatal("compatibility API returned before synchronous result or wrong method", progress)
		}
	}
}
func TestOriginalServiceTupleBoundary(t *testing.T) {
	for _, raw := range []string{`[false,"list"]`, `false`, `{"value":false}`, `{"__plugin_service_tuple":[0,"zero"]}`, `{"__plugin_service_tuple":[null,"null"]}`, `{"__plugin_service_tuple":[true,"ok"]}`} {
		if got := originalServiceResultError(json.RawMessage(raw)); got != "" {
			t.Fatal("non-failure result", raw, got)
		}
	}
	raw, err := plugins.ServiceResultTuple(false, "")
	if err != nil || originalServiceResultError(raw) != "定时任务返回失败" {
		t.Fatal("empty tuple reason")
	}
}
