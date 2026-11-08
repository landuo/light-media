package artifacts

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const goModuleProxyHost = "proxy.golang.org"
const goChecksumHost = "sum.golang.org"
const goChecksumRoute = "/sumdb/" + goChecksumHost + "/"

var goModuleProxyPath = regexp.MustCompile(`^/[a-zA-Z0-9._~/!@+-]+$`)

// The Go subprocess receives a build-scoped loopback address. The App's trusted
// client handles upstream routing and credentials. Go still verifies module
// checksums and signed sumdb responses with its built-in public key.
func startSourceProxy(ctx context.Context, upstream *http.Client) (string, func(), error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	token := "/" + rand.Text()
	client := *upstream
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Fragment != "" ||
			req.URL.Host != via[0].URL.Host && (via[0].URL.Host != goModuleProxyHost ||
				req.URL.Host != "storage.googleapis.com" || !strings.HasSuffix(via[0].URL.Path, ".zip")) {
			return errors.New("module proxy redirect refused")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	var requests atomic.Uint64
	var remaining atomic.Int64
	remaining.Store(256 << 20)
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       10 * time.Second,
		MaxHeaderBytes:    8 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, authorized := strings.CutPrefix(r.URL.Path, token)
			limit, valid := sourceProxyLimit(route)
			if r.Method != http.MethodGet || r.URL.IsAbs() || r.URL.RawQuery != "" || !authorized || !valid {
				http.NotFound(w, r)
				return
			}
			if requests.Add(1) > 512 {
				http.Error(w, "module proxy request limit exceeded", http.StatusTooManyRequests)
				return
			}
			host := goModuleProxyHost
			if checksumPath, isChecksum := strings.CutPrefix(route, goChecksumRoute); isChecksum {
				if checksumPath == "supported" {
					w.WriteHeader(http.StatusOK)
					return
				}
				host, route = goChecksumHost, "/"+checksumPath
			}
			requestCtx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			target := &url.URL{Scheme: "https", Host: host, Path: route}
			request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target.String(), nil)
			if err != nil {
				http.Error(w, "invalid module proxy request", http.StatusBadRequest)
				return
			}
			request.Header.Set("Accept-Encoding", "identity")
			response, err := client.Do(request)
			if err != nil {
				http.Error(w, "module proxy unavailable", http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
					http.NotFound(w, r)
				} else {
					http.Error(w, "module proxy unavailable", http.StatusBadGateway)
				}
				return
			}
			if response.ContentLength > limit || response.Header.Get("Content-Encoding") != "" {
				http.Error(w, "unsupported module proxy response", http.StatusBadGateway)
				return
			}
			budget := limit
			if response.ContentLength >= 0 {
				budget = response.ContentLength
				w.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
			}
			if remaining.Add(-budget) < 0 {
				remaining.Add(budget)
				w.Header().Del("Content-Length")
				http.Error(w, "module proxy download limit exceeded", http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
			n, err := io.CopyN(w, response.Body, budget+1)
			remaining.Add(budget - min(budget, n))
			if n > budget || err != nil && err != io.EOF {
				panic(http.ErrAbortHandler)
			}
		}),
	}
	go server.Serve(listener)
	return "http://" + listener.Addr().String() + token, func() { _ = server.Close() }, nil
}

func sourceProxyLimit(route string) (int64, bool) {
	if len(route) > 2048 || path.Clean(route) != route || !goModuleProxyPath.MatchString(route) {
		return 0, false
	}
	if tail, ok := strings.CutPrefix(route, goChecksumRoute); ok {
		return MaxIndexBytes, tail == "supported" || tail == "latest" ||
			strings.HasPrefix(tail, "lookup/") || strings.HasPrefix(tail, "tile/")
	}
	if strings.HasPrefix(route, "/sumdb/") {
		return 0, false
	}
	if strings.HasSuffix(route, "/@latest") && route != "/@latest" {
		return MaxIndexBytes, true
	}
	mod, version, ok := strings.Cut(route, "/@v/")
	if !ok || mod == "" || strings.Contains(version, "/") {
		return 0, false
	}
	if strings.HasSuffix(version, ".zip") {
		return MaxArtifactBytes, true
	}
	return MaxIndexBytes, version == "list" || strings.HasSuffix(version, ".info") || strings.HasSuffix(version, ".mod")
}
