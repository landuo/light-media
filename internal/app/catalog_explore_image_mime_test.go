package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
)

const catalogExploreMIMEOrigin = "https://img3.doubanio.com/view/mime-fixture.jpg"

func catalogExploreMIMEBytes(t *testing.T, contentType string) []byte {
	t.Helper()
	if contentType != "image/webp" {
		return catalogImageMIMEBytes(t, contentType)
	}
	// Complete synthetic 1x1 WebP, not only a MIME signature.
	data, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func catalogExploreMIMEFixture(t *testing.T, declaredType string, data []byte) (*App, *atomic.Int64) {
	t.Helper()
	withoutProxyEnv(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Dir: dir}
	calls := new(atomic.Int64)
	// Exercise actual HTTP response/body transport, but only through the exact
	// owned loopback listener. No application workers or external services run.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/view/mime-fixture.jpg" || r.URL.RawQuery != "" || r.Header.Get("Referer") != "https://movie.douban.com/" || r.Header.Get("Accept") != "image/jpeg,image/png,image/webp" || r.Header.Get("User-Agent") != "light-media/1.0" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("owned image server received an unexpected request or browser credentials")
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", declaredType)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	owned := ownedLoopbackFixtureTransport(t, server)
	prior := assetImageClient
	t.Cleanup(func() { assetImageClient = prior })
	assetImageClient = &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: fixedFixtureTransport{
			t: t, host: "img3.doubanio.com", paths: []string{"/view/mime-fixture.jpg"},
			respond: func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != catalogExploreMIMEOrigin || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "https://movie.douban.com/" {
					t.Error("unexpected source URL or headers blocked before owned transport")
					return nil, errors.New("invalid synthetic explore image request")
				}
				return owned.RoundTrip(r)
			},
		},
	}
	return a, calls
}

func catalogExploreMIMERequest(method, source, imageURL string) *http.Request {
	query := url.Values{"source": {source}, "url": {imageURL}}
	r := httptest.NewRequest(method, "/api/catalog/explore/image?"+query.Encode(), nil)
	// These synthetic incoming credentials must not reach the provider request.
	r.Header.Set("Cookie", "fixture_session=not-a-real-session")
	r.Header.Set("Authorization", "Bearer synthetic-browser-only")
	return r
}

func catalogExploreMIMECachePath(a *App) string {
	sum := sha256.Sum256([]byte("douban:" + catalogExploreMIMEOrigin))
	return filepath.Join(a.Dir, "catalog-explore-images", hex.EncodeToString(sum[:])+".img")
}

func TestCatalogExploreImageMIMEUsesBytesForMissHitHeadAndRange(t *testing.T) {
	for _, contentType := range []string{"image/jpeg", "image/png", "image/webp"} {
		for _, kind := range []string{"GET", "HEAD", "Range"} {
			t.Run(contentType+"/"+kind, func(t *testing.T) {
				data := catalogExploreMIMEBytes(t, contentType)
				// Neither an upstream image declaration nor the .jpg source URL
				// nor the .img cache suffix may override the actual image bytes.
				declaredType := "image/jpeg"
				if contentType == declaredType {
					declaredType = "image/png"
				}
				a, calls := catalogExploreMIMEFixture(t, declaredType, data)
				for pass := 0; pass < 2; pass++ {
					method, status, body := http.MethodGet, http.StatusOK, data
					if kind == "HEAD" {
						method, body = http.MethodHead, nil
					} else if kind == "Range" {
						status, body = http.StatusPartialContent, data[8:20]
					}
					r := catalogExploreMIMERequest(method, "douban", catalogExploreMIMEOrigin)
					if kind == "Range" {
						r.Header.Set("Range", "bytes=8-19")
					}
					w := httptest.NewRecorder()
					if err := a.catalogExploreImage(w, r); err != nil {
						t.Fatal(err)
					}
					if w.Code != status || w.Header().Get("Content-Type") != contentType || !bytes.Equal(w.Body.Bytes(), body) {
						t.Fatalf("pass %d: status=%d MIME=%q body bytes=%d", pass, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
					}
					length := len(data)
					if kind == "Range" {
						length = 12
						if w.Header().Get("Content-Range") != "bytes 8-19/"+strconv.Itoa(len(data)) {
							t.Fatal("range boundaries changed")
						}
					} else if w.Header().Get("Content-Range") != "" {
						t.Fatal("non-range response acquired a Content-Range")
					}
					if w.Header().Get("Content-Length") != strconv.Itoa(length) || w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, max-age=86400" {
						t.Fatal("image response headers changed")
					}
					if calls.Load() != 1 {
						t.Fatalf("pass %d: expected one owned upstream request, got %d", pass, calls.Load())
					}
				}
				cached, err := os.ReadFile(catalogExploreMIMECachePath(a))
				if err != nil || !bytes.Equal(cached, data) {
					t.Fatal("cache lost complete source image bytes", err)
				}
			})
		}
	}
}

