package larkmessager

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestOriginalSourceClientFaultPrefixes(t *testing.T) {
	f, e := os.Open("testdata/source-oracle.jsonl.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, _ := gzip.NewReader(f)
	defer z.Close()
	d := json.NewDecoder(z)
	count := 0
	for {
		var c oracleCase
		if e = d.Decode(&c); e == io.EOF {
			break
		} else if e != nil {
			t.Fatal(e)
		}
		if c.Kind != "client_calls" {
			continue
		}
		count++
		input := asInput[Object](t, c.Input)
		t.Run(text(input["method"])+"/"+str(count), func(t *testing.T) {
			var mu sync.Mutex
			calls := []any{}
			fault := asInput[struct {
				Fault int `json:"fault"`
			}](t, c.Input).Fault
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := Object{"method": r.Method, "url": APIBase + strings.TrimPrefix(r.URL.Path, "/open-apis")}
				isToken := strings.Contains(r.URL.Path, "/tenant_access_token/")
				timeout := 15
				if isToken || strings.Contains(r.URL.Path, "/reactions") {
					timeout = 10
				}
				call["timeout"] = timeout
				if r.ContentLength > 0 {
					var body Object
					_ = decode(mustRead(r.Body), &body)
					call["json"] = body
				}
				if !isToken {
					call["headers"] = Object{"Authorization": r.Header.Get("Authorization"), "Content-Type": r.Header.Get("Content-Type")}
				}
				if len(r.URL.Query()) > 0 {
					params := Object{}
					for k, v := range r.URL.Query() {
						params[k] = v[0]
					}
					call["params"] = params
				}
				mu.Lock()
				calls = append(calls, call)
				n := len(calls)
				mu.Unlock()
				if n == fault {
					conn, _, _ := w.(http.Hijacker).Hijack()
					_ = conn.Close()
					return
				}
				apiSuccess(w, r)
			}))
			defer server.Close()
			a, e := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: server.URL, UUID: func() string { return "11111111-2222-4333-8444-555555555555" }})
			if e != nil {
				t.Fatal(e)
			}
			client := NewClient(a)
			args := obj(input["args"])
			var result any
			var err error
			switch input["method"] {
			case "send_text":
				var r *Result
				r, err = client.SendText(context.Background(), text(args["text"]), Target{UserID: text(args["userid"])})
				if r == nil || !r.Success {
					result = Object{"success": false}
				} else {
					result = Object{"success": true, "message_id": r.MessageID, "chat_id": r.ChatID, "msg_type": r.MsgType}
				}
			case "_send_message":
				var r *Result
				r, err = client.SendMessage(context.Background(), text(args["receive_id"]), text(args["receive_id_type"]), text(args["msg_type"]), obj(args["content"]))
				if r != nil {
					result = Object{"success": r.Success, "message_id": r.MessageID, "chat_id": r.ChatID, "msg_type": r.MsgType}
				}
			case "_reply_message":
				var r *Result
				r, err = client.ReplyMessage(context.Background(), text(args["message_id"]), text(args["msg_type"]), obj(args["content"]))
				if r != nil {
					result = Object{"success": r.Success, "message_id": r.MessageID, "chat_id": r.ChatID, "msg_type": r.MsgType, "root_id": r.RootID, "parent_id": r.ParentID, "thread_id": r.ThreadID}
				}
			case "add_message_reaction":
				var id string
				id, err = client.AddReaction(context.Background(), text(args["message_id"]), text(args["emoji_type"]))
				result = nullString(id)
			case "delete_message_reaction":
				result, err = client.DeleteReaction(context.Background(), text(args["message_id"]), text(args["reaction_id"]))
			default:
				t.Fatal(input)
			}
			expected := asInput[Object](t, c.Output)
			if _, sourceError := expected["error"]; sourceError {
				if err == nil {
					t.Fatal("source fault escaped but Go success")
				}
			} else {
				equalJSON(t, uuidJSON(expected["result"]), result)
			}
			mu.Lock()
			actualCalls := clone(calls)
			mu.Unlock()
			want := canonicalCalls(arr(expected["calls"]))
			got := canonicalCalls(actualCalls)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("request/failure prefix differs\nwant %s\ngot %s", uuidJSON(want), uuidJSON(got))
			}
		})
	}
	if count != 15 {
		t.Fatal(count)
	}
}
func canonicalCalls(calls []any) any {
	out := clone(calls)
	for _, c := range out {
		p := obj(obj(c)["json"])
		if s, ok := p["content"].(string); ok {
			var v any
			if decode([]byte(s), &v) == nil {
				p["content"] = v
			}
		}
	}
	raw := uuidJSON(out)
	var v any
	_ = decode(raw, &v)
	return v
}
