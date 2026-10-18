package iyuuautoseed

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"light-media/internal/networkproxy"
)

func TestSiteProxyMappingSelectedByOriginalFlag(t *testing.T) {
	var direct, proxied atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct.Add(1); io.WriteString(w, "owned direct") }))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		if r.URL.String() != origin.URL+"/download.php?id=7" || r.Header.Get("Cookie") != "owned-cookie=value" {
			t.Error("site source scope/auth lost")
		}
		io.WriteString(w, "owned proxied")
	}))
	defer proxy.Close()
	for _, flag := range []bool{false, true} {
		transport, err := NewNativeSiteHTTP([]SiteTransportBinding{{Ref: "owned", Fields: map[string]any{"url": origin.URL + "/", "proxy": flag, "ua": "Owned-UA", "cookie": "owned-cookie=value"}, AllowedOrigins: []string{origin.URL}, ProxyConfig: networkproxy.Config{HTTP: proxy.URL, HTTPS: proxy.URL}}}, SiteTransportOptions{Guard: func(context.Context) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		bound, err := transport.TorrentHTTP("owned")
		if err != nil {
			t.Fatal(err)
		}
		response, err := bound.Get(context.Background(), origin.URL+"/download.php?id=7")
		transport.Close()
		if err != nil || response == nil {
			t.Fatal("site HTTP request failed", err)
		}
		expected := "owned direct"
		if flag {
			expected = "owned proxied"
		}
		if string(response.Content) != expected {
			t.Fatal("wrong selected route")
		}
	}
	if direct.Load() != 1 || proxied.Load() != 1 {
		t.Fatal("site proxy flag ignored")
	}
}
