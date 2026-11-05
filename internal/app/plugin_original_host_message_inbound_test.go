package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func ownedIncomingMessage(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	worker := os.Getenv("OWNED_HOST_MESSAGE_FIXTURE_BINARY")
	if worker == "" {
		t.Fatal("OWNED_HOST_MESSAGE_FIXTURE_BINARY must name the actual standard SDK worker")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, worker)
	object, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("installed ordinary message worker not loaded")
	}
	return a, cookie, object.(*originalPluginInstance)
}

func ownedIncomingCall(t *testing.T, p *originalPluginInstance, method string, input any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.callOriginalMethod(context.Background(), method, raw)
	if err != nil {
		t.Fatal(method, err)
	}
	return out.Result
}

func ownedIncomingTrace(t *testing.T, p *originalPluginInstance) []map[string]json.RawMessage {
	t.Helper()
	raw := ownedIncomingCall(t, p, "http", plugins.HTTPRequest{Key: "snapshot"})
	var httpResponse plugins.HTTPResponse
	var out struct {
		Trace []map[string]json.RawMessage `json:"trace"`
	}
	if json.Unmarshal(raw, &httpResponse) != nil || httpResponse.Status != 200 || json.Unmarshal(httpResponse.Body, &out) != nil {
		t.Fatal("invalid worker snapshot")
	}
	return out.Trace
}

func ownedIncomingWait(t *testing.T, p *originalPluginInstance, want func([]map[string]json.RawMessage) bool) []map[string]json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		rows := ownedIncomingTrace(t, p)
		if want(rows) {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ordinary message trace did not reach expected source state")
	return nil
}

func ownedIncomingEvents(rows []map[string]json.RawMessage, event string) []map[string]json.RawMessage {
	out := []map[string]json.RawMessage{}
	for _, row := range rows {
		if string(row["kind"]) != `"event"` {
			continue
		}
		var value struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(row["value"], &value) == nil && value.Type == event {
			out = append(out, value.Data)
		}
	}
	return out
}

func ownedIncomingHasKind(rows []map[string]json.RawMessage, kind string) bool {
	for _, row := range rows {
		if string(row["kind"]) == fmt.Sprintf("%q", kind) {
			return true
		}
	}
	return false
}

