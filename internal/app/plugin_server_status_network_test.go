package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"light-media/internal/plugins/serverstatus"
)

const pluginServerStatusNetworkFixtureBody = "Active connections: 12\nserver accepts handled requests\n1000 995 12345\nReading: 2 Writing: 3 Waiting: 7\n"

func pluginServerStatusEncodeFixture(t *testing.T, kind string, body []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	var writer io.WriteCloser
	switch kind {
	case "gzip":
		writer = gzip.NewWriter(&b)
	case "deflate":
		writer = zlib.NewWriter(&b)
	case "deflate-raw":
		writer, _ = flate.NewWriter(&b, flate.DefaultCompression)
	case "br":
		writer = brotli.NewWriter(&b)
	case "zstd":
		writer, _ = zstd.NewWriter(&b, zstd.WithEncoderConcurrency(1))
	default:
		t.Fatal("unknown synthetic codec")
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPluginServerStatusNetworkActualRequestUtilsLoopbackContract(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/server-status-http-original-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	// Keep explicit JSON field tags for the two reference names.
	var source struct {
		Cases []struct {
			Path, Mode string
			Result     struct {
				None             bool
				Status, Protocol int
				Text             string
				ContentHex       string `json:"content_hex"`
			}
			Wire []struct {
				Path    string
				Headers map[string]string
			}
		}
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	type requestRecord struct {
		Path     string
		Header   http.Header
		Protocol string
	}
	records := []requestRecord{}
	compressed := map[string][]byte{}
	for _, kind := range []string{"gzip", "deflate", "deflate-raw", "br", "zstd"} {
		compressed["/"+kind] = pluginServerStatusEncodeFixture(t, kind, []byte(pluginServerStatusNetworkFixtureBody))
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		records = append(records, requestRecord{Path: r.URL.Path, Header: r.Header.Clone(), Protocol: r.Proto})
		mu.Unlock()
		for _, name := range []string{"Authorization", "Cookie", "Accept", "Referer"} {
			if r.Header.Get(name) != "" {
				t.Errorf("invented/credential header %s", name)
			}
		}
		if r.Method != "GET" {
			t.Error("not source GET")
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/status")
			w.WriteHeader(302)
			return
		}
		if r.URL.Path == "/slow" {
			time.Sleep(180 * time.Millisecond)
		}
		body := []byte(pluginServerStatusNetworkFixtureBody)
		ctype := "text/plain"
		switch r.URL.Path {
		case "/latin1":
			body = append(body, 0xe9, 0x80)
		case "/utf8":
			body = append(body, []byte("标签")...)
			ctype = "text/plain; charset=utf-8"
		case "/invalid-utf8":
			body = append(body, 0xff, 0xff, 0xe2, 0x82)
			ctype = "text/plain; charset=utf-8"
		case "/json-default":
			body = append(body, []byte("标签")...)
			ctype = "application/json"
		case "/apparent":
			body = append(body, []byte("标签")...)
			ctype = ""
		}
		if value, ok := compressed[r.URL.Path]; ok {
			body = value
			kind := strings.TrimPrefix(r.URL.Path, "/")
			if kind == "deflate-raw" {
				kind = "deflate"
			}
			w.Header().Set("Content-Encoding", kind)
		}
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		} else {
			w.Header()["Content-Type"] = nil
		}
		count := len(body)
		if r.URL.Path == "/truncated" {
			count += 10
		}
		w.Header().Set("Content-Length", fmt.Sprint(count))
		w.Header().Set("Server", "synthetic-nginx")
		code := 200
		if r.URL.Path == "/503" {
			code = 503
		}
		w.WriteHeader(code)
		if r.URL.Path == "/trickle" {
			for offset := 0; offset < len(body); offset += 12 {
				end := offset + 12
				if end > len(body) {
					end = len(body)
				}
				time.Sleep(30 * time.Millisecond)
				if _, err := w.Write(body[offset:end]); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		} else {
			w.Write(body)
		}
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	for _, c := range source.Cases {
		if c.Path == "" || c.Mode != "" {
			continue
		}
		t.Run(c.Path, func(t *testing.T) {
			mu.Lock()
			records = nil
			mu.Unlock()
			adapter := pluginServerStatusNetwork{testOrigin: server.URL + c.Path}
			if c.Path == "/slow" {
				adapter.testTimeout = 30 * time.Millisecond
			}
			if c.Path == "/trickle" {
				adapter.testTimeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			start := time.Now()
			response, err := adapter.Status(ctx, serverstatus.ServerURL, 20*time.Second)
			if err != nil {
				t.Fatal("request fixture", err)
			}
			if c.Result.None {
				if response != nil {
					t.Fatal("RequestException was not None")
				}
				return
			}
			if response == nil || response.Status != c.Result.Status || response.Protocol != c.Result.Protocol || response.Text != c.Result.Text || hex.EncodeToString(response.Content) != c.Result.ContentHex {
				t.Fatalf("actual RequestUtils differs: response=%+v want=%+v", response, c.Result)
			}
			if c.Path == "/trickle" && time.Since(start) <= adapter.testTimeout {
				t.Fatal("fixture did not distinguish read idle from total deadline")
			}
			mu.Lock()
			got := append([]requestRecord(nil), records...)
			mu.Unlock()
			if len(got) != len(c.Wire) {
				t.Fatal("source redirects changed", len(got), len(c.Wire))
			}
			for i, record := range got {
				want := c.Wire[i]
				if record.Path != want.Path || record.Protocol != "HTTP/1.1" {
					t.Fatal("path or HTTP protocol differs", record.Path, record.Protocol)
				}
				for _, name := range []string{"User-Agent", "Accept-Encoding", "Content-Type", "Connection"} {
					if record.Header.Get(name) != want.Headers[name] {
						t.Fatal("wire header differs", name, record.Header.Get(name), want.Headers[name])
					}
				}
			}
		})
	}
}

func TestPluginServerStatusNetworkScopeTLSIndependenceAndCertificateProjection(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, pluginServerStatusNetworkFixtureBody)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	adapter := pluginServerStatusNetwork{testOrigin: server.URL + "/status"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	strict := &http.Client{Timeout: time.Second}
	for _, pass := range []int{0, 1} {
		if _, err := strict.Get(server.URL); err == nil {
			t.Fatal("ordinary client trust policy was weakened", pass)
		}
		if pass == 0 {
			if response, err := adapter.Status(ctx, serverstatus.ServerURL, 20*time.Second); err != nil || response == nil {
				t.Fatal("fixed public HTTP policy did not reproduce source", err)
			}
			if _, err := adapter.TLS(ctx, "movie-pilot.org", 443, 5*time.Second); err == nil {
				t.Fatal("independent TLS inherited HTTP policy")
			}
		}
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	adapter.testTLSRoots = roots
	cert, err := adapter.TLS(ctx, "movie-pilot.org", 443, 5*time.Second)
	if err != nil || cert.Version == "" || cert.NotAfter != server.Certificate().NotAfter.UTC().Format("Jan _2 15:04:05 2006 GMT") || !reflect.DeepEqual(cert.Issuer, pluginServerStatusName(server.Certificate().RawIssuer)) || !reflect.DeepEqual(cert.Subject, pluginServerStatusName(server.Certificate().RawSubject)) {
		t.Fatal("strict verified certificate projection failed", cert, err)
	}
	before := requests.Load()
	for _, target := range []string{server.URL + "/status", "http://unrelated.invalid/private", serverstatus.ServerURL + "?x=1"} {
		if _, err := adapter.Status(ctx, target, 20*time.Second); err == nil {
			t.Fatal("worker-selected URL accepted")
		}
	}
	if _, err := adapter.Status(ctx, serverstatus.ServerURL, time.Second); err == nil {
		t.Fatal("worker-selected timeout accepted")
	}
	if _, err := adapter.TLS(ctx, "unrelated.invalid", 443, 5*time.Second); err == nil {
		t.Fatal("worker-selected TLS host accepted")
	}
	for _, origin := range []string{"https://example.invalid/status", "https://user:pass@127.0.0.1/status", "https://127.0.0.1/status?x=1"} {
		if _, err := (pluginServerStatusNetwork{testOrigin: origin}).origin(); err == nil {
			t.Fatal("unsafe fixture origin accepted")
		}
	}
	if requests.Load() != before {
		t.Fatal("scope rejection performed network I/O")
	}
}

func TestPluginServerStatusNetworkBoundsRedirectFailureAndFreshSession(t *testing.T) {
	var outside, connections atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { outside.Add(1) }))
	defer other.Close()
	expanded := bytes.Repeat([]byte("x"), pluginServerStatusBodyLimit+1)
	bomb := pluginServerStatusEncodeFixture(t, "gzip", expanded)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		switch r.URL.Path {
		case "/outside":
			w.Header().Set("Location", other.URL)
			w.WriteHeader(302)
		case "/loop":
			w.Header().Set("Location", "/loop")
			w.WriteHeader(302)
		case "/large":
			w.Write(expanded)
		case "/bomb":
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(bomb)
		case "/broken":
			w.Header().Set("Content-Encoding", "gzip")
			w.Write([]byte("not-gzip"))
		default:
			io.WriteString(w, pluginServerStatusNetworkFixtureBody)
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	for _, path := range []string{"/outside", "/loop", "/large", "/bomb", "/broken"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		response, err := (pluginServerStatusNetwork{testOrigin: server.URL + path}).Status(ctx, serverstatus.ServerURL, 20*time.Second)
		cancel()
		if response != nil {
			t.Fatal("failed request returned truncated success", path)
		}
		if path == "/large" || path == "/bomb" {
			if !errors.Is(err, errPluginServerStatusBodyLimit) {
				t.Fatal("limit was not an explicit failure", path, err)
			}
		} else if err != nil {
			t.Fatal("ordinary RequestException leaked", path, err)
		}
	}
	if outside.Load() != 0 {
		t.Fatal("redirect left authorized origin")
	}
	before := connections.Load()
	for i := 0; i < 2; i++ {
		response, err := (pluginServerStatusNetwork{testOrigin: server.URL + "/status"}).Status(context.Background(), serverstatus.ServerURL, 20*time.Second)
		if err != nil || response == nil {
			t.Fatal(err)
		}
	}
	if connections.Load()-before != 2 {
		t.Fatal("separate samples reused a source Session connection")
	}
}

func TestPluginServerStatusNetworkHTTPSProxyCertificateRemainsStrictWithCredentials(t *testing.T) {
	var destinationRequests, connects atomic.Int32
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationRequests.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("proxy credential reached destination")
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, pluginServerStatusNetworkFixtureBody)
	}))
	origin.Config.ErrorLog = log.New(io.Discard, "", 0)
	origin.StartTLS()
	defer origin.Close()
	originURL, _ := url.Parse(origin.URL)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != originURL.Host {
			t.Error("proxy target escaped fixture")
			w.WriteHeader(403)
			return
		}
		connects.Add(1)
		if r.Header.Get("Proxy-Authorization") == "" {
			t.Error("synthetic proxy credential not bound to CONNECT")
		}
		upstream, err := net.DialTimeout("tcp", originURL.Host, time.Second)
		if err != nil {
			t.Error(err)
			w.WriteHeader(502)
			return
		}
		downstream, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		if _, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
			err = buffer.Flush()
		}
		if err != nil {
			upstream.Close()
			downstream.Close()
			return
		}
		go func() { io.Copy(upstream, downstream); upstream.Close(); downstream.Close() }()
		io.Copy(downstream, upstream)
		upstream.Close()
		downstream.Close()
	}))
	proxy.Config.ErrorLog = log.New(io.Discard, "", 0)
	proxy.StartTLS()
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("synthetic-proxy-user", "synthetic-only")
	adapter := pluginServerStatusNetwork{testOrigin: origin.URL + "/status", testProxyURL: proxyURL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if response, err := adapter.Status(ctx, serverstatus.ServerURL, 20*time.Second); err != nil || response != nil || connects.Load() != 0 {
		t.Fatal("origin TLS relaxation reached HTTPS proxy", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(proxy.Certificate())
	adapter.testProxyRoots = roots
	if response, err := adapter.Status(ctx, serverstatus.ServerURL, 20*time.Second); err != nil || response == nil || response.Status != 200 || connects.Load() != 1 || destinationRequests.Load() != 1 {
		t.Fatal("separate verified proxy and source origin policies failed", err)
	}
	if _, err := adapter.TLS(ctx, "movie-pilot.org", 443, 5*time.Second); err == nil {
		t.Fatal("independent TLS inherited proxy/origin HTTP roots or policy")
	}
}

func TestPluginServerStatusNetworkDNSAndCharacterDecodingBoundaries(t *testing.T) {
	calls := 0
	adapter := pluginServerStatusNetwork{testResolve: func(_ context.Context, host string) ([]net.IPAddr, error) {
		calls++
		if host != "movie-pilot.org" {
			t.Error("unexpected DNS target")
		}
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("2001:db8::1")}, {IP: net.ParseIP("192.0.2.1")}}, nil
	}}
	ips, err := adapter.DNS(context.Background(), "movie-pilot.org", 443)
	if err != nil || !reflect.DeepEqual(ips, []string{"192.0.2.1", "2001:db8::1", "192.0.2.1"}) {
		t.Fatal("OS resolver order lost before stable Machine dedup", ips, err)
	}
	if _, err := adapter.DNS(context.Background(), "unrelated.invalid", 443); err == nil {
		t.Fatal("arbitrary DNS query accepted")
	}
	if _, err := adapter.DNS(context.Background(), "movie-pilot.org", 80); err == nil || calls != 1 {
		t.Fatal("arbitrary DNS port accepted")
	}
	for _, c := range []struct {
		body        []byte
		label, want string
	}{
		{[]byte{0xff, 0xff, 0xe2, 0x82}, "utf-8", "���"},
		{[]byte{0xe2, 0x82, 'x', 0xed, 0xa0, 0x80}, "utf-8", "�x���"},
		{[]byte{0x80, 0xe9}, "ISO-8859-1", "\u0080é"},
		{[]byte{0x80, 0xe9}, "windows-1252", "€é"},
		{[]byte{0x80, 0xff}, "ascii", "��"},
		{[]byte{0xff, 0xfe, 0x2d, 0x4e}, "utf-16", "中"},
		{[]byte{0xef, 0xbb, 0xbf, 'a'}, "utf-8-sig", "a"},
		{[]byte{0xff, 'a'}, "invalid-codec", "�a"},
	} {
		if got := pluginServerStatusDecode(c.body, c.label); got != c.want {
			t.Fatal("Python decode boundary differs", c.label, got, c.want)
		}
	}
	if _, err := pluginServerStatusText([]byte{0xe9}, ""); !errors.Is(err, errPluginServerStatusEncoding) {
		t.Fatal("ambiguous absent-header charset was silently guessed")
	}
}
