package autobackup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"light-media/internal/plugins"
)

func TestSDKFixtureProcess(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--auto-backup-worker" {
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
	return plugins.Command{Path: exe, Args: []string{"-test.run=^TestSDKFixtureProcess$", "--", "--auto-backup-worker"}}
}
func TestActualSDKUsesTrustedConfigAndOpaqueOccurrences(t *testing.T) {
	r := &rig{trace: []any{}, files: map[string]Object{}, dav: Object{}, zip: true}
	p := New(r.deps())
	config := Object{"notify": true, "back_path": "/owned/backup"}
	calls := 0
	host := func(ctx context.Context, id plugins.Identity, cap string, b json.RawMessage) (json.RawMessage, error) {
		if id.PluginID != "AutoBackup" || id.Version != "2.1.4" || id.InstanceID != "owned-backup" {
			return nil, errors.New("wrong identity")
		}
		calls++
		switch cap {
		case ConfigureCapability:
			if string(b) != "{}" {
				return nil, errors.New("worker supplied config")
			}
			return json.RawMessage(`null`), p.Init(ctx, config)
		case ExecuteCapability:
			if string(b) != "{}" && string(b) != `{"event_id":"owned-source-occurrence"}` {
				return nil, errors.New("unbound backup occurrence")
			}
			v, e := p.Backup(ctx)
			out, _ := json.Marshal([]any{v.Success, v.Message})
			return out, e
		case APICapability:
			if string(b) != `{"request_id":"owned-api-request"}` {
				return nil, errors.New("unbound API")
			}
			v, e := p.APIBackup(ctx, "owned-key", "owned-key")
			out, _ := json.Marshal(v)
			return out, e
		case FormCapability:
			return Form("/owned/plugin")
		}
		return nil, errors.New("ungranted capability")
	}
	for _, v := range []plugins.Invocation{{Action: "init", Config: json.RawMessage(`{"back_path":"/untrusted"}`)}, {Action: "run"}, {Action: "once", Input: json.RawMessage(`{"event_id":"owned-source-occurrence"}`)}, {Action: "api", Input: json.RawMessage(`{"request_id":"owned-api-request"}`)}} {
		b, _ := json.Marshal(v)
		result, e := (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "owned-backup", "invoke", b, host)
		if e != nil || result.HostCalls != 1 {
			t.Fatal(v.Action, e, result.HostCalls)
		}
	}
	if calls != 4 || len(r.trace) != 6 {
		t.Fatal("real SDK did not apply expected backup+notice effects", calls, r.trace)
	}
	result, e := (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "owned-backup", "config_form", nil, host)
	expected, _ := Form("/owned/plugin")
	var a, b any
	decode(t, result.Result, &a)
	decode(t, expected, &b)
	if e != nil || canonical(a) != canonical(b) {
		t.Fatal("original form lost", e)
	}
}
func TestOriginalFormMetadataAndInvalidWorkerInputs(t *testing.T) {
	if e := Manifest().Validate(); e != nil {
		t.Fatal(e)
	}
	expected, e := os.ReadFile("reference/form.json")
	if e != nil {
		t.Fatal(e)
	}
	actual, e := Form("/owned/plugin")
	var a, b any
	decode(t, expected, &a)
	decode(t, actual, &b)
	if e != nil || canonical(a) != canonical(b) || string(Commands()) != "null" || string(Page()) != "null" {
		t.Fatal("source form/None commands/page lost")
	}
	calls := 0
	deny := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("unexpected host call")
	}
	for _, raw := range []string{`{"action":"api","input":{"request_id":"x","apikey":"secret"}}`, `{"action":"once","input":{"event_id":"x","destination":"/untrusted"}}`, `{"action":"service","input":{"request_id":"x"}}`, `{"action":"api","input":null}`, `{"action":"delete_backup"}`} {
		if _, e := Handle(context.Background(), "invoke", json.RawMessage(raw), deny); e == nil {
			t.Fatal("unbound input accepted", raw)
		}
	}
	if calls != 0 {
		t.Fatal("untrusted worker request reached host")
	}
}

func TestSourceAPIWrongKeyHasNoEffectsAndDisabledPluginCanRun(t *testing.T) {
	r := &rig{trace: []any{}, files: map[string]Object{}, dav: Object{}, zip: true}
	p := New(r.deps())
	value, e := p.APIBackup(context.Background(), "wrong-owned-key", "owned-key")
	if e != nil || len(r.trace) != 0 || canonical(value) != `{"success":false,"message":"API密钥错误","message_i18n":"API密钥错误","data":{}}` {
		t.Fatal("wrong key branch changed", value, e, r.trace)
	}
	if e = p.Init(context.Background(), Object{"enabled": false}); e != nil {
		t.Fatal(e)
	}
	value, e = p.APIBackup(context.Background(), "owned-key", "owned-key")
	if e != nil || canonical(value) != `[true,"备份完成 备份文件 /owned/backup/bk_20261018090203.zip"]` || len(r.trace) != 1 {
		t.Fatal("source disabled manual API behavior changed", value, e, r.trace)
	}
	r.trace = []any{}
	out, e := p.BackupResponse(context.Background())
	if e != nil || !out.Success || out.Message == nil || len(r.trace) != 1 {
		t.Fatal(out, e)
	}
}
