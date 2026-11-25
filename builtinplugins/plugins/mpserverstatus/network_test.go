package mpserverstatus

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// Native owned TLS peer compared against actual Python3.12/Requests source
// observations. No public DNS, proxy, original application or real service.
func TestNativeRequestUtilsActual312Reference(t *testing.T) {
	raw, e := os.ReadFile("reference/source-http-reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Provenance Object
		Cases      []struct {
			Path, Mode string
			Result     Object
		}
	}
	if e = decode(raw, &source); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(source.Provenance["python"].(string), "3.12.13") {
		t.Fatal("wrong source runtime")
	}
	var mu sync.Mutex
	wire := []Object{}
	var zstd []byte
	for _, c := range source.Cases {
		if c.Path == "/zstd" {
			zstd, e = hex.DecodeString(c.Result["content_hex"].(string))
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers := map[string]string{}
		for k, v := range r.Header {
			headers[k] = strings.Join(v, ", ")
		}
		wire = append(wire, Object{"path": r.URL.Path, "headers": headers})
		mu.Unlock()
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected credentials")
		}
		if r.URL.Path == "/slow" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(150 * time.Millisecond):
			}
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/status")
			w.WriteHeader(302)
			return
		}
		body := []byte(stubStatus)
		contentType := "text/plain"
		encoding := ""
		switch r.URL.Path {
		case "/latin1":
			body = append(body, 0xe9, 0x80)
		case "/utf8":
			body = append(body, []byte("标签")...)
			contentType = "text/plain; charset=utf-8"
		case "/invalid-utf8":
			body = append(body, 0xff, 0xff, 0xe2, 0x82)
			contentType = "text/plain; charset=utf-8"
		case "/json-default":
			body = append(body, []byte("标签")...)
			contentType = "application/json"
		case "/apparent":
			body = append(body, []byte("标签")...)
			contentType = ""
		case "/gzip":
			var b bytes.Buffer
			z := gzip.NewWriter(&b)
			_, _ = z.Write(body)
			_ = z.Close()
			body = b.Bytes()
			encoding = "gzip"
		case "/deflate":
			var b bytes.Buffer
			z := zlib.NewWriter(&b)
			_, _ = z.Write(body)
			_ = z.Close()
			body = b.Bytes()
			encoding = "deflate"
		case "/deflate-raw":
			var b bytes.Buffer
			z, _ := flate.NewWriter(&b, flate.DefaultCompression)
			_, _ = z.Write(body)
			_ = z.Close()
			body = b.Bytes()
			encoding = "deflate"
		case "/br":
			var b bytes.Buffer
			z := brotli.NewWriter(&b)
			_, _ = z.Write(body)
			_ = z.Close()
			body = b.Bytes()
			encoding = "br"
		case "/zstd":
			body = zstd
			encoding = "zstd"
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		} else {
			w.Header()["Content-Type"] = nil
		}
		if encoding != "" {
			w.Header().Set("Content-Encoding", encoding)
		}
		size := len(body)
		if r.URL.Path == "/truncated" {
			size += 10
		}
		w.Header().Set("Content-Length", fmt.Sprint(size))
		if r.URL.Path == "/503" {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(200)
		}
		if r.URL.Path == "/trickle" {
			for offset := 0; offset < len(body); offset += 12 {
				time.Sleep(30 * time.Millisecond)
				end := offset + 12
				if end > len(body) {
					end = len(body)
				}
				_, _ = w.Write(body[offset:end])
				w.(http.Flusher).Flush()
			}
		} else {
			_, _ = w.Write(body)
		}
	}))
	defer server.Close()
	environment := []string{}
	native := &NativeNetwork{environment: &environment}
	for _, c := range source.Cases {
		if c.Path == "" {
			continue
		}
		t.Run(c.Path, func(t *testing.T) {
			mu.Lock()
			wire = nil
			mu.Unlock()
			timeout := time.Second
			if c.Path == "/slow" {
				timeout = 30 * time.Millisecond
			}
			if c.Path == "/trickle" {
				timeout = 100 * time.Millisecond
			}
			start := time.Now()
			got, e := native.Status(context.Background(), server.URL+c.Path, timeout)
			if e != nil {
				t.Fatal(e)
			}
			elapsed := time.Since(start)
			if truth(c.Result["none"]) {
				if got != nil {
					t.Fatal("source RequestException should return None")
				}
				return
			}
			if got == nil {
				t.Fatal("unexpected None")
			}
			if got.Status != int(number(c.Result["status"])) || nativeTestText(t, got) != c.Result["text"] || hex.EncodeToString(got.Content) != c.Result["content_hex"] || got.Protocol != int(number(c.Result["protocol"])) {
				t.Fatalf("source response mismatch: status=%d text=%q protocol=%d", got.Status, nativeTestText(t, got), got.Protocol)
			}
			if c.Path == "/trickle" && elapsed <= timeout {
				t.Fatal("read idle timeout became total timeout")
			}
			mu.Lock()
			defer mu.Unlock()
			expectedWire := 1
			if c.Path == "/redirect" {
				expectedWire = 2
			}
			if len(wire) != expectedWire {
				t.Fatal("source request count", wire)
			}
			for i, w := range wire {
				headers := w["headers"].(map[string]string)
				ct := "application/x-www-form-urlencoded; charset=UTF-8"
				if c.Path == "/redirect" && i == 1 {
					ct = ""
				}
				if headers["User-Agent"] != "python-urllib3/2.7.0" || headers["Accept-Encoding"] != "gzip, deflate, br" || headers["Content-Type"] != ct {
					t.Fatal("source wire mismatch", headers)
				}
				if headers["Accept"] != "" || headers["Referer"] != "" {
					t.Fatal("source headers invented", headers)
				}
			}
		})
	}
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	var portInt int
	_, _ = fmt.Sscan(port, &portInt)
	if _, e := native.TLS(context.Background(), host, portInt, time.Second); e == nil {
		t.Fatal("independent TLS probe must reject source HTTP's untrusted certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	native.roots = &tls.Config{RootCAs: roots}
	cert, e := native.TLS(context.Background(), host, portInt, time.Second)
	if e != nil || cert.NotAfter == "" {
		t.Fatal("verified owned TLS", e)
	}
	ips, e := native.DNS(context.Background(), host, portInt)
	if e != nil || len(ips) != 1 || ips[0] != host {
		t.Fatal(ips, e)
	}
}

func TestNativeRedirectLimitSessionCookiesAndCancellation(t *testing.T) {
	var requests int
	var mu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		switch r.URL.Path {
		case "/loop":
			w.Header().Set("Location", "/loop")
			w.WriteHeader(302)
		case "/cookie":
			if r.Header.Get("Cookie") != "" {
				t.Error("cookie leaked across independent source sessions")
			}
			http.SetCookie(w, &http.Cookie{Name: "owned", Value: "source", Path: "/"})
			w.Header().Set("Location", "/cookie-target")
			w.WriteHeader(307)
		case "/cookie-target":
			if r.Header.Get("Cookie") != "owned=source" || r.Header.Get("Content-Type") == "" {
				t.Error("source redirect session lost", r.Header)
			}
			_, _ = io.WriteString(w, stubStatus)
		case "/block":
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	environment := []string{}
	n := &NativeNetwork{environment: &environment}
	got, e := n.Status(context.Background(), server.URL+"/loop", time.Second)
	if e != nil || got != nil {
		t.Fatal(got, e)
	}
	if requests != 31 {
		t.Fatal("source permits exactly 30 redirects,31 totalrequests", requests)
	}
	for i := 0; i < 2; i++ {
		if got, e = n.Status(context.Background(), server.URL+"/cookie", time.Second); got == nil || e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := n.Status(ctx, server.URL+"/block", time.Second); done <- e }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case e := <-done:
		if e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled native request leaked")
	}
}

// The original RequestUtils returns None on ordinary network failures. A host
// scope failure must still be surfaced and cannot become that source None.
func TestNativeHostGuardFailure(t *testing.T) {
	sentinel := fmt.Errorf("owned private capability rejected")
	n := &NativeNetwork{caller: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel }}
	_, e := n.Status(context.Background(), ServerURL, time.Second)
	var boundary HostBoundaryError
	if !errorsAs(e, &boundary) {
		t.Fatal("host boundary swallowed", e)
	}
}
func errorsAs(e error, target *HostBoundaryError) bool {
	for e != nil {
		if x, ok := e.(HostBoundaryError); ok {
			*target = x
			return true
		}
		x, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = x.Unwrap()
	}
	return false
}