func TestCatalogExploreImageMIMEPreservesMultipartRanges(t *testing.T) {
	data := catalogExploreMIMEBytes(t, "image/png")
	a, calls := catalogExploreMIMEFixture(t, "image/jpeg", data)
	for pass := 0; pass < 2; pass++ {
		r := catalogExploreMIMERequest(http.MethodGet, "douban", catalogExploreMIMEOrigin)
		r.Header.Set("Range", "bytes=0-7,12-19")
		w := httptest.NewRecorder()
		if err := a.catalogExploreImage(w, r); err != nil {
			t.Fatal(err)
		}
		contentType, parameters, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
		if err != nil || contentType != "multipart/byteranges" || w.Code != http.StatusPartialContent {
			t.Fatal("multipart range response changed", err)
		}
		parts := multipart.NewReader(w.Body, parameters["boundary"])
		for _, span := range [][2]int{{0, 8}, {12, 20}} {
			part, err := parts.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(part)
			wantRange := "bytes " + strconv.Itoa(span[0]) + "-" + strconv.Itoa(span[1]-1) + "/" + strconv.Itoa(len(data))
			if err != nil || part.Header.Get("Content-Type") != "image/png" || part.Header.Get("Content-Range") != wantRange || !bytes.Equal(body, data[span[0]:span[1]]) {
				t.Fatal("multipart range lost actual image MIME or byte offsets", err)
			}
		}
		if _, err = parts.NextPart(); err != io.EOF {
			t.Fatal("unexpected extra range part", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("multipart cache hit requested upstream again")
	}
}

func TestCatalogExploreImageMIMERejectsNonImageDownloads(t *testing.T) {
	for _, tc := range []struct {
		name, declaredType, body string
	}{
		{"html", "image/jpeg", "<!doctype html><html>not an image</html>"},
		{"json", "image/png", `{"error":"synthetic upstream failure"}`},
		{"svg", "image/webp", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`},
		{"unsupported_gif", "image/jpeg", "GIF89a\x01\x00\x01\x00\x00\x00\x00;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, calls := catalogExploreMIMEFixture(t, tc.declaredType, []byte(tc.body))
			w := httptest.NewRecorder()
			r := catalogExploreMIMERequest(http.MethodGet, "douban", catalogExploreMIMEOrigin)
			if err := a.catalogExploreImage(w, r); err == nil {
				t.Fatal("non-image bytes accepted based on upstream declaration")
			}
			if calls.Load() != 1 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
				t.Fatal("rejected image wrote a response or made unexpected requests")
			}
			entries, err := os.ReadDir(filepath.Join(a.Dir, "catalog-explore-images"))
			if (err != nil && !os.IsNotExist(err)) || len(entries) != 0 {
				t.Fatal("non-image download or temporary file was retained", err)
			}
		})
	}
}

func TestCatalogExploreImageMIMERevalidatesExistingCache(t *testing.T) {
	for _, validUpstream := range []bool{false, true} {
		t.Run(strconv.FormatBool(validUpstream), func(t *testing.T) {
			data := []byte("<!doctype html><html>not an image</html>")
			if validUpstream {
				data = catalogExploreMIMEBytes(t, "image/png")
			}
			a, calls := catalogExploreMIMEFixture(t, "image/jpeg", data)
			cache := catalogExploreMIMECachePath(a)
			if err := os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cache, []byte("not a cached image"), 0600); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			err := a.catalogExploreImage(w, catalogExploreMIMERequest(http.MethodGet, "douban", catalogExploreMIMEOrigin))
			if validUpstream {
				if err != nil || w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), data) {
					t.Fatal("invalid cache was not replaced with actual upstream image", err)
				}
				cached, readErr := os.ReadFile(cache)
				if readErr != nil || !bytes.Equal(cached, data) {
					t.Fatal("replacement image was not persisted", readErr)
				}
			} else if err == nil || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
				t.Fatal("invalid cached or upstream bytes were served as an image")
			}
			if calls.Load() != 1 {
				t.Fatal("existing non-image cache bypassed revalidation")
			}
		})
	}
}

func TestCatalogExploreImageMIMEKeepsSourceURLAllowlist(t *testing.T) {
	data := catalogExploreMIMEBytes(t, "image/jpeg")
	a, calls := catalogExploreMIMEFixture(t, "image/jpeg", data)
	if err := a.catalogExploreImage(httptest.NewRecorder(), catalogExploreMIMERequest(http.MethodGet, "douban", catalogExploreMIMEOrigin)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, imageURL string }{
		{"douban", "https://127.0.0.1/view/mime-fixture.jpg"},
		{"douban", "https://img3.doubanio.com.attacker.invalid/view/mime-fixture.jpg"},
		{"douban", "http://img3.doubanio.com/view/mime-fixture.jpg"},
		{"douban", "https://img3.doubanio.com:443/view/mime-fixture.jpg"},
		{"douban", "https://user@img3.doubanio.com/view/mime-fixture.jpg"},
		{"douban", catalogExploreMIMEOrigin + "#fragment"},
		{"douban", "https://lain.bgm.tv/view/mime-fixture.jpg"},
		{"bangumi", catalogExploreMIMEOrigin},
		{"anilist", catalogExploreMIMEOrigin},
		{"unknown", catalogExploreMIMEOrigin},
	} {
		w := httptest.NewRecorder()
		if err := a.catalogExploreImage(w, catalogExploreMIMERequest(http.MethodGet, tc.source, tc.imageURL)); err == nil {
			t.Fatal("source URL allowlist was bypassed")
		}
		if w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" || calls.Load() != 1 {
			t.Fatal("rejected source URL served cached bytes or reached owned transport")
		}
	}
}
