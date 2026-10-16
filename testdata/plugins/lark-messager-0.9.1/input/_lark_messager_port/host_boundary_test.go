package larkmessager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDirectoryResultsCannotAuthorizeUnboundRecipients(t *testing.T) {
	for _, recipient := range []bool{false, true} {
		t.Run(str(recipient), func(t *testing.T) {
			var sent atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "batch_get_id") {
					io.WriteString(w, `{"code":0,"data":{"user_list":[{"email":"owned@example.invalid","user_id":"ou_derived"},{"email":"not-requested@example.invalid","user_id":"ou_not_requested"}]}}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/messages") {
					sent.Add(1)
				}
				apiSuccess(w, r)
			}))
			defer s.Close()
			b := testBinding()
			b.Targets = nil
			b.ContactIdentifiers = []string{"owned@example.invalid"}
			if recipient {
				b.RecipientIdentifiers = []string{"owned@example.invalid", "not-requested@example.invalid"}
			}
			a, e := NewAdapter(b, NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
			if e != nil {
				t.Fatal(e)
			}
			c := NewClient(a)
			if _, e = c.BatchGetID(context.Background(), []string{"owned@example.invalid"}, nil, nil); e != nil {
				t.Fatal(e)
			}
			_, e = c.SendText(context.Background(), "hello", Target{UserID: "ou_derived"})
			if recipient {
				if e != nil || sent.Load() != 1 {
					t.Fatal(e, sent.Load())
				}
			} else if !errors.Is(e, ErrDenied) || sent.Load() != 0 {
				t.Fatal(e, sent.Load())
			}
			_, e = c.SendText(context.Background(), "hello", Target{UserID: "ou_not_requested"})
			if !errors.Is(e, ErrDenied) {
				t.Fatal(e)
			}
		})
	}
}

func TestGuardRecheckedAfterAuthentication(t *testing.T) {
	var retired atomic.Bool
	var effects atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tenant_access_token/") {
			retired.Store(true)
		} else {
			effects.Add(1)
		}
		apiSuccess(w, r)
	}))
	defer s.Close()
	a, e := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL, Guard: func(_ context.Context, s Scope) error {
		if s.Owner != "owned" || s.Generation != 1 || s.Revision != 1 || s.InvocationID != "synthetic" {
			return ErrDenied
		}
		if retired.Load() {
			return ErrDenied
		}
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = NewClient(a).SendText(context.Background(), "text", Target{UserID: "ou_owned"})
	if !errors.Is(e, ErrDenied) || effects.Load() != 0 {
		t.Fatal(e, effects.Load())
	}
}

func TestDisplayFilenameUsesOnlyGrantedBytesAndSourceMIME(t *testing.T) {
	var uploads, sends atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/files") {
			uploads.Add(1)
			mr, e := r.MultipartReader()
			if e != nil {
				t.Error(e)
				return
			}
			part, e := mr.NextPart()
			if e != nil {
				t.Error(e)
				return
			}
			if part.FileName() != "shown.bin" || part.Header.Get("Content-Type") != "application/octet-stream" || string(mustRead(part)) != "owned bytes" {
				t.Error("wrong bound file")
			}
			io.WriteString(w, `{"code":0,"data":{"file_key":"file_derived"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			sends.Add(1)
		}
		apiSuccess(w, r)
	}))
	defer s.Close()
	b := testBinding()
	b.Files = map[string]Blob{"owned-file": {Name: "original.bin", Data: []byte("owned bytes")}}
	a, e := NewAdapter(b, NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	r, e := NewClient(a).SendFile(context.Background(), "owned-file", "", "", "shown.bin", Target{UserID: "ou_owned"})
	if e != nil || !r.Success || uploads.Load() != 1 || sends.Load() != 1 {
		t.Fatal(r, e, uploads.Load(), sends.Load())
	}
	_, e = a.API(context.Background(), Request{Op: "upload_file", Blob: &Blob{Name: "shown.bin", MIME: "application/octet-stream", Data: []byte("ungranted bytes")}})
	if !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
}

func TestFetchChatsDoesNotPersistPrivateServerMessages(t *testing.T) {
	private := "PRIVATE_RESPONSE_SYNTHETIC_SECRET"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tenant_access_token/") {
			apiSuccess(w, r)
			return
		}
		io.WriteString(w, `{"code":999,"msg":"`+private+`"}`)
	}))
	defer s.Close()
	a, e := NewAdapter(testBinding(), NewMemoryJournal(), Options{TestAPIOrigin: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	kv := newTestKV()
	p := New(PluginHost{API: a, KV: kv})
	_, e = p.Init(Object{"enabled": true, "app_id": "SYNTHETIC_APP", "app_secret": "SYNTHETIC_SECRET"})
	if e != nil {
		t.Fatal(e)
	}
	r, e := p.FetchChats(context.Background())
	if e != nil || r["ok"] != false || strings.Contains(string(uuidJSON(kv.data)), private) {
		t.Fatal(r, e)
	}
}

func TestUnsupportedNonFiniteCardKitDoesNotConfirm(t *testing.T) {
	for _, body := range []string{`{"code":NaN}`, `{"code":Infinity}`, `{"code":-Infinity}`, `{"code":0,"extra":NaN}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/tenant_access_token/") {
					apiSuccess(w, r)
					return
				}
				io.WriteString(w, body)
			}))
			defer s.Close()
			j := NewMemoryJournal()
			a, e := NewAdapter(testBinding(), j, Options{TestAPIOrigin: s.URL})
			if e != nil {
				t.Fatal(e)
			}
			ok, e := NewClient(a).UpdateStreaming(context.Background(), "card_owned", StreamBodyID, "text", 1)
			if ok || !errors.Is(e, ErrUnknown) {
				t.Fatal(ok, e)
			}
			for _, row := range j.Snapshot() {
				if row.State != "unknown" {
					t.Fatal(row.State)
				}
			}
		})
	}
}
