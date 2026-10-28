package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (a *App) loggingSettings() map[string]any {
	c := a.Settings()
	days, total, rotate := loggingBudgets(c)
	level := c.LogLevel
	if level == "" {
		level = "INFO"
	}
	overrides := []debugOverride{}
	a.logs.mu.Lock()
	for k, d := range a.logs.overrides {
		if d.ExpiresAt.After(time.Now()) {
			overrides = append(overrides, d)
		} else {
			delete(a.logs.overrides, k)
		}
	}
	a.logs.mu.Unlock()
	return map[string]any{"level": level, "retain_days": days, "total_mib": total, "rotate_mib": rotate, "debug_overrides": overrides, "health": a.logs.health()}
}

var logScopeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`)

func (a *App) loggingAPI(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == "GET" {
		return a.loggingSettings(), nil
	}
	var in struct {
		Level      *string `json:"level"`
		RetainDays *int    `json:"retain_days"`
		TotalMiB   *int    `json:"total_mib"`
		RotateMiB  *int    `json:"rotate_mib"`
		Debug      *struct {
			Scope   string `json:"scope"`
			ID      string `json:"id"`
			Minutes int    `json:"minutes"`
			Enabled bool   `json:"enabled"`
		} `json:"debug"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		return nil, bad("INVALID_LOG_SETTINGS", "日志设置内容无效")
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return nil, bad("INVALID_LOG_SETTINGS", "日志设置必须为一个JSON对象")
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	c := a.Settings()
	if in.Level != nil {
		if *in.Level != "INFO" && *in.Level != "WARN" && *in.Level != "ERROR" {
			return nil, bad("INVALID_LOG_LEVEL", "请选择INFO、WARN或ERROR；DEBUG请按模块或站点临时开启")
		}
		c.LogLevel = *in.Level
	}
	if in.RetainDays != nil {
		if *in.RetainDays < 1 || *in.RetainDays > 90 {
			return nil, bad("INVALID_LOG_BUDGET", "日志保留天数必须为1至90")
		}
		c.LogRetainDays = *in.RetainDays
	}
	if in.TotalMiB != nil {
		if *in.TotalMiB < 1 || *in.TotalMiB > 1024 {
			return nil, bad("INVALID_LOG_BUDGET", "日志总大小必须为1至1024MiB")
		}
		c.LogTotalMiB = *in.TotalMiB
	}
	if in.RotateMiB != nil {
		if *in.RotateMiB < 1 || *in.RotateMiB > 20 {
			return nil, bad("INVALID_LOG_BUDGET", "单文件大小必须为1至20MiB")
		}
		c.LogMaxMiB = *in.RotateMiB
	}
	_, total, rotate := loggingBudgets(c)
	if rotate > total {
		return nil, bad("INVALID_LOG_BUDGET", "单文件大小不能大于日志总大小")
	}
	if in.Debug != nil {
		v := in.Debug
		if (v.Scope != "module" && v.Scope != "site") || !logScopeID.MatchString(v.ID) {
			return nil, bad("INVALID_DEBUG_SCOPE", "请选择具体的模块或站点")
		}
		if v.Minutes == 0 {
			v.Minutes = 30
		}
		if v.Minutes < 1 || v.Minutes > 120 {
			return nil, bad("INVALID_DEBUG_EXPIRY", "临时DEBUG期限必须为1至120分钟")
		}
		if v.Scope == "site" {
			found := false
			for _, s := range c.Sites {
				if s.ID == v.ID {
					found = true
				}
			}
			if !found {
				return nil, bad("INVALID_DEBUG_SCOPE", "站点尚未配置")
			}
		}
		a.logs.mu.Lock()
		count := len(a.logs.overrides)
		a.logs.mu.Unlock()
		if v.Enabled && count >= 32 {
			return nil, bad("DEBUG_SCOPE_LIMIT", "临时DEBUG范围已达上限，请先关闭其他范围")
		}
	}
	if in.Level != nil || in.RetainDays != nil || in.TotalMiB != nil || in.RotateMiB != nil {
		if err := a.saveSettings(r.Context(), c); err != nil {
			return nil, err
		}
	}
	if in.Debug != nil {
		v := in.Debug
		a.logs.mu.Lock()
		key := v.Scope + ":" + v.ID
		if v.Enabled {
			a.logs.overrides[key] = debugOverride{Scope: v.Scope, ID: v.ID, ExpiresAt: time.Now().UTC().Add(time.Duration(v.Minutes) * time.Minute)}
		} else {
			delete(a.logs.overrides, key)
		}
		a.logs.mu.Unlock()
		a.log("INFO", "logging", "debug_scope_updated", "临时DEBUG范围已更新", "", "")
	}
	return a.loggingSettings(), nil
}

