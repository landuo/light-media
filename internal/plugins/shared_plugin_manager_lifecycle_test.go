package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// SDK wire checks only: this HostCaller records calls, not manager lifecycle,
// SQLite, scheduler or a real worker. Those need the separate host gates.
func TestSharedPluginManagerLifecycleWire(t *testing.T) {
	ctx := context.Background()
	type invocation struct {
		method string
		raw    json.RawMessage
	}
	var calls []invocation
	state := json.RawMessage(`{"value":[123456789012345678901234567890,"\ud800",false,null]}`)
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, invocation{method, append(json.RawMessage(nil), raw...)})
		switch method {
		case "plugin.manager.delete_plugin_config":
			return json.RawMessage(`false`), nil
		case "plugin.manager.delete_plugin_data":
			return json.RawMessage(`true`), nil
		case "plugin.manager.get_plugin_state":
			return state, nil
		case "plugin.manager.get_local_plugins":
			return json.RawMessage(`[{"id":"Owned","state":[1],"plugin_order":123456789012345678901234567890}]`), nil
		default:
			return json.RawMessage(`null`), nil
		}
	}
	m := SharedPluginManager{Host: host}
	s := SharedPluginScheduler{Host: host}
	if err := m.InitPlugin(ctx, "Owned", json.RawMessage(`{"n":123456789012345678901234567890}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.RemovePlugin(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if out, err := m.DeletePluginConfig(ctx, PluginManagerDeleteRequest{PluginID: "Owned"}); err != nil || out {
		t.Fatalf("default force/false reply: %v %v", out, err)
	}
	if out, err := m.DeletePluginData(ctx, PluginManagerDeleteRequest{PluginID: "Stopped", Force: true}); err != nil || !out {
		t.Fatalf("force/true reply: %v %v", out, err)
	}
	got, err := m.GetPluginState(ctx, "Owned")
	if err != nil || !bytes.Equal(got, state) {
		t.Fatalf("state coerced: %s %v", got, err)
	}
	state[0] = '['
	if got[0] != '{' {
		t.Fatal("state aliases host reply buffer")
	}
	rows, err := m.GetLocalPlugins(ctx)
	if err != nil || len(rows) != 1 || string(rows[0]) != `{"id":"Owned","state":[1],"plugin_order":123456789012345678901234567890}` {
		t.Fatalf("local source row changed: %s %v", rows, err)
	}
	for _, job := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`""`), json.RawMessage(`"scheduler_job"`)} {
		if err := s.RemovePluginJob(ctx, PluginSchedulerRemoveRequest{PluginID: "Owned", JobID: job}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdatePluginJob(ctx, ""); err != nil {
		t.Fatal(err)
	}
	want := []invocation{
		{"plugin.manager.init_plugin", json.RawMessage(`{"plugin_id":"Owned","conf":{"n":123456789012345678901234567890}}`)},
		{"plugin.manager.remove_plugin", json.RawMessage(`{"plugin_id":""}`)},
		{"plugin.manager.delete_plugin_config", json.RawMessage(`{"plugin_id":"Owned","force":false}`)},
		{"plugin.manager.delete_plugin_data", json.RawMessage(`{"plugin_id":"Stopped","force":true}`)},
		{"plugin.manager.get_plugin_state", json.RawMessage(`{"plugin_id":"Owned"}`)},
		{"plugin.manager.get_local_plugins", json.RawMessage(`{}`)},
		{"plugin.scheduler.remove_plugin_job", json.RawMessage(`{"plugin_id":"Owned"}`)},
		{"plugin.scheduler.remove_plugin_job", json.RawMessage(`{"plugin_id":"Owned","job_id":null}`)},
		{"plugin.scheduler.remove_plugin_job", json.RawMessage(`{"plugin_id":"Owned","job_id":""}`)},
		{"plugin.scheduler.remove_plugin_job", json.RawMessage(`{"plugin_id":"Owned","job_id":"scheduler_job"}`)},
		{"plugin.scheduler.update_plugin_job", json.RawMessage(`{"plugin_id":""}`)},
	}
	if len(calls) != len(want) {
		t.Fatalf("got %d calls, want %d", len(calls), len(want))
	}
	for i, row := range want {
		if calls[i].method != row.method || !bytes.Equal(calls[i].raw, row.raw) {
			t.Fatalf("call %d: %+v want %+v", i, calls[i], row)
		}
	}
	for _, raw := range []string{`false`, `true`, `null`, `0`, `123456789012345678901234567890`, `1e100`, `"text"`, `[]`, `{}`} {
		m.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		out, err := m.GetPluginState(ctx, "Owned")
		if err != nil || string(out) != raw {
			t.Fatalf("finite state %q changed: %s %v", raw, out, err)
		}
	}
	m.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`[]`), nil
	}
	if rows, err := m.GetLocalPlugins(ctx); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty local list changed: %#v %v", rows, err)
	}
}

func TestSharedPluginManagerLifecycleBoundaries(t *testing.T) {
	ctx := context.Background()
	calls := 0
	host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`null`), nil
	}
	m := SharedPluginManager{Host: host}
	s := SharedPluginScheduler{Host: host}
	bad := string([]byte{0xff})
	invalid := []func() error{
		func() error { return m.InitPlugin(ctx, bad, json.RawMessage(`{}`)) },
		func() error { return m.InitPlugin(ctx, "Owned", nil) },
		func() error { return m.InitPlugin(ctx, "Owned", json.RawMessage(`NaN`)) },
		func() error { return m.InitPlugin(ctx, "Owned", json.RawMessage(`{} {}`)) },
		func() error { return m.InitPlugin(ctx, "Owned", json.RawMessage{'"', 0xff, '"'}) },
		func() error { return m.RemovePlugin(ctx, bad) },
		func() error { return m.RemovePlugin(ctx, strings.Repeat("x", 128<<10)) },
		func() error { _, e := m.DeletePluginConfig(ctx, PluginManagerDeleteRequest{PluginID: bad}); return e },
		func() error { _, e := m.DeletePluginData(ctx, PluginManagerDeleteRequest{PluginID: bad}); return e },
		func() error { _, e := m.GetPluginState(ctx, bad); return e },
		func() error { return s.RemovePluginJob(ctx, PluginSchedulerRemoveRequest{PluginID: bad}) },
		func() error {
			return s.RemovePluginJob(ctx, PluginSchedulerRemoveRequest{PluginID: "Owned", JobID: json.RawMessage(`42`)})
		},
		func() error {
			return s.RemovePluginJob(ctx, PluginSchedulerRemoveRequest{PluginID: "Owned", JobID: json.RawMessage(`{}`)})
		},
		func() error {
			return s.RemovePluginJob(ctx, PluginSchedulerRemoveRequest{PluginID: "Owned", JobID: json.RawMessage(`"x" true`)})
		},
		func() error { return s.UpdatePluginJob(ctx, bad) },
	}
	for i, call := range invalid {
		if err := call(); err == nil {
			t.Fatalf("invalid input %d accepted", i)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid input reached HostCaller %d times", calls)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.GetPluginState(canceled, "Owned"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := m.RemovePlugin(nil, "Owned"); err == nil {
		t.Fatal("nil context accepted")
	}
	if calls != 0 {
		t.Fatal("closed context reached HostCaller")
	}
	if _, err := (SharedPluginManager{}).GetLocalPlugins(ctx); err == nil {
		t.Fatal("missing manager host accepted")
	}
	if err := (SharedPluginScheduler{}).UpdatePluginJob(ctx, "Owned"); err == nil {
		t.Fatal("missing scheduler host accepted")
	}
	sentinel := errors.New("owned authority denied")
	m.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel }
	if _, err := m.GetPluginState(ctx, "Owned"); !errors.Is(err, sentinel) {
		t.Fatalf("host authority error hidden: %v", err)
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`NaN`), json.RawMessage(`Infinity`), json.RawMessage(`{} null`), json.RawMessage{'"', 0xff, '"'}} {
		m.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return raw, nil }
		if _, err := m.GetPluginState(ctx, "Owned"); err == nil {
			t.Fatalf("invalid/non-JSON reply accepted: %q", raw)
		}
	}
	for _, raw := range []string{`null`, `0`, `"false"`, `{}`} {
		m.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		if _, err := m.DeletePluginData(ctx, PluginManagerDeleteRequest{PluginID: "Owned"}); err == nil {
			t.Fatalf("nonboolean delete reply %s accepted", raw)
		}
	}
	for _, raw := range []string{`null`, `{}`, `[null]`, `[42]`, `[[]]`} {
		m.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		if _, err := m.GetLocalPlugins(ctx); err == nil {
			t.Fatalf("nonobject local list %s accepted", raw)
		}
	}
	m.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`true`), nil
	}
	if err := m.RemovePlugin(ctx, "Owned"); err == nil {
		t.Fatal("non-None lifecycle reply accepted")
	}
}
