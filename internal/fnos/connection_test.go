package fnos

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNormalizeHostAndSharedValidation(t *testing.T) {
	for _, host := range []string{"https://nas.example:5667", "http://192.168.8.129:8005", "http://10.2.3.4", "http://172.31.1.2", "http://127.0.0.1", "http://localhost:8005", "http://[::1]:8005", "http://[fd00::1]:8005", "http://[::ffff:192.168.8.129]:8005"} {
		for _, suffix := range []string{"", "/", "/v", "/v/"} {
			normalized, err := NormalizeHost(host + suffix)
			if err != nil || normalized != host+"/v" {
				t.Fatalf("normalize %q = %q, %v", host+suffix, normalized, err)
			}
			service, err := NewService(Config{Host: host + suffix, Username: "u", Password: "p"})
			if err != nil || service.host != normalized {
				t.Fatalf("service normalization disagrees: %q, %v", host+suffix, err)
			}
		}
	}
	for _, host := range []string{
		"", "ftp://192.168.8.129/v", "http://nas.example/v", "http://8.8.8.8/v", "http://[2001:4860:4860::8888]/v", "http://[::ffff:8.8.8.8]/v", "http://192.168.8.129.example/v", "http://localhost.example/v", "http://127.1/v", "http://2130706433/v", "http://169.254.1.2/v",
		"https://user:password@nas.example/v", "https://nas.example/v?token=x", "https://nas.example/v?", "https://nas.example/v#private", "https://nas.example/v#", "https://nas.example/wrong/path", "https://nas.example/v/../v", "https://nas.example/%76", "https://nas.example:65536/v", "https://nas.example:0/v", "https://nas.example:/v",
	} {
		if _, err := NormalizeHost(host); err == nil {
			t.Fatalf("unsafe host normalized: %q", host)
		}
		if _, err := NewService(Config{Host: host, Username: "u", Password: "p"}); err == nil {
			t.Fatalf("service accepted unsafe host: %q", host)
		}
		result := Probe(context.Background(), Config{Host: host, Username: "u", Password: "p"})
		if result.Code != "INVALID_HOST" || result.Status != "failed" || result.Requests != 0 || result.Message == "" {
			t.Fatalf("probe accepted unsafe host: %q, %+v", host, result)
		}
	}
}

