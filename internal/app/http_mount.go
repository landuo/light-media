package app

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// This fixed mount is the HTTPS reverse-proxy entry point. Proxies preserve the
// path and original Host (including the public port). Forwarded headers are not
// trusted: the LAN root keeps its own cookie and the HTTPS mount always issues
// a separate Secure cookie, even when TLS terminates upstream.
const httpsMountPath = "/light-media"

type httpsMountContextKey struct{}

func mountEntrypoints(next http.Handler) http.Handler {
	prefixed := http.StripPrefix(httpsMountPath, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == httpsMountPath || r.URL.Path == httpsMountPath+"/api" {
			// ServeMux would otherwise redirect the stripped /api to /api/.
			target := r.URL.Path + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
			return
		}
		if strings.HasPrefix(r.URL.Path, httpsMountPath+"/") {
			// Do not let ServeMux's canonical-path redirect leave this mount.
			clean := path.Clean(r.URL.Path)
			if clean != strings.TrimSuffix(r.URL.Path, "/") {
				http.Error(w, "invalid path", http.StatusBadRequest)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), httpsMountContextKey{}, true))
			prefixed.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func onHTTPSMount(r *http.Request) bool {
	value, _ := r.Context().Value(httpsMountContextKey{}).(bool)
	return value
}

func sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	cookie := &http.Cookie{Name: "lm_session", Value: value, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
	if onHTTPSMount(r) {
		cookie.Name = "__Secure-lm_session"
		cookie.Path = httpsMountPath + "/"
		cookie.Secure = true
	}
	return cookie
}

func sameRequestOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Non-browser clients still authenticate with the session cookie.
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return false
	}
	scheme := "http"
	if onHTTPSMount(r) || r.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)
}
