package app

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// StartupHandler exposes the management shell while ordered downloader/plugin
// initialization is running. No business endpoint can observe or mutate a
// partially initialized plugin registry. Start remains synchronous for callers
// that already rely on its original lifecycle; the server owns joining it.
func (a *App) StartupHandler(started <-chan struct{}) http.Handler {
	a.startupPending.Store(true)
	return mountEntrypoints(a.requestLogged(a.startupHTTP(nativeDisplayDeadline(a.handler(), 30*time.Second), started)))
}

func (a *App) startupHTTP(next http.Handler, started <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		select {
		case <-started:
			a.startupPending.Store(false)
			next.ServeHTTP(w, r)
			return
		default:
		}
		path := r.URL.Path
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			(path == "/" || path == "/index.html" || path == "/version.txt" || strings.HasPrefix(path, "/assets/")) {
			// Serve the static shell directly, without consulting custom plugin
			// routes or anonymous plugin frontend getters during initialization.
			if a.web != nil {
				a.web.ServeHTTP(w, r)
				return
			}
		}
		if startupManagementRequest(r) {
			next.ServeHTTP(w, r) // Existing authentication and origin checks apply.
			return
		}
		w.Header().Set("Retry-After", "3")
		if path == "/healthz" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, r, map[string]any{"status": "starting", "version": appVersion})
			return
		}
		a.writeError(w, r, &APIError{"SERVICE_STARTING", "服务正在初始化下载器和插件，可先查看日志；初始化完成后页面会自动恢复", http.StatusServiceUnavailable})
	})
}

func startupManagementRequest(r *http.Request) bool {
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/api/auth/setup", "/api/auth/login", "/api/auth/logout":
			return true
		}
		return false
	}
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/api/auth/status", "/api/ready", "/api/status", "/api/settings", "/api/logs", "/api/logs/stream", "/api/logs/export", "/api/logging":
		return true
	}
	return false
}

// These native display requests must carry an end-to-end caller budget across
// plugin discovery, connection-pool waits and upstream reads. Do not apply it
// to mutations, plugin custom APIs, downloads, or SSE streams: their original
// side-effect and streaming lifetimes remain unchanged.
func nativeDisplayRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/catalog/") {
		return true
	}
	switch path {
	case "/api/auth/status", "/api/ready", "/api/settings", "/api/status", "/api/downloads", "/api/subscriptions", "/api/directories/manage", "/api/directories/options", "/api/directories/transfer-types", "/api/files/list", "/api/files/roots", "/api/history/transfers", "/api/history/transfers/queue", "/api/jobs":
		return true
	}
	return false
}

func nativeDisplayDeadline(next http.Handler, budget time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !nativeDisplayRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
