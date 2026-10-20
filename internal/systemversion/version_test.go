package systemversion

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

type oracleResponse struct {
	Body        string
	Status      int
	Error, None bool
}
type oracle struct {
	ReleaseCases []struct {
		Name    string
		Input   oracleResponse
		Version *string
	} `json:"release_cases"`
	FrontendCases []struct {
		Name  string
		Input struct {
			Exists    bool
			FileHex   string `json:"file_hex"`
			ReadError bool   `json:"read_error"`
		}
		Fallback, Version string
	} `json:"frontend_cases"`
	MessageCases []struct {
		Name          string
		Server, Front oracleResponse
		LocalServer   string `json:"local_server"`
		LocalFront    string `json:"local_front"`
		Notification  map[string]any
	} `json:"message_cases"`
}

func loadOracle(t *testing.T) oracle {
	t.Helper()
	raw, e := os.ReadFile("testdata/original.json")
	if e != nil {
		t.Fatal(e)
	}
	var f oracle
	if json.Unmarshal(raw, &f) != nil || len(f.ReleaseCases) != 29 || len(f.FrontendCases) != 9 || len(f.MessageCases) != 3 {
		t.Fatal("actual installed oracle missing")
	}
	return f
}
func selectFixture(in oracleResponse) (Release, error) {
	if in.Error || in.None || in.Status >= 400 {
		return Release{}, nil
	}
	return SelectRelease([]byte(in.Body))
}
func TestInstalledSystemVersionReleaseAndMessage(t *testing.T) {
	f := loadOracle(t)
	for _, tc := range f.ReleaseCases {
		t.Run(tc.Name, func(t *testing.T) {
			got, e := selectFixture(tc.Input)
			if tc.Name == "escaped_surrogate" {
				if !errors.Is(e, ErrDependency) {
					t.Fatal("source surrogate was silently replaced", got, e)
				}
				return
			}
			if e != nil || !reflect.DeepEqual(got.Version, tc.Version) {
				t.Fatal("original release differs", got.Version, tc.Version, e)
			}
		})
	}
	for _, tc := range f.MessageCases {
		t.Run("message_"+tc.Name, func(t *testing.T) {
			s, e := selectFixture(tc.Server)
			if e != nil {
				t.Fatal(e)
			}
			f, e := selectFixture(tc.Front)
			if e != nil {
				t.Fatal(e)
			}
			if got := Message(s, f, tc.LocalServer, tc.LocalFront); got != tc.Notification["title"] {
				t.Fatal("original message changed", got, tc.Notification)
			}
		})
	}
}
func TestInstalledSystemVersionFileDecode(t *testing.T) {
	for _, tc := range loadOracle(t).FrontendCases {
		if !tc.Input.Exists || tc.Input.ReadError {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			b, e := hex.DecodeString(tc.Input.FileHex)
			if e != nil {
				t.Fatal(e)
			}
			if got := DecodeVersionFile(b); got != tc.Version {
				t.Fatal("source UTF8 errors=replace/strip differs", got, tc.Version)
			}
		})
	}
}
func TestSystemVersionRequestsFreshDefaultAndHTTPFailure(t *testing.T) {
	for _, status := range []int{200, 302, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("User-Agent") != "python-urllib3/2.7.0" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded; charset=UTF-8" || r.Header.Get("Accept") != "" || r.Header.Get("Cookie") != "" {
					t.Error("source RequestUtils default None headers/fresh cookie changed", r.Header)
				}
				w.Header().Set("Set-Cookie", "owned=must-not-reuse")
				w.WriteHeader(status)
				fmt.Fprint(w, `[{"tag_name":"v2.12.1"}]`)
			}))
			defer srv.Close()
			for i := 0; i < 2; i++ {
				got, e := Fetch(context.Background(), nil, []string{}, map[string]string{}, srv.URL, nil)
				if e != nil || got.Status != status || (got.Version != nil) != (status < 400) {
					t.Fatal(got, e)
				}
			}
			if calls != 2 {
				t.Fatal("missing real owned HTTP", calls)
			}
		})
	}
}

func TestInstalledVersionRequestUtilsActualOwnedHTTPHeaders(t *testing.T) {
	raw, e := os.ReadFile("testdata/wire-original.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Name    string
			Headers map[string]string
			Proxy   map[string]string
			Capture struct {
				URL, Method string
				WireHeaders map[string]string `json:"wire_headers"`
				Kwargs      struct {
					Timeout int
					Verify  bool
				}
			}
		}
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 3 {
		t.Fatal("actual installed RequestUtils fixture absent")
	}
	for _, tc := range fixture.Cases {
		if tc.Proxy != nil {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			var calls int
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.Capture.Method || "https://"+r.Host+r.URL.RequestURI() != tc.Capture.URL {
					t.Error("original request target/method differs")
				}
				if len(r.Header) != len(tc.Capture.WireHeaders) {
					t.Error("wire header presence differs", r.Header, tc.Capture.WireHeaders)
				}
				for k, v := range tc.Capture.WireHeaders {
					if r.Header.Get(k) != v {
						t.Error("actual source wire header changed", k, r.Header.Get(k), v)
					}
				}
				fmt.Fprint(w, `[{"tag_name":"v2.11"}]`)
			}))
			defer srv.Close()
			base := http.DefaultTransport.(*http.Transport).Clone()
			defer base.CloseIdleConnections()
			base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "api.github.com:443" {
					return nil, errors.New("owned fixture refuses other dials")
				}
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			}
			got, e := Fetch(context.Background(), base, []string{}, tc.Headers, tc.Capture.URL, nil)
			if e != nil || got.Version == nil || *got.Version != "v2.11" || len(got.BodySHA) != 64 || calls != 1 || tc.Capture.Kwargs.Timeout != 20 || tc.Capture.Kwargs.Verify {
				t.Fatal(got, e, calls)
			}
		})
	}
}
