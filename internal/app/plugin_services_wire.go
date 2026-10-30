package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Only the fixed versioned service routes use MoviePilot's wire format. Native
// /plugins/services and the older unversioned host aliases keep their envelope.
type originalServiceWireRoute struct {
	version  string
	kind     string
	jobID    string
	token    bool
	redirect bool
}

func originalServiceWirePath(path string) (originalServiceWireRoute, bool) {
	route := originalServiceWireRoute{}
	if !strings.HasPrefix(path, "v1/") && !strings.HasPrefix(path, "v2/") {
		return route, false
	}
	route.version, path = path[:2], path[3:]
	route.redirect = strings.HasSuffix(path, "/")
	path = strings.TrimRight(path, "/")
	switch path {
	case "dashboard/schedule", "dashboard/schedule2":
		route.kind, route.token = "list", path == "dashboard/schedule2"
	case "system/runscheduler", "system/runscheduler2":
		route.kind, route.token = "run", path == "system/runscheduler2"
	default:
		candidate := path
		if strings.HasPrefix(candidate, "dashboard/schedule2/") {
			route.token = true
			candidate = "dashboard/schedule/" + strings.TrimPrefix(candidate, "dashboard/schedule2/")
		}
		id, ok := originalServiceProgressPath(candidate)
		if !ok {
			return originalServiceWireRoute{}, false
		}
		route.kind, route.jobID = "progress", id
	}
	return route, true
}

// Explicitly project the source ScheduleInfo schema; native identities and
// can_run are not part of this public compatibility response.
type originalServiceWireInfo struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	NameI18N         *string        `json:"name_i18n"`
	Provider         string         `json:"provider"`
	ProviderI18N     *string        `json:"provider_i18n"`
	Status           string         `json:"status"`
	StatusI18N       *string        `json:"status_i18n"`
	NextRun          *string        `json:"next_run"`
	NextRunI18N      *string        `json:"next_run_i18n"`
	Progress         float64        `json:"progress"`
	ProgressText     *string        `json:"progress_text"`
	ProgressTextI18N *string        `json:"progress_text_i18n"`
	ProgressEnable   bool           `json:"progress_enable"`
	ProgressDetail   map[string]any `json:"progress_detail"`
}

func writeOriginalServiceWireJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeOriginalServiceWireInternalError(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeOriginalServiceWireInternalError(w http.ResponseWriter) {
	// Starlette's unhandled response-model/scheduler error is plain text, not a
	// host APIError envelope. Never reflect or log the plugin error's contents.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte("Internal Server Error"))
}

func writeOriginalServiceWireMethod(w http.ResponseWriter, version string) {
	w.Header().Set("Allow", http.MethodGet)
	value := any(map[string]any{"detail": "Method Not Allowed"})
	if version == "v2" {
		value = map[string]any{"success": false, "message": "Method Not Allowed", "data": map[string]any{}}
	}
	writeOriginalServiceWireJSON(w, http.StatusMethodNotAllowed, value)
}

func writeOriginalServiceWireResponse(w http.ResponseWriter, success bool, message any, data any) {
	if data == nil {
		data = map[string]any{}
	}
	writeOriginalServiceWireJSON(w, http.StatusOK, map[string]any{
		"success": success, "message": message, "message_i18n": message, "data": data,
	})
}

func redirectOriginalServiceWire(w http.ResponseWriter, r *http.Request) {
	scheme, prefix := "http", ""
	if r.TLS != nil || onHTTPSMount(r) {
		scheme = "https"
	}
	if onHTTPSMount(r) {
		prefix = httpsMountPath
	}
	// Do not trust forwarded host/scheme, decode/re-encode query tokens, or use
	// http.Redirect (which adds an HTML body and Content-Type absent in source).
	target := url.URL{Scheme: scheme, Host: r.Host,
		Path:     prefix + strings.TrimRight(r.URL.Path, "/"),
		RawQuery: r.URL.RawQuery, ForceQuery: r.URL.ForceQuery}
	if r.URL.RawPath != "" {
		target.RawPath = prefix + strings.TrimRight(r.URL.RawPath, "/")
	}
	w.Header().Set("Location", target.String())
	w.Header().Del("Content-Type")
	w.WriteHeader(http.StatusTemporaryRedirect)
}

func (a *App) originalServiceWireHTTP(w http.ResponseWriter, r *http.Request, route originalServiceWireRoute) {
	if route.redirect {
		redirectOriginalServiceWire(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeOriginalServiceWireMethod(w, route.version)
		return
	}
	switch route.kind {
	case "list":
		items, err := a.originalPublicServicesResult(time.Now())
		if err != nil {
			writeOriginalServiceWireInternalError(w)
			return
		}
		rows := make([]originalServiceWireInfo, 0, len(items))
		for _, item := range items {
			rows = append(rows, originalServiceWireInfo{
				ID: item.ID, Name: item.Name, NameI18N: item.NameI18N,
				Provider: item.Provider, ProviderI18N: item.ProviderI18N,
				Status: item.Status, StatusI18N: item.StatusI18N,
				NextRun: item.NextRun, NextRunI18N: item.NextRunI18N,
				Progress: item.Progress, ProgressText: item.ProgressText,
				ProgressTextI18N: item.ProgressTextI18N, ProgressEnable: item.ProgressEnable,
				ProgressDetail: item.ProgressDetail,
			})
		}
		var value any = rows
		if route.version == "v2" {
			value = map[string]any{"success": true, "message": "", "data": rows}
		}
		writeOriginalServiceWireJSON(w, http.StatusOK, value)
	case "progress":
		progress := a.originalServiceProgressSnapshot(route.jobID)
		if err := normalizeOriginalServiceProgressProjection(progress); err != nil {
			writeOriginalServiceWireInternalError(w)
		} else if progress == nil {
			writeOriginalServiceWireResponse(w, false, "后台服务不存在", nil)
		} else {
			writeOriginalServiceWireResponse(w, true, nil, progress)
		}
	case "run":
		if !sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			a.writeError(w, r, &APIError{"ORIGIN_REJECTED", "禁止跨站写入请求", 403})
			return
		}
		jobID, present := originalServiceWireQuery(r.URL.RawQuery, "jobid")
		if !present {
			var value any = map[string]any{"detail": []any{map[string]any{
				"type": "missing", "loc": []string{"query", "jobid"}, "msg": "Field required", "input": nil,
			}}}
			if route.version == "v2" {
				value = map[string]any{"success": false, "message": "Field required", "data": map[string]any{}}
			}
			writeOriginalServiceWireJSON(w, http.StatusUnprocessableEntity, value)
			return
		}
		if jobID == "" {
			writeOriginalServiceWireResponse(w, false, "命令不能为空！", nil)
			return
		}
		instance, service := "", ""
		state := &a.pluginOriginalDirectJobs
		state.mu.Lock()
		for key, job := range state.jobs {
			if originalServiceID(job) == jobID {
				instance, service = key.instance, key.service
				break
			}
		}
		state.mu.Unlock()
		// Acknowledgement is separate from callback business success/failure.
		_ = a.runOriginalService(r.Context(), instance, service, true)
		writeOriginalServiceWireResponse(w, true, nil, nil)
	}
}

// Starlette QueryParams decodes ASGI query bytes as Latin-1, then calls Python
// parse_qsl(keep_blank_values=True): '&' is the only separator, malformed '%'
// escapes are literal, UTF-8 errors use replacement, and the last scalar wins.
// This applies only to source service jobid, never token authority/global URLs.
func originalServiceWireQuery(raw, key string) (value string, present bool) {
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		name, item, _ := strings.Cut(pair, "=")
		if originalServiceWireUnquote(name) == key {
			value, present = originalServiceWireUnquote(item), true
		}
	}
	return
}

func originalServiceWireUnquote(raw string) string {
	var out strings.Builder
	// Python unquote processes ASCII runs separately from literal Latin-1
	// characters, so escaped UTF-8 bytes cannot merge across an unescaped byte.
	for len(raw) > 0 {
		if raw[0] >= utf8.RuneSelf {
			out.WriteRune(rune(raw[0]))
			raw = raw[1:]
			continue
		}
		end := 0
		for end < len(raw) && raw[end] < utf8.RuneSelf {
			end++
		}
		run := raw[:end]
		raw = raw[end:]
		decoded := make([]byte, 0, len(run))
		for i := 0; i < len(run); i++ {
			if run[i] == '+' {
				decoded = append(decoded, ' ')
			} else if run[i] == '%' && i+2 < len(run) && serviceWireHex(run[i+1]) >= 0 && serviceWireHex(run[i+2]) >= 0 {
				decoded = append(decoded, byte(serviceWireHex(run[i+1])*16+serviceWireHex(run[i+2])))
				i += 2
			} else {
				decoded = append(decoded, run[i])
			}
		}
		for len(decoded) > 0 {
			r, size := utf8.DecodeRune(decoded)
			if r == utf8.RuneError && size == 1 {
				// Python replaces the maximal valid prefix of an incomplete /
				// malformed sequence once, not each byte or all adjacent errors.
				lead, want := decoded[0], 1
				switch {
				case lead >= 0xc2 && lead <= 0xdf:
					want = 2
				case lead >= 0xe0 && lead <= 0xef:
					want = 3
				case lead >= 0xf0 && lead <= 0xf4:
					want = 4
				}
				for size < want && size < len(decoded) {
					b := decoded[size]
					if b < 0x80 || b > 0xbf || size == 1 && (lead == 0xe0 && b < 0xa0 || lead == 0xed && b >= 0xa0 || lead == 0xf0 && b < 0x90 || lead == 0xf4 && b >= 0x90) {
						break
					}
					size++
				}
			}
			out.WriteRune(r)
			decoded = decoded[size:]
		}
	}
	return out.String()
}

func serviceWireHex(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}
