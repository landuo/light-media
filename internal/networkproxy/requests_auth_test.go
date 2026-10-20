package networkproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type requestsAuthCase struct {
	Name        string            `json:"name"`
	Proxy       string            `json:"proxy"`
	Headers     map[string]string `json:"headers"`
	SourceError string            `json:"source_error"`
}

func mustAuthURL(t *testing.T, value string) *url.URL {
	t.Helper()
	u, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestRequestsHTTPProxyAuthLeavesSOCKSUTF8(t *testing.T) {
	proxy := mustAuthURL(t, "socks5h://%E4%B8%AD:%C3%BF@owned.invalid:1080")
	route, err := requestsRoute(http.DefaultTransport.(*http.Transport), proxy)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://owned.invalid/", nil)
	got, err := route.Proxy(req)
	if err != nil || got.User.Username() != "中" {
		t.Fatal("SOCKS credentials changed")
	}
	password, _ := got.User.Password()
	if password != "ÿ" {
		t.Fatal("SOCKS password changed")
	}
}

func requestsAuthCases(t *testing.T) []requestsAuthCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/requests-http-proxy-auth.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []requestsAuthCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 32 {
		t.Fatal("installed oracle case count changed")
	}
	return fixture.Cases
}

func TestRequestsHTTPProxyAuthInstalledWire(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credential leaked into TLS origin")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()
	for _, tc := range requestsAuthCases(t) {
		if tc.SourceError != "" {
			continue
		}
		for _, tunnel := range []bool{false, true} {
			t.Run(tc.Proxy+map[bool]string{false: "/forward", true: "/connect"}[tunnel], func(t *testing.T) {
				observed := make(chan string, 1)
				proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					observed <- r.Header.Get("Proxy-Authorization")
					if r.Method != "CONNECT" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					upstream, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
					if err != nil {
						t.Error("owned TLS origin dial failed")
						w.WriteHeader(502)
						return
					}
					defer upstream.Close()
					client, buffered, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error("owned CONNECT hijack failed")
						return
					}
					defer client.Close()
					if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
						return
					}
					if err = buffered.Flush(); err != nil {
						return
					}
					done := make(chan struct{})
					go func() { io.Copy(upstream, buffered); upstream.Close(); close(done) }()
					io.Copy(client, upstream)
					client.Close()
					<-done
				}))
				if tc.Proxy[:5] == "https" {
					proxy.StartTLS()
				} else {
					proxy.Start()
				}
				defer proxy.Close()
				parsed, err := RequestsEnvironmentProxy([]string{"http_proxy=" + tc.Proxy}, mustAuthURL(t, "http://owned.invalid"))
				if err != nil {
					t.Fatal(err)
				}
				parsed.Host = proxy.Listener.Addr().String()
				base := http.DefaultTransport.(*http.Transport).Clone()
				base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // owned ephemeral TLS endpoints only
				transport := NewRequestsTransport(base, func() []string { return []string{"http_proxy=" + parsed.String(), "https_proxy=" + parsed.String()} })
				defer transport.CloseIdleConnections()
				target := "http://owned.invalid/synthetic"
				if tunnel {
					target = origin.URL + "/synthetic"
				}
				response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(target)
				if err != nil {
					t.Fatal("owned proxy request failed", err)
				}
				response.Body.Close()
				if response.StatusCode != 204 {
					t.Fatal(response.StatusCode)
				}
				if got, want := <-observed, tc.Headers["Proxy-Authorization"]; got != want {
					t.Fatalf("auth bytes differ from installed source: got %q want %q", got, want)
				}
			})
		}
	}
}

func TestRequestsHTTPProxyAuthUnicodeBeforeDial(t *testing.T) {
	for _, tc := range requestsAuthCases(t) {
		if tc.SourceError == "" {
			continue
		}
		t.Run(tc.Name+tc.Proxy[:5], func(t *testing.T) {
			var calls atomic.Int32
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.DialContext = func(context.Context, string, string) (net.Conn, error) {
				calls.Add(1)
				return nil, errors.New("unexpected owned dial")
			}
			transport := NewRequestsTransport(base, func() []string { return []string{"http_proxy=" + tc.Proxy, "https_proxy=" + tc.Proxy} })
			defer transport.CloseIdleConnections()
			for _, target := range []string{"http://owned.invalid/x", "https://owned.invalid/x"} {
				req, _ := http.NewRequest("GET", target, nil)
				_, err := transport.RoundTrip(req)
				if !errors.Is(err, ErrRequestsProxyAuthEncoding) || err.Error() != ErrRequestsProxyAuthEncoding.Error() {
					t.Fatalf("expected fixed encoding error, got %T", err)
				}
			}
			if calls.Load() != 0 {
				t.Fatal("encoding failure admitted network")
			}
		})
	}
}
