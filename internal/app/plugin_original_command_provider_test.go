package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOriginalCommandMenuProviderActualWorker(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	manifest := installOrderWorker(t, a, cookie, os.Getenv("OWNED_COMMAND_MUTATOR"))
	type delivery struct {
		PID   int `json:"pid"`
		Input struct {
			Args   []json.RawMessage          `json:"args"`
			Kwargs map[string]json.RawMessage `json:"kwargs"`
		} `json:"input"`
	}
	received := make(chan delivery, 20)
	var status atomic.Int64
	status.Store(http.StatusNoContent)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value delivery
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&value) != nil {
			t.Error("invalid actual menu request")
		}
		received <- value
		w.WriteHeader(int(status.Load()))
	}))
	defer sink.Close()
	config := map[string]any{"enabled": true, "provider": true, "endpoint": sink.URL, "mode": "mutate", "command": "/owned_menu"}
	configure := func() {
		t.Helper()
		raw, _ := json.Marshal(config)
		r := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/"+manifest.PluginID, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	pop := func() delivery {
		t.Helper()
		select {
		case value := <-received:
			if value.PID == 0 || value.PID == os.Getpid() {
				t.Fatal("menu did not come from actual SDK worker", value.PID)
			}
			return value
		default:
			t.Fatal("menu provider did not reach local HTTP receiver")
			return delivery{}
		}
	}
	assertNoDelivery := func() {
		t.Helper()
		select {
		case value := <-received:
			t.Fatal("unexpected menu delivery", value)
		default:
		}
	}
	prepare := func(pid string, reset bool) (originalCommandRegisterDecision, error) {
		t.Helper()
		a.pluginOriginalCommandsMu.Lock()
		defer a.pluginOriginalCommandsMu.Unlock()
		if reset {
			a.pluginOriginalRegisteredCommands = nil
		}
		return a.prepareOriginalCommandRegistration(context.Background(), pid)
	}
	configure()
	value := pop()
	if value.Input.Args == nil || len(value.Input.Args) != 0 || len(value.Input.Kwargs) != 1 {
		t.Fatal("register_commands call shape", value.Input)
	}
	var commands map[string]json.RawMessage
	if json.Unmarshal(value.Input.Kwargs["commands"], &commands) != nil {
		t.Fatal("missing commands keyword", value.Input)
	}
	if _, ok := commands["/not_executable"]; ok {
		t.Fatal("chain injected an executable name")
	}
	for _, name := range []string{"/redo", "/owned_menu_hidden"} {
		if _, ok := commands[name]; ok {
			t.Fatal("removed/hidden menu leaked to provider", name)
		}
	}
	if !originalBaseJSONEqual(commands["/owned_menu"], json.RawMessage(`{"type":"plugin","description":"changed menu only","category":null,"opaque":900719925474099312345}`)) {
		t.Fatal("provider did not receive actual Chain mutation", string(commands["/owned_menu"]))
	}
	if strings.Contains(string(value.Input.Kwargs["commands"]), "not_a_method") {
		t.Fatal("execution payload leaked into menu")
	}
	configure()
	assertNoDelivery()
	// A normal null return and a normal false return both complete the module
	// call; the dispatcher, not the menu caller, owns their continuation rules.
	config["source"], config["menu_result"] = manifest.PluginID, false
	configure()
	_ = pop()
	decision, err := prepare(manifest.PluginID, false)
	if err != nil || !decision.Forced || !decision.ShouldRegister {
		t.Fatal("normal false module result was treated as a failure", decision, err)
	}
	_ = pop()
	config["mode"] = "cancel"
	configure()
	assertNoDelivery()
	config["provider"], config["mode"], config["source"] = false, "observe", ""
	configure()
	assertNoDelivery()
	decision, err = prepare("", true)
	if !decision.ShouldRegister || !errors.Is(err, errOriginalCommandRegisterProviderUnavailable) {
		t.Fatal("missing provider lost explicit unavailable", decision, err)
	}
	config["provider"], config["source"], config["mode"] = true, manifest.PluginID, "force"
	status.Store(http.StatusBadGateway)
	configure()
	_ = pop()
	if message := a.systemMessages.get("plugin"); !strings.Contains(string(message), "Go 插件执行失败") || strings.Contains(string(message), "owned menu receiver") {
		t.Fatal("provider failure did not reach safe system diagnostic", message)
	}
	decision, err = prepare("", false)
	if err != nil || decision.ShouldRegister {
		t.Fatal("failed provider rolled back source registered snapshot", decision, err)
	}
	assertNoDelivery()
	decision, err = prepare(manifest.PluginID, false)
	if err == nil || !decision.Forced {
		t.Fatal("provider rejection was reported as success", decision, err)
	}
	_ = pop()
	status.Store(http.StatusNoContent)
	decision, err = prepare(manifest.PluginID, false)
	if err != nil || !decision.Forced {
		t.Fatal("forced registration recovery failed", decision, err)
	}
	_ = pop()
	assertNoDelivery()
}
