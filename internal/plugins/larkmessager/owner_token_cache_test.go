package larkmessager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOwnerTokenCacheKeepsOriginalWarmAndForceSemantics(t *testing.T) {
	var tokenCalls, sends atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			tokenCalls.Add(1)
			w.Write([]byte(`{"code":0,"tenant_access_token":"owned-token","expire":7200}`))
			return
		}
		sends.Add(1)
		w.Write([]byte(`{"code":0,"data":{"message_id":"om_owned","chat_id":"oc_owned"}}`))
	}))
	defer s.Close()
	cache := &OwnerTokenCache{}
	makeAdapter := func(inv string, rev int64) *Adapter {
		b := testBinding()
		b.InvocationID = inv
		b.Revision = rev
		a, e := NewAdapter(b, NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
		if e != nil {
			t.Fatal(e)
		}
		if e = cache.Bind(a); e != nil {
			t.Fatal(e)
		}
		return a
	}
	send := func(a *Adapter) {
		t.Helper()
		defer cache.Capture(a)
		_, e := a.API(context.Background(), Request{Op: "send", IDs: []string{"ou_owned", "open_id"}, Payload: Object{"receive_id": "ou_owned", "msg_type": "text", "content": "{\"text\":\"owned\"}"}})
		if e != nil {
			t.Fatal(e)
		}
	}
	first := makeAdapter("first", 1)
	send(first)
	second := makeAdapter("second", 1)
	send(second)
	if tokenCalls.Load() != 1 || sends.Load() != 2 {
		t.Fatal("warm cache lost across workers", tokenCalls.Load(), sends.Load())
	}
	if _, e := second.ForceToken(context.Background()); e != nil {
		t.Fatal(e)
	}
	cache.Capture(second)
	if tokenCalls.Load() != 2 {
		t.Fatal("force stopped authenticating")
	}
	cache.Reset()
	send(makeAdapter("third", 1))
	if tokenCalls.Load() != 3 {
		t.Fatal("Init did not clear cache")
	}
	send(makeAdapter("fourth", 2))
	if tokenCalls.Load() != 4 {
		t.Fatal("revision reused old credential cache")
	}
}
