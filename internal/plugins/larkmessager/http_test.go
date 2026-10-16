package larkmessager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testBinding() Binding {
	return Binding{Owner: "owned", Generation: 1, Revision: 1, InvocationID: "synthetic", AppID: "SYNTHETIC_APP", AppSecret: "SYNTHETIC_SECRET", Targets: map[string][]string{"open_id": {"ou_owned"}}, Messages: []string{"om_owned"}, Cards: []string{"card_owned"}, Resources: map[string][]string{"reaction:om_owned": {"r_owned"}}}
}
func apiSuccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(r.URL.Path, "/tenant_access_token/") {
		io.WriteString(w, `{"code":0,"tenant_access_token":"SYNTHETIC_TOKEN","expire":7200}`)
		return
	}
	io.WriteString(w, `{"code":0,"data":{"message_id":"om_reply","chat_id":"oc_reply","msg_type":"text","root_id":"om_root","parent_id":"om_parent","thread_id":"omt_thread"}}`)
}
func mustRead(r io.Reader) []byte { b, _ := io.ReadAll(r); return b }
func TestBoundTextHTTPAndFaultReceipts(t *testing.T) {
	for _, fault := range []int{0, 1, 2} {
		t.Run(str(fault), func(t *testing.T) {
			var mu sync.Mutex
			calls := []Object{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body Object
				_ = decode(mustRead(r.Body), &body)
				mu.Lock()
				calls = append(calls, Object{"method": r.Method, "path": r.URL.Path, "query": r.URL.Query().Encode(), "body": body, "auth": r.Header.Get("Authorization")})
				n := len(calls)
				mu.Unlock()
				if fault == n {
					conn, _, _ := w.(http.Hijacker).Hijack()
					_ = conn.Close()
					return
				}
				apiSuccess(w, r)
			}))
			defer server.Close()
			journal := NewMemoryJournal()
			a, e := NewAdapter(testBinding(), journal, Options{TestAPIOrigin: server.URL, UUID: func() string { return "11111111-2222-4333-8444-555555555555" }})
			if e != nil {
				t.Fatal(e)
			}
			result, e := NewClient(a).SendText(context.Background(), "你好", Target{UserID: "ou_owned"})
			if fault == 0 {
				if e != nil || !result.Success || result.MessageID != "om_reply" {
					t.Fatal(e, result)
				}
				if len(calls) != 2 || calls[0]["auth"] != "" || calls[1]["auth"] != "Bearer SYNTHETIC_TOKEN" {
					t.Fatal(calls)
				}
				payload := obj(calls[1]["body"])
				if payload["uuid"] != "11111111-2222-4333-8444-555555555555" || asInput[Object](t, []byte(text(payload["content"])))["text"] != "你好" {
					t.Fatal(payload)
				}
			} else if e == nil || result.Success {
				t.Fatal("fault reported success", result)
			}
			rows := journal.Snapshot()
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			for _, r := range rows {
				expected := "confirmed"
				if fault == 1 {
					expected = "rejected"
				}
				if fault == 2 {
					expected = "unknown"
				}
				if r.State != expected {
					t.Fatal(r.State, expected)
				}
			}
			mu.Lock()
			oldCalls := len(calls)
			mu.Unlock()
			replay, _ := NewAdapter(testBinding(), journal, Options{TestAPIOrigin: server.URL})
			_, re := NewClient(replay).SendText(context.Background(), "你好", Target{UserID: "ou_owned"})
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != oldCalls {
				t.Fatal("replayed effect", calls)
			}
			if fault == 2 && !errors.Is(re, ErrUnknown) {
				t.Fatal("unknown automatically retried", re)
			}
			if fault == 0 && re != nil {
				t.Fatal(re)
			}
		})
	}
}
func TestBindingsDenyBeforeAnyNetwork(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; apiSuccess(w, r) }))
	defer s.Close()
	a, e := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	cases := []Request{{Op: "send", IDs: []string{"ou_other", "open_id"}, Payload: Object{"receive_id": "ou_other"}}, {Op: "reply", IDs: []string{"om_other"}}, {Op: "edit", IDs: []string{"../om_owned"}}, {Op: "user_batch", Payload: Object{"emails": []string{"user@other.invalid"}}}, {Op: "resource_get", IDs: []string{"om_owned", "file_other"}, Query: Object{"type": "file"}}, {Op: "arbitrary_rpc"}}
	for _, r := range cases {
		if _, e = a.API(context.Background(), r); !errors.Is(e, ErrDenied) {
			t.Fatalf("%s: %v", r.Op, e)
		}
	}
	if _, e = a.ReadFile(context.Background(), "/etc/passwd"); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e = a.FetchImage(context.Background(), "http://127.0.0.1/private"); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatal("unbound network attempted", calls)
	}
}
func TestFixedCardKitMethodsAndTokenWindow(t *testing.T) {
	paths := []string{}
	now := time.Unix(2000000000, 0)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if strings.Contains(r.URL.Path, "/tenant_access_token/") {
			apiSuccess(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/content") {
			io.WriteString(w, "null\x00")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/settings") {
			io.WriteString(w, `{"code":1}`)
			return
		}
		apiSuccess(w, r)
	}))
	defer s.Close()
	a, _ := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL, Now: func() time.Time { return now }})
	c := NewClient(a)
	ok, e := c.UpdateStreaming(context.Background(), "card_owned", StreamBodyID, "hello", 1)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	ok, e = c.CloseStreaming(context.Background(), "card_owned", 2)
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	want := []string{"POST /open-apis/auth/v3/tenant_access_token/internal", "PUT /open-apis/cardkit/v1/cards/card_owned/elements/mp_stream_body/content", "PATCH /open-apis/cardkit/v1/cards/card_owned/settings"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatal(paths)
	}
	now = now.Add(7140 * time.Second)
	if _, e = a.ForceToken(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(paths) != 4 {
		t.Fatal(paths)
	}
}
func TestRedirectRefusesCredentialsAcrossOrigin(t *testing.T) {
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	a, _ := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	_, e := NewClient(a).SendText(context.Background(), "text", Target{UserID: "ou_owned"})
	if e == nil || leaked {
		t.Fatal(e, leaked)
	}
}
func TestNoPrivateResponseErrorLeak(t *testing.T) {
	secret := "PRIVATE_SERVER_ECHO_SYNTHETIC_SECRET"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tenant_access_token/") {
			apiSuccess(w, r)
			return
		}
		json.NewEncoder(w).Encode(Object{"code": 999, "msg": secret})
	}))
	defer s.Close()
	a, _ := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	_, e := NewClient(a).SendText(context.Background(), "text", Target{UserID: "ou_owned"})
	if e == nil || strings.Contains(e.Error(), secret) {
		t.Fatal(e)
	}
}
