package iyuuautoseed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// This checks the actual native transport boundary: source URL preparation must
// happen before both owner-origin admission and the HTTP request on the wire.
func TestSourceURLPreparationActualHTTPAndOriginGrant(t *testing.T) {
	var calls atomic.Int32
	var gotURI atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotURI.Store(r.RequestURI)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("owned"))
	}))
	defer server.Close()
	unicodeOrigin := strings.Replace(server.URL, "http://", "HTTP://", 1)
	transport, e := NewNativeSiteHTTP([]SiteTransportBinding{{Ref: "owned", Fields: map[string]any{"url": unicodeOrigin + "/", "ua": "owned-UA"}, AllowedOrigins: []string{unicodeOrigin}}}, SiteTransportOptions{Guard: liveGuard})
	if e != nil {
		t.Fatal(e)
	}
	defer transport.Close()
	torrent, e := transport.TorrentHTTP("owned")
	if e != nil {
		t.Fatal(e)
	}
	res, e := torrent.Get(context.Background(), unicodeOrigin+"/媒体/./x/../下载?q=a%7e%2f 中#片段")
	if e != nil || res == nil || res.Status != 200 {
		t.Fatalf("owned URL %v/%v", res, e)
	}
	if got, want := gotURI.Load(), "/%E5%AA%92%E4%BD%93/%E4%B8%8B%E8%BD%BD?q=a~%2F%20%E4%B8%AD"; got != want {
		t.Fatalf("actual HTTP URI %q want %q", got, want)
	}

	idnaTransport, e := NewNativeSiteHTTP([]SiteTransportBinding{{Ref: "unicode", Fields: map[string]any{"url": "http://bücher.invalid/"}, AllowedOrigins: []string{"http://bücher.invalid"}}}, SiteTransportOptions{Guard: liveGuard})
	if e != nil {
		t.Fatal(e)
	}
	defer idnaTransport.Close()
	if !idnaTransport.entries["unicode"].origins["http://xn--bcher-kva.invalid"] {
		t.Fatal("source IDNA normalization did not reach origin grant")
	}
	before := calls.Load()
	if _, e = torrent.Get(context.Background(), strings.Replace(unicodeOrigin, "127.0.0.1", "evil.invalid", 1)+"/"); e == nil {
		t.Fatal("ungranted prepared origin accepted")
	}
	if calls.Load() != before {
		t.Fatal("ungranted origin dispatched")
	}
	for _, bad := range []string{"http://[invalid]/", strings.Replace(unicodeOrigin, "HTTP://", "httpx://", 1) + "/\xed\xa0\x80", "http://"} {
		res, e = torrent.Get(context.Background(), bad)
		if e != nil || res != nil {
			t.Fatalf("source prepare failure must yield no response: %q %v/%v", bad, res, e)
		}
	}
	if calls.Load() != before {
		t.Fatal("source prepare failure dispatched")
	}
	if _, e = torrent.Get(context.Background(), server.URL+"/ascii"); e != nil {
		t.Fatalf("canonical origin alias no longer granted: %v", e)
	}
}

func TestInstalledHTMLRecoveryConsumedByActualResolver(t *testing.T) {
	// libxml2 halts tree construction beyond its actual default 256-node depth.
	// The old tokenizer-only resolver incorrectly accepted the hidden deep URL.
	deep := strings.Repeat("<div>", 260) + `<a href="download.php?id=hidden">hidden</a>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(deep))
	}))
	defer server.Close()
	fields := map[string]any{"url": server.URL + "/", "ua": "owned-UA"}
	transport, e := NewNativeSiteHTTP([]SiteTransportBinding{{Ref: "owned", Fields: fields, AllowedOrigins: []string{server.URL}}}, SiteTransportOptions{Guard: liveGuard})
	if e != nil {
		t.Fatal(e)
	}
	defer transport.Close()
	got, e := (Resolver{HTTP: transport}).Resolve(context.Background(), map[string]any{"torrent_id": 7}, Site{Ref: "owned", Fields: fields}, "download.php?hash={id}")
	if e != nil || got != "" {
		t.Fatalf("actual source depth result %q/%v", got, e)
	}
}
