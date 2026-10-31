package autosignin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func ownedMaterialString(s string) plugins.OCRValue {
	return plugins.OCRValue{Kind: "string", Points: []rune(s)}
}
func ownedMaterialObject(entries ...plugins.OCREntry) plugins.OCRValue {
	return plugins.OCRValue{Kind: "object", Entries: entries}
}
func ownedMaterialEntry(key string, value plugins.OCRValue) plugins.OCREntry {
	return plugins.OCREntry{Key: []rune(key), Value: value}
}
func bindOwnedMaterial(t *testing.T, address, cookie string, guard func(context.Context) error) *BoundSiteHTTP {
	t.Helper()
	b, err := BindSiteHTTP(SiteHTTPBinding{URL: address, Cookie: cookie, UserAgent: "Owned-UA", RequestsProxy: &RequestsProxyPolicy{Environment: os.Environ()}, BeforeRequest: guard})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOrdinaryMaterialQueryHeadersAndResponse(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/empty-ua" {
			if values, present := r.Header["User-Agent"]; !present || len(values) != 1 || values[0] != "" {
				t.Errorf("explicit empty User-Agent field lost: %#v", r.Header)
			}
			_, _ = w.Write([]byte("empty-ua"))
			return
		}
		if r.URL.Path == "/form" {
			if r.URL.RawQuery != "q=query" || string(body) != "z=last&a=first" || r.Method != "POST" {
				t.Errorf("form/query separation: %s %s %q", r.Method, r.URL.RawQuery, body)
			}
			_, _ = w.Write([]byte("form"))
			return
		}
		if r.URL.RawQuery != "existing=first&z=last&repeat=one&repeat=%E9%9B%AA+two&flag=True" || len(body) != 0 {
			t.Errorf("ordered query/body: %q %q", r.URL.RawQuery, body)
		}
		if r.Header.Get("Cookie") != "owned=one" || r.Header.Get("User-Agent") != "python-urllib3/2.7.0" || r.Header.Get("X-Owned-Ticket") != string([]byte{'c', 'a', 'f', 0xe9}) {
			t.Error("source prepared headers differ")
		}
		if _, present := r.Header["Accept"]; present {
			t.Error("explicit null Accept was not removed")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Location", "magnet:?xt=urn:btih:OWNED")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("owned"))
	}))
	defer server.Close()
	b := bindOwnedMaterial(t, server.URL, "owned=one", nil)
	params := ownedMaterialObject(ownedMaterialEntry("z", ownedMaterialString("last")), ownedMaterialEntry("repeat", plugins.OCRValue{Kind: "list", Items: []plugins.OCRValue{ownedMaterialString("one"), {Kind: "null"}, ownedMaterialString("雪 two")}}), ownedMaterialEntry("flag", plugins.OCRValue{Kind: "bool", Bool: true}))
	headers := ownedMaterialObject(ownedMaterialEntry("X-Owned-Ticket", ownedMaterialString("café")), ownedMaterialEntry("Accept", plugins.OCRValue{Kind: "null"}), ownedMaterialEntry("User-Agent", plugins.OCRValue{Kind: "null"}))
	for _, method := range []string{"GET", "POST"} {
		response, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: method, URL: server.URL + "/wire?existing=first", Params: params, Headers: headers})
		if err != nil || response == nil || response.Status != http.StatusFound || string(response.Content) != "owned" || response.Headers["Location"] != "magnet:?xt=urn:btih:OWNED" || response.ContentType != "application/octet-stream" {
			t.Fatalf("%s raw response: %+v %v", method, response, err)
		}
	}
	response, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "POST", URL: server.URL + "/form", Params: ownedMaterialObject(ownedMaterialEntry("q", ownedMaterialString("query"))), Form: map[string]string{"a": "first", "z": "last"}, FormOrder: []string{"z", "a"}})
	if err != nil || response == nil || response.Text != "form" {
		t.Fatalf("helper form: %+v %v", response, err)
	}
	for _, value := range []plugins.OCRValue{ownedMaterialString(" leading"), {Kind: "int", Number: "1"}} {
		response, err = b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL, Headers: ownedMaterialObject(ownedMaterialEntry("X-Owned", value))})
		if response != nil || err != nil {
			t.Fatalf("caught InvalidHeader must return None: %+v %v", response, err)
		}
	}
	_, err = b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL, Headers: ownedMaterialObject(ownedMaterialEntry("X-Owned", ownedMaterialString("雪")))})
	var source *OrdinaryMaterialSourceError
	if !errors.As(err, &source) || source.Class != "UnicodeEncodeError" {
		t.Fatalf("uncaught Latin-1 preparation error: %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("invalid headers reached network: %d", hits.Load())
	}
	emptyAgent := bindOwnedMaterial(t, server.URL, "", nil)
	emptyAgent.binding.UserAgent = ""
	for _, headers := range []plugins.OCRValue{{Kind: "null"}, ownedMaterialObject(ownedMaterialEntry("User-Agent", ownedMaterialString("")))} {
		response, err := emptyAgent.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL + "/empty-ua", Headers: headers})
		if err != nil || response == nil || response.Text != "empty-ua" {
			t.Fatalf("empty User-Agent: %+v %v", response, err)
		}
	}
}

