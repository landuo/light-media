package apprise

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/andybalholm/brotli"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalHTTPWire(t *testing.T) {
	f, e := os.Open("testdata/http-wire.jsonl.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	scan := bufio.NewScanner(z)
	n := 0
	for scan.Scan() {
		n++
		var c struct {
			Path, Query   string
			Added, Result bool
			Calls         []map[string]any
		}
		if e := json.Unmarshal(scan.Bytes(), &c); e != nil {
			t.Fatal(e)
		}
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			calls := []map[string]any{}
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
					return
				}
				headers := map[string]string{}
				for k, v := range r.Header {
					if !strings.EqualFold(k, "Content-Length") {
						headers[strings.ToLower(k)] = base64.StdEncoding.EncodeToString([]byte(strings.Join(v, ", ")))
					}
				}
				mu.Lock()
				calls = append(calls, map[string]any{"method": r.Method, "path": r.RequestURI, "headers": headers, "body": string(data)})
				mu.Unlock()
				if strings.HasPrefix(r.URL.Path, "/redirect/") {
					status, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/redirect/"))
					w.Header().Set("Location", "/final")
					w.Header().Set("Set-Cookie", "owned=yes; Path=/")
					w.WriteHeader(status)
				} else {
					w.WriteHeader(204)
				}
			}))
			defer server.Close()
			port := &HTTPTransport{Environment: func() []string { return nil }}
			client := New(port.Send)
			added, e := client.Add(strings.Replace(server.URL, "http:", "json:", 1) + c.Path + "?" + c.Query)
			if e != nil || added != c.Added {
				t.Fatal(added, e, c.Added)
			}
			result, e := client.Notify(context.Background(), "标题", "正文", nil)
			if e != nil || result != c.Result {
				t.Fatal(result, e, c.Result)
			}
			mu.Lock()
			defer mu.Unlock()
			raw, _ := json.Marshal(calls)
			var normalized []map[string]any
			json.Unmarshal(raw, &normalized)
			if !reflect.DeepEqual(normalized, c.Calls) {
				t.Fatalf("%s %s got=%s expected=%v", c.Path, c.Query, raw, c.Calls)
			}
		})
	}
	if scan.Err() != nil {
		t.Fatal(scan.Err())
	}
	if n != 50 {
		t.Fatal(n)
	}
}

func TestHTTPActualTLSVerificationAndCACredentials(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	send := func(port *HTTPTransport, verify bool) bool {
		t.Helper()
		client := New(port.Send)
		suffix := ""
		if !verify {
			suffix = "?verify=no"
		}
		ok, e := client.Add(strings.Replace(server.URL, "https:", "jsons:", 1) + suffix)
		if !ok || e != nil {
			t.Fatal(ok, e)
		}
		result, e := client.Notify(context.Background(), "title", "body", nil)
		if e != nil {
			t.Fatal(e)
		}
		return result == true
	}
	port := &HTTPTransport{Environment: func() []string { return nil }}
	if send(port, true) {
		t.Fatal("untrusted self-signed accepted")
	}
	if calls.Load() != 0 {
		t.Fatal(calls.Load())
	}
	if !send(port, false) {
		t.Fatal("explicit source verify=no failed")
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if !send(&HTTPTransport{Base: base, Environment: func() []string { return nil }}, true) {
		t.Fatal("owned root rejected")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certifiCA) {
		t.Fatal("certifi source roots unavailable")
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}

func TestHTTPActualCancellationAndRedirectAuthorization(t *testing.T) {
	entered := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(closed) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	port := &HTTPTransport{Environment: func() []string { return nil }}
	go func() {
		_, e := port.Send(ctx, Request{Method: "GET", URL: server.URL, Verify: true, Timeout: [2]float64{4, 4}})
		done <- e
	}()
	<-entered
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request not canceled")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("owned socket not closed")
	}
	denied := errors.New("owned_destination_denied")
	var requests int
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Location", "http://unowned.invalid/secret")
		w.WriteHeader(302)
	}))
	defer redirect.Close()
	port.Authorize = func(ctx context.Context, u *url.URL) error {
		if u.Host == strings.TrimPrefix(redirect.URL, "http://") {
			return nil
		}
		return denied
	}
	_, e := port.Send(context.Background(), Request{Method: "GET", URL: redirect.URL, Verify: true, Timeout: [2]float64{4, 4}})
	if !errors.Is(e, denied) || requests != 1 {
		t.Fatal(e, requests)
	}
}

