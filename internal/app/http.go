package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/fnos"
	"light-media/internal/integrations"
	"light-media/internal/priorityrules"
)

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeBodyWithLimit(w, r, v, 2<<20)
}

// A local word list may occupy 16 MiB after JSON escaping. Keep the existing
// 2 MiB allowance for the rest of the subscription form, without increasing
// the body limit of any other endpoint.
const subscriptionBodyLimit = pluginDataValueLimit + (2 << 20)

func decodeSubscriptionBody(w http.ResponseWriter, r *http.Request, in *SubscriptionRequest) error {
	return decodeBodyWithLimit(w, r, in, subscriptionBodyLimit)
}

func decodeBodyWithLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	if e := d.Decode(v); e != nil {
		return bad("INVALID_JSON", "请求内容无效或过大")
	}
	if d.Decode(new(any)) != io.EOF {
		return bad("INVALID_JSON", "请求只能包含一个JSON对象")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, r *http.Request, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "request_id": w.Header().Get("X-Request-ID")})
}
func (a *App) writeError(w http.ResponseWriter, r *http.Request, e error) {
	out := &APIError{Code: "INTERNAL_ERROR", Message: "操作失败，请查看关联日志", Status: 500}
	var api *APIError
	var cat *catalog.Error
	var fn *fnos.ServiceError
	var dl *download.Error
	if errors.As(e, &api) {
		out = api
	} else if errors.As(e, &cat) {
		out = &APIError{Code: strings.ToUpper(cat.Code), Message: cat.Error(), Status: 502}
	} else if errors.As(e, &fn) {
		out = &APIError{Code: "FNOS_" + strings.ToUpper(fn.Code), Message: fn.Error(), Status: 502}
	} else if errors.As(e, &dl) {
		out = &APIError{Code: "DOWNLOADER_" + strings.ToUpper(dl.Code), Message: dl.Error(), Status: 502}
	} else if errors.Is(e, sql.ErrNoRows) {
		out = &APIError{"NOT_FOUND", "记录不存在", 404}
	}
	if out.Status >= 500 {
		a.writeLog(map[string]any{"time": now(), "id": id("log_"), "level": "ERROR", "module": "api", "event": "request_failed", "message": e.Error(), "error_code": out.Code, "http_status": out.Status, "request_id": w.Header().Get("X-Request-ID"), "app_version": appVersion})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": out, "request_id": w.Header().Get("X-Request-ID")})
}
func (a *App) Handler() http.Handler { return mountEntrypoints(a.requestLogged(a.handler())) }
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if e := a.Store.DB.PingContext(r.Context()); e != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		writeJSON(w, r, map[string]string{"status": "ok", "version": appVersion})
	})
	mux.HandleFunc("/api/", a.api)
	if a.web != nil {
		mux.Handle("/", a.web)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("X-Request-ID") == "" {
			w.Header().Set("X-Request-ID", id("req_"))
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if a.servePluginOrganizationCompatAPI(w, r) || a.servePluginCustomAPI(w, r) {
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" && r.Method != "HEAD" {
				if !sameRequestOrigin(r) {
					a.writeError(w, r, &APIError{"ORIGIN_REJECTED", "请求来源不匹配", 403})
					return
				}
				if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
					a.writeError(w, r, &APIError{"ORIGIN_REJECTED", "禁止跨站写入请求", 403})
					return
				}
			}
			if r.URL.Path != "/api/auth/status" && r.URL.Path != "/api/auth/login" && r.URL.Path != "/api/auth/setup" && !a.authenticated(r) {
				a.writeError(w, r, &APIError{"UNAUTHORIZED", "请先登录", 401})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := strings.TrimPrefix(r.URL.Path, "/api/")
	if strings.HasPrefix(p, "plugins/") && a.pluginLogHTTP(w, r, strings.TrimPrefix(p, "plugins/")) {
		return
	}
	method := r.Method
	var out any
	var e error
	switch {
	case p == "settings/api-token/reveal" && method == "POST":
		out = map[string]string{"token": a.Settings().APIToken}
	case p == "plugins" || strings.HasPrefix(p, "plugins/"):
		out, e = a.pluginAPI(w, r, strings.TrimPrefix(strings.TrimPrefix(p, "plugins"), "/"))
	case p == "auth/status" && method == "GET":
		out = map[string]any{"setup_required": !a.hasAdmin(), "authenticated": a.authenticated(r)}
	case (p == "auth/login" || p == "auth/setup") && method == "POST":
		_, e = a.auth(w, r)
		out = map[string]bool{"authenticated": e == nil}
	case p == "auth/logout" && method == "POST":
		if c, err := r.Cookie(sessionCookie(r, "", -1).Name); err == nil {
			_, e = a.Store.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(c.Value))
		}
		http.SetCookie(w, sessionCookie(r, "", -1))
		out = map[string]bool{"authenticated": false}
	case p == "status" && method == "GET":
		out = a.status(ctx)
	case p == "control" && method == "POST":
		var in struct {
			Paused bool `json:"paused"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			e = a.setControl(ctx, in.Paused)
		}
		out = a.status(ctx)
	case p == "settings" && method == "GET":
		out = a.publicSettings()
	case p == "settings/rules" || strings.HasPrefix(p, "settings/rules/"):
		out, e = a.priorityRulesAPI(w, r, strings.TrimPrefix(strings.TrimPrefix(p, "settings/rules"), "/"))
	case p == "settings/resource-metadata":
		out, e = a.resourceMetadataSettingsAPI(w, r)
	case p == "settings" && method == "PUT":
		var c Settings
		e = decodeBody(w, r, &c)
		if e == nil {
			a.settingsMu.Lock()
			e = validateFNOSCredentialChange(c.FNOS, a.Settings().FNOS)
			if e == nil {
				a.preserveSecrets(&c)
				e = a.saveSettings(ctx, c)
			}
			a.settingsMu.Unlock()
		}
		out = a.publicSettings()
	case p == "settings/test" && method == "POST":
		var in struct {
			Target string `json:"target"`
			ID     string `json:"id"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.testConnection(ctx, in.Target, in.ID)
		}
	case strings.HasPrefix(p, "directories/"):
		out, e = a.directoryAPI(r, strings.TrimPrefix(p, "directories/"))
	case p == "files/content" && method == "GET":
		e = a.serveBrowserContent(w, r)
		if e == nil {
			return
		}
	case strings.HasPrefix(p, "files/"):
		out, e = a.fileManagerAPI(r, strings.TrimPrefix(p, "files/"))
	case strings.HasPrefix(p, "history/"):
		out, e = a.transferHistoryAPI(r, strings.TrimPrefix(p, "history/"))
	case p == "sites/export" && method == "GET":
		a.exportManagedSites(w, r)
		return
	case p == "sites/import" && method == "POST":
		var in json.RawMessage
		if e = decodeBody(w, r, &in); e == nil {
			out, e = a.importManagedSites(ctx, in)
		}
	case p == "sites/priorities" && method == "POST":
		var in []sitePriority
		if e = decodeBody(w, r, &in); e == nil {
			out, e = a.saveSitePriorities(ctx, in)
		}
	case p == "sites/manage" && method == "GET":
		out, e = a.managedSites(ctx)
	case p == "sites/manage" && method == "POST":
		var in SiteConfig
		if e = decodeBody(w, r, &in); e == nil {
			out, e = a.saveManagedSite(ctx, in)
		}
	case strings.HasPrefix(p, "sites/manage/"):
		parts := strings.Split(strings.TrimPrefix(p, "sites/manage/"), "/")
		if len(parts) == 1 && method == "DELETE" {
			out, e = a.deleteManagedSite(ctx, parts[0])
		} else if len(parts) == 2 && parts[1] == "refresh" && method == "POST" {
			out, e = a.refreshManagedSite(ctx, parts[0])
		} else if len(parts) == 2 && parts[1] == "history" && method == "GET" {
			out, e = a.managedSiteHistory(ctx, parts[0])
		} else if len(parts) == 2 && parts[1] == "resources" && method == "POST" {
			var in struct {
				Query string `json:"query"`
				Page  int    `json:"page"`
			}
			if e = decodeBody(w, r, &in); e == nil {
				out, e = a.browseManagedSiteResources(ctx, parts[0], in.Query, in.Page)
			}
		} else {
			e = &APIError{"NOT_FOUND", "接口不存在", 404}
		}
	case p == "downloaders/manage" && method == "GET":
		out, e = a.managedDownloaders(ctx)
	case p == "downloaders/manage" && method == "POST":
		var in ManagedDownloaderRequest
		if e = decodeBody(w, r, &in); e == nil {
			out, e = a.saveManagedDownloader(ctx, in)
		}
	case strings.HasPrefix(p, "downloaders/manage/"):
		parts := strings.Split(strings.TrimPrefix(p, "downloaders/manage/"), "/")
		if len(parts) == 1 && method == "DELETE" {
			out, e = a.deleteManagedDownloader(ctx, parts[0])
		} else if len(parts) == 2 && parts[1] == "test" && method == "POST" {
			out, e = a.testManagedDownloader(ctx, parts[0])
		} else {
			e = &APIError{"NOT_FOUND", "接口不存在", 404}
		}
	case strings.HasPrefix(p, "catalog/") && method == "GET":
		if p == "catalog/image" {
			if err := a.catalogImage(w, r); err != nil {
				a.writeError(w, r, err)
			}
			return
		}
		out, e = a.catalogAPI(r, strings.TrimPrefix(p, "catalog/"))
	case p == "search/resources" && method == "POST":
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			a.streamSearch(w, r)
			return
		}
		var in SearchRequest
		if e = decodeBody(w, r, &in); e == nil {
			out, e = a.searchResources(ctx, in)
		}
	case p == "search" && method == "POST":
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			a.streamSearch(w, r)
			return
		}
		var in SearchRequest
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.search(ctx, in, false)
		}
	case p == "downloads/add" && method == "POST":
		var in NativeDownloadRequest
		if e = decodeBody(w, r, &in); e == nil {
			out, e = a.addNativeDownload(ctx, in)
		}
	case p == "downloads/task-action" && method == "POST":
		var in struct {
			DownloaderID string `json:"downloader_id"`
			Hash         string `json:"hash"`
			Action       string `json:"action"`
		}
		if e = decodeBody(w, r, &in); e == nil {
			e = a.downloaderTaskAction(ctx, in.DownloaderID, in.Hash, in.Action)
			out = map[string]bool{"ok": e == nil}
		}
	case p == "downloads/preview" && method == "POST":
		var in PreviewRequest
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.previewDownload(ctx, in, false)
		}
	case p == "downloads/confirm" && method == "POST":
		var in struct {
			PreviewID   string `json:"preview_id"`
			AcceptExtra bool   `json:"accept_extra_files"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.confirmDownload(ctx, in.PreviewID, in.AcceptExtra)
		}
	case p == "downloads" && method == "GET":
		out, e = a.downloadList(ctx, r.URL.Query().Get("downloader_id"))
	case strings.HasPrefix(p, "downloads/") && strings.HasSuffix(p, "/files") && method == "GET":
		out, e = a.downloadFiles(ctx, strings.TrimSuffix(strings.TrimPrefix(p, "downloads/"), "/files"))
	case strings.HasPrefix(p, "downloads/") && strings.HasSuffix(p, "/import-preview") && method == "POST":
		var in DownloadReviewRequest
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.previewDownloadReview(ctx, strings.TrimSuffix(strings.TrimPrefix(p, "downloads/"), "/import-preview"), in)
		}
	case strings.HasPrefix(p, "downloads/") && strings.HasSuffix(p, "/import-confirm") && method == "POST":
		var in struct {
			PlanID              string `json:"plan_id"`
			ConfirmTargetChange bool   `json:"confirm_target_change"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.confirmDownloadReview(ctx, strings.TrimSuffix(strings.TrimPrefix(p, "downloads/"), "/import-confirm"), in.PlanID, in.ConfirmTargetChange)
		}
	case strings.HasPrefix(p, "downloads/") && strings.HasSuffix(p, "/action") && method == "POST":
		var in struct {
			Action string `json:"action"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			e = a.downloadAction(ctx, strings.TrimSuffix(strings.TrimPrefix(p, "downloads/"), "/action"), in.Action)
		}
		out = map[string]bool{"accepted": e == nil}
	case p == "subscriptions" && method == "GET":
		out, e = a.subscriptionList(ctx)
	case p == "subscriptions" && method == "POST":
		var in SubscriptionRequest
		e = decodeSubscriptionBody(w, r, &in)
		if e == nil {
			var saved Subscription
			saved, e = a.saveSubscription(ctx, "", in)
			if e == nil {
				out, e = a.subscriptionEditorValue(ctx, saved)
			}
		}
	case strings.HasPrefix(p, "subscriptions/"):
		key := strings.TrimPrefix(p, "subscriptions/")
		if strings.HasSuffix(key, "/check") && method == "POST" {
			key = strings.TrimSuffix(key, "/check")
			var s Subscription
			e = a.Store.Get(ctx, "subscription", key, &s)
			if e == nil {
				out, e = a.enqueue(ctx, "subscription", map[string]any{"id": key, "revision": s.Revision}, id("wf_"))
			}
		} else if method == "PUT" {
			var in SubscriptionRequest
			e = decodeSubscriptionBody(w, r, &in)
			if e == nil {
				var saved Subscription
				saved, e = a.saveSubscription(ctx, key, in)
				if e == nil {
					out, e = a.subscriptionEditorValue(ctx, saved)
				}
			}
		} else if method == "DELETE" {
			e = a.cancelSubscription(ctx, key)
			out = map[string]bool{"cancelled": e == nil}
		} else {
			e = &APIError{"NOT_FOUND", "接口不存在", 404}
		}
	case p == "calendar" && method == "GET":
		out, e = a.calendar(ctx, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	case p == "schedule/preview" && method == "POST":
		var in struct {
			Schedule Schedule `json:"schedule"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = previewSchedule(in.Schedule)
		}
	case p == "import/scan" && method == "POST":
		var in struct {
			Path      string `json:"path"`
			Recursive bool   `json:"recursive"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.scanFiles(ctx, in.Path, in.Recursive)
		}
	case p == "import/preview" && method == "POST":
		var in ImportRequest
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.previewImport(ctx, in)
		}
	case p == "import/execute" && method == "POST":
		var in struct {
			PlanID  string   `json:"plan_id"`
			FileIDs []string `json:"file_ids"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.executeImport(ctx, in.PlanID, in.FileIDs)
		}
	case p == "import/plans" && method == "GET":
		out, e = a.Store.List(ctx, "import_plan", 100, 0)
	case p == "assets" && method == "GET":
		out, e = a.Store.List(ctx, "asset", 500, atoi(r.URL.Query().Get("offset")))
	case p == "assets/candidates" && method == "GET":
		out, e = a.libraryCandidates(ctx, r.URL.Query().Get("root_id"), atoi(r.URL.Query().Get("limit")), r.URL.Query().Get("cursor"))
	case p == "assets/register" && method == "POST":
		var in LibraryRegisterRequest
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.registerLibraryAsset(ctx, in)
		}
	case p == "assets/scan" && method == "POST":
		var in struct {
			RootID string `json:"root_id"`
		}
		e = decodeBody(w, r, &in)
		if e == nil {
			out, e = a.enqueue(ctx, "library_scan", map[string]string{"root_id": in.RootID}, id("wf_"))
		}
	case strings.HasPrefix(p, "assets/"):
		out, e = a.assetAPI(w, r, strings.TrimPrefix(p, "assets/"))
	case p == "jobs" && method == "GET":
		var jobs []Job
		jobs, e = a.jobs(ctx, 100)
		if e == nil {
			out, e = a.jobViews(ctx, jobs)
		}
	case strings.HasPrefix(p, "jobs/"):
		out, e = a.jobAPI(w, r, strings.TrimPrefix(p, "jobs/"))
	case p == "notifications" && method == "GET":
		out, e = a.notifications(ctx)
	case strings.HasPrefix(p, "notifications/"):
		out, e = a.notificationAPI(r, strings.TrimPrefix(p, "notifications/"))
	case p == "audit" && method == "GET":
		out, e = a.Store.List(ctx, "audit", 100, 0)
	case p == "logs" && method == "GET":
		out, e = a.queryLogs(ctx, logQuery(r))
	case p == "logs/health" && method == "GET":
		out = a.logs.health()
	case p == "settings/logging" && (method == "GET" || method == "PATCH"):
		out, e = a.loggingAPI(w, r)
	case p == "frontend-errors" && method == "POST":
		out, e = a.frontendErrorAPI(w, r)
	case p == "logs/stream" && method == "GET":
		a.streamLogs(w, r)
		return
	case p == "logs/export" && method == "GET":
		a.exportLogs(w, r)
		return
	case p == "fnos/libraries" && method == "GET":
		c := a.Settings().FNOS
		var client *fnos.Service
		client, e = fnos.NewService(fnos.Config{Host: c.Host, Username: c.Username, Password: c.Password})
		if e == nil {
			out, e = client.Libraries(ctx)
		}
	default:
		e = &APIError{"NOT_FOUND", "接口不存在", 404}
	}
	if e != nil {
		a.writeError(w, r, e)
		return
	}
	writeJSON(w, r, out)
}
func atoi(s string) int { v, _ := strconv.Atoi(s); return v }
func (a *App) status(ctx context.Context) map[string]any {
	counts := map[string]int{}
	for _, k := range []string{"subscription", "intent", "asset", "import_plan"} {
		var n int
		_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind=?`, k).Scan(&n)
		counts[k] = n
	}
	var active, failed int
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state IN ('queued','running','retry_wait','waiting_paused')`).Scan(&active)
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state='failed'`).Scan(&failed)
	counts["active_jobs"] = active
	counts["failed_jobs"] = failed
	a.controlMu.Lock()
	state, n := a.control, a.activeWrites
	a.controlMu.Unlock()
	a.logMu.Lock()
	logErr := a.logError
	a.logMu.Unlock()
	logStatus := "ok"
	if logErr != "" {
		logStatus = "failed"
	}
	return map[string]any{"logging": map[string]string{"status": logStatus, "error": logErr}, "control": state, "active_writes": n, "counts": counts, "version": appVersion, "time": now()}
}
func (a *App) publicSettings() map[string]any {
	c := a.Settings()
	secretState := map[string]any{"tmdb": c.TMDB.APIKey != "" || c.TMDB.AccessToken != "", "fnos": c.FNOS.Password != "", "api_token": c.APIToken != ""}
	c.APIToken = ""
	siteState := map[string]bool{}
	for i := range c.Sites {
		siteState[c.Sites[i].ID] = c.Sites[i].Cookie != "" || c.Sites[i].APIKey != ""
		hideSiteSecrets(&c.Sites[i])
	}
	downState := map[string]bool{}
	for i := range c.Downloaders {
		downState[c.Downloaders[i].ID] = c.Downloaders[i].Password != ""
		c.Downloaders[i].Password = ""
	}
	c.TMDB.APIKey = ""
	c.TMDB.AccessToken = ""
	c.FNOS.Password = ""
	secretState["sites"], secretState["downloaders"] = siteState, downState
	b, _ := json.Marshal(c)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	publicDirectories := []map[string]any{}
	for _, rule := range c.Directories {
		publicDirectories = append(publicDirectories, publicDirectory(c, rule))
	}
	m["directories"] = publicDirectories
	if downloaders, ok := m["downloaders"].([]any); ok {
		for _, entry := range downloaders {
			if d, ok := entry.(map[string]any); ok {
				for _, configured := range c.Downloaders {
					if configured.ID == d["id"] {
						d["enabled"] = downloaderEnabled(configured)
						break
					}
				}
				delete(d, "mappings")
				delete(d, "download_path")
				delete(d, "shared_paths")
			}
		}
	}
	if fn, ok := m["fnos"].(map[string]any); ok {
		delete(fn, "mappings")
		delete(fn, "library_guid")
	}
	m["secret_state"] = secretState
	return m
}

func sameFNOSCredentialEndpoint(incoming, previous string) bool {
	if incoming == previous {
		return true
	}
	next, nextErr := fnos.NormalizeHost(incoming)
	old, oldErr := fnos.NormalizeHost(previous)
	if nextErr != nil || oldErr != nil {
		return false
	}
	if next == old {
		return true
	}
	nextURL, _ := url.Parse(next)
	oldURL, _ := url.Parse(old)
	nextIP, nextIPErr := netip.ParseAddr(nextURL.Hostname())
	oldIP, oldIPErr := netip.ParseAddr(oldURL.Hostname())
	if nextIPErr != nil || oldIPErr != nil || nextIP.Zone() != "" || oldIP.Zone() != "" || !nextIP.Unmap().IsPrivate() || nextIP.Unmap() != oldIP.Unmap() {
		return false
	}
	// fnOS exposes the same NAS account through these default local entries.
	// Keep arbitrary ports and public/hostname destinations behind the existing
	// explicit-password requirement rather than treating every same-host URL alike.
	defaultEntry := func(endpoint *url.URL) bool {
		switch endpoint.Scheme + ":" + endpoint.Port() {
		case "http:8005", "http:5666", "https:5667":
			return true
		}
		return false
	}
	return defaultEntry(nextURL) && defaultEntry(oldURL)
}

func validateFNOSCredentialChange(incoming, previous FNConfig) error {
	if incoming.Password == "" && previous.Password != "" &&
		(incoming.Username != previous.Username || !sameFNOSCredentialEndpoint(incoming.Host, previous.Host)) {
		return bad("FNOS_CREDENTIALS_REQUIRED", "飞牛影视地址或账号已修改，请重新输入密码；原配置已保留")
	}
	return nil
}

func (a *App) preserveSecrets(c *Settings) {
	// The system settings form does not edit these controls. Its cached full
	// snapshot must not overwrite newer changes made in the plugin/site flow.
	c.IndexerSites = a.Settings().IndexerSites
	c.RSSSites = a.Settings().RSSSites
	old := a.Settings()
	// Rules have independent section revisions. A cached system/directory form
	// is never an authority to overwrite a more recently saved rule section.
	c.PriorityRules = old.PriorityRules
	if c.APIToken == "" {
		c.APIToken = old.APIToken
	}
	if c.Roots == nil {
		c.Roots = old.Roots
	}
	if c.AllowedRoots == nil {
		c.AllowedRoots = old.AllowedRoots
	}
	unchangedDirectories := len(c.Directories) == len(old.Directories)
	if unchangedDirectories {
		for i, rule := range c.Directories {
			incoming, _ := json.Marshal(publicDirectory(*c, rule))
			previous, _ := json.Marshal(publicDirectory(old, old.Directories[i]))
			if string(incoming) != string(previous) {
				unchangedDirectories = false
				break
			}
		}
	}
	if c.Directories == nil || unchangedDirectories {
		// Editing another settings section must not reinterpret legacy directory
		// records from their clean public projection or invalidate pending plans.
		c.Directories = old.Directories
	} else {
		for i := range c.Directories {
			if c.Directories[i].SourcePath != "" {
				_ = preparePhysicalDirectory(c, &c.Directories[i])
			}
		}
	}
	if c.TMDB.APIKey == "" && c.TMDB.AccessToken == "" {
		c.TMDB = old.TMDB
	}
	if c.FNOS.Password == "" && sameFNOSCredentialEndpoint(c.FNOS.Host, old.FNOS.Host) && c.FNOS.Username == old.FNOS.Username {
		c.FNOS.Password = old.FNOS.Password
		if c.FNOS.Mappings == nil {
			c.FNOS.Mappings = old.FNOS.Mappings
		}
		if c.FNOS.LibraryGUID == "" {
			c.FNOS.LibraryGUID = old.FNOS.LibraryGUID
		}
	}
	for i := range c.Sites {
		for _, v := range old.Sites {
			if c.Sites[i].ID == v.ID && c.Sites[i].BaseURL == v.BaseURL && c.Sites[i].Kind == v.Kind {
				preserveSiteSecrets(&c.Sites[i], v)
			}
		}
	}
	for i := range c.Downloaders {
		c.Downloaders[i].SharedPaths = true
		for _, v := range old.Downloaders {
			if c.Downloaders[i].ID == v.ID && c.Downloaders[i].BaseURL == v.BaseURL && c.Downloaders[i].Kind == v.Kind {
				if c.Downloaders[i].Mappings == nil {
					c.Downloaders[i].Mappings = v.Mappings
				}
				if c.Downloaders[i].DownloadPath == "" {
					c.Downloaders[i].DownloadPath = v.DownloadPath
				}
			}
			if c.Downloaders[i].ID == v.ID && c.Downloaders[i].BaseURL == v.BaseURL && c.Downloaders[i].Username == v.Username && c.Downloaders[i].Password == "" {
				c.Downloaders[i].Password = v.Password
			}
		}
	}
}
func validateSettings(c Settings) error {
	if c.PriorityRules != nil {
		if err := priorityrules.ValidateConfiguration(*c.PriorityRules); err != nil {
			return bad("INVALID_PRIORITY_RULES", "优先级规则配置无效或超过资源上限")
		}
	}
	if c.APIToken != "" && (len(c.APIToken) < 16 || len(c.APIToken) > 512 || invalidSiteImportText(c.APIToken, 512)) {
		return bad("INVALID_API_TOKEN", "API令牌长度须为16到512字节，不能包含控制字符")
	}
	if c.SiteDataRefreshHours != nil && (*c.SiteDataRefreshHours < 0 || *c.SiteDataRefreshHours > 8760) {
		return bad("INVALID_SITE_INTERVAL", "站点数据刷新间隔须为0到8760小时")
	}
	for _, list := range [][]string{c.IndexerSites, c.RSSSites} {
		if len(list) > 1024 {
			return bad("CONFIG_LIMIT", "站点选择数量超出上限")
		}
		for _, id := range list {
			if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") {
				return bad("INVALID_SITES", "站点引用无效")
			}
		}
	}
	if e := validateDirectoryRules(c); e != nil {
		return e
	}
	if c.MaterialMaxMiB != 0 && (c.MaterialMaxMiB < 32 || c.MaterialMaxMiB > 2048) {
		return bad("INVALID_MATERIAL_BUDGET", "种子材料归档上限必须为32至2048MiB")
	}
	if c.AutoImportMode != "" && c.AutoImportMode != "hardlink" && c.AutoImportMode != "copy" && c.AutoImportMode != "auto" {
		return bad("INVALID_IMPORT_MODE", "自动整理方式无效")
	}
	if c.LogLevel != "" && c.LogLevel != "DEBUG" && c.LogLevel != "INFO" && c.LogLevel != "WARN" && c.LogLevel != "ERROR" {
		return bad("INVALID_LOG_LEVEL", "日志级别无效")
	}
	if c.LogMaxMiB < 0 || c.LogMaxMiB > 20 || c.LogRetainFiles < 0 || c.LogRetainFiles > 20 || c.LogRetainDays < 0 || c.LogRetainDays > 90 || c.LogTotalMiB < 0 || c.LogTotalMiB > 1024 || (c.LogTotalMiB > 0 && c.LogMaxMiB > c.LogTotalMiB) {
		return bad("INVALID_LOG_BUDGET", "日志文件上限无效")
	}
	if c.DebugUntil != "" {
		t, e := time.Parse(time.RFC3339, c.DebugUntil)
		if e != nil || t.After(time.Now().Add(2*time.Hour+time.Minute)) {
			return bad("INVALID_DEBUG_EXPIRY", "临时DEBUG期限最长2小时")
		}
	}
	if len(c.Sites) > 128 || len(c.Downloaders) > 8 || len(c.Roots) > 128 || len(c.AllowedRoots) > 128 {
		return bad("CONFIG_LIMIT", "配置数量超出上限")
	}
	if c.TMDB.APIKey != "" && c.TMDB.AccessToken != "" {
		return bad("INVALID_TMDB", "API Key与Access Token只需填写一种")
	}
	seen := map[string]bool{}
	for _, s := range c.Sites {
		if seen["s"+s.ID] || s.ID == "" {
			return bad("DUPLICATE_ID", "站点ID重复或为空")
		}
		seen["s"+s.ID] = true
		if s.Enabled {
			if e := validateManagedSiteConfig(s.Config); e != nil {
				return bad("INVALID_SITE", e.Error())
			}
		}
		if s.DailyBudget < 0 || s.DailyBudget > 10000 {
			return bad("INVALID_BUDGET", "站点每日预算无效")
		}
	}
	for _, d := range c.Downloaders {
		if seen["d"+d.ID] || d.ID == "" {
			return bad("DUPLICATE_ID", "下载器ID重复或为空")
		}
		seen["d"+d.ID] = true
		if _, e := download.New(d.Config); downloaderEnabled(d) && e != nil {
			return bad("INVALID_DOWNLOADER", e.Error())
		}
		if d.DownloadPath != "" && !filepath.IsAbs(d.DownloadPath) {
			return bad("INVALID_DOWNLOAD_PATH", "下载器保存目录必须是绝对路径")
		}
		for _, m := range d.Mappings {
			if !filepath.IsAbs(m.From) || !filepath.IsAbs(m.To) {
				return bad("INVALID_MAPPING", "路径映射必须是绝对路径")
			}
		}
	}
	for _, root := range c.Roots {
		if root.ID == "" || seen["r"+root.ID] {
			return bad("DUPLICATE_ID", "媒体库ID重复或为空")
		}
		seen["r"+root.ID] = true
		if root.Kind != "movie" && root.Kind != "tv" && root.Kind != "mixed" {
			return bad("INVALID_ROOT", "媒体目录类型无效")
		}
		if e := safeRoot(root.Path); e != nil {
			return e
		}
	}
	for _, p := range c.AllowedRoots {
		if e := safeRoot(p); e != nil {
			return e
		}
	}
	return nil
}
func safeRoot(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" || strings.ContainsAny(p, "\x00\r\n") {
		return bad("INVALID_ROOT", "请选择具体的绝对媒体目录")
	}
	for _, blocked := range []string{"/etc", "/proc", "/sys", "/dev", "/var/run"} {
		if within(blocked, p) {
			return bad("INVALID_ROOT", "系统目录不能作为媒体目录")
		}
	}
	return nil
}
func (a *App) testConnection(ctx context.Context, target, key string) (any, error) {
	c := a.Settings()
	switch target {
	case "tmdb":
		v, e := a.catalog().Browse(ctx, "movie", "popular", 1)
		if e != nil {
			return nil, e
		}
		return map[string]any{"status": "ok", "samples": len(v.Results)}, nil
	case "site":
		s, e := a.site(key)
		if e != nil {
			return nil, e
		}
		result := integrations.ProbeSite(ctx, integrations.Site{ID: s.ID, Kind: s.Kind, BaseURL: s.BaseURL, Cookie: s.Cookie, APIKey: s.APIKey, Query: ""})
		return result, nil
	case "downloader":
		d, e := a.configuredDownloader(key)
		if e != nil {
			return nil, e
		}
		return integrations.ProbeDownloader(ctx, integrations.Downloader{ID: d.ID, Kind: d.Kind, BaseURL: d.BaseURL, Username: d.Username, Password: d.Password}), nil
	case "fnos":
		return fnos.Probe(ctx, fnos.Config{Host: c.FNOS.Host, Username: c.FNOS.Username, Password: c.FNOS.Password}), nil
	}
	return nil, bad("INVALID_TARGET", "未知的检测目标")
}
func (a *App) catalogAPI(r *http.Request, action string) (any, error) {
	q := r.URL.Query()
	ctx := r.Context()
	page := atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	kind := q.Get("kind")
	if kind == "" {
		kind = q.Get("media_type")
	}
	query := q.Get("q")
	if query == "" {
		query = q.Get("query")
	}
	var result any
	var cachedAt time.Time
	var e error
	switch action {
	case "browse", "search":
		var p catalog.Page
		key := action + ":" + kind + ":" + q.Get("list") + ":" + query + ":" + strconv.Itoa(page)
		ttl := time.Hour
		if q.Get("list") == "top_rated" {
			ttl = 6 * time.Hour
		}
		cachedAt, e = a.cached(ctx, key, ttl, &p, func() (any, error) {
			if action == "search" {
				return a.catalog().Search(ctx, query, kind, page)
			}
			return a.catalog().Browse(ctx, kind, q.Get("list"), page)
		})
		if e == nil {
			e = a.decorateMedia(ctx, p.Results)
		}
		result = map[string]any{"results": p.Results, "page": p.Page, "total_pages": p.TotalPages, "total_results": p.TotalResults, "has_more": p.Page < p.TotalPages, "cached_at": cachedAt}
	case "detail":
		var m catalog.Media
		m, e = a.media(ctx, kind, int64(atoi(q.Get("id"))))
		if e == nil {
			items := []catalog.Media{m}
			e = a.decorateMedia(ctx, items)
			m = items[0]
		}
		result = m
	case "season":
		var s catalog.Season
		s, e = a.season(ctx, int64(atoi(q.Get("id"))), atoi(q.Get("season")))
		result = s
	case "person":
		var p catalog.Person
		_, e = a.cached(ctx, "person:"+q.Get("id"), 7*24*time.Hour, &p, func() (any, error) { return a.catalog().Person(ctx, int64(atoi(q.Get("id")))) })
		result = p
	case "person/credits":
		var p catalog.Page
		_, e = a.cached(ctx, "credits:"+q.Get("id"), 24*time.Hour, &p, func() (any, error) { return a.catalog().PersonCredits(ctx, int64(atoi(q.Get("id")))) })
		result = p
	default:
		e = &APIError{"NOT_FOUND", "接口不存在", 404}
	}
	if action == "person/credits" && e == nil {
		p := result.(catalog.Page)
		e = a.decorateMedia(ctx, p.Results)
		result = p
	}
	return result, e
}
func (a *App) media(ctx context.Context, kind string, tmdb int64) (catalog.Media, error) {
	var m catalog.Media
	if !validMedia(kind, tmdb) {
		return m, bad("INVALID_MEDIA", "请选择明确的电影或剧集身份")
	}
	_, e := a.cached(ctx, "media:"+kind+":"+strconv.FormatInt(tmdb, 10), 24*time.Hour, &m, func() (any, error) { return a.catalog().Detail(ctx, kind, tmdb) })
	return m, e
}
func (a *App) season(ctx context.Context, tmdb int64, n int) (catalog.Season, error) {
	var s catalog.Season
	_, e := a.cached(ctx, fmt.Sprintf("season:%d:%d", tmdb, n), 12*time.Hour, &s, func() (any, error) { return a.catalog().Season(ctx, tmdb, n) })
	return s, e
}