func TestOrdinaryMaterialSourceTLSAndProxyIsolation(t *testing.T) {
	var originHits, directHits, proxyHits, connects atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { originHits.Add(1); _, _ = w.Write([]byte("origin")) }))
	defer origin.Close()
	b := bindOwnedMaterial(t, origin.URL, "", nil)
	for i := 0; i < 2; i++ {
		if response, _ := b.Do(context.Background(), PageRequest{Method: "GET", URL: origin.URL, Binary: true}); response != nil {
			t.Fatal("ordinary Do TLS weakened")
		}
		if i == 0 {
			response, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: origin.URL})
			if err != nil || response == nil || string(response.Content) != "origin" {
				t.Fatalf("private origin TLS: %+v %v", response, err)
			}
		}
	}
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		_, _ = w.Write([]byte("unexpected direct"))
	}))
	defer direct.Close()
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "CONNECT" {
			connects.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		proxyHits.Add(1)
		if !r.URL.IsAbs() || r.URL.String() != direct.URL+"/fixed" {
			t.Errorf("not exact owned absolute-form request: %s", r.URL)
		}
		_, _ = w.Write([]byte("fixed proxy response")) // No CONNECT, forwarding or upstream request.
	}))
	defer proxy.Close()
	proxied, err := BindSiteHTTP(SiteHTTPBinding{URL: direct.URL, RequestsProxy: &RequestsProxyPolicy{Environment: os.Environ(), Explicit: map[string]string{"http": proxy.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	if response, _ := proxied.Do(context.Background(), PageRequest{Method: "GET", URL: direct.URL + "/fixed", Binary: true}); response != nil {
		t.Fatal("ordinary HTTPS proxy TLS weakened")
	}
	response, err := proxied.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: direct.URL + "/fixed"})
	if err != nil || response == nil || string(response.Content) != "fixed proxy response" {
		t.Fatalf("private proxy TLS: %+v %v", response, err)
	}
	if originHits.Load() != 1 || proxyHits.Load() != 1 || directHits.Load() != 0 || connects.Load() != 0 {
		t.Fatalf("route isolation: origin=%d proxy=%d direct=%d connect=%d", originHits.Load(), proxyHits.Load(), directHits.Load(), connects.Load())
	}
}

func TestOrdinaryMaterialRedirectCredentialsAndMethods(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		for _, key := range []string{"Cookie", "Authorization", "Proxy-Authorization", "X-Owned-Token", "Referer"} {
			if r.Header.Get(key) != "" {
				t.Errorf("cross-origin %s leaked", key)
			}
		}
		_, _ = w.Write([]byte("target"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			if r.Header.Get("Cookie") != "owned=secret" || r.Header.Get("Authorization") != "owned-auth" {
				t.Error("initial owned credentials missing")
			}
			http.SetCookie(w, &http.Cookie{Name: "ownedJar", Value: "same-origin", Path: "/"})
			http.Redirect(w, r, target.URL, http.StatusFound)
		case "/post":
			http.Redirect(w, r, "/get", http.StatusFound)
		case "/get":
			body, _ := io.ReadAll(r.Body)
			if r.Method != "GET" || len(body) != 0 || r.Header.Get("Content-Type") != "" {
				t.Error("302 POST did not rebuild as bodyless GET")
			}
			_, _ = w.Write([]byte("post-get"))
		case "/307":
			http.Redirect(w, r, "/preserved", http.StatusTemporaryRedirect)
		case "/preserved":
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded; charset=UTF-8" {
				t.Error("307 GET content header was dropped")
			}
			_, _ = w.Write([]byte("preserved"))
		}
	}))
	defer source.Close()
	b := bindOwnedMaterial(t, source.URL, "owned=secret", nil)
	headers := ownedMaterialObject(ownedMaterialEntry("Authorization", ownedMaterialString("owned-auth")), ownedMaterialEntry("Proxy-Authorization", ownedMaterialString("owned-proxy-auth")), ownedMaterialEntry("X-Owned-Token", ownedMaterialString("owned-token")), ownedMaterialEntry("Referer", ownedMaterialString(source.URL+"/owned")))
	response, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: source.URL + "/cross", Headers: headers})
	if err != nil || response == nil || response.Status != 302 || targetHits.Load() != 0 {
		t.Fatalf("helper GET unexpectedly followed: %+v %v", response, err)
	}
	response, err = b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: source.URL + "/cross", Headers: headers, AllowRedirects: true})
	if err != nil || response == nil || response.Text != "target" || targetHits.Load() != 1 {
		t.Fatalf("descriptor follow: %+v %v", response, err)
	}
	for _, input := range []OrdinaryMaterialRequest{{Method: "POST", URL: source.URL + "/post", Form: map[string]string{"owned": "form"}, AllowRedirects: true}, {Method: "GET", URL: source.URL + "/307", AllowRedirects: true}} {
		response, err := b.DoOrdinaryMaterial(context.Background(), input)
		if err != nil || response == nil || response.Status != 200 {
			t.Fatalf("same-origin method handling: %+v %v", response, err)
		}
	}
}