// Actual socket.py823 / urllib3.util.connection.py source performs lookup
// before serial per-address connect attempts. Pure Go must not accidentally
// introduce Happy Eyeballs or make the connect timeout a DNS timeout.
func TestNativeConnectPreservesSourceAddressOrder(t *testing.T) {
	lookupDone := false
	attempts := []string{}
	success, other := net.Pipe()
	defer success.Close()
	defer other.Close()
	n := &NativeNetwork{lookup: func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host != "owned.invalid" {
			t.Fatal(host)
		}
		// This is an owned lookup boundary, not actual DNS. It deliberately takes
		// longer than the connect timeout, as source getaddrinfo can do.
		time.Sleep(15 * time.Millisecond)
		lookupDone = true
		return []net.IPAddr{{IP: net.ParseIP("2001:db8::1")}, {IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("192.0.2.2")}}, nil
	}, dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if !lookupDone || ctx.Err() != nil {
			t.Fatal("whole connect timeout incorrectly included lookup")
		}
		attempts = append(attempts, address)
		if len(attempts) < 3 {
			return nil, fmt.Errorf("owned socket rejection")
		}
		return success, nil
	}}
	got, e := n.connect(context.Background(), "tcp", "owned.invalid:443", time.Millisecond)
	if e != nil || got != success || strings.Join(attempts, ",") != "[2001:db8::1]:443,192.0.2.1:443,192.0.2.2:443" {
		t.Fatal(attempts, e)
	}
}

