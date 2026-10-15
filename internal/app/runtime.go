package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"light-media/internal/catalog"
)

type App struct {
	directoryFileMu  sync.Mutex
	RestartRequested chan struct{}
	logs             *runtimeLogger
	logError         string
	catMu            sync.Mutex
	catClient        *catalog.Client
	catCreds         TMDBConfig
	Store            *Store
	Dir              string
	web              http.Handler
	cfgMu            sync.RWMutex
	settingsMu       sync.Mutex
	config           Settings
	controlMu        sync.Mutex
	control          string
	activeWrites     int
	logMu            sync.Mutex
	authMu           sync.Mutex
	authAttempts     map[string][]time.Time
	jobWake          chan struct{}
	wg               sync.WaitGroup
	importMu         sync.Mutex
	subMu            sync.Mutex
	downloadMu       sync.Mutex
	materialMu       sync.Mutex
	materials        map[string][]byte
	siteLocks        sync.Map
	cancel           context.CancelFunc
}
type Options struct {
	DataDir string
	Web     http.Handler
}

const appVersion = "0.3.2"

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string        { return e.Message }
func bad(code, message string) *APIError { return &APIError{Code: code, Message: message, Status: 400} }
func New(opts Options) (*App, error) {
	s, e := OpenStore(opts.DataDir)
	if e != nil {
		return nil, e
	}
	a := &App{RestartRequested: make(chan struct{}, 1), Store: s, Dir: opts.DataDir, web: opts.Web, control: "running", authAttempts: map[string][]time.Time{}, jobWake: make(chan struct{}, 1), materials: map[string][]byte{}}
	var raw []byte
	e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&raw)
	if e == nil {
		e = s.Unseal(raw, &a.config)
	} else if errors.Is(e, sql.ErrNoRows) {
		e = nil
		a.config = Settings{Sites: []SiteConfig{}, Downloaders: []DownloaderConfig{}, Roots: []Root{}, AllowedRoots: []string{}}
	}
	if e != nil {
		s.Close()
		return nil, e
	}
	if e = mountedMediaRoots(&a.config, a.Dir, os.Getenv("LIGHT_MEDIA_MEDIA_ROOTS")); e != nil {
		s.Close()
		return nil, e
	}
	var control struct {
		State string `json:"state"`
	}
	if s.Get(context.Background(), "control", "global", &control) == nil && control.State != "running" {
		a.control = "paused"
	}
	if e = os.MkdirAll(filepath.Join(a.Dir, "logs"), 0700); e != nil {
		s.Close()
		return nil, e
	}
	// Temporary diagnostics are intentionally never restored from persisted settings.
	for i := range a.config.Downloaders {
		a.config.Downloaders[i].SharedPaths = true
	}
	a.config.DebugUntil = ""
	if a.config.LogLevel == "DEBUG" {
		a.config.LogLevel = "INFO"
	}
	a.logs, e = newRuntimeLogger(a)
	if e != nil {
		s.Close()
		return nil, e
	}
	return a, nil
}
func (a *App) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	for i := 0; i < 2; i++ {
		a.wg.Add(1)
		go func() { defer a.wg.Done(); a.worker(ctx) }()
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.scheduler(ctx) }()
	a.log("INFO", "runtime", "started", "服务已启动", "", "")
	a.controlMu.Lock()
	initialControl := a.control
	a.controlMu.Unlock()
	a.log("INFO", "runtime", "recovery_checked", fmt.Sprintf("数据库版本1；启动恢复%d个未完成任务；自动化状态%s", a.Store.RecoveredJobs, initialControl), "", "")
}
func (a *App) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	a.log("INFO", "runtime", "stopped", "服务已停止", "", "")
	a.logs.close()
	return a.Store.Close()
}
func (a *App) Settings() Settings {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	b, _ := json.Marshal(a.config)
	var c Settings
	_ = json.Unmarshal(b, &c)
	return c
}
func (a *App) saveSettings(ctx context.Context, c Settings) error {
	c.DebugUntil = "" // Use process-local, expiring module/site overrides.
	for i := range c.Sites {
		if c.Sites[i].Enabled {
			if err := normalizeManagedSite(&c.Sites[i]); err != nil {
				return err
			}
		}
	}
	if e := validateSettings(c); e != nil {
		return e
	}
	sealed, e := a.Store.Seal(c)
	if e != nil {
		return e
	}
	// Callers serialize read-modify-write with settingsMu. Publish destination
	// changes in the same lock order as download submission and file import:
	// a successful save cannot race an Add that already passed its snapshot
	// check, nor an import publishing to a previously reviewed root.
	// Validation and encryption above perform no network requests; site data
	// refresh never takes these locks.
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if e = ctx.Err(); e != nil {
		return e
	}
	previous := a.Settings()
	_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO records VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, sealed, now())
	if e != nil {
		return e
	}
	a.cfgMu.Lock()
	a.config = c
	a.cfgMu.Unlock()
	for _, site := range c.Sites {
		for _, old := range previous.Sites {
			if old.ID == site.ID && (old.BaseURL != site.BaseURL || old.Cookie != site.Cookie || old.APIKey != site.APIKey) {
				_, _ = a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='site_cooldown' AND id=?`, site.ID)
			}
		}
	}
	a.log("INFO", "settings", "updated", "连接和路径设置已更新", "", "")
	return nil
}
func (a *App) paused() bool {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	return a.control != "running"
}
func (a *App) beginWrite() (func(), error) {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if a.control != "running" {
		return nil, &APIError{"AUTOMATION_PAUSED", "自动化已暂停，请先恢复后执行写操作", 409}
	}
	a.activeWrites++
	return func() {
		a.controlMu.Lock()
		defer a.controlMu.Unlock()
		a.activeWrites--
		if a.control == "pausing" && a.activeWrites == 0 {
			a.control = "paused"
			_ = a.Store.Put(context.Background(), "control", "global", map[string]string{"state": "paused"})
		}
	}, nil
}
func (a *App) setControl(ctx context.Context, pause bool) error {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	state := "running"
	if pause {
		state = "paused"
		if a.activeWrites > 0 {
			state = "pausing"
		}
	}
	if e := a.Store.Put(ctx, "control", "global", map[string]string{"state": state}); e != nil {
		return e
	}
	a.control = state
	if !pause {
		_, _ = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='queued',run_at=unixepoch() WHERE state='waiting_paused'`)
		select {
		case a.jobWake <- struct{}{}:
		default:
		}
	}
	a.log("INFO", "control", state, "全局自动化状态已更新", "", "")
	return nil
}