func (a *App) streamLogs(w http.ResponseWriter, r *http.Request) {
	filters, e := validLogFilters(logQuery(r))
	if e != nil {
		a.writeError(w, r, e)
		return
	}
	select {
	case a.logs.streamSlots <- struct{}{}:
		defer func() { <-a.logs.streamSlots }()
	default:
		http.Error(w, "日志实时连接已达上限", http.StatusTooManyRequests)
		return
	}
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	send := func(event, key string, data any) bool {
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		b, _ := json.Marshal(data)
		var err error
		if event != "" {
			_, err = fmt.Fprintf(w, "event: %s\n", event)
		}
		if err == nil {
			_, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", key, b)
		}
		if err != nil {
			return false
		}
		f.Flush()
		return true
	}
	var after uint64
	resume := r.Header.Get("Last-Event-ID")
	if resume == "" {
		resume = r.URL.Query().Get("last_event_id")
	}
	if len(resume) > 128 {
		send("gap", "", map[string]string{"reason": "invalid_resume", "resume_id": ""})
		return
	}
	if resume != "" {
		parts := strings.Split(resume, ":")
		if len(parts) != 2 || parts[0] != a.logs.boot {
			send("gap", "", map[string]string{"reason": "instance_changed", "resume_id": ""})
			return
		}
		after, e = strconv.ParseUint(parts[1], 10, 64)
		if e != nil {
			send("gap", "", map[string]string{"reason": "invalid_resume", "resume_id": ""})
			return
		}
	} else {
		seq := a.logs.health().Sequence
		if seq > 100 {
			after = seq - 100
		}
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		started := time.Now()
		for count := 0; count < 512; count++ {
			entry, seq, gap, ok := a.logs.nextAfter(after)
			if gap {
				send("gap", fmt.Sprintf("%s:%d", a.logs.boot, seq), map[string]string{"reason": "history_evicted", "resume_id": fmt.Sprintf("%s:%d", a.logs.boot, seq)})
				return
			}
			if !ok {
				break
			}
			if filters["plugin_instance_id"] != "" {
				a.logs.fileMu.Lock()
				written := a.logs.pluginProjectionSequence
				a.logs.fileMu.Unlock()
				if entry.Seq > written {
					break
				}
			}
			var v map[string]any
			if json.Unmarshal(entry.Data, &v) == nil && logMatches(v, filters) {
				if filters["plugin_instance_id"] != "" {
					a.logs.fileMu.Lock()
					failed := a.logs.pluginProjectionFailures[entry.Seq]
					a.logs.fileMu.Unlock()
					if failed {
						send("gap", "", map[string]string{"reason": "plugin_file_write_failed", "resume_id": fmt.Sprintf("%s:%d", a.logs.boot, entry.Seq)})
						return
					}
					v["raw"] = pluginProjectionText(v)
				}
				if !send("", fmt.Sprintf("%s:%d", a.logs.boot, entry.Seq), v) {
					return
				}
			}
			after = entry.Seq
			if time.Since(started) > 2*time.Second || count == 511 {
				send("gap", "", map[string]string{"reason": "slow_client", "resume_id": fmt.Sprintf("%s:%d", a.logs.boot, after)})
				return
			}
		}
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, e := io.WriteString(w, ": heartbeat\n\n"); e != nil {
			return
		}
		f.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		case <-a.logs.done:
			return
		}
	}
}

var exportIP = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?::[0-9]{1,5})?\b`)
var exportPath = regexp.MustCompile(`(?:/[a-zA-Z0-9_\-.\p{Han}]+){2,}(?:\.[a-zA-Z0-9]+)?`)
var exportIPv6 = regexp.MustCompile(`(?i)\[?[0-9a-f]*:[0-9a-f:]*:[0-9a-f:]+\]?(?::[0-9]+)?`)
var exportQuotedPath = regexp.MustCompile(`"(/[^"\n]+)"`)
var exportAnyDomain = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}(?::[0-9]{1,5})?\b`)
var exportDomain = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+(?:com|net|org|top|cn|local|lan|io|me|tv|cc|xyz|site|club|vip|pt)(?::[0-9]{1,5})?\b`)

