package subscriptionstats

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestSourceProxyAuthUnicodeDoesNotBecomeFalse(t *testing.T) {
	for _, op := range []string{"done", "add"} {
		t.Run(op, func(t *testing.T) {
			var dials atomic.Int32
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.DialContext = func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("unexpected owned dial")
			}
			setting := ownedSettings("http://owned.invalid")
			setting.Proxies = map[string]string{"http": "http://%E4%B8%AD:pass@owned.invalid:8123", "https": "http://%E4%B8%AD:pass@owned.invalid:8123"}
			client := Client{Base: base, Guard: ownedGuard}
			result, err := client.send(context.Background(), setting, []byte("{}"), op)
			if err != nil || result.SourceReturned || result.SourceValue || result.SourceError != "UnicodeEncodeError" || result.Outcome != "not_requested" || result.HTTPStatus != 0 {
				t.Fatal(result, err)
			}
			if dials.Load() != 0 {
				t.Fatal("source encoding error sent traffic")
			}
			setting.Share = boolPtr(false)
			result, err = client.send(context.Background(), setting, nil, op)
			if err != nil || !result.SourceReturned || result.Outcome != "disabled" || result.SourceError != "" || dials.Load() != 0 {
				t.Fatal(result, err)
			}
		})
	}
}
