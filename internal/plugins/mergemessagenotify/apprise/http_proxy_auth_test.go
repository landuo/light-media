package apprise

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"light-media/internal/networkproxy"
)

func TestHTTPProxyAuthEncodingPreservedWithoutURL(t *testing.T) {
	var calls atomic.Int32
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("unexpected owned dial")
	}
	port := HTTPTransport{Base: base, Environment: func() []string { return []string{"http_proxy=http://synthetic:%E4%B8%AD@owned.invalid:8123"} }}
	_, err := port.Send(context.Background(), Request{Method: "GET", URL: "http://owned.invalid/synthetic", Timeout: [2]float64{1, 1}})
	if !errors.Is(err, networkproxy.ErrRequestsProxyAuthEncoding) || err != networkproxy.ErrRequestsProxyAuthEncoding {
		t.Fatalf("source encoding error was hidden or URL wrapped: %T", err)
	}
	if calls.Load() != 0 {
		t.Fatal("encoding failure sent traffic")
	}
}
