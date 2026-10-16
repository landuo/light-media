package autosignin

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func compressedFixture(t *testing.T, data []byte, codec string) []byte {
	t.Helper()
	var b strings.Builder
	var w io.WriteCloser
	var err error
	switch codec {
	case "gzip":
		w = gzip.NewWriter(&b)
	case "deflate":
		w = zlib.NewWriter(&b)
	case "deflate-raw":
		w, err = flate.NewWriter(&b, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriter(&b)
	case "zstd":
		w, err = zstd.NewWriter(&b)
	default:
		return data
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(b.String())
}
func TestActualRequestUtilsLoopbackNetworkReference(t *testing.T) {
	raw, err := readAutoSignInFixture("installed-http-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Cases []struct {
			Name, Method, Path string
			Cookie             any
			HeaderMode         HeaderMode `json:"header_mode"`
			ResponseHex        string     `json:"response_hex"`
			UA                 any
			Wire               []Object
			Expected           Object
		}
	}
	if err = Decode(raw, &ref); err != nil {
		t.Fatal(err)
	}
	if len(ref.Cases) != 26 {
		t.Fatal("reference count changed")
	}
	for _, c := range ref.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var mu sync.Mutex
			wire := []Object{}
			var origin string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				headers := Object{}
				for name, vs := range r.Header {
					value := strings.Join(vs, ", ")
					if strings.EqualFold(name, "referer") {
						value = strings.ReplaceAll(value, origin, "<origin>")
					}
					headers[strings.ToLower(name)] = value
				}
				mu.Lock()
				wire = append(wire, Object{"method": r.Method, "path": r.URL.RequestURI(), "headers": headers, "body_hex": hex.EncodeToString(body)})
				mu.Unlock()
				if strings.HasPrefix(r.URL.Path, "/redirect/") {
					w.Header().Set("Location", "/attendance.php")
					w.Header().Set("Set-Cookie", "newcookie=fresh; Path=/")
					w.WriteHeader(302)
					return
				}
				payload := []byte(`<a href="logout.php">退出登录</a><div>签到成功</div>`)
				if c.Expected != nil {
					payload, err = hex.DecodeString(textValue(c.Expected["raw_hex"]))
					if err != nil {
						t.Error(err)
					}
				}
				ct := "text/html; charset=UTF-8"
				codec := ""
				switch {
				case strings.HasPrefix(c.Path, "/latin1/"):
					ct = "text/html"
				case strings.HasPrefix(c.Path, "/gbk/"):
					ct = "text/html; charset=GBK"
				case strings.HasPrefix(c.Path, "/ascii/"):
					ct = "text/html; charset=ASCII"
				case strings.HasPrefix(c.Path, "/no-header/"):
					ct = ""
				}
				for _, name := range []string{"gzip", "deflate", "deflate-raw", "br", "zstd"} {
					if strings.HasPrefix(c.Path, "/"+name+"/") {
						codec = name
					}
				}
				if strings.HasPrefix(c.Path, "/bad-gzip/") {
					codec = "gzip"
				}
				encoded := compressedFixture(t, payload, codec)
				if c.ResponseHex != "" {
					encoded, _ = hex.DecodeString(c.ResponseHex)
				}
				if strings.HasPrefix(c.Path, "/bad-gzip/") {
					encoded = []byte("not-gzip")
				}
				if codec != "" {
					header := codec
					if header == "deflate-raw" {
						header = "deflate"
					}
					w.Header().Set("Content-Encoding", header)
				}
				if ct != "" {
					w.Header().Set("Content-Type", ct)
				} else {
					w.Header()["Content-Type"] = nil
				}
				status := 200
				if strings.HasPrefix(c.Path, "/503/") {
					status = 503
				}
				if strings.HasPrefix(c.Path, "/truncated/") {
					w.Header().Set("Content-Length", "9999")
				}
				w.WriteHeader(status)
				w.Write(encoded)
			}))
			defer server.Close()
			origin = server.URL
			binding, err := BindSiteHTTP(SiteHTTPBinding{URL: origin + "/library/", Cookie: textValue(c.Cookie), UserAgent: c.UA})
			if err != nil {
				t.Fatal(err)
			}
			req := PageRequest{Method: c.Method, URL: origin + c.Path, Timeout: 2, HeadersMode: c.HeaderMode}
			if c.HeaderMode == HeaderBaseCookie {
				if cookie, ok := c.Cookie.(string); ok {
					req.RawCookieHeader = &cookie
				}
			}
			if req.Method == "POST" {
				req.Form = map[string]string{}
				req.Referer = req.URL
			}
			res, err := binding.Do(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if c.Expected == nil {
				if res != nil {
					t.Fatal("source network failure must not become success")
				}
			} else {
				if res == nil {
					t.Fatal("expected response")
				}
				expectedStatus, _ := sourceInteger(c.Expected["status"])
				if res.Status != int(expectedStatus.Int64()) || res.Text != c.Expected["text"] {
					t.Errorf("response got %#v want %#v", res, c.Expected)
				}
			}
			mu.Lock()
			got := canonical(wire)
			mu.Unlock()
			if !reflect.DeepEqual(got, canonical(c.Wire)) {
				t.Errorf("wire got %#v\nwant %#v", got, canonical(c.Wire))
			}
		})
	}
}
func TestBoundSiteTransportNoCrossOriginAndStrictTLS(t *testing.T) {
	var protectedCalls, foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedCalls.Add(1)
		http.Redirect(w, r, foreign.URL+"/never", 302)
	}))
	defer origin.Close()
	b, err := BindSiteHTTP(SiteHTTPBinding{URL: origin.URL, Cookie: "sid=synthetic-only", UserAgent: "synthetic-UA"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: foreign.URL}); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatal("arbitrary target accepted", err)
	}
	if _, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: origin.URL}); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatal("cross origin redirect accepted", err)
	}
	if foreignCalls.Load() != 0 || protectedCalls.Load() != 1 {
		t.Fatal("credential-bearing request escaped binding")
	}
	tlsServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedCalls.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		io.WriteString(w, "ok")
	}))
	tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsServer.StartTLS()
	defer tlsServer.Close()
	b, err = BindSiteHTTP(SiteHTTPBinding{URL: tlsServer.URL, Cookie: "sid=synthetic-only"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Do(context.Background(), PageRequest{Method: "GET", URL: tlsServer.URL, Timeout: 2})
	if err != nil || res != nil || protectedCalls.Load() != 1 {
		t.Fatalf("untrusted certificate was accepted res=%v err=%v calls=%d", res, err, protectedCalls.Load())
	}
	roots := x509.NewCertPool()
	roots.AddCert(tlsServer.Certificate())
	b.testTLS = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	res, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: tlsServer.URL, Timeout: 2})
	if err != nil || res == nil || res.Text != "ok" {
		t.Fatal("owned trust fixture failed", res, err)
	}
	proxyURL, _ := url.Parse(tlsServer.URL)
	proxyURL.User = url.UserPassword("synthetic-proxy-user", "synthetic-proxy-password")
	b, _ = BindSiteHTTP(SiteHTTPBinding{URL: origin.URL, Cookie: "sid=synthetic-only", ProxyURL: proxyURL})
	before := protectedCalls.Load()
	res, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: origin.URL, Timeout: 2})
	if err != nil || res != nil || protectedCalls.Load() != before {
		t.Fatal("HTTPS proxy verification was weakened")
	}
}
func TestBoundSiteTransportIdleCancellationAndBounds(t *testing.T) {
	inflatedBody := compressedFixture(t, []byte(strings.Repeat("x", (1<<20)+1)), "gzip")
	var started = make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		switch r.URL.Path {
		case "/trickle":
			for i := 0; i < 5; i++ {
				time.Sleep(20 * time.Millisecond)
				io.WriteString(w, "a")
				w.(http.Flusher).Flush()
			}
		case "/slow":
			time.Sleep(150 * time.Millisecond)
			io.WriteString(w, "late")
		case "/cancel":
			close(started)
			<-r.Context().Done()
		case "/large":
			io.WriteString(w, strings.Repeat("x", (1<<20)+1))
		case "/inflated":
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(inflatedBody)
		}
	}))
	defer server.Close()
	b, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL})
	b.testIdle = 70 * time.Millisecond
	begin := time.Now()
	res, err := b.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL + "/trickle"})
	if err != nil || res == nil || res.Text != "aaaaa" || time.Since(begin) <= b.testIdle {
		t.Fatal("idle timeout became whole request deadline", res, err)
	}
	res, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL + "/slow"})
	if err != nil || res != nil {
		t.Fatal("source network timeout not None", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := b.Do(ctx, PageRequest{Method: "GET", URL: server.URL + "/cancel"}); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not release request")
	}
	b.testIdle = time.Second // only limits are under test in this phase, not slow fixture compression
	for _, path := range []string{"/large", "/inflated"} {
		if res, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL + path}); !errors.Is(err, ErrSiteResponseLimit) || res != nil {
			t.Fatal("bound became truncated success", path, res, err)
		}
	}
}

func TestBoundSiteTransportSlowHeadersAreIdleNotTotal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for _, part := range []string{"HTTP/1.1 200 OK\r\n", "Content-Type: text/plain; charset=UTF-8\r\n", "Content-Length: 2\r\n", "Connection: close\r\n", "\r\nok"} {
			if _, err = buffer.WriteString(part); err != nil {
				return
			}
			if err = buffer.Flush(); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()
	binding, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL})
	binding.testIdle = 60 * time.Millisecond
	start := time.Now()
	res, err := binding.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL})
	if err != nil || res == nil || res.Text != "ok" || time.Since(start) <= binding.testIdle {
		t.Fatal("header deadline became total timeout", res, err, time.Since(start))
	}
}
