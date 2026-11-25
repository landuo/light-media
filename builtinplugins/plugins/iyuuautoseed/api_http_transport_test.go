package iyuuautoseed

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newOwnedAPITransport(t *testing.T, options APITransportOptions) *APITransport {
	t.Helper()
	options.Guard = func(context.Context) error { return nil }
	transport, err := NewAPITransport(TrustedAPIBinding{Token: "synthetic-api-token"}, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	return transport
}

func TestAPITransportRequestsEnvironment(t *testing.T) {
	for _, row := range []struct {
		name        string
		environment func(string) []string
		proxied     bool
	}{
		{"lowercase", func(proxy string) []string { return []string{"http_proxy=" + proxy} }, true},
		{"lowercase-precedence", func(proxy string) []string {
			return []string{"HTTP_PROXY=http://127.0.0.1:1", "http_proxy=" + proxy}
		}, true},
		{"ALL_PROXY", func(proxy string) []string { return []string{"ALL_PROXY=" + proxy} }, true},
		{"NO_PROXY", func(proxy string) []string {
			return []string{"ALL_PROXY=" + proxy, "NO_PROXY=127.0.0.1"}
		}, false},
		{"no_proxy", func(proxy string) []string {
			return []string{"ALL_PROXY=" + proxy, "no_proxy=127.0.0.1"}
		}, false},
		{"explicit-empty", func(string) []string { return []string{} }, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			var direct, proxied, connections atomic.Int64
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				direct.Add(1)
				fmt.Fprint(w, `{}`)
			}))
			defer destination.Close()
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxied.Add(1)
				if !r.URL.IsAbs() || r.Header.Get("token") != "synthetic-api-token" {
					t.Error("proxy did not receive expected origin request")
				}
				fmt.Fprint(w, `{}`)
			}))
			proxy.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			proxy.Start()
			defer proxy.Close()
			// Even an ambient live proxy must not override a trusted empty snapshot.
			t.Setenv("http_proxy", proxy.URL)
			t.Setenv("ALL_PROXY", proxy.URL)
			environment := row.environment(proxy.URL)
			transport := newOwnedAPITransport(t, APITransportOptions{Environment: environment})
			if transport.origin != "https://2025.iyuu.cn" || transport.timeout != 20*time.Second {
				t.Fatal("production origin or default socket timeout changed")
			}
			// Only this package's test fixture changes the private origin, so the
			// production routing path can run offline without LoopbackOrigin's bypass.
			transport.origin = destination.URL
			for i := range environment {
				environment[i] = "ALL_PROXY=http://127.0.0.1:1"
			}
			t.Setenv("http_proxy", "http://127.0.0.1:1")
			t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
			for range 2 {
				response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
				if err != nil || response == nil || string(response.Body) != `{}` {
					t.Fatalf("snapshot request failed: response=%v error=%v", response, err)
				}
			}
			if row.proxied {
				if proxied.Load() != 2 || direct.Load() != 0 || connections.Load() != 2 {
					t.Fatal("proxy snapshot or per-logical-request connection isolation changed")
				}
			} else if direct.Load() != 2 || proxied.Load() != 0 {
				t.Fatal("explicit direct/NO_PROXY route ignored")
			}
		})
	}
}

func TestAPITransportCLIEnvironmentSnapshot(t *testing.T) {
	var calls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer proxy.Close()
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("no_proxy", "")
	t.Setenv("NO_PROXY", "")
	transport := newOwnedAPITransport(t, APITransportOptions{})
	transport.origin = "http://owned.invalid"
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
	if err != nil || response == nil || calls.Load() != 1 {
		t.Fatal("nil Environment did not freeze CLI environment at construction")
	}
}

func TestAPITransportLoopbackAlwaysDirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
	defer server.Close()
	transport := newOwnedAPITransport(t, APITransportOptions{
		LoopbackOrigin: server.URL, Environment: []string{"ALL_PROXY=http://127.0.0.1:1"},
	})
	response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
	if err != nil || response == nil {
		t.Fatal("loopback fixture used a proxy")
	}
}

func TestAPITransportSocketIdleTimeout(t *testing.T) {
	const idle = 150 * time.Millisecond
	t.Run("progress-has-no-total-time-limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			for _, chunk := range []string{"{", " ", " ", " ", " ", "}"} {
				if _, err := io.WriteString(w, chunk); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				time.Sleep(idle / 3)
			}
		}))
		defer server.Close()
		transport := newOwnedAPITransport(t, APITransportOptions{LoopbackOrigin: server.URL, Timeout: idle})
		start := time.Now()
		response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
		if err != nil || response == nil || string(response.Body) != "{    }" {
			t.Fatalf("progressing response failed: response=%v error=%v", response, err)
		}
		if time.Since(start) <= idle {
			t.Fatal("fixture did not exceed the socket timeout in total")
		}
	})
	for _, phase := range []string{"headers", "body"} {
		t.Run("stall-"+phase, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "body" {
					fmt.Fprint(w, "{")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			transport := newOwnedAPITransport(t, APITransportOptions{LoopbackOrigin: server.URL, Timeout: idle})
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			start := time.Now()
			response, err := transport.Call(ctx, APIRequest{Operation: SitesIndex})
			// Source Requests failures are intentionally an ordinary no_response.
			if err != nil || response != nil || ctx.Err() != nil {
				t.Fatalf("idle stall did not become source no_response: %v, %v", response, err)
			}
			if elapsed := time.Since(start); elapsed < idle/2 || elapsed > 2*time.Second {
				t.Fatalf("unexpected idle stall duration: %s", elapsed)
			}
		})
	}
}