func (a *App) diagnosticPlaceholder(kind, value string) string {
	m := hmac.New(sha256.New, a.Store.key)
	_, _ = m.Write([]byte(kind + ":" + value))
	return "[" + kind + "_" + hex.EncodeToString(m.Sum(nil)[:5]) + "]"
}
func (a *App) redactDiagnostic(v map[string]any) map[string]any {
	c := a.Settings()
	known := []string{a.Dir, c.FNOS.Host}
	known = append(known, c.AllowedRoots...)
	for _, root := range c.Roots {
		known = append(known, root.Path)
	}
	for _, s := range c.Sites {
		known = append(known, s.BaseURL)
	}
	for _, d := range c.Downloaders {
		known = append(known, d.BaseURL)
	}
	out := map[string]any{}
	for k, v := range v {
		text, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		text = cleanWithSettings(text, c)
		for _, value := range known {
			if value == "" {
				continue
			}
			if u, e := url.Parse(value); e == nil && u.Hostname() != "" {
				value = u.Hostname()
			}
			if len(value) > 2 {
				text = strings.ReplaceAll(text, value, a.diagnosticPlaceholder("PRIVATE", value))
			}
		}
		text = exportIP.ReplaceAllStringFunc(text, func(s string) string { return a.diagnosticPlaceholder("IP", s) })
		text = exportIPv6.ReplaceAllStringFunc(text, func(s string) string {
			if ip, e := netip.ParseAddr(strings.Trim(s, "[]")); e == nil {
				return a.diagnosticPlaceholder("IP", ip.String())
			}
			return s
		})
		text = exportQuotedPath.ReplaceAllStringFunc(text, func(s string) string { return a.diagnosticPlaceholder("PATH", strings.Trim(s, `"`)) })
		text = exportPath.ReplaceAllStringFunc(text, func(s string) string { return a.diagnosticPlaceholder("PATH", s) })
		text = exportDomain.ReplaceAllStringFunc(text, func(s string) string { return a.diagnosticPlaceholder("HOST", s) })
		if k == "message" || k == "stack" {
			text = exportAnyDomain.ReplaceAllStringFunc(text, func(s string) string { return a.diagnosticPlaceholder("HOST", s) })
		}
		out[k] = text
	}
	return out
}
func (a *App) exportLogs(w http.ResponseWriter, r *http.Request) {
	q := logQuery(r)
	q["limit"] = "500"
	page, e := a.queryLogs(r.Context(), q)
	if e != nil {
		a.writeError(w, r, e)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "attachment; filename=light-media-diagnostics.jsonl")
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
	enc := json.NewEncoder(w)
	for _, v := range page.Entries {
		if enc.Encode(a.redactDiagnostic(v)) != nil {
			return
		}
	}
	// A bounded export is deliberately explicit: no hidden unbounded scan or false completeness.
	_ = enc.Encode(map[string]any{"type": "export_meta", "complete": page.Complete, "truncated": page.Truncated, "gap": page.Gap, "next_cursor": page.NextCursor, "scanned_bytes": page.ScannedBytes, "filters": a.redactDiagnostic(map[string]any{"level": q["level"], "module": q["module"], "from": q["from"], "to": q["to"]})})
	a.auditRequest(r.Context(), "GET.logs.export", w.Header().Get("X-Request-ID"), 200)
}

func (a *App) frontendErrorAPI(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Page      string `json:"page"`
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
		Build     string `json:"build"`
		Message   string `json:"message"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		return nil, bad("INVALID_FRONTEND_ERROR", "前端诊断内容无效")
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return nil, bad("INVALID_FRONTEND_ERROR", "前端诊断必须为一个JSON对象")
	}
	pages := map[string]bool{}
	for _, p := range strings.Fields("home discover detail search subscriptions downloads import assets jobs settings logs calendar person workflows sites downloaders files history resource-search") {
		pages[p] = true
	}
	codes := map[string]string{"VUE_RENDER_ERROR": "页面渲染发生异常", "UNHANDLED_REJECTION": "页面异步操作发生异常", "SCRIPT_ERROR": "页面脚本发生异常"}
	message, ok := codes[in.Code]
	if !ok || !pages[in.Page] || len(in.Build) > 40 || !regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,40}$`).MatchString(in.Build) || len(in.RequestID) > 100 || (in.RequestID != "" && !regexp.MustCompile(`^req_[a-zA-Z0-9_-]+$`).MatchString(in.RequestID)) || len(in.Message) > 512 {
		return nil, bad("INVALID_FRONTEND_ERROR", "前端诊断字段不在允许范围")
	}
	session := ""
	if cookie, err := r.Cookie(sessionCookie(r, "", 0).Name); err == nil {
		session = cookie.Value
	}
	session = tokenHash(session)
	now := time.Now()
	a.logs.mu.Lock()
	recent := []time.Time{}
	for _, t := range a.logs.frontend[session] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		a.logs.mu.Unlock()
		return nil, &APIError{"FRONTEND_ERROR_RATE_LIMIT", "前端诊断过于频繁，请稍后重试", 429}
	}
	if len(a.logs.frontend) >= 256 {
		for k, ts := range a.logs.frontend {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= time.Minute {
				delete(a.logs.frontend, k)
			}
		}
	}
	if len(a.logs.frontend) >= 256 && len(recent) == 0 {
		a.logs.mu.Unlock()
		return nil, &APIError{"FRONTEND_ERROR_RATE_LIMIT", "前端诊断容量已满，请稍后重试", 429}
	}
	a.logs.frontend[session] = append(recent, now)
	a.logs.mu.Unlock()
	// Never store supplied exception text, browser state or form values. Codes map
	// to server-owned summaries, even if a buggy client submits sensitive text.
	a.writeLog(map[string]any{"time": now.UTC().Format(time.RFC3339Nano), "level": "WARN", "module": "frontend", "event": "frontend_error", "message": message, "page": in.Page, "code": in.Code, "request_id": in.RequestID, "build": in.Build})
	return map[string]bool{"accepted": true}, nil
}