func TestOriginalIncomingMessageActualSDKSourceOrdinaryRoutes(t *testing.T) {
	oracleRaw, err := os.ReadFile("../../testdata/plugin-host-message-20261031/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		RouteCases []struct {
			Name, Text, Channel string
			Pending, Status     bool
			PendingAfterStatus  bool `json:"pending_after_status"`
			Exception           *string
			Trace               []map[string]json.RawMessage
		} `json:"route_cases"`
	}
	if json.Unmarshal(oracleRaw, &oracle) != nil {
		t.Fatal("invalid fixed-source oracle")
	}
	for _, sample := range oracle.RouteCases {
		// These fixed-source observations remain in the oracle as provenance,
		// but exercise the explicitly excluded model-prefix routing contract.
		if sample.Name == "explicit-ordinary" || sample.Name == "explicit-ordinary-prefix" || sample.Name == "empty-explicit" {
			continue
		}
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, p := ownedIncomingMessage(t)
			if sample.PendingAfterStatus {
				configured := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedHostMessage", `{"pending_on_processing":true}`, cookie, "")
				assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
				a.wg.Wait()
			}
			if sample.Pending {
				channel, source := "飞书", "ParsedSource"
				ownedIncomingCall(t, p, "pending", plugins.InputInteractionRequest{UserID: json.Number("9007199254740993"), Channel: &channel, Source: &source, ChatID: "c1"})
			}
			payload, _ := json.Marshal(map[string]any{"info": map[string]any{"userid": json.Number("9007199254740993"), "username": "owned-name", "channel": sample.Channel, "source": "ParsedSource", "text": sample.Text, "message_id": "m1", "chat_id": "c1", "reply_to_message_id": 42}})
			source := "Owned"
			accepted := ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: payload})
			if string(accepted) != `{"schema_version":1,"accepted":true}` {
				t.Fatal("background acknowledgement missing", string(accepted))
			}
			a.wg.Wait()
			rows := ownedIncomingTrace(t, p)
			for _, sourceRow := range sample.Trace {
				var kind string
				_ = json.Unmarshal(sourceRow["kind"], &kind)
				if kind == "event" {
					var eventType string
					_ = json.Unmarshal(sourceRow["type"], &eventType)
					got := ownedIncomingEvents(rows, eventType)
					if len(got) != 1 {
						t.Fatalf("source event %s count %d, trace=%s", eventType, len(got), mustJSON(rows))
					}
					var expected map[string]json.RawMessage
					_ = json.Unmarshal(sourceRow["data"], &expected)
					for key, value := range expected {
						if key == "processing_status" {
							continue
						}
						if !jsonEqual(value, got[0][key]) {
							t.Fatalf("source event %s field %s differs: %s vs %s", eventType, key, value, got[0][key])
						}
					}
				}
			}
			var historyCount int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='message'`).Scan(&historyCount); err != nil {
				t.Fatal(err)
			}
			expectedHistory := 0
			for _, r := range sample.Trace {
				if string(r["kind"]) == `"history"` {
					expectedHistory++
				}
			}
			if historyCount != expectedHistory {
				t.Fatal("source input/history order differs", historyCount, expectedHistory)
			}
			if sample.Pending && (ownedIncomingHasKind(rows, "processing_begin") || len(ownedIncomingEvents(rows, "command.excute")) != 0) {
				t.Fatal("pending plugin input did not take priority")
			}
			if sample.PendingAfterStatus && (len(ownedIncomingEvents(rows, "message.action")) != 1 || len(ownedIncomingEvents(rows, "command.excute")) != 0) {
				t.Fatal("pending input created by status module did not take priority")
			}
			if sample.Status && !sample.Pending && (!ownedIncomingHasKind(rows, "processing_begin") || !ownedIncomingHasKind(rows, "processing_end")) {
				t.Fatal("ordinary channel processing lifecycle missing")
			}
			if sample.Exception != nil {
				flushTestLogs(t, a)
				logs, err := a.readLogs(map[string]string{"module": "plugins"})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, log := range logs {
					if log["event"] == "incoming_message_failed" {
						found = true
					}
				}
				if !found {
					t.Fatal("background malformed callback failure unobservable")
				}
			}
		})
	}
}

func mustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	ad := json.NewDecoder(bytes.NewReader(a))
	ad.UseNumber()
	bd := json.NewDecoder(bytes.NewReader(b))
	bd.UseNumber()
	return ad.Decode(&av) == nil && bd.Decode(&bv) == nil && mustJSON(av) == mustJSON(bv)
}

func TestOriginalIncomingMessageActualSDKBytesFailureRevocationAndRecovery(t *testing.T) {
	a, _, p := ownedIncomingMessage(t)
	source := "Owned"
	body := []byte{0xff, 0, 'x'}
	ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: body, Form: plugins.MessageValues{"x": {"first", "last"}}, Args: plugins.MessageValues{"source": {"ignored", "Owned"}}})
	a.wg.Wait()
	rows := ownedIncomingTrace(t, p)
	digest := sha256.Sum256(body)
	if len(rows) != 1 || string(rows[0]["body_sha256"]) != fmt.Sprintf("%q", hex.EncodeToString(digest[:])) || !strings.Contains(string(rows[0]["form"]), `"first","last"`) {
		t.Fatal("raw body or multi-value form changed", mustJSON(rows))
	}
	badSource := "Foreign"
	raw, _ := json.Marshal(plugins.ReceiveMessageRequestV1{Source: &badSource, Args: plugins.MessageValues{"source": {"Owned"}}})
	if _, err := p.callOriginalMethod(context.Background(), "receive", raw); err == nil {
		t.Fatal("inconsistent source accepted")
	}
	ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: []byte(`{"mode":"fail"}`)})
	a.wg.Wait()
	flushTestLogs(t, a)
	logs, err := a.readLogs(map[string]string{"module": "plugins"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, log := range logs {
		if log["event"] == "incoming_message_failed" {
			found = true
		}
	}
	if !found {
		t.Fatal("parser background failure unobservable")
	}
	// Source run_module reports a failed plugin method through the ordinary
	// system helper/event path before process() receives no parser result. The
	// Go adaptation exposes only the transport code, never private exception text.
	safeSystemError := false
	for {
		message := a.systemMessages.get("plugin")
		if len(message) == 0 {
			break
		}
		if strings.Contains(string(message), "owned parser failure") || strings.Contains(string(message), `\"mode\"`) {
			t.Fatal("parser failure leaked private worker text", string(message))
		}
		if strings.Contains(string(message), "Go 插件执行失败：") {
			safeSystemError = true
		}
	}
	if !safeSystemError {
		t.Fatal("parser failure omitted the ordinary safe system error")
	}
	ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: []byte(`{"info":{"userid":"owned","channel":"Web","source":"Owned","text":"recovered"}}`)})
	a.wg.Wait()
	if len(ownedIncomingEvents(ownedIncomingTrace(t, p), "user.message")) != 1 {
		t.Fatal("parser failure disabled following ordinary message")
	}
	callback, revoke := p.originalBaseSessionHost(context.Background(), "receive", nil)
	revoke()
	identity := plugins.Identity{PluginID: p.installation.PluginID, InstanceID: p.installation.InstanceID, Version: p.installation.Version}
	if _, err := callback(context.Background(), identity, plugins.MessageReceiveCapability, json.RawMessage(`{"source":"Owned","body":""}`)); err == nil {
		t.Fatal("revoked incoming producer still admitted")
	}
}

