package cleaninvalidseed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func TestSDKFixtureProcess(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--clean-invalid-seed-worker" {
			if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, Manifest(), Handle); e != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}
func sdkCommand(t *testing.T) plugins.Command {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return plugins.Command{Path: exe, Args: []string{"-test.run=^TestSDKFixtureProcess$", "--", "--clean-invalid-seed-worker"}}
}
func TestActualSDKExecutesControlledCleanupAndBoundCommand(t *testing.T) {
	var corpus struct {
		Cases []struct {
			ID       string
			Input    map[string]any
			Expected map[string]any
		}
	}
	decodeFile(t, "reference/oracle.json", &corpus)
	var input map[string]any
	var expected map[string]any
	for _, v := range corpus.Cases {
		if v.ID == "delete_keep_data" {
			input = v.Input
			expected = v.Expected
		}
	}
	if input == nil {
		t.Fatal("missing source case")
	}
	h := newSyntheticHost(input)
	m := New(h)
	calls := 0
	host := func(ctx context.Context, id plugins.Identity, cap string, raw json.RawMessage) (json.RawMessage, error) {
		if id.PluginID != "CleanInvalidSeed" || id.Version != "2.1" || id.InstanceID != "synthetic-cleaner" {
			return nil, errors.New("wrong identity")
		}
		calls++
		switch cap {
		case ConfigureCapability:
			if string(raw) != "{}" {
				return nil, errors.New("worker supplied config")
			}
			e := m.Init(ctx, input["config"].(map[string]any))
			return json.RawMessage(`null`), e
		case ExecuteCapability:
			if string(raw) == `{"action":"clean"}` {
				return json.RawMessage(`null`), m.Clean(ctx)
			}
			if string(raw) == `{"event_id":"owned-toggle"}` {
				return json.RawMessage(`null`), m.HandleCommand(ctx, map[string]any{"action": "toggle_notify_all", "channel": "synthetic", "user": "synthetic-user"})
			}
			return nil, errors.New("unbound effect")
		case FormCapability:
			return Form([]string{"Synthetic qB", "Synthetic TR"}), nil
		}
		return nil, errors.New("ungranted capability")
	}
	for _, action := range []string{"init", "run"} {
		b, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"downloaders":["untrusted-worker"]}`)})
		out, e := (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "synthetic-cleaner", "invoke", b, host)
		if e != nil || out.HostCalls != 1 || string(out.Result) != "null" {
			t.Fatal("real SDK did not execute source-returning operation", action, e, out.HostCalls)
		}
	}
	if calls != 2 || !reflect.DeepEqual(canonical(h.trace), expected["trace"]) {
		t.Fatal("SDK returned null without expected cleanup effects", h.trace)
	}
	b, _ := json.Marshal(plugins.Invocation{Action: "command", Input: json.RawMessage(`{"event_id":"owned-toggle"}`)})
	_, e := (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "synthetic-cleaner", "invoke", b, host)
	if e != nil || m.Config["notify_all"] != true || calls != 3 {
		t.Fatal("bound command was not applied", e, calls)
	}
}
func TestSDKFormAndUntrustedEffects(t *testing.T) {
	h := func(_ context.Context, _ plugins.Identity, cap string, raw json.RawMessage) (json.RawMessage, error) {
		if cap != FormCapability || string(raw) != "{}" {
			return nil, errors.New("unexpected capability")
		}
		return Form([]string{"Synthetic qB", "Synthetic TR"}), nil
	}
	r, e := (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "synthetic-cleaner", "config_form", nil, h)
	if e != nil || !reflect.DeepEqual(canonical(r.Result), canonical(Form([]string{"Synthetic qB", "Synthetic TR"}))) || r.HostCalls != 1 {
		t.Fatal("dynamic original form lost", e)
	}
	var c map[string]any
	decodeFile(t, "reference/oracle.json", &c)
	if !reflect.DeepEqual(c["form"], canonical(Form([]string{"Synthetic qB", "Synthetic TR"}))) || !reflect.DeepEqual(c["commands"], canonical(Commands())) || c["api"] != nil || c["page"] != nil {
		t.Fatal("original form, commands, None API/page differ")
	}
	if Manifest().HasPage || len(Manifest().APIs) > 0 {
		t.Fatal("invented source API/page")
	}
	calls := 0
	deny := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("unexpected host call")
	}
	for _, raw := range []string{`{"action":"event","input":{"event_id":"x","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`, `{"action":"command","input":{"action":"delete_invalid_torrents"}}`, `{"action":"event","input":null}`, `{"action":"delete_all"}`} {
		if _, e := Handle(context.Background(), "invoke", json.RawMessage(raw), deny); e == nil {
			t.Fatal("unbound destructive input accepted", raw)
		}
	}
	if calls != 0 {
		t.Fatal("invalid invocation reached host")
	}
}
