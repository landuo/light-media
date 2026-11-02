package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"light-media/internal/networkproxy"
)

// Exact helper extracted from the accepted private TLS owned artifact test.
// No legacy Tasks, Wiki or statistics test/production consumer is imported.
func pluginPrivateOwnedHTTP(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	peer.Start()
	t.Cleanup(peer.Close)
	request, err := http.NewRequest("GET", peer.URL+"/index", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := networkproxy.RequestsProxy(request)
	if err != nil || proxy != nil {
		t.Fatal("owned artifact route is not available in unchanged environment; no alternate route")
	}
	return peer
}
