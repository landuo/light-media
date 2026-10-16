package app

import (
	"net/http"
	"strings"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(n int) {
	if w.status != 0 {
		return
	}
	w.status = n
	w.ResponseWriter.WriteHeader(n)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (a *App) requestLogged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if w.Header().Get("X-Request-ID") == "" {
			w.Header().Set("X-Request-ID", id("req_"))
		}
		r = r.WithContext(a.traceContext(r.Context(), "", "", w.Header().Get("X-Request-ID")))
		start := time.Now()
		capture := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(capture, r)
		status := capture.status
		if status == 0 {
			status = 200
		}
		module := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")[0]
		allowed := map[string]bool{"auth": true, "status": true, "settings": true, "catalog": true, "search": true, "downloads": true, "subscriptions": true, "calendar": true, "schedule": true, "import": true, "assets": true, "jobs": true, "logs": true, "fnos": true, "backups": true, "restore": true, "control": true, "notifications": true, "audit": true}
		allowed["frontend-errors"] = true
		for _, name := range []string{"files", "history", "sites", "downloaders", "plugins"} {
			allowed[name] = true
		}
		if !allowed[module] {
			module = "unknown"
		}
		level := "DEBUG"
		if r.Method != "GET" {
			level = "INFO"
		}
		if status >= 400 {
			level = "WARN"
		}
		if r.Method != "GET" && status < 400 && module != "auth" && module != "logs" && module != "frontend-errors" {
			a.auditRequest(r.Context(), r.Method+"."+module, capture.Header().Get("X-Request-ID"), status)
		}
		a.writeLog(map[string]any{"id": id("log_"), "time": now(), "level": level, "module": "http." + module, "event": "request_completed", "message": "API 请求处理完成", "request_id": capture.Header().Get("X-Request-ID"), "method": r.Method, "http_status": status, "duration_ms": time.Since(start).Milliseconds(), "app_version": appVersion})
	})
}