// The user excludes all model-specific routing. These strings have no special
// prefix semantics: the ordinary command registry receives their exact text.
func TestOriginalIncomingMessageActualSDKNoReservedModelPrefixes(t *testing.T) {
	for _, text := range []string{"/airport owned", "/noai: /owned_inbound owned"} {
		t.Run(text, func(t *testing.T) {
			a, cookie, p := ownedIncomingMessage(t)
			source := "Owned"
			body, _ := json.Marshal(map[string]any{"info": map[string]any{"userid": "owned", "channel": "Web", "source": source, "text": text}})
			ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: body})
			a.wg.Wait()
			rows := ownedIncomingTrace(t, p)
			commands := ownedIncomingEvents(rows, "command.excute")
			if len(commands) != 1 || string(commands[0]["cmd"]) != string(mustRawJSON(text)) || len(ownedIncomingEvents(rows, "user.message")) != 0 || len(ownedIncomingEvents(rows, "owned.inbound.command")) != 0 {
				t.Fatal("ordinary inbound command was reserved or rewritten", mustJSON(rows))
			}
			response := callTest(t, a.Handler(), "POST", "/api/message/web", string(mustRawJSON(map[string]string{"text": text})), cookie, "")
			if response.Code != 200 {
				t.Fatal("ordinary Web command was reserved", response.Code, response.Body.String())
			}
			a.wg.Wait()
			commands = ownedIncomingEvents(ownedIncomingTrace(t, p), "command.excute")
			if len(commands) != 2 || string(commands[1]["cmd"]) != string(mustRawJSON(text)) {
				t.Fatal("ordinary Web command was rewritten", mustJSON(commands))
			}
		})
	}
}

func mustRawJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func TestOriginalIncomingMessageActualSDKCloseCancelsParser(t *testing.T) {
	a, _, p := ownedIncomingMessage(t)
	source := "Owned"
	ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: []byte(`{"mode":"block"}`)})
	ownedIncomingWait(t, p, func(rows []map[string]json.RawMessage) bool { return ownedIncomingHasKind(rows, "parse") })
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("shutdown failed to cancel accepted ordinary parser")
	}
}

func TestOriginalIncomingMessageActualSDKProcessSourceProjection(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-message-20261031/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name   string
			Parsed json.RawMessage
			Trace  []struct {
				Kind   string
				Kwargs map[string]json.RawMessage
			}
		} `json:"process_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("invalid source process oracle")
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, _, p := ownedIncomingMessage(t)
			source := "Owned"
			body, _ := json.Marshal(map[string]any{"info": sample.Parsed})
			ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: body})
			a.wg.Wait()
			rows := ownedIncomingTrace(t, p)
			var expected map[string]json.RawMessage
			for _, r := range sample.Trace {
				if r.Kind == "handle" {
					expected = r.Kwargs
				}
			}
			if expected == nil {
				if ownedIncomingHasKind(rows, "processing_begin") || len(ownedIncomingEvents(rows, "user.message")) != 0 {
					t.Fatal("source ignored invalid/empty message, target handled it")
				}
				return
			}
			got := ownedIncomingEvents(rows, "user.message")
			if len(got) != 1 {
				t.Fatal("source parsed valid ordinary message missing", mustJSON(rows))
			}
			for _, key := range []string{"userid", "channel", "source", "text"} {
				if !jsonEqual(expected[key], got[0][key]) {
					t.Fatal("source process projection differs", key, string(expected[key]), string(got[0][key]))
				}
			}
			history, err := a.originalWebMessageHistory(httptest.NewRequest("GET", "/api/message/web", nil))
			if err != nil {
				t.Fatal(err)
			}
			entries := history.([]map[string]any)
			if len(entries) != 1 || !jsonEqual(expected["username"], json.RawMessage(mustJSON(entries[0]["userid"]))) {
				t.Fatal("source username fallback/history identity differs", mustJSON(entries), string(expected["username"]))
			}
		})
	}
}

func TestOriginalIncomingMessageActualSDKCloneColdNoReplay(t *testing.T) {
	a, cookie, p := ownedIncomingMessage(t)
	source := "Owned"
	ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: []byte(`{"info":{"userid":"owned","channel":"Web","source":"Owned","text":"source message"}}`)})
	a.wg.Wait()
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostMessage/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("OwnedHostMessagetwin")
	if !ok {
		t.Fatal("clone did not load")
	}
	clone := value.(*originalPluginInstance)
	if len(ownedIncomingTrace(t, clone)) != 0 {
		t.Fatal("clone copied source runtime data")
	}
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedHostMessagetwin", `{"source":"CloneSource"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	cloneSource := "CloneSource"
	ownedIncomingCall(t, clone, "receive", plugins.ReceiveMessageRequestV1{Source: &cloneSource, Body: []byte(`{"info":{"userid":"owned","channel":"Web","source":"CloneSource","text":"clone message"}}`)})
	a.wg.Wait()
	rows := ownedIncomingTrace(t, clone)
	if !ownedIncomingHasKind(rows, "parse") {
		t.Fatal("clone own parser not invoked")
	}
	before := mustJSON(rows)
	directory := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err := cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	value, ok = cold.pluginRunningInstances.Load("OwnedHostMessagetwin")
	if !ok {
		t.Fatal("clone missing on cold start")
	}
	if after := mustJSON(ownedIncomingTrace(t, value.(*originalPluginInstance))); after != before {
		t.Fatal("cold start replayed accepted incoming message")
	}
}

