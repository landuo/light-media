package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func catalogImageMIMEBytes(t *testing.T, contentType string) []byte {
	t.Helper()
	pixels := image.NewRGBA(image.Rect(0, 0, 3, 2))
	pixels.Set(1, 1, color.RGBA{R: 211, G: 73, B: 29, A: 255})
	var data bytes.Buffer
	var err error
	switch contentType {
	case "image/jpeg":
		err = jpeg.Encode(&data, pixels, nil)
	case "image/png":
		err = png.Encode(&data, pixels)
	default:
		t.Fatal("unsupported synthetic image format")
	}
	if err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func catalogImageMIMEFixture(t *testing.T, contentType string, data []byte) (*App, *int) {
	t.Helper()
	withoutProxyEnv(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// No full application, credentials, background workers or network transport.
	a := &App{Dir: dir}
	calls := 0
	old := assetImageClient
	t.Cleanup(func() { assetImageClient = old })
	assetImageClient = &http.Client{Transport: fixedFixtureTransport{
		t: t, host: "image.tmdb.org", paths: []string{"/t/p/w342/mime-fixture.jpg"},
		respond: func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusOK, ContentLength: int64(len(data)),
				Header: http.Header{"Content-Type": []string{contentType}},
				Body:   io.NopCloser(bytes.NewReader(data)),
			}, nil
		},
	}}
	return a, &calls
}

func catalogImageMIMECachePath(a *App) string {
	sum := sha256.Sum256([]byte("w342/mime-fixture.jpg"))
	return filepath.Join(a.Dir, "catalog-images", hex.EncodeToString(sum[:])+".img")
}

func TestCatalogImageMIMEUsesBytesForMissHitHeadAndRange(t *testing.T) {
	for _, contentType := range []string{"image/jpeg", "image/png"} {
		for _, kind := range []string{"GET", "HEAD", "Range"} {
			t.Run(contentType+"/"+kind, func(t *testing.T) {
				data := catalogImageMIMEBytes(t, contentType)
				// Deliberately disagree with the bytes. Neither this upstream
				// declaration nor the requested .jpg suffix determines the result.
				declaredType := "image/png"
				if contentType == "image/png" {
					declaredType = "image/jpeg"
				}
				a, calls := catalogImageMIMEFixture(t, declaredType, data)
				for pass := 0; pass < 2; pass++ {
					method, status, body := http.MethodGet, http.StatusOK, data
					if kind == "HEAD" {
						method, body = http.MethodHead, nil
					} else if kind == "Range" {
						status, body = http.StatusPartialContent, data[8:20]
					}
					r := httptest.NewRequest(method, "/api/catalog/image?path=%2Fmime-fixture.jpg", nil)
					if kind == "Range" {
						r.Header.Set("Range", "bytes=8-19")
					}
					w := httptest.NewRecorder()
					if err := a.catalogImage(w, r); err != nil {
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
					}
					if w.Header().Get("Content-Length") != strconv.Itoa(length) || w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, max-age=86400" {
						t.Fatal("image response headers changed")
					}
				}
				if *calls != 1 {
					t.Fatalf("cache hit requested upstream again: %d calls", *calls)
				}
				cached, err := os.ReadFile(catalogImageMIMECachePath(a))
				if err != nil || !bytes.Equal(cached, data) {
					t.Fatal("cache did not retain the complete actual image bytes", err)
				}
			})
		}
	}
}

func TestCatalogImageMIMEPreservesMultipartRanges(t *testing.T) {
	data := catalogImageMIMEBytes(t, "image/png")
	a, calls := catalogImageMIMEFixture(t, "image/jpeg", data)
	for pass := 0; pass < 2; pass++ {
		r := httptest.NewRequest(http.MethodGet, "/api/catalog/image?path=%2Fmime-fixture.jpg", nil)
		r.Header.Set("Range", "bytes=0-7,12-19")
		w := httptest.NewRecorder()
		if err := a.catalogImage(w, r); err != nil {
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
			if err != nil || part.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, data[span[0]:span[1]]) {
				t.Fatal("multipart range lost the actual image MIME or byte offsets", err)
			}
		}
		if _, err = parts.NextPart(); err != io.EOF {
			t.Fatal("unexpected extra range part", err)
		}
	}
	if *calls != 1 {
		t.Fatal("multipart cache hit requested upstream again")
	}
}

func TestCatalogImageMIMERejectsNonImageDownloads(t *testing.T) {
	for _, tc := range []struct {
		name, declaredType, body string
	}{
		{"html", "image/jpeg", "<!doctype html><html>not an image</html>"},
		{"json", "image/png", `{"error":"synthetic upstream failure"}`},
		{"svg", "image/webp", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`},
		{"unsupported_gif", "image/jpeg", "GIF89a\x01\x00\x01\x00\x00\x00\x00;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, calls := catalogImageMIMEFixture(t, tc.declaredType, []byte(tc.body))
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/catalog/image?path=%2Fmime-fixture.jpg", nil)
			if err := a.catalogImage(w, r); err == nil {
				t.Fatal("non-image bytes accepted based on the upstream declaration")
			}
			if *calls != 1 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
				t.Fatal("rejected image wrote a response or made unexpected requests")
			}
			entries, err := os.ReadDir(filepath.Join(a.Dir, "catalog-images"))
			if err != nil || len(entries) != 0 {
				t.Fatal("non-image download or its temporary file was retained", err)
			}
		})
	}
}

func TestCatalogImageMIMERevalidatesExistingCache(t *testing.T) {
	for _, validUpstream := range []bool{false, true} {
		t.Run(strconv.FormatBool(validUpstream), func(t *testing.T) {
			data := []byte("<!doctype html><html>not an image</html>")
			if validUpstream {
				data = catalogImageMIMEBytes(t, "image/png")
			}
			a, calls := catalogImageMIMEFixture(t, "image/jpeg", data)
			cache := catalogImageMIMECachePath(a)
			if err := os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cache, []byte("not a cached image"), 0600); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/catalog/image?path=%2Fmime-fixture.jpg", nil)
			err := a.catalogImage(w, r)
			if validUpstream {
				if err != nil || w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), data) {
					t.Fatal("invalid cache was not replaced by the actual upstream image", err)
				}
			} else if err == nil || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
				t.Fatal("invalid cached or upstream bytes were served as an image")
			}
			if *calls != 1 {
				t.Fatal("existing non-image cache bypassed revalidation")
			}
		})
	}
}
