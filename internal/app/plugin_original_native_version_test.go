package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"light-media/internal/systemversion"
)

type ownedNativeVersionTransport func(*http.Request) (*http.Response, error)

func (f ownedNativeVersionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type ownedNativeVersionBody struct {
	io.Reader
	closed *int
}

func (b *ownedNativeVersionBody) Close() error { *b.closed++; return nil }

// Only fixed recording RoundTripper inputs: no listener/socket/upstream. This
// verifies the actual private request, response closure and unknown semantics.
func TestOriginalNativeVersionFixedRequestAndPrivateTLS(t *testing.T) {
	first, second := newOriginalNativeVersionTransport(), newOriginalNativeVersionTransport()
	defer first.CloseIdleConnections()
	defer second.CloseIdleConnections()
	if first == second || first.TLSClientConfig == second.TLSClientConfig || !first.TLSClientConfig.InsecureSkipVerify || !second.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("explicit private TLS policy not isolated")
	}
	first.TLSClientConfig.InsecureSkipVerify = false
	if !second.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("private TLS policy shared")
	}
	for _, sample := range []struct {
		name               string
		status             int
		body, want, reason string
	}{
		{"native", 200, `[{"tag_name":"v0.4.19"}]`, "v0.4.19", ""},
		{"empty", 200, `[]`, "", "no-release"}, {"invalid", 200, `{`, "", "metadata"},
		{"unavailable", 403, `{"message":"owned rate limit"}`, "", "status"},
		{"redirect", 302, ``, "", "status"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			calls, closed := 0, 0
			client := &http.Client{Transport: ownedNativeVersionTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.String() != systemversion.NativeReleasesURL || r.URL.User != nil || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("User-Agent") != "light-media/"+appVersion || r.Header.Get("Accept") != "application/vnd.github+json" {
					t.Fatal("fixed credential-free native request changed")
				}
				return &http.Response{StatusCode: sample.status, Header: http.Header{"Location": []string{"https://other.invalid/release"}}, Body: &ownedNativeVersionBody{Reader: strings.NewReader(sample.body), closed: &closed}, Request: r}, nil
			})}
			version, reason, err := originalNativeReleaseFromClient(context.Background(), client)
			got := ""
			if version != nil {
				got = *version
			}
			if err != nil || got != sample.want || reason != sample.reason || calls != 1 || closed != 1 {
				t.Fatal(got, reason, err, calls, closed)
			}
		})
	}
	client := &http.Client{Transport: ownedNativeVersionTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("owned transport failure must not enter notice")
	})}
	if version, reason, err := originalNativeReleaseFromClient(context.Background(), client); version != nil || reason != "query" || err != nil {
		t.Fatal("query failure not unknown")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := originalNativeReleaseFromClient(ctx, client); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was hidden")
	}
}
