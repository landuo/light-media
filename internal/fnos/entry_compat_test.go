package fnos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const entryUsername = "synthetic-entry-user"
const entryPassword = "synthetic-entry-password"
const entryToken = "synthetic-entry-session"

type entryCounters struct {
	versions atomic.Int32
	logins   atomic.Int32
	reads    atomic.Int32
}

func verifyEntrySignature(t *testing.T, request *http.Request, payload []byte) {
	t.Helper()
	auth, err := url.ParseQuery(request.Header.Get("authx"))
	if err != nil || len(auth.Get("nonce")) != 6 || auth.Get("timestamp") == "" || request.Header.Get("authx") != Signature(request.URL.Path, string(payload), auth.Get("nonce"), auth.Get("timestamp")) {
		t.Error("entry request signature did not cover its actual path and body")
	}
}

func verifyCredentialFreeVersion(t *testing.T, request *http.Request, payload []byte) {
	t.Helper()
	if request.Method != http.MethodGet || request.URL.Path != "/v/api/v1/sys/version" || request.URL.RawQuery != "" || len(payload) != 0 || request.Header.Get("Authorization") != "" {
		t.Error("entry resolution received something other than a credential-free version GET")
	}
	for _, secret := range []string{entryUsername, entryPassword, entryToken} {
		if strings.Contains(request.URL.String(), secret) || strings.Contains(fmt.Sprint(request.Header), secret) || strings.Contains(string(payload), secret) {
			t.Error("entry resolution exposed a credential")
		}
	}
	verifyEntrySignature(t, request, payload)
}

func entryMediaHandler(t *testing.T, counts *entryCounters) http.HandlerFunc {
	t.Helper()
	return func(writer http.ResponseWriter, request *http.Request) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error("could not read synthetic request")
		}
		if request.TLS == nil {
			t.Error("media API received an unencrypted credential-capable request")
		}
		verifyEntrySignature(t, request, payload)
		switch request.URL.Path {
		case "/v/api/v1/sys/version":
			verifyCredentialFreeVersion(t, request, payload)
			counts.versions.Add(1)
			fmt.Fprint(writer, `{"code":0,"data":{"version":"1.0-synthetic","mediasrvVersion":"1.0-synthetic"}}`)
		case "/v/api/v1/login":
			counts.logins.Add(1)
			var login map[string]string
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "" || request.Header.Get("Content-Type") != "application/json" || json.Unmarshal(payload, &login) != nil || login["username"] != entryUsername || login["password"] != entryPassword || login["app_name"] != "trimemedia-web" {
				t.Error("resolved TLS login lost its signed POST body")
			}
			fmt.Fprintf(writer, `{"code":0,"data":{"token":%q}}`, entryToken)
		case "/v/api/v1/user/info", "/v/api/v1/mdb/list", "/v/api/v1/item/list":
			counts.reads.Add(1)
			if request.Header.Get("Authorization") != entryToken {
				t.Error("resolved media read lost its in-memory session")
			}
			switch request.URL.Path {
			case "/v/api/v1/user/info":
				fmt.Fprint(writer, `{"code":0,"data":{"is_admin":1}}`)
			case "/v/api/v1/mdb/list":
				fmt.Fprintf(writer, `{"code":0,"data":[{"guid":%q,"name":"影视库","category":"Mix","dir_list":["/synthetic"]}]}`, serviceLibGUID)
			default:
				fmt.Fprint(writer, `{"code":0,"data":{"list":[]}}`)
			}
		default:
			t.Error("resolved media API received an unexpected operation")
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}
}

func exerciseEntryConnection(t *testing.T, host string, expectedProbeRequests int, transport http.RoundTripper) {
	t.Helper()
	config := Config{Host: host, Username: entryUsername, Password: entryPassword}
	var result Result
	if transport == nil {
		result = Probe(context.Background(), config)
	} else {
		result = probeWithTransport(context.Background(), config, transport)
	}
	if result.Status != "passed" || !result.Authenticated || len(result.Libraries) != 1 || result.Libraries[0].Name != "影视库" || result.Requests != expectedProbeRequests {
		t.Fatalf("entry probe did not authenticate and read its library: status=%s code=%s authenticated=%t libraries=%d requests=%d", result.Status, result.Code, result.Authenticated, len(result.Libraries), result.Requests)
	}
	var service *Service
	var err error
	if transport == nil {
		service, err = NewService(config)
	} else {
		service, err = newService(config, transport)
	}
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := service.Libraries(context.Background())
	if err != nil || len(libraries) != 1 || libraries[0].GUID != serviceLibGUID {
		t.Fatalf("resolved service could not read its library: libraries=%d err=%v", len(libraries), err)
	}
}

