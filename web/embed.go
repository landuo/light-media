package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed all:dist
var files embed.FS

// Handler serves the compiled UI. Authentication remains enforced by the API.
func Handler() http.Handler {
	content, err := fs.Sub(files, "dist")
	if err != nil {
		panic("web dist missing")
	}
	return staticHandler(content)
}

// Explicit encoding quality overrides a wildcard, including gzip;q=0.
func acceptsGzip(header string) bool {
	wildcard := false
	for _, entry := range strings.Split(header, ",") {
		parts := strings.Split(entry, ";")
		encoding := strings.TrimSpace(strings.ToLower(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if encoding == "gzip" {
			return quality > 0
		}
		if encoding == "*" {
			wildcard = quality > 0
		}
	}
	return wildcard
}

func staticHandler(content fs.FS) http.Handler {
	server := http.FileServer(http.FS(content))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Add("Vary", "Accept-Encoding")
		// Ranges use the identity representation, so byte offsets remain meaningful.
		// FileServer retains HEAD, redirects, conditional requests and path handling.
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.Header.Get("Range") == "" && acceptsGzip(r.Header.Get("Accept-Encoding")) {
			name := path.Clean("/" + r.URL.Path)
			if name == "/" {
				name = "/index.html"
			}
			if !strings.HasSuffix(r.URL.Path, "/index.html") {
				if info, err := fs.Stat(content, strings.TrimPrefix(name, "/")+".gz"); err == nil && !info.IsDir() {
					cloned := r.Clone(r.Context())
					cloned.URL.Path = name + ".gz"
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
					if kind := mime.TypeByExtension(path.Ext(name)); kind != "" {
						w.Header().Set("Content-Type", kind)
					}
					server.ServeHTTP(w, cloned)
					return
				}
			}
		}
		server.ServeHTTP(w, r)
	})
}