var urlPattern = regexp.MustCompile(`(?i)https?://[^\s"<>]+`)

func cleanWithSettings(s string, c Settings) string {
	secrets := []string{c.TMDB.APIKey, c.TMDB.AccessToken, c.FNOS.Password}
	for _, x := range c.Sites {
		secrets = append(secrets, x.Cookie, x.APIKey)
	}
	for _, x := range c.Downloaders {
		secrets = append(secrets, x.Password)
	}
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return urlPattern.ReplaceAllString(s, "[URL]")
}
func (a *App) clean(s string) string {
	return boundedLogString(cleanWithSettings(s, a.Settings()), 2048)
}
func (a *App) log(level, module, event, message, workflow, job string) {
	entry := map[string]any{"time": now(), "id": id("log_"), "level": level, "module": module, "event": event, "message": message, "app_version": appVersion, "workflow_id": workflow, "job_id": job}
	a.writeLog(entry)
}
func (a *App) writeLog(entry map[string]any) {
	if a.logs != nil {
		a.logs.enqueue(entry)
	}
}
func (a *App) event(ctx context.Context, j Job, level, module, event, message string) {
	message = a.clean(message)
	_, _ = a.Store.DB.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), j.WorkflowID, j.ID, level, module, event, message)
	a.log(level, module, event, message, j.WorkflowID, j.ID)
}

