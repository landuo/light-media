package larkmessager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSDKWorker(t *testing.T) {
	for _, a := range os.Args {
		if a == "--lark-worker" {
			if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, Manifest(), Handle); e != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}
func TestSDKBoundNoticeToOwnedLarkAPI(t *testing.T) {
	if e := Manifest().Validate(); e != nil {
		t.Fatal(e)
	}
	var sent atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			sent.Add(1)
			var payload Object
			_ = decode(mustRead(r.Body), &payload)
			if payload["receive_id"] != "ou_owned" {
				t.Error("recipient was not host bound")
			}
			card := asInput[Object](t, []byte(text(payload["content"])))
			if card["schema"] != "2.0" {
				t.Error("actual Go card builder not used")
			}
		}
		apiSuccess(w, r)
	}))
	defer s.Close()
	a, e := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	kv := newTestKV()
	p := New(PluginHost{API: a, KV: kv})
	trustedConfig := Object{"enabled": true, "app_id": "SYNTHETIC_APP", "app_secret": "SYNTHETIC_SECRET", "FEISHU_OPEN_ID": "ou_owned"}
	host := func(ctx context.Context, id plugins.Identity, cap string, raw json.RawMessage) (json.RawMessage, error) {
		if id.PluginID != "LarkMessager" || id.InstanceID != "owned-lark" {
			return nil, ErrDenied
		}
		switch cap {
		case ConfigureCapability:
			if string(raw) != "{}" {
				return nil, ErrDenied
			}
			_, e := p.Init(trustedConfig)
			return json.RawMessage(`null`), e
		case ModuleCapability:
			if string(raw) != `{"call_id":"owned-notice"}` {
				return nil, ErrDenied
			}
			r, e := p.SendDirect(ctx, Notification{Title: "Owned title", Text: "Owned text", MType: "插件"})
			b, _ := json.Marshal(r)
			return b, e
		}
		return nil, ErrDenied
	}
	exe, _ := os.Executable()
	command := plugins.Command{Path: exe, Args: []string{"-test.run=^TestSDKWorker$", "--", "--lark-worker"}}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, action := range []string{"init", "module"} {
		raw, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"app_secret":"worker must not choose","FEISHU_OPEN_ID":"ou_intruder"}`), Input: json.RawMessage(`{"call_id":"owned-notice"}`)})
		result, e := (plugins.Runner{}).Run(ctx, command, Manifest(), "owned-lark", "invoke", raw, host)
		if e != nil || result.HostCalls != 1 {
			t.Fatal(action, e, result)
		}
		if action == "module" {
			var r Result
			if json.Unmarshal(result.Result, &r) != nil || !r.Success || r.MessageID != "om_reply" {
				t.Fatal("real core did not confirm message", r)
			}
		}
	}
	if sent.Load() != 1 {
		t.Fatal(sent.Load())
	}
	if len(p.Modules()) != 13 {
		t.Fatal("worker stop retired host modules")
	}
}
func TestSDKRejectsWorkerSelectedData(t *testing.T) {
	calls := 0
	host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, nil }
	for _, input := range []string{`{"call_id":"id","recipient":"ou_bad"}`, `{"recipient":"ou_bad"}`, `{"call_id":1}`, `{"call_id":""}`, `{"call_id":"id"} {}`} {
		raw, _ := json.Marshal(plugins.Invocation{Action: "module", Input: json.RawMessage(input)})
		_, e := Handle(context.Background(), "invoke", raw, host)
		if e == nil {
			t.Fatal("accepted", input)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}
func TestSDKBoundIngressToActualParser(t *testing.T) {
	var forwards atomic.Int32
	p := New(PluginHost{Forward: func(ctx context.Context, v Object) error {
		forwards.Add(1)
		parsed, e := New(PluginHost{}).ParseMessage(ctx, "Lark", v)
		if e != nil || parsed["text"] != "hello" || parsed["userid"] != "ou_owned" {
			return errors.New("Go receiving/parser chain failed")
		}
		return nil
	}})
	body := Object{"header": Object{"event_type": "im.message.receive_v1"}, "event": Object{"sender": Object{"sender_id": Object{"open_id": "ou_owned"}}, "message": Object{"message_id": "om_owned", "chat_id": "oc_owned", "message_type": "text", "content": "{\"text\":\" hello \"}"}}}
	host := func(ctx context.Context, id plugins.Identity, cap string, raw json.RawMessage) (json.RawMessage, error) {
		if cap != APICapability || string(raw) != `{"request_id":"trusted-ingress"}` {
			return nil, ErrDenied
		}
		return json.RawMessage(`null`), p.DispatchTrusted(ctx, body)
	}
	exe, _ := os.Executable()
	command := plugins.Command{Path: exe, Args: []string{"-test.run=^TestSDKWorker$", "--", "--lark-worker"}}
	raw, _ := json.Marshal(plugins.HTTPRequest{RequestID: "trusted-ingress", Body: []byte(`{"malicious":"ignored"}`)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, e := (plugins.Runner{}).Run(ctx, command, Manifest(), "owned-lark", "http_api", raw, host)
	if e != nil || result.HostCalls != 1 || forwards.Load() != 1 {
		t.Fatal(e, result, forwards.Load())
	}
}
func TestOwnedMultipartFileVoiceAndCaption(t *testing.T) {
	var uploads, messages atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tenant_access_token/") {
			apiSuccess(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files") {
			uploads.Add(1)
			if r.URL.Query().Get("file_type") != "stream" {
				t.Error("file type not query")
			}
			if e := r.ParseMultipartForm(4096); e != nil {
				t.Error(e)
			}
			defer r.MultipartForm.RemoveAll()
			file, h, e := r.FormFile("file")
			if e != nil {
				t.Error(e)
				return
			}
			defer file.Close()
			b, _ := io.ReadAll(file)
			if string(b) != "owned bytes" || h.Filename != "owned.opus" {
				t.Error(h.Filename, string(b))
			}
			io.WriteString(w, `{"code":0,"data":{"file_key":"file_owned"}}`)
			return
		}
		messages.Add(1)
		apiSuccess(w, r)
	}))
	defer s.Close()
	b := testBinding()
	b.Files = map[string]Blob{"owned-voice": {Name: "owned.opus", Data: []byte("owned bytes")}}
	a, _ := NewAdapter(b, NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	r, e := NewClient(a).SendVoice(context.Background(), "owned-voice", "caption", Target{UserID: "ou_owned"})
	if e != nil || !r.Success || uploads.Load() != 1 || messages.Load() != 2 {
		t.Fatal(e, r, uploads.Load(), messages.Load())
	}
}