func TestOriginalIncomingMessageActualSDKFullFinishAndNormalizedFeedback(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-message-20261031/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Finish []struct {
			Name                string
			User, Message, Chat json.RawMessage
			Trace               []struct{ Kwargs json.RawMessage }
		} `json:"finish_cases"`
		Callback struct {
			Parsed json.RawMessage
			Trace  []struct {
				Kind string
				Data map[string]json.RawMessage
			}
		} `json:"normalized_username_callback"`
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("invalid source finish oracle")
	}
	var cases []map[string]json.RawMessage
	_ = json.Unmarshal(source["finish_cases"], &cases)
	for _, sample := range cases {
		var name string
		_ = json.Unmarshal(sample["name"], &name)
		t.Run(name, func(t *testing.T) {
			a, cookie, p := ownedIncomingMessage(t)
			config, _ := json.Marshal(map[string]any{"processing_status": map[string]json.RawMessage{"userid": sample["status_user"], "message_id": sample["status_message"], "chat_id": sample["status_chat"], "metadata": json.RawMessage(`{"owned":true}`)}})
			reply := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedHostMessage", string(config), cookie, "")
			assertOriginalRegistrySuccess(t, reply.Code, reply.Body.Bytes())
			a.wg.Wait()
			source := "Owned"
			ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: []byte(`{"info":{"userid":9007199254740993,"channel":"飞书","source":"ParsedSource","text":"ordinary","message_id":"original-message","chat_id":"original-chat"}}`)})
			a.wg.Wait()
			var expected []struct{ Kwargs json.RawMessage }
			_ = json.Unmarshal(sample["trace"], &expected)
			found := false
			for _, row := range ownedIncomingTrace(t, p) {
				if string(row["kind"]) != `"processing_end"` {
					continue
				}
				var call struct{ Kwargs json.RawMessage }
				_ = json.Unmarshal(row["call"], &call)
				if len(expected) != 1 || !jsonEqual(expected[0].Kwargs, call.Kwargs) {
					t.Fatal("full _mark->ChainBase->module source wire differs", string(expected[0].Kwargs), string(call.Kwargs))
				}
				found = true
			}
			if !found {
				t.Fatal("finish module not invoked")
			}
		})
	}
	t.Run("normalized-callback-feedback", func(t *testing.T) {
		a, _, p := ownedIncomingMessage(t)
		source := "Owned"
		body, _ := json.Marshal(map[string]json.RawMessage{"info": oracle.Callback.Parsed})
		ownedIncomingCall(t, p, "receive", plugins.ReceiveMessageRequestV1{Source: &source, Body: body})
		a.wg.Wait()
		var expected map[string]json.RawMessage
		for _, row := range oracle.Callback.Trace {
			if row.Kind == "post" {
				expected = row.Data
			}
		}
		found := false
		for _, row := range ownedIncomingTrace(t, p) {
			if string(row["kind"]) != `"post"` {
				continue
			}
			var call struct {
				Kwargs struct {
					Message map[string]json.RawMessage `json:"message"`
				} `json:"kwargs"`
			}
			_ = json.Unmarshal(row["call"], &call)
			for key, value := range expected {
				if !jsonEqual(value, call.Kwargs.Message[key]) {
					t.Fatal("normalized callback field differs", key, string(value), string(call.Kwargs.Message[key]))
				}
			}
			found = true
		}
		if !found {
			t.Fatal("ordinary malformed callback feedback missing")
		}
	})
}
