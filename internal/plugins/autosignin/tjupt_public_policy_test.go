package autosignin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// All endpoints and certificates below are owned synthetic fixtures, not
// evidence that any origin is present in the installed user's allowlist.
func TestTjuptPublicPolicyTLSRedirectCookiesRevocationAndLimits(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		for _, h := range []string{"Cookie", "Authorization", "Proxy-Authorization"} {
			if r.Header.Get(h) != "" {
				t.Errorf("credential header reached public endpoint: %s", h)
			}
		}
		if r.Header.Get("Referer") != "https://movie.douban.com" {
			t.Error("incorrect image referer")
		}
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Set-Cookie", "injected=secret; Path=/; Secure")
			http.Redirect(w, r, "/image", 302)
		case "/cross":
			http.Redirect(w, r, "https://unapproved.invalid/image", 302)
		case "/large":
			io.WriteString(w, strings.Repeat("x", (1<<20)+1))
		case "/headers":
			w.Header().Set("X-Oversized", strings.Repeat("x", 70<<10))
			io.WriteString(w, "x")
		case "/wait":
			started <- struct{}{}
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0xff, 0x00, 0xfe})
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	var guards atomic.Int32
	deny := errors.New("owned revoked grant")
	var revoke atomic.Bool
	binding := TjuptPublicBinding{ImageOrigins: []string{server.URL}, RootCAs: roots, BeforeRequest: func(context.Context) error {
		n := guards.Add(1)
		if revoke.Load() && n%2 == 0 {
			return deny
		}
		return nil
	}}
	p, err := BindTjuptPublicHTTPWithBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://unapproved.invalid/image", "https://movie.douban.com/image", strings.Replace(server.URL, "https:", "http:", 1) + "/image", server.URL + "@evil.invalid/image"} {
		if _, err := p.Image(context.Background(), address); !errors.Is(err, ErrSiteTargetDenied) {
			t.Fatalf("unauthorized origin accepted: %v", err)
		}
	}
	if calls.Load() != 0 || guards.Load() != 0 {
		t.Fatal("denied origin performed I/O or grant callback")
	}
	response, err := p.Image(context.Background(), server.URL+"/redirect")
	if err != nil || response == nil || len(response.Content) != 3 || calls.Load() != 2 || guards.Load() != 2 {
		t.Fatalf("redirect failed: %#v %v", response, err)
	}
	revoke.Store(true)
	_, err = p.Image(context.Background(), server.URL+"/redirect")
	if !errors.Is(err, deny) || calls.Load() != 3 || guards.Load() != 4 {
		t.Fatalf("redirect guard failed: %v calls=%d guards=%d", err, calls.Load(), guards.Load())
	}
	revoke.Store(false)
	_, err = p.Image(context.Background(), server.URL+"/cross")
	if !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatal(err)
	}
	_, err = p.Image(context.Background(), server.URL+"/large")
	if !errors.Is(err, ErrSiteResponseLimit) {
		t.Fatalf("body cap absent: %v", err)
	}
	response, err = p.Image(context.Background(), server.URL+"/headers")
	if response != nil {
		t.Fatal("header cap absent")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.Image(ctx, server.URL+"/wait"); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request never arrived")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel failed")
	}
	before := calls.Load()
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = p.Image(ctx, server.URL+"/image")
	if !errors.Is(err, context.Canceled) || calls.Load() != before {
		t.Fatal("pre-cancel performed I/O")
	}
}