func TestHTTPActualReadTimeoutIsInactivity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "6")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for i := 0; i < 6; i++ {
			time.Sleep(30 * time.Millisecond)
			if _, e := w.Write([]byte("x")); e != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()
	port := &HTTPTransport{Environment: func() []string { return nil }}
	response, e := port.Send(context.Background(), Request{Method: "GET", URL: server.URL, Verify: true, Timeout: [2]float64{1, .1}})
	if e != nil || string(response.Content) != "xxxxxx" {
		t.Fatal(response.Status, e, string(response.Content))
	}
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer blocked.Close()
	_, e = port.Send(context.Background(), Request{Method: "GET", URL: blocked.URL, Verify: true, Timeout: [2]float64{1, .05}})
	if e == nil {
		t.Fatal("silent body did not time out")
	}
}

func TestHTTPActualCompression(t *testing.T) {
	payload := []byte("synthetic attachment\x00\xff")
	for _, kind := range []string{"gzip", "deflate", "raw-deflate", "br", "gzip, br", "unrecognized"} {
		t.Run(kind, func(t *testing.T) {
			var encoded bytes.Buffer
			switch kind {
			case "gzip", "gzip, br":
				writer := gzip.NewWriter(&encoded)
				writer.Write(payload)
				writer.Close()
			case "deflate":
				writer := zlib.NewWriter(&encoded)
				writer.Write(payload)
				writer.Close()
			case "raw-deflate":
				writer, _ := flate.NewWriter(&encoded, flate.DefaultCompression)
				writer.Write(payload)
				writer.Close()
			case "br":
				writer := brotli.NewWriter(&encoded)
				writer.Write(payload)
				writer.Close()
			default:
				encoded.Write(payload)
			}
			if kind == "gzip, br" {
				raw := append([]byte(nil), encoded.Bytes()...)
				encoded.Reset()
				writer := brotli.NewWriter(&encoded)
				writer.Write(raw)
				writer.Close()
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				encoding := kind
				if kind == "raw-deflate" {
					encoding = "deflate"
				}
				w.Header().Set("Content-Encoding", encoding)
				w.Write(encoded.Bytes())
			}))
			defer server.Close()
			port := &HTTPTransport{Environment: func() []string { return nil }}
			reply, e := port.Send(context.Background(), Request{Method: "GET", URL: server.URL, Verify: true, Timeout: [2]float64{1, 1}})
			if e != nil || !bytes.Equal(reply.Content, payload) {
				t.Fatal(e, reply.Content)
			}
		})
	}
}

func TestNativeClientActualIndependentServicesAndAllChunks(t *testing.T) {
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	var active, maximum, total atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if old >= current || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		total.Add(1)
		entered <- struct{}{}
		<-release
		if r.URL.Path == "/failed" {
			w.WriteHeader(400)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	port := &HTTPTransport{Environment: func() []string { return nil }}
	client := New(port.Send)
	for _, path := range []string{"/first", "/failed", "/third"} {
		ok, e := client.Add(strings.Replace(server.URL, "http:", "json:", 1) + path + "?overflow=split")
		if e != nil || !ok {
			t.Fatal(ok, e)
		}
	}
	done := make(chan any, 1)
	go func() {
		v, e := client.Notify(context.Background(), "title", strings.Repeat("x", 32769), nil)
		if e != nil {
			done <- e
		} else {
			done <- v
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first service did not start")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("services serialized")
	}
	close(release)
	select {
	case v := <-done:
		if v != false {
			t.Fatal(v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native notify did not finish")
	}
	if total.Load() != 6 || maximum.Load() < 2 {
		t.Fatal(total.Load(), maximum.Load())
	}
}
