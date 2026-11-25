package iyuuautoseed

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceIYUUAPITransport222(t *testing.T) {
	t.Run("owned-TLS-source-wire", testSourceIYUUAPIWire)
	t.Run("closed-origin-and-authority", testIYUUAPITransportAuthority)
}

func TestSourceIYUUAPIRequestFailures222(t *testing.T) {
	var source struct {
		Cases []struct{ Name, Message string }
	}
	raw, err := os.ReadFile("reference/api-http-network-failure-oracle.json")
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Cases) != 2 {
		t.Fatalf("missing original request failure capture: %v", err)
	}
	for _, row := range source.Cases {
		t.Run(row.Name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				conn, stream, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if row.Name == "truncated-body" {
					fmt.Fprint(stream, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\nshort")
					if err := stream.Flush(); err != nil {
						t.Error(err)
					}
				}
			}))
			defer server.Close()
			transport, err := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{
				LoopbackOrigin: server.URL, Guard: func(context.Context) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()
			helper, err := NewHelper(transport, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := helper.request(t.Context(), APIRequest{Operation: SitesIndex})
			if err != nil || result.Failure == nil || result.Failure.Kind != "no_response" ||
				result.Failure.sourceMessage != row.Message || requests.Load() != 1 {
				t.Fatalf("ordinary request failure did not match source: %+v, %v", result, err)
			}
		})
	}
}

func testSourceIYUUAPIWire(t *testing.T) {
	var fixture struct {
		Origin         string
		Hashes         []string
		Unix           int64
		Result         any
		Message        string
		FailureMessage string `json:"failure_message"`
		Calls          []struct {
			Method, URL, Body string
			Headers           map[string]string
			Reply             struct {
				Status  int
				Reason  string
				Headers map[string]string
				Wire    []byte `json:"wire_base64"`
			}
		}
	}
	raw, err := os.ReadFile("reference/api-http-wire-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil || len(fixture.Calls) != 4 {
		t.Fatalf("invalid original wire observations: %v", err)
	}
	var count atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := count.Add(1) - 1
		if at >= int64(len(fixture.Calls)) {
			t.Errorf("unexpected request %d", at)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		want := fixture.Calls[at]
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != want.Method || fixture.Origin+r.URL.RequestURI() != want.URL || string(body) != want.Body {
			t.Errorf("request %d differs from pinned method, path or body", at)
		}
		for _, header := range []string{"User-Agent", "Accept", "Accept-Encoding", "Connection", "token", "Content-Type"} {
			if got, expected := r.Header.Get(header), want.Headers[header]; got != expected {
				t.Errorf("request %d header %s differs from pinned source", at, header)
			}
		}
		if want.Reply.Status >= 400 {
			// net/http normally uses a built-in reason phrase. The source
			// retains the upstream reason rather than substituting one.
			conn, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			fmt.Fprintf(buffered, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", want.Reply.Status, want.Reply.Reason, len(want.Reply.Wire))
			buffered.Write(want.Reply.Wire)
			if err := buffered.Flush(); err != nil {
				t.Error(err)
			}
			return
		}
		w.Header()["Content-Type"] = nil
		for key, value := range want.Reply.Headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(want.Reply.Status)
		w.Write(want.Reply.Wire)
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport, err := NewAPITransport(TrustedAPIBinding{Token: fixture.Calls[0].Headers["token"]}, APITransportOptions{
		Guard: func(context.Context) error { return nil }, RootCAs: roots, LoopbackOrigin: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	helper, err := NewHelper(transport, func() time.Time { return time.Unix(fixture.Unix, 0) })
	if err != nil {
		t.Fatal(err)
	}
	result, err := helper.SeedInfo(t.Context(), fixture.Hashes)
	if err != nil || result.Failure != nil || !reflect.DeepEqual(result.Data, fixture.Result) || fixture.Message != "" {
		t.Fatalf("SeedInfo differs from source: result=%+v error=%v", result, err)
	}
	failure, err := helper.request(t.Context(), APIRequest{Operation: SitesIndex})
	if err != nil || failure.Failure == nil || failure.Failure.Kind != "http" || failure.Failure.sourceMessage != fixture.FailureMessage {
		t.Fatalf("HTTP failure differs from source: result=%+v error=%v", failure, err)
	}
	if count.Load() != int64(len(fixture.Calls)) {
		t.Fatalf("got %d requests, original produced %d", count.Load(), len(fixture.Calls))
	}
}

func testIYUUAPITransportAuthority(t *testing.T) {
	allow := func(context.Context) error { return nil }
	for _, origin := range []string{"http://localhost:80", "http://192.0.2.1", "http://127.0.0.1/path", "http://x@127.0.0.1", "http://127.0.0.1?", "http://127.0.0.1/%2F"} {
		if tr, err := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{Guard: allow, LoopbackOrigin: origin}); err == nil {
			tr.Close()
			t.Errorf("accepted invalid fixture origin %q", origin)
		}
	}
	if _, err := NewAPITransport(TrustedAPIBinding{}, APITransportOptions{}); err == nil {
		t.Fatal("guard was optional")
	}
	var requests, redirects atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/redirect" {
			redirects.Add(1)
			return
		}
		http.Redirect(w, r, "/redirect", http.StatusFound)
	}))
	defer server.Close()
	token := "owned-synthetic-guard-token"
	revoked := false
	transport, err := NewAPITransport(TrustedAPIBinding{Token: token}, APITransportOptions{
		LoopbackOrigin: server.URL,
		Guard: func(context.Context) error {
			if revoked {
				return errors.New("revoked")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	for _, request := range []APIRequest{
		{Operation: SitesIndex},
		{Operation: Operation("arbitrary_rpc")},
		{Operation: ReportExisting, Params: map[string]any{"sid_list": []any{}, "origin": server.URL}},
	} {
		_, err := transport.Call(t.Context(), request)
		if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), server.URL) {
			t.Fatal("invalid request was accepted or exposed transport details")
		}
	}
	revoked = true
	if _, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex}); err == nil {
		t.Fatal("owner revocation was ignored")
	}
	if requests.Load() != 1 || redirects.Load() != 0 {
		t.Fatal("a denied request reached the loopback endpoint")
	}
}