func TestNativeNoHiddenGETRetryAfterRedirectSocketDrop(t *testing.T) {
	raw, e := os.ReadFile("reference/source-redirect-drop-reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct{ Trace []string }
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	trace := []string{}
	drops := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		trace = append(trace, r.URL.Path)
		if r.URL.Path == "/drop" {
			drops++
		}
		count := drops
		mu.Unlock()
		if r.Header.Get("Content-Length") != "" || r.Header.Get("Transfer-Encoding") != "" || r.ContentLength != 0 {
			t.Error("non-retry adapter changed source empty wire body", r.Header)
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/drop")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(302)
			return
		}
		if count == 1 {
			socket, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			socket.Close()
			return
		}
		_, _ = io.WriteString(w, stubStatus)
	}))
	defer server.Close()
	environment := []string{}
	n := &NativeNetwork{environment: &environment}
	got, e := n.Status(context.Background(), server.URL+"/redirect", time.Second)
	if e != nil || got != nil {
		t.Fatal("source max_retries=0 must return None", got, e)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(trace, ",") != strings.Join(source.Trace, ",") {
		t.Fatal("unexpected automatic native GET replay", trace, source.Trace)
	}
}

func TestNativeHTTPSProxyVerifiedBeforeCredentials(t *testing.T) {
	var proxyCalls, originCalls atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials escaped to destination")
		}
		if r.URL.Path == "/status" {
			w.Header().Set("Location", "/final")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(302)
			return
		}
		_, _ = io.WriteString(w, stubStatus)
	}))
	defer origin.Close()
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Method != "CONNECT" || r.Host != "movie-pilot.org:443" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("owned-user:owned-secret")) {
			t.Error("canonical owned CONNECT/auth mismatch")
			http.Error(w, "denied", 403)
			return
		}
		upstream, e := net.Dial("tcp", origin.Listener.Addr().String())
		if e != nil {
			t.Error(e)
			return
		}
		socket, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			upstream.Close()
			t.Error(e)
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		go func() { defer upstream.Close(); defer socket.Close(); _, _ = io.Copy(upstream, socket) }()
		_, _ = io.Copy(socket, upstream)
		socket.Close()
		upstream.Close()
	}))
	defer proxy.Close()
	endpoint, e := url.Parse(proxy.URL)
	if e != nil {
		t.Fatal(e)
	}
	endpoint.User = url.UserPassword("owned-user", "owned-secret")
	environment := []string{"https_proxy=" + endpoint.String(), "no_proxy="}
	n := &NativeNetwork{environment: &environment}
	got, e := n.Status(context.Background(), ServerURL, time.Second)
	if e != nil || got != nil || proxyCalls.Load() != 0 || originCalls.Load() != 0 {
		t.Fatal("invalid proxy certificate sent credentials or reached origin", e, proxyCalls.Load(), originCalls.Load())
	}
	roots := x509.NewCertPool()
	roots.AddCert(proxy.Certificate())
	n.proxyRoots = roots
	got, e = n.Status(context.Background(), ServerURL, time.Second)
	if e != nil || got == nil || nativeTestText(t, got) != stubStatus || proxyCalls.Load() != 1 || originCalls.Load() != 2 {
		t.Fatal("valid HTTPS proxy/origin policy or redirect failed", e, proxyCalls.Load(), originCalls.Load())
	}
	// The origin uses its separate self-signed certificate: success proves
	// trusting the proxy did not replace the source destination policy.
}

func TestNativeHTTPSProxyHandshakeCancellation(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	entered := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		socket, e := listener.Accept()
		if e != nil {
			return
		}
		defer socket.Close()
		var first [1]byte
		if _, e = io.ReadFull(socket, first[:]); e != nil {
			return
		}
		if first[0] != 22 {
			t.Error("credentials preceded TLS handshake")
		}
		close(entered)
		_, _ = io.Copy(io.Discard, socket)
		close(closed)
	}()
	environment := []string{"https_proxy=https://owned-user:owned-secret@" + listener.Addr().String(), "no_proxy="}
	n := &NativeNetwork{environment: &environment}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := n.Status(ctx, ServerURL, time.Second); done <- e }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no owned TLS handshake")
	}
	cancel()
	select {
	case e := <-done:
		if e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy handshake survived scope cancellation")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("owned proxy socket leaked")
	}
}

func TestNativeRejectsRedirectDestinationCredentials(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://owned-user:owned-secret@movie-pilot.org/private")
		w.WriteHeader(302)
	}))
	defer server.Close()
	environment := []string{}
	n := &NativeNetwork{environment: &environment}
	_, e := n.Status(context.Background(), server.URL, time.Second)
	var boundary HostBoundaryError
	if !errorsAs(e, &boundary) {
		t.Fatal("destination credential boundary swallowed", e)
	}
}

func nativeTestText(t *testing.T, r *Response) string {
	t.Helper()
	v, e := r.sourceText()
	if e != nil {
		t.Fatal(e)
	}
	return v
}