// Kept for internal callers; HTTP uses the explicit cursor/coverage response.
func (a *App) readLogs(q map[string]string) ([]map[string]any, error) {
	page, err := a.queryLogs(context.Background(), q)
	return page.Entries, err
}
func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (a *App) hasAdmin() bool {
	var n int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM admin`).Scan(&n)
	return n > 0
}
func (a *App) auth(w http.ResponseWriter, r *http.Request) (bool, error) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if e := decodeBody(w, r, &in); e != nil {
		return false, e
	}
	if len(in.Username) < 1 || len(in.Username) > 64 || len(in.Password) < 10 || len(in.Password) > 72 {
		return false, bad("INVALID_CREDENTIALS", "用户名需为1–64字节，密码需为10–72字节")
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.authMu.Lock()
	ts := a.authAttempts[host]
	valid := ts[:0]
	for _, t := range ts {
		if time.Since(t) < 15*time.Minute {
			valid = append(valid, t)
		}
	}
	if len(valid) >= 10 {
		a.authMu.Unlock()
		return false, &APIError{"RATE_LIMITED", "登录尝试过多，请15分钟后重试", 429}
	}
	// Reserve an attempt before bcrypt so concurrent requests cannot bypass the
	// limit. Only failed attempts remain after a successful authentication.
	a.authAttempts[host] = append(valid, time.Now())
	if len(a.authAttempts) > 1000 {
		a.authAttempts = map[string][]time.Time{host: a.authAttempts[host]}
	}
	a.authMu.Unlock()
	if r.URL.Path == "/api/auth/setup" {
		hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if e != nil {
			return false, e
		}
		if _, e = a.Store.DB.ExecContext(r.Context(), `INSERT INTO admin(id,username,password_hash) VALUES(1,?,?)`, in.Username, hash); e != nil {
			return false, &APIError{"ALREADY_CONFIGURED", "管理员已创建，请登录", 409}
		}
	} else {
		var name string
		var hash []byte
		e := a.Store.DB.QueryRowContext(r.Context(), `SELECT username,password_hash FROM admin WHERE id=1`).Scan(&name, &hash)
		if e != nil || name != in.Username || bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) != nil {
			return false, &APIError{"UNAUTHORIZED", "用户名或密码不正确", 401}
		}
	}
	token := id("")
	_, e := a.Store.DB.ExecContext(r.Context(), `INSERT INTO sessions VALUES(?,?)`, tokenHash(token), time.Now().Add(7*24*time.Hour).Unix())
	if e != nil {
		return false, e
	}
	a.authMu.Lock()
	delete(a.authAttempts, host)
	a.authMu.Unlock()
	http.SetCookie(w, sessionCookie(r, token, 604800))
	a.log("INFO", "auth", "login", "管理员已登录", "", "")
	return true, nil
}
func (a *App) authenticated(r *http.Request) bool {
	cookie, e := r.Cookie(sessionCookie(r, "", 0).Name)
	if e != nil || len(cookie.Value) > 128 {
		return false
	}
	var expires int64
	e = a.Store.DB.QueryRowContext(r.Context(), `SELECT expires_at FROM sessions WHERE token_hash=?`, tokenHash(cookie.Value)).Scan(&expires)
	return e == nil && expires > time.Now().Unix()
}
func (a *App) cached(ctx context.Context, key string, ttl time.Duration, dst any, fetch func() (any, error)) (time.Time, error) {
	var c struct {
		At   time.Time       `json:"at"`
		Data json.RawMessage `json:"data"`
	}
	e := a.Store.Get(ctx, "cache", key, &c)
	cached := e == nil
	if cached && time.Since(c.At) < ttl {
		return c.At, json.Unmarshal(c.Data, dst)
	}
	value, e := fetch()
	if e != nil {
		if cached {
			a.log("WARN", "catalog", "cache_stale", "元数据服务不可用，使用历史缓存", "", "")
			return c.At, json.Unmarshal(c.Data, dst)
		}
		return time.Time{}, e
	}
	b, e := json.Marshal(value)
	if e != nil {
		return time.Time{}, e
	}
	c.At = time.Now().UTC()
	c.Data = b
	if e = a.Store.Put(ctx, "cache", key, c); e != nil {
		return time.Time{}, e
	}
	return c.At, json.Unmarshal(b, dst)
}
func (a *App) catalog() *catalog.Client {
	c := a.Settings().TMDB
	a.catMu.Lock()
	defer a.catMu.Unlock()
	if a.catClient == nil || a.catCreds != c {
		a.catClient = catalog.New(catalog.Credentials{APIKey: c.APIKey, AccessToken: c.AccessToken})
		a.catCreds = c
	}
	return a.catClient
}

func (a *App) recentLogs(job string) ([]map[string]any, error) {
	entries, _, _, _ := a.logs.snapshot(0)
	out := []map[string]any{}
	for i := len(entries) - 1; i >= 0; i-- {
		var entry map[string]any
		if json.Unmarshal(entries[i].Data, &entry) == nil && (job == "" || entry["job_id"] == job) {
			out = append(out, entry)
		}
	}
	return out, nil
}
