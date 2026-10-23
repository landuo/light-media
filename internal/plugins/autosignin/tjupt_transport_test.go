package autosignin

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestActualInstalledRequestsRequoteTjuptLabels(t *testing.T) {
	raw, err := readAutoSignInFixture("requote-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	// Preserve source underscore names in a narrow wire fixture.
	var wire struct {
		Cases []struct {
			Label string `json:"label"`
			URL   string `json:"prepared_url"`
			Error string `json:"error"`
		}
	}
	if err = Decode(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Cases) != 24 {
		t.Fatal("requote reference count")
	}
	for _, c := range wire.Cases {
		if c.Error != "" {
			t.Fatal("unhandled original URL error", c)
		}
		got := sourceRequoteURI("https://movie.douban.com/j/subject_suggest?q=" + c.Label)
		if got != c.URL {
			t.Errorf("label %q got %q want %q", c.Label, got, c.URL)
		}
	}
}
func TestTjuptPublicGrantNoCredentialsExactRefererAndStrictTLS(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "https://movie.douban.com" {
			t.Error("uncredentialed image source headers differ")
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0xff, 0xfe, 0xfd})
	}))
	defer server.Close()
	public, err := BindTjuptPublicHTTPWithBinding(TjuptPublicBinding{ImageOrigins: []string{"https://img.doubanio.com", server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://evil.invalid/a.png", "https://user@img.doubanio.com/a.png", "http://img.doubanio.com/a.png", "https://img.doubanio.com:444/a.png"} {
		if _, err := public.Image(context.Background(), address); !errors.Is(err, ErrSiteTargetDenied) {
			t.Fatal("unapproved public URL accepted", address, err)
		}
	}
	bound, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL})
	response, err := public.Image(context.Background(), server.URL+"/image.png")
	if err != nil || response != nil || calls.Load() != 0 {
		t.Fatal("public images bypass strict TLS", response, err, calls.Load())
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	public, err = BindTjuptPublicHTTPWithBinding(TjuptPublicBinding{ImageOrigins: []string{server.URL}, RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	response, err = public.Image(context.Background(), server.URL+"/image.png")
	if err != nil || response == nil || string(response.Content) != string([]byte{0xff, 0xfe, 0xfd}) || calls.Load() != 1 {
		t.Fatal("owned public binary fixture failed", response, err, calls.Load())
	}
	// The ordinary same-origin client has no cross-origin Referer authority.
	_, err = bound.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL + "/image.png", NoCookies: true, Referer: "https://movie.douban.com"})
	if !errors.Is(err, ErrSiteTargetDenied) || calls.Load() != 1 {
		t.Fatal("public exception leaked into Generic", err)
	}
	for _, origin := range []string{"http://img.doubanio.com", "https://user@img.doubanio.com", "https://img.doubanio.com:443", "https://img.doubanio.com:65536", "https://img.doubanio.com:0", "https://img.doubanio.com/path", "https://img.doubanio.com/?x=1"} {
		if _, err := BindTjuptPublicHTTP([]string{origin}); !errors.Is(err, ErrSiteTargetDenied) {
			t.Fatal("invalid host origin grant", origin, err)
		}
	}
}
