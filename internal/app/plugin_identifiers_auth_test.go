package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRemoteIdentifiersInstalledURLAuthAndSourceErrors(t *testing.T) {
	raw, err := os.ReadFile("testdata/remote-identifiers-url-auth-requests.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Userinfo, Authorization string
			SourceError             string `json:"source_error"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 8 {
		t.Fatal("installed auth fixture invalid")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Userinfo, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != tc.Authorization {
					t.Error("installed source auth differs")
				}
				w.Write([]byte("Owned => Go"))
			}))
			defer srv.Close()
			setting, _ := identifiersSyntheticNetwork(context.Background())
			setting.GitHubHeaders = map[string]string{"Authorization": "Bearer synthetic"}
			f := identifiersFetcher{base: identifiersTransport(), settings: func(context.Context) (identifiersNetworkSettings, error) { return setting, nil }}
			_, err := f.text(context.Background(), strings.Replace(srv.URL, "://", "://"+tc.Userinfo, 1)+"/github")
			if tc.SourceError != "" {
				if pluginErrorCodeInternal(err) != "IDENTIFIERS_SOURCE_UNICODE_ERROR" || calls.Load() != 0 {
					t.Fatal("source UnicodeEncodeError hidden or sent", err, calls.Load())
				}
			} else if err != nil || calls.Load() != 1 {
				t.Fatal("source normal auth failed", err, calls.Load())
			}
		})
	}
	var dials atomic.Int32
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected owned dial")
	}
	setting, _ := identifiersSyntheticNetwork(context.Background())
	setting.Proxy = map[string]string{"http": "http://%E4%B8%AD:pass@owned.invalid:8123", "https": "http://%E4%B8%AD:pass@owned.invalid:8123"}
	f := identifiersFetcher{base: base, settings: func(context.Context) (identifiersNetworkSettings, error) { return setting, nil }}
	_, err = f.text(context.Background(), "http://owned.invalid/plain")
	if pluginErrorCodeInternal(err) != "IDENTIFIERS_SOURCE_UNICODE_ERROR" || dials.Load() != 0 {
		t.Fatal("source proxy UnicodeEncodeError hidden or sent", err, dials.Load())
	}
}
