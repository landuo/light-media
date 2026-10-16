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
)

func TestOwnedStreamingLifecycleDerivedReceipts(t *testing.T) {
	for _, failPUT := range []bool{false, true} {
		t.Run(str(failPUT), func(t *testing.T) {
			var calls []string
			var sequences []any
			var mu sync.Mutex
			snapshot := func() ([]string, []any) {
				mu.Lock()
				defer mu.Unlock()
				return append([]string{}, calls...), append([]any{}, sequences...)
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch {
				case strings.Contains(r.URL.Path, "/tenant_access_token/"):
					apiSuccess(w, r)
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/cardkit/v1/cards"):
					io.WriteString(w, `{"code":0,"data":{"card_id":"card_derived"}}`)
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/im/v1/messages"):
					v := asInput[Object](t, mustRead(r.Body))
					card := asInput[Object](t, []byte(text(v["content"])))
					if obj(card["data"])["card_id"] != "card_derived" {
						t.Error(card)
					}
					io.WriteString(w, `{"code":0,"data":{"message_id":"om_derived","chat_id":"oc_owned","msg_type":"interactive"}}`)
				case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/elements/mp_stream_body/content"):
					v := asInput[Object](t, mustRead(r.Body))
					sequences = append(sequences, v["sequence"])
					if failPUT {
						conn, _, _ := w.(http.Hijacker).Hijack()
						_ = conn.Close()
						return
					}
					io.WriteString(w, "null\x00") // installed no-code 2xx acceptance
				case r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/settings"):
					v := asInput[Object](t, mustRead(r.Body))
					sequences = append(sequences, v["sequence"])
					if asInput[Object](t, []byte(text(v["settings"]))) == nil {
						t.Error("missing settings")
					}
					io.WriteString(w, `{"code":0}`)
				default:
					t.Error("unexpected route", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer s.Close()
			b := testBinding()
			b.Messages = nil
			b.Cards = nil
			j := NewMemoryJournal()
			a, e := NewAdapter(b, j, Options{TestAPIOrigin: s.URL})
			if e != nil {
				t.Fatal(e)
			}
			c := NewClient(a)
			r, e := c.SendStreaming(context.Background(), "Title", "Original", Target{UserID: "ou_owned"})
			if e != nil || r == nil || !r.Success || r.MessageID != "om_derived" {
				t.Fatal(r, e)
			}
			ok, e := c.EditMessage(context.Background(), text(r.MessageID), "Title", "Updated", nil, r.Metadata, "oc_owned")
			if failPUT {
				seen, _ := snapshot()
				if ok || !errors.Is(e, ErrUnknown) {
					t.Fatal(ok, e)
				}
				if len(seen) != 4 {
					t.Fatal("failed CardKit must not fall back to normal edit", seen)
				}
				if text(obj(r.Metadata["feishu_streaming"])["sequence"]) != "1" {
					t.Fatal(r.Metadata)
				}
				// Replay the exact operation prefix; unknown PUT cannot be retried.
				replay, _ := NewAdapter(b, j, Options{TestAPIOrigin: s.URL})
				rc := NewClient(replay)
				rr, re := rc.SendStreaming(context.Background(), "Title", "Original", Target{UserID: "ou_owned"})
				if re != nil {
					t.Fatal(re)
				}
				_, re = rc.EditMessage(context.Background(), text(rr.MessageID), "Title", "Updated", nil, rr.Metadata, "oc_owned")
				seen, _ = snapshot()
				if !errors.Is(re, ErrUnknown) || len(seen) != 4 {
					t.Fatal(re, seen)
				}
				return
			}
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			p := New(PluginHost{API: a})
			p.client = c
			ok, e = p.Finalize(context.Background(), "飞书", r.Metadata)
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			seen, seqs := snapshot()
			if !reflect.DeepEqual(seqs, []any{json.Number("1"), json.Number("2")}) {
				t.Fatal(seqs)
			}
			want := []string{"POST /open-apis/auth/v3/tenant_access_token/internal", "POST /open-apis/cardkit/v1/cards", "POST /open-apis/im/v1/messages", "PUT /open-apis/cardkit/v1/cards/card_derived/elements/mp_stream_body/content", "PATCH /open-apis/cardkit/v1/cards/card_derived/settings"}
			if !reflect.DeepEqual(seen, want) {
				t.Fatal(seen)
			}
			for _, row := range j.Snapshot() {
				if row.State != "confirmed" {
					t.Fatal(row)
				}
			}
		})
	}
}

func TestContentResourceAndRecipientGrants(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; apiSuccess(w, r) }))
	defer s.Close()
	a, e := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range []Request{
		{Op: "send", IDs: []string{"ou_owned", "open_id"}, Payload: Object{"receive_id": "ou_owned", "content": Object{"image_key": "img_other"}}},
		{Op: "card_create", Payload: Object{"card": Object{"body": Object{"elements": []any{Object{"img_key": "img_other"}}}}}},
		{Op: "send", IDs: []string{"ou_owned", "open_id"}, Payload: Object{"receive_id": "ou_owned", "content": Object{"type": "card", "data": Object{"card_id": "card_other"}}}},
		{Op: "reaction_delete", IDs: []string{"om_owned", "r_other"}},
	} {
		if _, e = a.API(context.Background(), r); !errors.Is(e, ErrDenied) {
			t.Fatal(r, e)
		}
	}
	if calls != 0 {
		t.Fatal("foreign resources reached HTTP", calls)
	}
	_, e = NewClient(a).SendMessage(context.Background(), "ou_owned", "open_id", "interactive", Object{"type": "card", "data": Object{"card_id": "card_owned"}})
	if e != nil {
		t.Fatal("explicit card grant denied", e)
	}
}

type failedReceiptJournal struct{ *MemoryJournal }

func (j failedReceiptJournal) Finish(context.Context, Receipt) error {
	return errors.New("synthetic journal commit failure")
}

func TestUnknownCreateOrReceiptCannotFallThroughToSecondMessage(t *testing.T) {
	for _, receiptFailure := range []bool{false, true} {
		t.Run(str(receiptFailure), func(t *testing.T) {
			calls := []string{}
			var mu sync.Mutex
			snapshot := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, calls...) }
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				if strings.Contains(r.URL.Path, "/tenant_access_token/") {
					apiSuccess(w, r)
					return
				}
				if !receiptFailure {
					conn, _, _ := w.(http.Hijacker).Hijack()
					_ = conn.Close()
					return
				}
				io.WriteString(w, `{"code":0,"data":{"card_id":"card_derived"}}`)
			}))
			defer s.Close()
			var journal Journal = NewMemoryJournal()
			if receiptFailure {
				journal = failedReceiptJournal{NewMemoryJournal()}
			}
			a, e := NewAdapter(testBinding(), journal, Options{TestAPIOrigin: s.URL})
			if e != nil {
				t.Fatal(e)
			}
			result, e := NewClient(a).SendStreaming(context.Background(), "T", "text", Target{UserID: "ou_owned"})
			if result != nil || !errors.Is(e, ErrUnknown) {
				t.Fatal(result, e)
			}
			if seen := snapshot(); len(seen) != 2 {
				t.Fatal("unknown card create followed by another remote effect", seen)
			}
			_, e = NewClient(a).SendText(context.Background(), "again", Target{UserID: "ou_owned"})
			if seen := snapshot(); !errors.Is(e, ErrUnknown) || len(seen) != 2 {
				t.Fatal(e, seen)
			}
		})
	}
}