func TestAPITransportContextCancellation(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "body" {
					fmt.Fprint(w, "{")
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			transport := newOwnedAPITransport(t, APITransportOptions{LoopbackOrigin: server.URL})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := transport.Call(ctx, APIRequest{Operation: SitesIndex}); done <- err }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("request never reached cancellation fixture")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("context cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation waited for socket idle timeout")
			}
		})
	}
}

func TestAPITransportProxyHandshakeCancellation(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "2025.iyuu.cn:443" {
			t.Error("proxy request escaped the fixed HTTPS origin")
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		close(started)
		var b [1]byte
		conn.Read(b[:]) // No CONNECT reply: cancellation must close the handshake.
		close(closed)
	}))
	defer proxy.Close()
	transport := newOwnedAPITransport(t, APITransportOptions{Environment: []string{"https_proxy=" + proxy.URL}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := transport.Call(ctx, APIRequest{Operation: SitesIndex}); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy CONNECT did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("proxy handshake cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy handshake did not cancel promptly")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled CONNECT socket remained open")
	}
}

func TestAPITransportFixedOriginHTTPSProxy(t *testing.T) {
	for _, variable := range []string{"https_proxy", "ALL_PROXY"} {
		t.Run(variable, func(t *testing.T) {
			var originCalls, connects atomic.Int64
			destination := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				if r.Host != "2025.iyuu.cn" || r.URL.Path != "/reseed/sites/index" || r.Header.Get("token") != "synthetic-api-token" || r.Header.Get("Proxy-Authorization") != "" {
					t.Error("fixed origin or proxy credential isolation changed")
				}
				fmt.Fprint(w, `{}`)
			}))
			// Mint only a local test certificate for the production hostname.
			templateServer := httptest.NewTLSServer(http.NotFoundHandler())
			certificate := templateServer.TLS.Certificates[0]
			template := *templateServer.Certificate()
			templateServer.Close()
			template.IPAddresses = nil
			template.DNSNames = []string{"2025.iyuu.cn"}
			der, err := x509.CreateCertificate(rand.Reader, &template, &template, template.PublicKey, certificate.PrivateKey)
			if err != nil {
				t.Fatal(err)
			}
			certificate.Certificate = [][]byte{der}
			destination.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
			destination.StartTLS()
			defer destination.Close()
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connects.Add(1)
				if r.Method != http.MethodConnect || r.Host != "2025.iyuu.cn:443" || r.Header.Get("token") != "" {
					t.Error("CONNECT escaped fixed origin or leaked origin token")
					return
				}
				upstream, err := net.DialTimeout("tcp", destination.Listener.Addr().String(), time.Second)
				if err != nil {
					t.Error("owned tunnel destination dial failed")
					return
				}
				defer upstream.Close()
				conn, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n")
				if err := buffered.Flush(); err != nil {
					return
				}
				go func() {
					io.Copy(upstream, buffered)
					upstream.Close()
				}()
				io.Copy(conn, upstream)
			}))
			defer proxy.Close()
			roots := x509.NewCertPool()
			roots.AddCert(destination.Certificate())
			transport := newOwnedAPITransport(t, APITransportOptions{RootCAs: roots, Environment: []string{variable + "=" + proxy.URL}})
			response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
			if err != nil || response == nil || connects.Load() != 1 || originCalls.Load() != 1 {
				t.Fatalf("fixed origin TLS tunnel failed: response=%v error=%v", response, err)
			}
		})
	}
}

func TestAPITransportBodyLimitPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat(" ", maxAPIResponse+1))
	}))
	defer server.Close()
	transport := newOwnedAPITransport(t, APITransportOptions{LoopbackOrigin: server.URL})
	response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
	if response != nil || !errors.Is(err, errAPIResponseLimit) {
		t.Fatalf("body limit lost: response=%v error=%v", response, err)
	}
}

func TestAPITransportTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
	defer server.Close()
	t.Run("untrusted", func(t *testing.T) {
		transport := newOwnedAPITransport(t, APITransportOptions{LoopbackOrigin: server.URL})
		response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
		if response != nil || err == nil || err.Error() != "IYUU TLS verification failed" {
			t.Fatalf("untrusted TLS response=%v error=%v", response, err)
		}
	})
	t.Run("trusted", func(t *testing.T) {
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		transport := newOwnedAPITransport(t, APITransportOptions{LoopbackOrigin: server.URL, RootCAs: roots})
		response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
		if err != nil || response == nil {
			t.Fatalf("trusted TLS response=%v error=%v", response, err)
		}
	})
	t.Run("trusted-root-still-verifies-hostname", func(t *testing.T) {
		certificate := server.TLS.Certificates[0]
		template := *server.Certificate()
		template.IPAddresses = nil
		template.DNSNames = []string{"different.invalid"}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, template.PublicKey, certificate.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		certificate.Certificate = [][]byte{der}
		mismatch := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
		mismatch.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
		mismatch.StartTLS()
		defer mismatch.Close()
		roots := x509.NewCertPool()
		roots.AddCert(mismatch.Certificate())
		transport := newOwnedAPITransport(t, APITransportOptions{LoopbackOrigin: mismatch.URL, RootCAs: roots})
		response, err := transport.Call(t.Context(), APIRequest{Operation: SitesIndex})
		if response != nil || err == nil || !strings.Contains(err.Error(), "TLS verification failed") {
			t.Fatalf("hostname verification lost: response=%v error=%v", response, err)
		}
	})
}
