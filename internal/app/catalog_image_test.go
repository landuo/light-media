package app

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type imageTransport func(*http.Request) (*http.Response, error)

func (f imageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCatalogImageUsesOfficialOriginAndCachesBoundedImage(t *testing.T) {
	a, _, _ := importTestApp(t)
	var data bytes.Buffer
	if e := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); e != nil {
		t.Fatal(e)
	}
	old := assetImageClient
	t.Cleanup(func() { assetImageClient = old })
	calls := 0
	assetImageClient = &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "image.tmdb.org" || r.URL.Path != "/t/p/w342/fixture.jpg" || r.URL.RawQuery != "" {
			t.Fatalf("unexpected image origin %s", r.URL.Redacted())
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(bytes.NewReader(data.Bytes()))}, nil
	})}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/catalog/image?path=%2Ffixture.jpg&size=w342", nil)
		if e := a.catalogImage(w, r); e != nil {
			t.Fatal(e)
		}
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data.Bytes()) || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/jpeg") {
			t.Fatal("image response not preserved")
		}
	}
	if calls != 1 {
		t.Fatalf("cache missed: %d upstream calls", calls)
	}
	entries, e := os.ReadDir(filepath.Join(a.Dir, "catalog-images"))
	if e != nil || len(entries) != 1 {
		t.Fatal("temporary image leaked")
	}
	for _, path := range []string{"https://unrelated.invalid/x.jpg", "//unrelated.invalid/x.jpg", "/../x.jpg", "/x.svg"} {
		if e := a.catalogImage(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/catalog/image?path="+url.QueryEscape(path), nil)); e == nil {
			t.Fatal("unsafe image accepted")
		}
	}
	if e := a.catalogImage(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/catalog/image?path=%2Ffixture.jpg&size=original", nil)); e == nil {
		t.Fatal("unbounded original accepted")
	}
	if calls != 1 {
		t.Fatal("invalid request made upstream call")
	}
}

func TestCatalogImageRefusesOversizedUpstreamWithoutCache(t *testing.T) {
	a, _, _ := importTestApp(t)
	old := assetImageClient
	t.Cleanup(func() { assetImageClient = old })
	assetImageClient = &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: 9 << 20, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(strings.NewReader("not-consumed"))}, nil
	})}
	if e := a.catalogImage(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/catalog/image?path=%2Foversize.jpg", nil)); e == nil {
		t.Fatal("oversized image accepted")
	}
	entries, e := os.ReadDir(filepath.Join(a.Dir, "catalog-images"))
	if e != nil || len(entries) != 0 {
		t.Fatal("invalid image cached")
	}
}
