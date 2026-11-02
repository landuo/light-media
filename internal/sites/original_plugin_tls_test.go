package sites

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"light-media/internal/networkproxy"
)

type originalPluginNeverNetwork struct {
	t     *testing.T
	calls int
}

func (r *originalPluginNeverNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	r.t.Error("guarded request reached network seam")
	return nil, errors.New("must not call")
}

func TestOriginalPluginSiteTransportPreservesGuards(t *testing.T) {
	base := publicSiteTransport().(*http.Transport)
	beforeTLS := base.TLSClientConfig
	clone := originalPluginTLSClone(base)
	defer clone.CloseIdleConnections()
	if clone == base || clone.Proxy != nil || base.Proxy != nil || clone.DialContext == nil || reflect.ValueOf(clone.DialContext).Pointer() != reflect.ValueOf(base.DialContext).Pointer() || clone.TLSClientConfig == nil || !clone.TLSClientConfig.InsecureSkipVerify || base.TLSClientConfig != beforeTLS || beforeTLS != nil && beforeTLS.InsecureSkipVerify {
		t.Fatal("Nexus private clone lost public dialer or changed shared TLS")
	}
	valid := Config{ID: "owned-nexus", Kind: "nexusphp", BaseURL: "https://tracker.example.com/", Cookie: "owned=session", UserAgent: "owned-policy"}
	plain, err := New(valid)
	if err != nil {
		t.Fatal(err)
	}
	private, err := NewOriginalPlugin(valid)
	if err != nil {
		t.Fatal(err)
	}
	emptyProxy, err := NewOriginalPluginWithProxy(valid, networkproxy.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer private.CloseIdleConnections()
	defer emptyProxy.CloseIdleConnections()
	if plain.closeProxyTransport != nil || private.closeProxyTransport == nil || emptyProxy.closeProxyTransport == nil || private.http.Timeout != plain.http.Timeout || private.base.String() != plain.base.String() || private.config != plain.config || private.http.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("constructor validation/ownership/timeout/redirect changed")
	}
	for _, origin := range []string{"https://127.0.0.1/", "https://localhost/", "https://owned.invalid/", "https://tracker.example.com:8443/", "https://u:p@tracker.example.com/"} {
		invalid := valid
		invalid.BaseURL = origin
		_, normalErr := New(invalid)
		_, privateErr := NewOriginalPlugin(invalid)
		if normalErr == nil || privateErr == nil || normalErr.Error() != privateErr.Error() {
			t.Fatalf("origin validation diverged: %q %v %v", origin, normalErr, privateErr)
		}
	}
	// No DNS/listener/socket: exercise the real pre-I/O request guards.
	transport := &originalPluginNeverNetwork{t: t}
	private.http.Transport = transport
	guardErr := errors.New("owned current authority changed")
	guards := 0
	private.SetRequestGuard(func(context.Context) error { guards++; return guardErr })
	target, _ := url.Parse("https://tracker.example.com/index.php")
	_, _, err = private.request(context.Background(), http.MethodGet, target, nil, map[string]string{"Cookie": "owned=session"}, 1024)
	if err != guardErr || guards != 1 || transport.calls != 0 {
		t.Fatal("current request guard lost", err, guards, transport.calls)
	}
	other, _ := url.Parse("https://other.example.com/index.php")
	_, _, err = private.request(context.Background(), http.MethodGet, other, nil, map[string]string{"Cookie": "owned=session"}, 1024)
	if err == nil || err.Error() != "site_credential_origin_mismatch" || guards != 1 || transport.calls != 0 {
		t.Fatal("origin isolation lost", err)
	}
	invalidProxy := networkproxy.Config{HTTP: "file:///owned", HTTPS: "file:///owned"}
	_, normalErr := NewWithProxy(valid, invalidProxy)
	_, privateErr := NewOriginalPluginWithProxy(valid, invalidProxy)
	if normalErr == nil || privateErr == nil || normalErr.Error() != privateErr.Error() {
		t.Fatal("explicit proxy validation changed", normalErr, privateErr)
	}
}
