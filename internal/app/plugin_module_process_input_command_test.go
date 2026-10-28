package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// Runs through a real cmd/server and real SDK objects created by the service
// root. Only input/plugin materials are synthetic, never dispatch success.
func verifyOwnedProductionInputCommands(t *testing.T, request func(string, string, any, int) []byte, stop, start func(), pid func(string) int) {
	t.Helper()
	const source, clone, user = "OwnedInput", "OwnedInputservice", "service-acceptance"
	events := func(id, kind string) []map[string]json.RawMessage {
		t.Helper()
		var response struct {
			Events []struct {
				Type string
				Data map[string]json.RawMessage
			}
		}
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/events", nil, 200), &response) != nil {
			t.Fatal("actual input events response")
		}
		out := []map[string]json.RawMessage{}
		for _, event := range response.Events {
			if event.Type == kind {
				out = append(out, event.Data)
			}
		}
		return out
	}
	waitCount := func(id, kind string, want int) []map[string]json.RawMessage {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for {
			got := events(id, kind)
			if len(got) == want {
				return got
			}
			if len(got) > want || time.Now().After(until) {
				t.Fatalf("actual %s %s callbacks %d want%d", id, kind, len(got), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	message := func(text string) []byte {
		return request("POST", "/api/v1/message/web", map[string]any{"text": text, "user": "forged", "source": "forged", "channel": "Telegram"}, 200)
	}
	baseSource, baseClone := len(events(source, "plugin.action")), len(events(clone, "plugin.action"))
	message("/owned \u001c甲\u0085乙\n丙")
	for id, base := range map[string]int{source: baseSource, clone: baseClone} {
		rows := waitCount(id, "plugin.action", base+1)
		data := rows[len(rows)-1]
		for key, value := range map[string]string{"action": "command", "channel": "Web", "source": user, "user": user, "arg_str": "甲 乙 丙"} {
			var got string
			_ = json.Unmarshal(data[key], &got)
			if got != value {
				t.Fatal("actual command source context", id, key, got, value)
			}
		}
	}
	message("/fail")
	waitCount(source, "plugin.action", baseSource+2)
	waitCount(clone, "plugin.action", baseClone+2)
	message("/owned after-handler-failure")
	waitCount(source, "plugin.action", baseSource+3)
	waitCount(clone, "plugin.action", baseClone+3)
	baseSource, baseClone = baseSource+2, baseClone+2
	for _, text := range []string{"/missing", "/OWNED"} {
		message(text)
	}
	if len(events(source, "plugin.action")) != baseSource+1 || len(events(clone, "plugin.action")) != baseClone+1 {
		t.Fatal("unknown command invoked worker")
	}
	if os.Getenv("OWNED_COMMAND_PROCESS_FIXTURE_BINARY") != "" {
		verifyOwnedProductionCommandProvider(t, request)
	}
	input := func(id, operation string, payload any) []byte {
		return request("POST", "/api/v1/plugin/"+id+"/input", map[string]any{"operation": operation, "input": payload}, 200)
	}
	query := map[string]any{"user_id": user, "channel": "Web", "source": user}
	create := func(id string, timeout int) plugins.PendingInputInteraction {
		t.Helper()
		payload := map[string]any{"user_id": user, "channel": "Web", "source": user, "prompt_id": "owned", "payload": map[string]any{"large": json.Number("9007199254740993")}, "timeout_seconds": timeout}
		var result plugins.PendingInputInteraction
		if json.Unmarshal(input(id, "create", payload), &result) != nil || len(result.RequestID) != 12 || result.PluginID != id {
			t.Fatal("actual SDK input creation")
		}
		return result
	}
	baseSourceInput, baseCloneInput := len(events(source, "message.action")), len(events(clone, "message.action"))
	first := create(source, 120)
	second := create(clone, 120)
	if first.RequestID == second.RequestID {
		t.Fatal("replace reused input identity")
	}
	if strings.TrimSpace(string(input(source, "pop", query))) != "null" {
		t.Fatal("source popped clone input")
	}
	input(source, "remove", map[string]any{"request_id": second.RequestID})
	message("/owned literal input")
	rows := waitCount(clone, "message.action", baseCloneInput+1)
	data := rows[len(rows)-1]
	if string(data["input_text"]) != `"/owned literal input"` || string(data["input_session_id"]) != `"`+second.RequestID+`"` || !strings.Contains(string(data["payload"]), "9007199254740993") || len(events(source, "message.action")) != baseSourceInput {
		t.Fatal("actual clone next-input ownership or literal slash")
	}
	if len(events(source, "plugin.action")) != baseSource+1 {
		t.Fatal("input text also executed command")
	}
	create(source, 120)
	reply := message("\u001cQuIt\u001f")
	if !strings.Contains(string(reply), "已取消插件输入") {
		t.Fatal("actual Python whitespace cancellation")
	}
	rows = waitCount(source, "message.action", baseSourceInput+1)
	if string(rows[len(rows)-1]["cancelled"]) != "true" {
		t.Fatal("actual cancel callback missing")
	}
	create(source, 1)
	time.Sleep(1100 * time.Millisecond)
	reply = message("/owned after-expiry")
	rows = waitCount(source, "message.action", baseSourceInput+2)
	if string(rows[len(rows)-1]["expired"]) != "true" {
		t.Fatal("actual expiry callback missing")
	}
	waitCount(source, "plugin.action", baseSource+2)
	waitCount(clone, "plugin.action", baseClone+2)
	if strings.TrimSpace(string(input(source, "get", query))) != "null" {
		t.Fatal("expired input remains pending")
	}
	create(clone, 120)
	oldSource, oldClone := pid(source), pid(clone)
	stop()
	start()
	if pid(source) == oldSource || pid(clone) == oldClone {
		t.Fatal("cold worker process did not change")
	}
	if strings.TrimSpace(string(input(clone, "get", query))) != "null" {
		t.Fatal("in-memory input survived actual restart")
	}
	until := time.Now().Add(5 * time.Second)
	for {
		var response struct{ Data struct{ Accepted bool } }
		if json.Unmarshal(message("/owned cold"), &response) == nil && response.Data.Accepted {
			break
		}
		if time.Now().After(until) {
			t.Fatal("actual cold command registration not restored")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitCount(source, "plugin.action", 1)
	waitCount(clone, "plugin.action", 1)
	t.Logf("actual Linux ordinary command+input: trusted Web context, source+clone declared-event broadcast, missing/case no-op, actual handler failure then successful command, replacement/clone isolation, literal slash consumed first, Unicode cancel, real 1s expiry then command, restart pending cleared and command rebuilt; worker PIDs %d/%d -> %d/%d", oldSource, oldClone, pid(source), pid(clone))
}
