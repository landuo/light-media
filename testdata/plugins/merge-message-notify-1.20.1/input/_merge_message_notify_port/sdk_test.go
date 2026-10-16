package mergemessagenotify

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
	"testing"
)

func TestSDKFixtureProcess(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--merge-message-worker" {
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
	return plugins.Command{Path: exe, Args: []string{"-test.run=^TestSDKFixtureProcess$", "--", "--merge-message-worker"}}
}
func TestActualSDKBoundNoticeProducesSourceRequest(t *testing.T) {
	ctx := context.Background()
	sent := []any{}
	p := New(Host{HTTP: func(_ context.Context, r Request) (Response, error) {
		sent = append(sent, map[string]any{"url": r.URL, "method": r.Method, "kwargs": r.Kwargs})
		return Response{Status: 200, JSON: map[string]any{"errcode": 0}}, nil
	}})
	calls := 0
	host := func(ctx context.Context, id plugins.Identity, cap string, raw json.RawMessage) (json.RawMessage, error) {
		if id.PluginID != "MergeMessageNotify" || id.Version != "1.20.1" || id.InstanceID != "synthetic-merge" {
			return nil, errors.New("wrong identity")
		}
		calls++
		switch cap {
		case ConfigureCapability:
			if string(raw) != "{}" {
				return nil, errors.New("worker-supplied config")
			}
			return json.RawMessage(`null`), p.Init(ctx, map[string]any{"enable": true, "enable_channels": []any{"custom.iyuu"}, "custom.iyuu.token": "synthetic-token"}, false)
		case EventCapability:
			if string(raw) != `{"event_id":"owned-notice"}` {
				return nil, errors.New("unbound notice")
			}
			r := p.DispatchEvent(ctx, map[string]any{"title": "合成标题", "text": "合成正文", "type": NotifyType("Plugin")})
			if r.Success != 1 || r.Failed != 0 {
				return nil, errors.New("message not delivered by source algorithm")
			}
			return json.RawMessage(`null`), nil
		case FormCapability:
			elements, data, e := p.Form(ctx)
			if e != nil {
				return nil, e
			}
			return json.Marshal([]any{elements, data})
		}
		return nil, errors.New("ungranted capability")
	}
	for _, action := range []string{"init", "event"} {
		input := json.RawMessage(`{"event_id":"owned-notice"}`)
		raw, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"custom.iyuu.token":"worker-cannot-authorize"}`), Input: input})
		r, e := (plugins.Runner{}).Run(ctx, sdkCommand(t), Manifest(), "synthetic-merge", "invoke", raw, host)
		if e != nil || r.HostCalls != 1 || string(r.Result) != "null" {
			t.Fatal(action, e, r.HostCalls)
		}
	}
	equal(t, "actual source HTTP", sent, []any{map[string]any{"url": "https://iyuu.cn/synthetic-token.send", "method": "post", "kwargs": map[string]any{"params": obj("text", "合成标题", "desp", "合成正文")}}})
	r, e := (plugins.Runner{}).Run(ctx, sdkCommand(t), Manifest(), "synthetic-merge", "config_form", nil, host)
	if e != nil || r.HostCalls != 1 {
		t.Fatal(e)
	}
	if len(p.Channels()) != 17 || calls != 3 {
		t.Fatal("worker stop retired live owner", calls)
	}
	p.Stop()
	if len(p.Channels()) != 0 {
		t.Fatal("owner stop did not clear registrations")
	}
	if Manifest().HasPage || len(Manifest().APIs) > 0 {
		t.Fatal("invented original page/API")
	}
}
func TestSDKRejectsUnboundNoticeAndEffects(t *testing.T) {
	calls := 0
	deny := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("unexpected")
	}
	for _, raw := range []string{`{"action":"event","input":{"title":"not-owned"}}`, `{"action":"event","input":{"event_id":"x","url":"https://foreign.invalid"}}`, `{"action":"event","input":null}`, `{"action":"send","input":{"event_id":"x"}}`} {
		if _, e := Handle(context.Background(), "invoke", json.RawMessage(raw), deny); e == nil {
			t.Fatal(raw)
		}
	}
	if calls != 0 {
		t.Fatal("untrusted data reached host")
	}
}