func TestOrdinaryMaterialBodyLimitsAndBinary(t *testing.T) {
	body := bytes.Repeat([]byte("A"), OrdinaryMaterialMaxBytes+1)
	gzipTooLarge := compressedFixture(t, body, "gzip")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exact":
			_, _ = w.Write(body[:OrdinaryMaterialMaxBytes])
		case "/oversize":
			_, _ = w.Write(body)
		case "/gzip-oversize":
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(gzipTooLarge)
		case "/native":
			_, _ = w.Write(body[:2<<20])
		case "/binary":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write([]byte{0x80, 0x81})
		case "/broken-gzip":
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write([]byte("not gzip"))
		}
	}))
	defer server.Close()
	b := bindOwnedMaterial(t, server.URL, "", nil)
	response, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL + "/exact"})
	if err != nil || response == nil || !bytes.Equal(response.Content, body[:OrdinaryMaterialMaxBytes]) {
		t.Fatalf("exact material limit: %v", err)
	}
	for _, path := range []string{"/oversize", "/gzip-oversize"} {
		if _, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL + path}); !errors.Is(err, ErrSiteResponseLimit) {
			t.Fatalf("%s limit: %v", path, err)
		}
	}
	if _, err := b.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL + "/native", Binary: true}); !errors.Is(err, ErrSiteResponseLimit) {
		t.Fatalf("native 1MiB limit changed: %v", err)
	}
	response, err = b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL + "/binary"})
	if err != nil || response == nil || !bytes.Equal(response.Content, []byte{0x80, 0x81}) || !errors.Is(response.TextError, ErrSiteEncodingNotReady) {
		t.Fatalf("binary bytes lost to text decoder: %+v %v", response, err)
	}
	response, err = b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL + "/broken-gzip"})
	if response != nil || err != nil {
		t.Fatalf("source caught decoder error: %+v %v", response, err)
	}
}

func TestOrdinaryMaterialGuardCancellationAndClose(t *testing.T) {
	var hits, guards atomic.Int32
	var revoked atomic.Bool
	started := make(chan struct{}, 1)
	closed := make(chan string, 8)
	idlePeer := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/revoke":
			revoked.Store(true)
			http.Redirect(w, r, "/never", http.StatusFound)
		case "/slow":
			started <- struct{}{}
			<-r.Context().Done()
		default:
			if r.URL.Path == "/idle" {
				idlePeer <- r.RemoteAddr
			}
			_, _ = w.Write([]byte("owned"))
		}
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- conn.RemoteAddr().String():
			default:
			}
		}
	}
	server.Start()
	defer server.Close()
	denied := errors.New("owned current guard denied")
	b := bindOwnedMaterial(t, server.URL, "", func(ctx context.Context) error {
		guards.Add(1)
		if revoked.Load() {
			return denied
		}
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.DoOrdinaryMaterial(ctx, OrdinaryMaterialRequest{Method: "GET", URL: server.URL}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if hits.Load() != 0 || guards.Load() != 0 {
		t.Fatal("pre-cancel performed request")
	}
	if _, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL + "/revoke", AllowRedirects: true}); !errors.Is(err, denied) {
		t.Fatalf("redirect current guard: %v", err)
	}
	if hits.Load() != 1 || guards.Load() != 2 {
		t.Fatalf("guard/hop counts %d/%d", guards.Load(), hits.Load())
	}
	revoked.Store(false)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := b.DoOrdinaryMaterial(ctx, OrdinaryMaterialRequest{Method: "GET", URL: server.URL + "/slow"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("owned request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned cancel did not exit")
	}
	for len(closed) != 0 {
		<-closed
	}
	response, err := b.DoOrdinaryMaterial(context.Background(), OrdinaryMaterialRequest{Method: "GET", URL: server.URL + "/idle"})
	if err != nil || response == nil || !strings.EqualFold(response.Text, "owned") {
		t.Fatalf("idle result: %+v %v", response, err)
	}
	wantPeer := <-idlePeer
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case peer := <-closed:
			if peer == wantPeer {
				return
			}
		case <-deadline.C:
			t.Fatal("exact private request retained idle connection after return")
		}
	}
}