func TestPrivateHTTPEntryResolvesTLSBeforeAnyCredentialRequest(t *testing.T) {
	counts := &entryCounters{}
	media := httptest.NewTLSServer(entryMediaHandler(t, counts))
	defer media.Close()
	var entryRequests atomic.Int32
	entry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		entryRequests.Add(1)
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error("could not read entry request")
		}
		verifyCredentialFreeVersion(t, request, payload)
		http.Redirect(writer, request, "https://127.0.0.1:5667/v/api/v1/sys/version", http.StatusFound)
	}))
	defer entry.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		target := ""
		switch address {
		case "127.0.0.1:5666":
			target = entry.Listener.Addr().String()
		case "127.0.0.1:5667":
			target = media.Listener.Addr().String()
		default:
			return nil, fmt.Errorf("unexpected synthetic connection target")
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	exerciseEntryConnection(t, "http://127.0.0.1:5666/v", 6, transport)
	if entryRequests.Load() != 2 || counts.versions.Load() != 2 || counts.logins.Load() != 2 || counts.reads.Load() != 5 {
		t.Fatalf("unexpected resolution/session requests: entry=%d version=%d login=%d reads=%d", entryRequests.Load(), counts.versions.Load(), counts.logins.Load(), counts.reads.Load())
	}
}

func TestDirectPrivateSelfSignedTLSEntryAuthenticates(t *testing.T) {
	counts := &entryCounters{}
	media := httptest.NewTLSServer(entryMediaHandler(t, counts))
	defer media.Close()
	exerciseEntryConnection(t, media.URL, 5, nil)
	if counts.versions.Load() != 2 || counts.logins.Load() != 2 || counts.reads.Load() != 5 {
		t.Fatalf("direct private TLS skipped version resolution or a session: version=%d login=%d reads=%d", counts.versions.Load(), counts.logins.Load(), counts.reads.Load())
	}
}

func assertBlockedEntry(t *testing.T, host string, sensitiveLocations ...string) {
	t.Helper()
	config := Config{Host: host, Username: entryUsername, Password: entryPassword}
	result := Probe(context.Background(), config)
	if result.Status != "failed" || result.Authenticated || (result.Code != "HTTP_302" && result.Code != "REDIRECT_BLOCKED") {
		t.Fatalf("unsafe entry redirect was not blocked: status=%s code=%s authenticated=%t", result.Status, result.Code, result.Authenticated)
	}
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Libraries(context.Background())
	serviceErrorCode(t, err, "redirect_blocked")
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, secret := range append([]string{entryUsername, entryPassword, entryToken, host}, sensitiveLocations...) {
		if strings.Contains(string(encoded), secret) || strings.Contains(err.Error(), secret) {
			t.Fatal("blocked entry error exposed credentials, configuration or redirect target")
		}
	}
}

func TestPrivateEntryRejectsRedirectToAnotherHostname(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer target.Close()
	parsed, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = "localhost:" + parsed.Port()
	redirect := parsed.String() + "/v/api/v1/sys/version"
	entry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		verifyCredentialFreeVersion(t, request, payload)
		http.Redirect(writer, request, redirect, http.StatusFound)
	}))
	defer entry.Close()
	assertBlockedEntry(t, entry.URL, target.URL, redirect)
	if received.Load() != 0 {
		t.Fatalf("cross-host redirect reached its target %d times", received.Load())
	}
}

func TestPrivateEntryRejectsHTTPSDowngrade(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer target.Close()
	redirect := target.URL + "/v/api/v1/sys/version"
	entry := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		verifyCredentialFreeVersion(t, request, payload)
		http.Redirect(writer, request, redirect, http.StatusFound)
	}))
	defer entry.Close()
	assertBlockedEntry(t, entry.URL, target.URL, redirect)
	if received.Load() != 0 {
		t.Fatalf("HTTPS downgrade reached its target %d times", received.Load())
	}
}

func TestPrivateEntryRejectsUpgradeToAnotherHTTPSPort(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer target.Close()
	redirect := target.URL + "/v/api/v1/sys/version"
	for _, entryTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured-tls=%t", entryTLS), func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				payload, _ := io.ReadAll(request.Body)
				verifyCredentialFreeVersion(t, request, payload)
				http.Redirect(writer, request, redirect, http.StatusFound)
			})
			var entry *httptest.Server
			if entryTLS {
				entry = httptest.NewTLSServer(handler)
			} else {
				entry = httptest.NewServer(handler)
			}
			defer entry.Close()
			assertBlockedEntry(t, entry.URL, target.URL, redirect)
			if received.Load() != 0 {
				t.Fatalf("redirect to an unapproved HTTPS port reached its target %d times", received.Load())
			}
		})
	}
}

func TestResolvedPrivateLoginRedirectNeverReplaysCredentials(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer target.Close()
	entry := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		verifyEntrySignature(t, request, payload)
		if request.URL.Path == "/v/api/v1/sys/version" {
			verifyCredentialFreeVersion(t, request, payload)
			fmt.Fprint(writer, `{"code":0,"data":{"version":"1.0-synthetic","mediasrvVersion":"1.0-synthetic"}}`)
			return
		}
		var login map[string]string
		if request.URL.Path != "/v/api/v1/login" || request.Method != http.MethodPost || json.Unmarshal(payload, &login) != nil || login["username"] != entryUsername || login["password"] != entryPassword {
			t.Error("credential redirect test did not receive its original signed login POST")
		}
		http.Redirect(writer, request, target.URL+"/v/api/v1/login", http.StatusFound)
	}))
	defer entry.Close()
	assertBlockedEntry(t, entry.URL, target.URL)
	if received.Load() != 0 {
		t.Fatalf("credential request redirect reached its target %d times", received.Load())
	}
}
