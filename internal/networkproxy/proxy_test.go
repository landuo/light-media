package networkproxy

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOriginalProxyASTFixtures(t *testing.T) {
	var source struct {
		Cases []struct {
			Setting  string
			Env      map[string]string
			Expected map[string]string
		}
	}
	b, err := os.ReadFile("testdata/original-proxy.json")
	if err != nil || json.Unmarshal(b, &source) != nil {
		t.Fatal(err)
	}
	for _, tc := range source.Cases {
		c, err := Resolve(tc.Setting, func(k string) string { return tc.Env[k] })
		if err != nil || c.HTTP != tc.Expected["http"] || c.HTTPS != tc.Expected["https"] {
			t.Fatalf("source mapping differs: %v", err)
		}
	}
}

func TestProxyRejectsMalformedURLsAndHidesCredentials(t *testing.T) {
	for _, s := range []string{"ftp://host", "http://", "http://host:0", "http://host:65536", "http://host/path", "http://host?key=x", "http://host/#fragment", "http://host\n"} {
		if _, err := Parse(s); err == nil {
			t.Fatalf("accepted malformed %q", s)
		}
	}
	if got := Endpoint("http://owned-user:owned-password@127.0.0.1:7890"); got != "http://127.0.0.1:7890" {
		t.Fatal("public endpoint leaks auth")
	}
	c := Config{HTTP: "http://owned-user:owned-password@127.0.0.1:1"}
	rt, err := c.Transport(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.(interface{ CloseIdleConnections() }).CloseIdleConnections()
	req, _ := http.NewRequest("GET", "http://owned-target.invalid/", nil)
	_, err = rt.RoundTrip(req)
	if err == nil || strings.Contains(err.Error(), "owned-") {
		t.Fatal("transport error credential leak", err)
	}
}

func TestHTTPProxyCarriesOnlyProxyAuthentication(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Host != "owned-target.invalid" || r.Header.Get("Proxy-Authorization") != "Basic b3duZWQtdXNlcjpvd25lZC1wYXNz" || r.Header.Get("Authorization") != "Bearer owned-target-token" {
			t.Error("proxy request/auth mismatch")
		}
		io.WriteString(w, "owned proxy response")
	}))
	defer server.Close()
	c := Config{HTTP: strings.Replace(server.URL, "://", "://owned-user:owned-pass@", 1)}
	rt, err := c.Transport(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.(interface{ CloseIdleConnections() }).CloseIdleConnections()
	req, _ := http.NewRequest("GET", "http://owned-target.invalid/", nil)
	req.Header.Set("Authorization", "Bearer owned-target-token")
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "owned proxy response" || calls != 1 {
		t.Fatal("actual proxy not used")
	}
}

func TestHTTPSProxyCONNECTKeepsTLSAndOriginAuth(t *testing.T) {
	var originCalls, connects int
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls++
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credential forwarded to origin")
		}
		io.WriteString(w, "owned TLS response")
	}))
	defer origin.Close()
	proxyServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects++
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 400)
			return
		}
		target, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, "owned dial failed", 502)
			return
		}
		incoming, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			target.Close()
			return
		}
		defer incoming.Close()
		defer target.Close()
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		done := make(chan struct{})
		go func() { io.Copy(target, buffer); target.Close(); close(done) }()
		io.Copy(incoming, target)
		incoming.Close()
		<-done
	}))
	defer proxyServer.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	roots.AddCert(proxyServer.Certificate())
	base.TLSClientConfig.RootCAs = roots
	rt, err := (Config{HTTPS: proxyServer.URL}).Transport(base)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.(interface{ CloseIdleConnections() }).CloseIdleConnections()
	client := http.Client{Transport: rt, Timeout: 5 * time.Second}
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "owned TLS response" || connects != 1 || originCalls != 1 {
		t.Fatal("CONNECT/TLS path not executed")
	}
}

type socksReceipt struct {
	host           string
	addressType    byte
	user, password string
}

func ownedSOCKS(t *testing.T, target string) (string, <-chan socksReceipt) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	receipts := make(chan socksReceipt, 4)
	var wg sync.WaitGroup
	t.Cleanup(func() { listener.Close(); wg.Wait() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				header := make([]byte, 2)
				if _, err := io.ReadFull(reader, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(reader, methods); err != nil {
					return
				}
				conn.Write([]byte{5, 2})
				version, err := reader.ReadByte()
				if err != nil || version != 1 {
					return
				}
				ul, _ := reader.ReadByte()
				user := make([]byte, int(ul))
				io.ReadFull(reader, user)
				pl, _ := reader.ReadByte()
				pass := make([]byte, int(pl))
				io.ReadFull(reader, pass)
				conn.Write([]byte{1, 0})
				head := make([]byte, 4)
				if _, err := io.ReadFull(reader, head); err != nil {
					return
				}
				var host string
				switch head[3] {
				case 1:
					b := make([]byte, 4)
					io.ReadFull(reader, b)
					host = net.IP(b).String()
				case 4:
					b := make([]byte, 16)
					io.ReadFull(reader, b)
					host = net.IP(b).String()
				case 3:
					l, _ := reader.ReadByte()
					b := make([]byte, int(l))
					io.ReadFull(reader, b)
					host = string(b)
				default:
					return
				}
				p := make([]byte, 2)
				io.ReadFull(reader, p)
				_ = binary.BigEndian.Uint16(p)
				receipts <- socksReceipt{host: host, addressType: head[3], user: string(user), password: string(pass)}
				remote, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer remote.Close()
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(remote, reader); remote.Close(); close(done) }()
				io.Copy(conn, remote)
				conn.Close()
				<-done
			}()
		}
	}()
	return listener.Addr().String(), receipts
}

func TestSOCKS5LocalAndSOCKS5hRemoteDestinationDNS(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy auth leaked")
		}
		io.WriteString(w, "owned SOCKS response")
	}))
	defer origin.Close()
	address, receipts := ownedSOCKS(t, strings.TrimPrefix(origin.URL, "http://"))
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			rt, err := (Config{HTTP: scheme + "://owned-user:owned-pass@" + address}).Transport(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.(interface{ CloseIdleConnections() }).CloseIdleConnections()
			host := "localhost"
			if scheme == "socks5h" {
				host = "owned-remote.invalid"
			}
			client := http.Client{Transport: rt, Timeout: 5 * time.Second}
			response, err := client.Get("http://" + host + ":12345/")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(b) != "owned SOCKS response" {
				t.Fatal("wrong response")
			}
			r := <-receipts
			if r.user != "owned-user" || r.password != "owned-pass" {
				t.Fatal("SOCKS credentials absent")
			}
			if scheme == "socks5" && net.ParseIP(r.host) == nil {
				t.Fatal("local DNS not used")
			}
			if scheme == "socks5h" && (r.addressType != 3 || r.host != host) {
				t.Fatal("remote DNS not used")
			}
		})
	}
}

func TestSOCKSHandshakeRespectsParentCancellation(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	rt, err := (Config{HTTP: "socks5://" + l.Addr().String()}).Transport(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.(interface{ CloseIdleConnections() }).CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:1/", nil)
	done := make(chan error, 1)
	go func() { _, err := rt.RoundTrip(req); done <- err }()
	conn := <-accepted
	defer conn.Close()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent cancellation ignored")
	}
}
