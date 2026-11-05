package browserrender

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The fixed helper passes an authenticated proxy to Chromium. The Go adapter
// keeps those credentials in a private relay, including the ordinary HTTPS
// CONNECT path; TLS still terminates at the requested site with verification.
func TestProxyActualHTTPSConnectAuthenticationAndCleanup(t *testing.T) {
	for _, auth := range []struct {
		name, username, password string
		source                   bool
	}{
		{"ordinary", "owned-user", "owned-password", false},
		{"source-percent-literals", "owned%40name", "synthetic%3Avalue", true},
	} {
		t.Run(auth.name, func(t *testing.T) {
			var siteCalls, connectCalls atomic.Int32
			site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				siteCalls.Add(1)
				if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "owned-site=one" {
					t.Error("proxy credentials crossed the TLS site boundary or site cookie was lost")
				}
				fmt.Fprint(w, "owned TLS response")
			}))
			defer site.Close()
			target, _ := url.Parse(site.URL)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connectCalls.Add(1)
				if r.Method != http.MethodConnect || r.Host != target.Host || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(auth.username+":"+auth.password)) {
					t.Error("authenticated upstream CONNECT changed")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				siteConnection, err := net.DialTimeout("tcp", target.Host, time.Second)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				defer siteConnection.Close()
				client, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer client.Close()
				_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
				if err = buffered.Flush(); err != nil {
					return
				}
				done := make(chan struct{})
				go func() { _, _ = io.Copy(siteConnection, buffered); siteConnection.Close(); close(done) }()
				_, _ = io.Copy(client, siteConnection)
				client.Close()
				<-done
			}))
			defer upstream.Close()
			proxyURL, _ := url.Parse(upstream.URL)
			proxyURL.User = url.UserPassword(auth.username, auth.password)
			rawProxy := proxyURL.String()
			if auth.source {
				var err error
				rawProxy, err = (ProxySettings{Server: upstream.URL, Username: auth.username, Password: auth.password}).nativeURL()
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			address, cleanup, err := browserProxy(ctx, rawProxy, func(context.Context) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			relay, _ := url.Parse(address)
			transport := site.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = http.ProxyURL(relay)
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, site.URL, nil)
			request.Header.Set("Cookie", "owned-site=one")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			contents, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(contents) != "owned TLS response" || siteCalls.Load() != 1 || connectCalls.Load() != 1 {
				t.Fatalf("HTTPS relay did not reach its verified site: %q %v", contents, err)
			}
			cleanup()
			connection, err := net.DialTimeout("tcp", relay.Host, time.Second)
			if err == nil {
				connection.Close()
				t.Fatal("private relay remained open after browser cleanup")
			}
		})
	}
}

func TestProxyCONNECTFailureDoesNotBecomeSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusProxyAuthRequired) }))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	address, cleanup, err := browserProxy(ctx, upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	relay, _ := url.Parse(address)
	connection, err := net.DialTimeout("tcp", relay.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(connection, "CONNECT owned.invalid:443 HTTP/1.1\r\nHost: owned.invalid:443\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusBadGateway || strings.Contains(response.Header.Get("Proxy-Authenticate"), "owned") {
		t.Fatalf("upstream denial was not preserved as relay failure: %v %v", response, err)
	}
	response.Body.Close()
}
