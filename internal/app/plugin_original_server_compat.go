package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Only the source statistics/rating routes belong to this adapter. The native
// /api/plugins extension keeps its data envelope. Root PUT/DELETE are source
// generic configuration/uninstall routes, not unsupported rating methods.
func (a *App) serveOriginalPluginServerAPI(w http.ResponseWriter, r *http.Request) bool {
	version, path := "", ""
	for _, v := range []string{"v1", "v2"} {
		prefix := "/api/" + v + "/plugin/"
		if strings.HasPrefix(r.URL.Path, prefix) {
			version, path = v, strings.TrimPrefix(r.URL.Path, prefix)
			break
		}
	}
	if version == "" {
		return false
	}
	canonical := strings.TrimRight(path, "/")
	root := canonical == "statistic" || canonical == "rating"
	item := strings.HasPrefix(canonical, "rating/") && len(strings.Split(canonical, "/")) == 2 && strings.TrimPrefix(canonical, "rating/") != ""
	if !root && !item {
		return false
	}
	if root && (r.Method == http.MethodPut || r.Method == http.MethodDelete) {
		return false
	}
	write := func(status int, body any) { originalPluginServerCompatJSON(w, r, version, status, body) }
	// The migration's administrator/session and same-origin boundaries remain:
	// an API token or a plugin ID never grants management authority.
	if !a.authenticated(r) {
		write(401, map[string]any{"detail": "Not authenticated"})
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && (!sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
		write(403, map[string]any{"detail": "Origin rejected"})
		return true
	}
	if strings.ContainsAny(r.URL.Path, "\\\x00") || strings.Contains(strings.ToLower(r.URL.RawPath), "%2f") || strings.Contains(strings.ToLower(r.URL.RawPath), "%5c") {
		write(400, map[string]any{"detail": "Invalid path"})
		return true
	}
	if path != canonical {
		location := *r.URL
		location.Path = strings.TrimRight(location.Path, "/")
		location.RawPath = ""
		if location.Scheme == "" {
			location.Scheme = "http"
			if r.TLS != nil {
				location.Scheme = "https"
			}
		}
		if location.Host == "" {
			location.Host = r.Host
		}
		w.Header().Set("Location", location.String())
		w.WriteHeader(http.StatusTemporaryRedirect)
		return true
	}
	if r.Method != http.MethodGet && !(item && r.Method == http.MethodPost) {
		// The first partial APIRoute match is GET; FastAPI does not union the
		// separate GET and POST route declarations' Allow headers.
		w.Header().Set("Allow", "GET")
		write(405, map[string]any{"detail": "Method Not Allowed"})
		return true
	}
	value, err := a.originalPluginServerHTTP(w, r, path)
	if err != nil {
		var source *originalPluginServerHTTPError
		if errors.As(err, &source) {
			if source.text {
				if r.Method == http.MethodHead {
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(source.status)
				} else {
					writeOriginalPluginServerError(w, err)
				}
			} else {
				write(source.status, source.body)
			}
			return true
		}
		var api *APIError
		if errors.As(err, &api) && api.Status < 500 {
			write(api.Status, map[string]any{"detail": api.Message})
			return true
		}
		writeOriginalPluginServerError(w, originalPluginServerFailure())
		return true
	}
	write(200, value)
	return true
}

func originalPluginServerCompatJSON(w http.ResponseWriter, r *http.Request, version string, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		writeOriginalPluginServerError(w, originalPluginServerFailure())
		return
	}
	if version == "v2" {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		_, success := fields["success"]
		_, message := fields["message"]
		_, data := fields["data"]
		if !(success && message && data) {
			if status < 400 {
				body = map[string]any{"success": true, "message": "", "data": json.RawMessage(raw)}
			} else {
				message := "请求失败"
				var text string
				if json.Unmarshal(fields["detail"], &text) == nil && text != "" {
					message = text
				} else {
					var details []struct {
						Message string `json:"msg"`
					}
					if json.Unmarshal(fields["detail"], &details) == nil {
						messages := []string{}
						for _, detail := range details {
							if detail.Message != "" {
								messages = append(messages, detail.Message)
							}
						}
						if len(messages) > 0 {
							message = strings.Join(messages, "; ")
						}
					}
				}
				body = map[string]any{"success": false, "message": message, "data": map[string]any{}}
			}
			raw, err = json.Marshal(body)
			if err != nil {
				writeOriginalPluginServerError(w, originalPluginServerFailure())
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(raw)
	}
}