func TestTjuptPublicPolicyEnvironmentOnlyAndFrozenHostTrust(t *testing.T) {
	var originHits, proxyHits, explicitHits atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials leaked")
		}
		io.WriteString(w, "owned image")
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		if r.Header.Get("Proxy-Authorization") != "Basic c3ludGhldGljOnNlY3JldA==" {
			t.Error("synthetic proxy auth absent")
		}
		if r.Method != "CONNECT" {
			t.Error("expected CONNECT")
			http.Error(w, "bad method", 400)
			return
		}
		target, _ := url.Parse(origin.URL)
		if r.Host != target.Host {
			t.Error("unexpected CONNECT target")
			http.Error(w, "bad target", 400)
			return
		}
		upstream, err := net.Dial("tcp", target.Host)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { io.Copy(upstream, conn); upstream.Close(); close(done) }()
		io.Copy(conn, upstream)
		conn.Close()
		<-done
	}))
	defer proxy.Close()
	explicit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitHits.Add(1)
		http.Error(w, "must not be selected", 500)
	}))
	defer explicit.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("synthetic", "secret")
	policy := &RequestsProxyPolicy{Environment: []string{"https_proxy=" + proxyURL.String()}, Explicit: map[string]string{"https": explicit.URL}}
	var revoked atomic.Bool
	denied := errors.New("owned public proxy grant revoked")
	binding := TjuptPublicBinding{ImageOrigins: []string{origin.URL}, RequestsProxy: policy, RootCAs: roots, BeforeRequest: func(context.Context) error {
		if revoked.Load() {
			return denied
		}
		return nil
	}}
	p, err := BindTjuptPublicHTTPWithBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	binding.ImageOrigins[0] = "https://unapproved.invalid"
	policy.Environment[0] = "https_proxy=" + explicit.URL
	policy.Explicit["https"] = explicit.URL
	u, _ := url.Parse(origin.URL)
	image := p.clients[originOf(u)].(tjuptPublicBound).bound
	suggest := p.clients["https://movie.douban.com:443"].(tjuptPublicBound).bound
	if image.binding.RootCAs == roots || suggest.binding.RootCAs == roots || image.binding.RootCAs == suggest.binding.RootCAs || image.binding.RequestsProxy == suggest.binding.RequestsProxy {
		t.Fatal("host state shared between clients")
	}
	// Mutate the original trust pool after bind, not merely its pointer.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(739), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(extra)
	if image.binding.RootCAs.Equal(roots) || suggest.binding.RootCAs.Equal(roots) {
		t.Fatal("mutated host trust reached bound clients")
	}
	for _, v := range []any{binding, SiteHTTPBinding{RootCAs: roots}} {
		data, err := json.Marshal(v)
		if err != nil || strings.Contains(string(data), "RootCAs") || strings.Contains(string(data), "ImageOrigins") || strings.Contains(string(data), proxy.URL) {
			t.Fatalf("host policy serialized: %s %v", data, err)
		}
	}
	field, ok := reflect.TypeOf(Registration{}).FieldByName("RootCAs")
	if !ok || field.Tag.Get("json") != "-" {
		t.Fatal("registration trust serializable")
	}
	response, err := p.Image(context.Background(), origin.URL+"/image")
	if err != nil || response == nil || string(response.Content) != "owned image" || proxyHits.Load() != 1 || originHits.Load() != 1 || explicitHits.Load() != 0 {
		t.Fatalf("environment routing failed: %v %v hits=%d/%d/%d", response, err, proxyHits.Load(), originHits.Load(), explicitHits.Load())
	}
	revoked.Store(true)
	_, err = p.Image(context.Background(), origin.URL+"/image")
	if !errors.Is(err, denied) || proxyHits.Load() != 1 || originHits.Load() != 1 {
		t.Fatalf("revocation guard ran after proxy I/O: %v", err)
	}
}

func TestTjuptHostTrustPropagation(t *testing.T) {
	roots := x509.NewCertPool()
	registration := Registration{Site: Object{"id": 1, "url": "https://www.tjupt.org/"}, RootCAs: roots}
	registered := NewRegisteredSites([]Registration{registration})
	if registered.rows[0].RootCAs == roots {
		t.Fatal("registration shares root pool")
	}
	h, err := BindRegistrationHooks(registered.rows[0], true)
	if err != nil {
		t.Fatal(err)
	}
	fixed, ok := h.HTTP.(*FixedSiteGrant)
	if !ok {
		t.Fatal("fixed grant missing")
	}
	for _, client := range fixed.clients {
		b := client.(*BoundSiteHTTP)
		if b.binding.RootCAs == nil || b.binding.RootCAs == registered.rows[0].RootCAs || !b.binding.RootCAs.Equal(roots) {
			t.Fatal("fixed trust snapshot lost")
		}
	}
	registration.Site = Object{"url": "https://kp.m-team.cc/", "token": "synthetic"}
	h, err = BindRegistrationHooks(registration, true)
	if err != nil {
		t.Fatal(err)
	}
	m := h.MTeam.(*BoundMTeam).api.(*BoundSiteHTTP)
	if m.binding.RootCAs == nil || m.binding.RootCAs == roots || !m.binding.RootCAs.Equal(roots) {
		t.Fatal("MTeam trust snapshot lost")
	}
	for _, address := range []string{"https://*.invalid", "https://example.invalid:", "https://example.invalid:0", "https://example.invalid:65536", "https://example.invalid?", "https://example.invalid/path", "https://user@example.invalid", "http://example.invalid"} {
		if _, err := BindTjuptPublicHTTPWithBinding(TjuptPublicBinding{ImageOrigins: []string{address}}); !errors.Is(err, ErrSiteTargetDenied) {
			t.Fatalf("invalid origin accepted: %s %v", address, err)
		}
	}
}
