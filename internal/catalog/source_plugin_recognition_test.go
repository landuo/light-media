package catalog

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"light-media/internal/networkproxy"
)

func TestSourceRecognitionActualTLSDisconnectThroughConfiguredTransportRetriesFour(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer peer.Close()
	base := peer.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = base.TLSClientConfig.Clone()
	base.TLSClientConfig.ServerName = "example.com"
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != apiHost+":443" {
			t.Error("recognition left fixed origin", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, peer.Listener.Addr().String())
	}
	route, err := (networkproxy.Config{}).Transport(base)
	if err != nil {
		t.Fatal(err)
	}
	client := NewWithTransport(Credentials{APIKey: "owned-test-key"}, route)
	defer client.CloseIdleConnections()
	_, err = client.SourceRecognitionSearchResponse(context.Background(), "multi", "Owned Network", "", "zh")
	var provider *Error
	if !errors.As(err, &provider) || provider.Code != "unavailable" || calls.Load() != 4 {
		t.Fatal("fixed RequestUtils stale-session retry + TMDb owned-session reset", calls.Load(), err)
	}
}
