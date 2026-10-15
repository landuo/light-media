package fnos

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestVersionEntryRejectsUnconfiguredPortsAndURLRewrites(t *testing.T) {
	for _, tc := range []struct {
		configured, location string
		allowed              bool
	}{
		{"http://192.168.8.129:5666/v", "https://192.168.8.129:5667" + versionAPIPath, true},
		{"http://192.168.8.129:8005/v", "https://192.168.8.129:5667" + versionAPIPath, true},
		{"https://192.168.8.129:8443/v", "https://192.168.8.129:8443" + versionAPIPath, true},
		{"http://192.168.8.129:5666/v", "https://192.168.8.129:8443" + versionAPIPath, false},
		{"https://192.168.8.129:5667/v", "https://192.168.8.129:8443" + versionAPIPath, false},
		{"https://192.168.8.129:5667/v", "http://192.168.8.129:5666" + versionAPIPath, false},
		{"http://192.168.8.129:5666/v", "https://192.168.8.130:5667" + versionAPIPath, false},
		{"http://192.168.8.129:5666/v", "https://user:password@192.168.8.129:5667" + versionAPIPath, false},
		{"http://192.168.8.129:5666/v", "https://192.168.8.129:5667/v/api/v1/login", false},
		{"http://192.168.8.129:5666/v", "https://192.168.8.129:5667" + versionAPIPath + "?target=other", false},
		{"http://192.168.8.129:5666/v", "https://192.168.8.129:5667" + versionAPIPath + "#", false},
		{"https://public.example/v", "https://public.example" + versionAPIPath, false},
	} {
		request, _ := url.Parse(tc.configured + "/api/v1/sys/version")
		if _, allowed := redirectedVersionEntry(tc.configured, request, tc.location); allowed != tc.allowed {
			t.Fatalf("configured=%q location=%q allowed=%t", tc.configured, tc.location, allowed)
		}
	}
}

func TestVersionEntryBudgetIncludesRedirectsAndSharesOneDeadline(t *testing.T) {
	entry := "http://127.0.0.1:5666/v"
	for _, tc := range []struct {
		name     string
		initial  int
		want     string
		attempts int
	}{
		{"request budget", 18, "REQUEST_LIMIT", 2},
		{"redirect budget", 0, "REDIRECT_LIMIT", maxEntryRedirects + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var chainDeadline time.Time
			transport := serviceTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := request.Context().Deadline()
				if !ok || time.Until(deadline) > 12*time.Second {
					t.Error("version resolution did not have a bounded deadline")
				}
				if calls == 1 {
					chainDeadline = deadline
				} else if deadline != chainDeadline {
					t.Error("redirect reset the version resolution deadline")
				}
				if request.Method != http.MethodGet || request.Header.Get("Authorization") != "" || request.URL.Path != versionAPIPath {
					t.Error("resolution budget was spent on a credential-bearing request")
				}
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://127.0.0.1:5667" + versionAPIPath}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			probe := client{host: entry, http: newFNOSHTTPClient(entry, transport), requests: tc.initial}
			_, err := probe.request(context.Background(), "/sys/version", nil)
			if err == nil || err.Error() != tc.want || calls != tc.attempts || probe.requests != tc.initial+tc.attempts {
				t.Fatalf("budget mismatch error=%v calls=%d requests=%d", err, calls, probe.requests)
			}
		})
	}
}

func TestFNOSTransportScopeAndCertificateCompatibilityAreLocal(t *testing.T) {
	base := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: false}}
	newFNOSHTTPClient("https://127.0.0.1:5667/v", base)
	newFNOSHTTPClient("https://public.example/v", base)
	if base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("LAN compatibility modified the caller's TLS policy")
	}
	configured, _ := url.Parse("http://127.0.0.1:5666/v")
	hits := 0
	guard := fnosTargetTransport{entry: configured, base: serviceTransport(func(request *http.Request) (*http.Response, error) {
		hits++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	for _, target := range []string{
		"https://127.0.0.1:8443" + versionAPIPath,
		"https://localhost:5667" + versionAPIPath,
		"http://127.0.0.1:8005" + versionAPIPath,
	} {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		if _, err := guard.RoundTrip(request); err == nil {
			t.Fatal("transport bypassed configured-origin and HTTPS5667 limits")
		}
	}
	if hits != 0 {
		t.Fatal("rejected target reached underlying transport")
	}
}