func TestPrivateHTTPProbeAndLibrariesAuthenticate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		auth, _ := url.ParseQuery(request.Header.Get("authx"))
		if request.Header.Get("authx") != Signature(request.URL.Path, string(payload), auth.Get("nonce"), auth.Get("timestamp")) {
			t.Error("HTTP request signature changed")
		}
		switch request.URL.Path {
		case "/v/api/v1/sys/version":
			fmt.Fprint(writer, `{"code":0,"data":{"version":"1.0-synthetic","mediasrvVersion":"1.0-synthetic"}}`)
		case "/v/api/v1/login":
			var login map[string]string
			if request.Method != "POST" || json.Unmarshal(payload, &login) != nil || login["username"] != "synthetic-user" || login["password"] != "synthetic-password" {
				t.Error("incorrect HTTP login")
			}
			fmt.Fprint(writer, `{"code":0,"data":{"token":"synthetic-session-token"}}`)
		case "/v/api/v1/user/info", "/v/api/v1/mdb/list", "/v/api/v1/item/list":
			if request.Header.Get("Authorization") != "synthetic-session-token" {
				t.Error("HTTP authenticated request lost its session")
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
			t.Errorf("unexpected HTTP operation %s", request.URL.Path)
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, suffix := range []string{"", "/", "/v", "/v/"} {
		t.Run("entry"+suffix, func(t *testing.T) {
			config := Config{Host: server.URL + suffix, Username: "synthetic-user", Password: "synthetic-password"}
			result := Probe(context.Background(), config)
			if result.Status != "passed" || !result.Authenticated || result.Requests != 5 || len(result.Libraries) != 1 || !strings.Contains(result.Message, "连接成功") {
				t.Fatalf("HTTP probe did not authenticate and read libraries: %+v", result)
			}
			service, err := NewService(config)
			if err != nil {
				t.Fatal(err)
			}
			libraries, err := service.Libraries(context.Background())
			if err != nil || len(libraries) != 1 || libraries[0].GUID != serviceLibGUID {
				t.Fatalf("HTTP service did not authenticate and read libraries: %+v, %v", libraries, err)
			}
		})
	}
}

func TestProbeExplainsLoginBusinessFailureOnlyAtLogin(t *testing.T) {
	for _, endpoint := range []string{"/sys/version", "/login"} {
		server := syntheticServer(t, map[string]string{endpoint: `{"code":107006,"message":"private-provider-body synthetic-password"}`})
		result := Probe(context.Background(), Config{Host: server.URL, Username: "synthetic-user", Password: "synthetic-password"})
		server.Close()
		if endpoint == "/login" {
			if result.Code != "AUTHENTICATION_FAILED" || !strings.Contains(result.Message, "账号密码") {
				t.Fatalf("login failure not explained: %+v", result)
			}
		} else if result.Code != "BUSINESS_107006" || strings.Contains(result.Message, "账号密码") {
			t.Fatalf("unrelated business failure called authentication failure: %+v", result)
		}
		output, _ := json.Marshal(result)
		if strings.Contains(string(output), "private-provider-body") || strings.Contains(string(output), "synthetic-password") {
			t.Fatal("login failure exposed provider text or credentials")
		}
	}
}

func TestUntrustedTLSRemainsRejectedWithUsefulMessage(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted TLS reached application handler") }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	config := Config{Host: server.URL, Username: "synthetic-user", Password: "synthetic-password"}
	result := Probe(context.Background(), config)
	if result.Status != "failed" || result.Code != "TLS_CERTIFICATE_FAILED" || !strings.Contains(result.Message, "证书校验失败") || result.Authenticated {
		t.Fatalf("untrusted TLS not explained: %+v", result)
	}
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Libraries(context.Background())
	serviceErrorCode(t, err, "tls_certificate_failed")
	output, _ := json.Marshal(result)
	for _, secret := range []string{config.Username, config.Password, server.URL} {
		if strings.Contains(string(output), secret) || strings.Contains(err.Error(), secret) {
			t.Fatal("TLS failure exposed configuration")
		}
	}
}

func TestNetworkFailureCodesAreFixedAndSanitized(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"canceled", context.Canceled, "canceled"},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection_refused"},
		{"certificate", x509.UnknownAuthorityError{}, "tls_certificate_failed"},
		{"network", errors.New("private-provider-body synthetic-password https://private.invalid"), "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := serviceTransport(func(*http.Request) (*http.Response, error) { return nil, test.err })
			service, err := newService(Config{Host: "http://127.0.0.1/v", Username: "synthetic-user", Password: "synthetic-password"}, transport)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Libraries(context.Background())
			serviceErrorCode(t, err, test.code)
			probe := client{host: "http://127.0.0.1/v", http: &http.Client{Transport: transport}}
			_, err = probe.request(context.Background(), "/sys/version", nil)
			want := strings.ToUpper(test.code)
			if test.code == "unavailable" {
				want = "TRANSPORT_FAILED"
			}
			if err == nil || err.Error() != want {
				t.Fatalf("probe error = %v, want %s", err, want)
			}
		})
	}
}

func TestProbeAndServiceReportActualTimeout(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		t.Run(fmt.Sprint("body=", flushHeaders), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if flushHeaders {
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(http.StatusOK)
					writer.(http.Flusher).Flush()
				}
				select {
				case <-request.Context().Done():
				case <-time.After(150 * time.Millisecond):
				}
			}))
			defer server.Close()
			config := Config{Host: server.URL, Username: "synthetic-user", Password: "synthetic-password"}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			result := Probe(ctx, config)
			cancel()
			if result.Code != "TIMEOUT" || !strings.Contains(result.Message, "超时") {
				t.Fatalf("timeout = %+v", result)
			}
			service, err := NewService(config)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err = service.Libraries(ctx)
			serviceErrorCode(t, err, "timeout")
		})
	}
}
