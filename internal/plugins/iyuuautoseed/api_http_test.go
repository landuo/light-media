package iyuuautoseed

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPITransportSourceWire(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("token") != "synthetic-token" {
			t.Error("missing token")
		}
		if r.Header.Get("Accept") != "*/*" {
			t.Error("wrong Accept")
		}
		raw, _ := io.ReadAll(r.Body)
		switch n {
		case 1:
			if r.Method != "GET" || r.URL.Path != "/reseed/sites/index" || len(raw) != 0 {
				t.Error("bad index request")
			}
			io.WriteString(w, `{"code":0,"data":{"sites":[{"id":1,"base_url":"https://invalid/"},{"id":"汉字","base_url":"https://invalid/"}]}}`)
		case 2:
			if r.Method != "POST" || r.URL.Path != "/reseed/sites/reportExisting" || string(raw) != `{"sid_list": [1, "\u6c49\u5b57"]}` {
				t.Errorf("report wire %q", raw)
			}
			io.WriteString(w, `{"code":0,"data":{"sid_sha1":"s"}}`)
		case 3:
			compact := `["a","😀 "]`
			want, _ := orderedJSON([]jsonField{{"hash", compact}, {"sha1", SHA1(compact)}, {"sid_sha1", "s"}, {"timestamp", json.Number("1720000000")}, {"version", "8.2.0"}}, true)
			if r.URL.Path != "/reseed/index/index" || string(raw) != string(want) {
				t.Errorf("index wire\ngot %s\nwant%s", raw, want)
			}
			io.WriteString(w, `{"code":0,"data":{"ok":true}}`)
		default:
			t.Error("unexpected call")
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport, e := NewAPITransport(TrustedAPIBinding{Token: "synthetic-token"}, APITransportOptions{Guard: func(context.Context) error { return nil }, RootCAs: roots, LoopbackOrigin: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	defer transport.Close()
	h, _ := NewHelper(transport, func() time.Time { return time.Unix(1720000000, 0) })
	r, e := h.SeedInfo(context.Background(), []string{"😀 ", "a"})
	if e != nil || r.Failure != nil {
		t.Fatalf("%v %v", r.Failure, e)
	}
	if calls.Load() != 3 {
		t.Fatal("call count")
	}
}
func TestAPITransportAuthorityAndFailure(t *testing.T) {
	for _, origin := range []string{"http://localhost:80", "http://192.0.2.1", "http://127.0.0.1/path", "http://x@127.0.0.1", "http://127.0.0.1?x=y"} {
		if _, e := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{Guard: func(context.Context) error { return nil }, LoopbackOrigin: origin}); e == nil {
			t.Fatalf("accepted %s", origin)
		}
	}
	if _, e := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{}); e == nil {
		t.Fatal("guard optional")
	}
	var received atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer s.Close()
	tr, e := NewAPITransport(TrustedAPIBinding{Token: "synthetic-sensitive"}, APITransportOptions{Guard: func(context.Context) error { return nil }, LoopbackOrigin: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	defer tr.Close()
	_, e = tr.Call(context.Background(), APIRequest{Operation: SitesIndex})
	if e == nil || strings.Contains(e.Error(), "sensitive") || strings.Contains(e.Error(), s.URL) {
		t.Fatal("unsafe error", e)
	}
	if received.Load() != 0 {
		t.Fatal("redirect followed")
	}
	if _, e = tr.Call(context.Background(), APIRequest{Operation: Operation("arbitrary_rpc")}); e == nil {
		t.Fatal("unclosed operation")
	}
	if _, e = tr.Call(context.Background(), APIRequest{Operation: ReportExisting, Params: map[string]any{"sid_list": []any{}, "origin": other.URL}}); e == nil {
		t.Fatal("unknown params allowed")
	}
	tr.guard = func(context.Context) error { return errors.New("revoked") }
	if _, e = tr.Call(context.Background(), APIRequest{Operation: SitesIndex}); e == nil {
		t.Fatal("revocation ignored")
	}
}
func TestAPITransportCancellationAndTLS(t *testing.T) {
	entered := make(chan struct{})
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer s.Close()
	tr, _ := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{Guard: func(context.Context) error { return nil }, LoopbackOrigin: s.URL})
	defer tr.Close()
	if _, e := tr.Call(context.Background(), APIRequest{Operation: SitesIndex}); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	tr2, _ := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{Guard: func(context.Context) error { return nil }, RootCAs: roots, LoopbackOrigin: s.URL})
	defer tr2.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := tr2.Call(ctx, APIRequest{Operation: SitesIndex}); done <- e }()
	<-entered
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
