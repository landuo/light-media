package catalog

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/logging"
	"light-media/internal/networkproxy"
)

type originalPluginPolicyTransport struct {
	calls   atomic.Int64
	closes  atomic.Int64
	t       *testing.T
	private bool
}

func (p *originalPluginPolicyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.calls.Add(1)
	if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != apiHost || r.URL.Path != "/3/movie/7" || r.URL.Query().Get("api_key") != "owned-policy" || r.URL.Query().Get("language") != "zh-CN" || r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") != "light-media/0.1" {
		p.t.Error("fixed catalog request contract changed")
	}
	if originalPluginHTTPS(r.Context()) != p.private {
		p.t.Error("wrong ordinary/private transport selected")
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":7}`)), Request: r}, nil
}
func (p *originalPluginPolicyTransport) CloseIdleConnections() { p.closes.Add(1) }

func TestOriginalPluginCatalogSharedPolicy(t *testing.T) {
	c := New(Credentials{APIKey: "owned-policy"})
	if c.interval != 250*time.Millisecond || cap(c.inflight) != 4 {
		t.Fatal("production scheduling defaults changed")
	}
	ordinary := &originalPluginPolicyTransport{t: t}
	private := &originalPluginPolicyTransport{t: t, private: true}
	originalHTTP := c.http
	c.http.Transport = logging.Transport(ordinary) // existing trusted in-memory test seam; no network
	factoryCalls := 0
	c.pluginHTTP.factory = func() (http.RoundTripper, error) { factoryCalls++; return private, nil }
	ctx := context.Background()
	var got map[string]int
	if err := c.get(ctx, "movie/7", nil, &got); err != nil || got["id"] != 7 {
		t.Fatal(got, err)
	}
	c.mu.Lock()
	firstNext := c.next
	c.mu.Unlock()
	if err := c.get(OriginalPluginContext(ctx), "movie/7", nil, &got); err != nil || got["id"] != 7 {
		t.Fatal(got, err)
	}
	c.mu.Lock()
	secondNext := c.next
	c.mu.Unlock()
	if c.http != originalHTTP || c.interval != 250*time.Millisecond || cap(c.inflight) != 4 || !secondNext.After(firstNext) || len(c.inflight) != 0 || factoryCalls != 1 || ordinary.calls.Load() != 1 || private.calls.Load() != 1 {
		t.Fatal("private policy forked object, limiter or request ownership")
	}
	selected, err := c.pluginHTTP.forContext(OriginalPluginContext(ctx), c.http)
	if err != nil || selected == c.http || selected.Timeout != c.http.Timeout || selected.Jar != c.http.Jar || selected.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("HTTP policy was not retained", err)
	}
	_ = c.responseError(429, "1")
	for _, requestCtx := range []context.Context{ctx, OriginalPluginContext(ctx)} {
		err := c.get(requestCtx, "movie/7", nil, &got)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != "rate_limited" {
			t.Fatal("429 block did not apply to both policies", err)
		}
	}
	canceled, cancel := context.WithCancel(OriginalPluginContext(ctx))
	cancel()
	if err := c.get(canceled, "movie/7", nil, &got); err == nil {
		t.Fatal("canceled request accepted")
	}
	if ordinary.calls.Load() != 1 || private.calls.Load() != 1 {
		t.Fatal("blocked/canceled request reached transport")
	}
	c.CloseIdleConnections()
	if ordinary.closes.Load() != 1 || private.closes.Load() != 1 {
		t.Fatal("owned pools were not closed independently")
	}

	owner := NewWithTransport(Credentials{APIKey: "owned-policy"}, ordinary)
	selected, err = owner.pluginHTTP.forContext(OriginalPluginContext(ctx), owner.http)
	if err != nil || selected != owner.http || owner.interval != 0 {
		t.Fatal("explicit owner transport seam reinterpreted", err)
	}
	if OriginalPluginContext(nil) != nil || originalPluginHTTPS(context.WithValue(ctx, "originalPluginHTTPS", true)) {
		t.Fatal("marker accepted untrusted string key")
	}
}

func TestOriginalPluginCatalogPoolCloneAndConcurrency(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{ServerName: "owned-sni", MinVersion: tls.VersionTLS12}
	base.Proxy = func(*http.Request) (*url.URL, error) { return url.Parse("http://owned.invalid:8080") }
	base.DialContext = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("must not dial") }
	originalTLS := base.TLSClientConfig
	factory := originalPluginBaseFactory(base)
	rt, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	clone := rt.(*http.Transport)
	defer clone.CloseIdleConnections()
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: apiHost}}
	before, _ := base.Proxy(req)
	after, _ := clone.Proxy(req)
	if clone == base || clone.TLSClientConfig == originalTLS || !clone.TLSClientConfig.InsecureSkipVerify || originalTLS.InsecureSkipVerify || clone.TLSClientConfig.ServerName != originalTLS.ServerName || clone.TLSClientConfig.MinVersion != originalTLS.MinVersion || before.String() != after.String() || reflect.ValueOf(clone.DialContext).Pointer() != reflect.ValueOf(base.DialContext).Pointer() {
		t.Fatal("clone modified original TLS/routing policy")
	}
	if originalPluginBaseFactory(&originalPluginPolicyTransport{t: t}) != nil {
		t.Fatal("opaque owner transport was reinterpreted")
	}

	e, err := NewOriginalPluginExplorer(New(Credentials{APIKey: "owned-policy"}), networkproxy.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ordinary := e.http
	private := &originalPluginPolicyTransport{t: t, private: true}
	var calls atomic.Int64
	e.pluginHTTP.factory = func() (http.RoundTripper, error) { calls.Add(1); return private, nil }
	const workers = 12
	selected := make([]*http.Client, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			selected[i], errs[i] = e.pluginHTTP.forContext(OriginalPluginContext(context.Background()), ordinary)
		}(i)
	}
	wg.Wait()
	for i := 0; i < workers; i++ {
		if errs[i] != nil || selected[i] == ordinary || selected[i] != selected[0] {
			t.Fatal("private pool split under concurrent selection", i, errs[i])
		}
	}
	if calls.Load() != 1 {
		t.Fatal("more than one private transport created")
	}
	untouched, err := e.pluginHTTP.forContext(context.Background(), ordinary)
	if err != nil || untouched != ordinary || e.http != ordinary {
		t.Fatal("ordinary Explorer client changed", err)
	}
	e.CloseIdleConnections()
	if private.closes.Load() != 1 {
		t.Fatal("Explorer private pool not closed")
	}
}
