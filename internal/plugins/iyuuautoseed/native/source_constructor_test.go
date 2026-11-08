package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSourceConstructorQBFixedAuthDoesNotRequireSID(t *testing.T) {
	for _, sample := range []struct {
		name, body string
		status     int
		key        string
		want       bool
	}{
		{"empty no SID", "", 200, "", true}, {"Ok no SID", "Ok.", 200, "", true}, {"Fails", "Fails.", 200, "", false}, {"auth reject", "", 401, "", false}, {"exact text", "Ok.\n", 200, "", false}, {"key app_version", "v5.1.2", 200, "owned-key", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var count atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				path := "/api/v2/auth/login"
				method := "POST"
				if sample.key != "" {
					path = "/api/v2/app/version"
					method = "GET"
					if r.Header.Get("Authorization") != "Bearer owned-key" {
						t.Error("source key auth missing")
					}
				} else {
					r.ParseForm()
					if !reflect.DeepEqual(r.Form, url.Values{"username": {"owned-user"}, "password": {"owned-pass"}}) {
						t.Error("source login form changed")
					}
				}
				if r.URL.Path != path || r.Method != method {
					t.Error("constructor used wrong operation", r.URL.Path, r.Method)
				}
				w.WriteHeader(sample.status)
				io.WriteString(w, sample.body)
			}))
			defer peer.Close()
			c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL, Username: "owned-user", Password: "owned-pass"}, IYUUOptions{APIKey: sample.key})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			err = c.SourceConstructorConnected(context.Background())
			if (err == nil) != sample.want || count.Load() != 1 {
				t.Fatal("constructor fact/auth count", err, count.Load())
			}
		})
	}
}
func TestSourceConstructorTransmissionUsesLegacySessionGetWithoutProbe(t *testing.T) {
	for _, sample := range []struct {
		name, args string
		status     int
		want       bool
	}{
		{"standard", `{"version":"3.00","rpc-version":16}`, 200, true}, {"missing version", `{"rpc-version":16}`, 200, false}, {"bad rpc", `{"version":"3.00","rpc-version":null}`, 200, false}, {"bool rpc", `{"version":null,"rpc-version":true}`, 200, true}, {"auth reject", `{}`, 401, false}, {"HTTP500 valid source JSON", `{"version":"3.00","rpc-version":16}`, 500, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var raw map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&raw) != nil || len(raw) != 2 || string(raw["method"]) != `"session-get"` || string(raw["arguments"]) != `{}` {
					t.Error("source ctor RPC shape changed", raw)
				}
				if n == 1 {
					w.Header().Set("X-Transmission-Session-Id", "owned-session")
					w.WriteHeader(409)
					return
				}
				if r.Header.Get("X-Transmission-Session-Id") != "owned-session" {
					t.Error("missing session handshake")
				}
				w.WriteHeader(sample.status)
				io.WriteString(w, `{"result":"success","arguments":`+sample.args+`}`)
			}))
			defer peer.Close()
			c, err := NewIYUUClient(Config{ID: "owned", Kind: "transmission", BaseURL: peer.URL}, IYUUOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			err = c.SourceConstructorConnected(context.Background())
			if (err == nil) != sample.want || calls.Load() != 2 {
				t.Fatal("TR source ctor fact", err, calls.Load())
			}
		})
	}
}
func TestSourceConstructorPreservesTLSAndCausalGuard(t *testing.T) {
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "Ok.") }))
	defer peer.Close()
	c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SourceConstructorConnected(context.Background()); err == nil {
		t.Fatal("source constructor disabled TLS verification")
	}
	var calls atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "Ok.") }))
	defer plain.Close()
	c, err = NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: plain.URL}, IYUUOptions{Guard: func(context.Context) error { return failure("owned_guard", "denied") }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SourceConstructorConnected(context.Background()); err == nil || !strings.Contains(err.Error(), "transport_failed") || calls.Load() != 0 {
		t.Fatal("ctor guard discarded", err, calls.Load())
	}
}
