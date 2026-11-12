package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticCompression(t *testing.T) {
	original := []byte("const message = 'hello, compressed world';")
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write(original)
	_ = gz.Close()
	content := fstest.MapFS{
		"index.html": {Data: original}, "index.html.gz": {Data: compressed.Bytes()},
		"assets/app.js": {Data: original}, "assets/app.js.gz": {Data: compressed.Bytes()},
	}
	handler := staticHandler(content)
	cases := []struct {
		name, method, path, encoding, byteRange string
		status                                  int
		encoded                                 bool
	}{
		{"gzip", "GET", "/assets/app.js", "br, gzip", "", 200, true},
		{"identity", "GET", "/assets/app.js", "", "", 200, false},
		{"disabled", "GET", "/assets/app.js", "*;q=1, gzip;q=0", "", 200, false},
		{"wildcard", "GET", "/assets/app.js", "*;q=.5", "", 200, true},
		{"invalid-quality", "GET", "/assets/app.js", "gzip;q=invalid", "", 200, false},
		{"head", "HEAD", "/assets/app.js", "gzip", "", 200, true},
		{"range", "GET", "/assets/app.js", "gzip", "bytes=0-4", 206, false},
		{"root", "GET", "/", "gzip", "", 200, true},
		{"index-redirect", "GET", "/index.html", "gzip", "", 301, false},
		{"missing", "GET", "/missing.js", "gzip", "", 404, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Accept-Encoding", tc.encoding)
			req.Header.Set("Range", tc.byteRange)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d", response.Code)
			}
			if got := response.Header().Get("Content-Encoding") == "gzip"; got != tc.encoded {
				t.Fatalf("encoded = %v", got)
			}
			if response.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatal("missing Vary")
			}
			if tc.method == "HEAD" {
				if response.Body.Len() != 0 || response.Header().Get("Content-Length") == "" {
					t.Fatal("invalid HEAD")
				}
				return
			}
			if tc.status == 200 {
				data := response.Body.Bytes()
				if tc.encoded {
					reader, err := gzip.NewReader(bytes.NewReader(data))
					if err != nil {
						t.Fatal(err)
					}
					data, err = io.ReadAll(reader)
					if err != nil {
						t.Fatal(err)
					}
					_ = reader.Close()
				}
				if !bytes.Equal(data, original) {
					t.Fatal("content differs")
				}
				if tc.path == "/assets/app.js" && !strings.Contains(response.Header().Get("Content-Type"), "javascript") {
					t.Fatal("wrong MIME")
				}
			}
			if tc.status == http.StatusPartialContent && response.Body.String() != string(original[:5]) {
				t.Fatal("invalid byte range")
			}
			expectedCache := "no-cache"
			if strings.HasPrefix(tc.path, "/assets/") {
				expectedCache = "public, max-age=31536000, immutable"
			}
			if tc.status < 400 && response.Header().Get("Cache-Control") != expectedCache {
				t.Fatal("wrong caching")
			}
		})
	}
}
