package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/media/words"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
	"light-media/internal/catalog"
	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

type App struct {
	originalFNOSRefresh              originalFNOSRefreshRuntime
	originalPluginTemporary          originalPluginTemporaryState
	pluginOriginalSourceSites        originalSourceSites
	pluginOriginalSourceConnections  originalSourceConnections
	originalHostBrowserHumanize      bool
	originalHostBrowserHumanPreset   string
	pluginOriginalBase               originalBaseState
	pluginOriginalLarkTestOrigin     string       // Set only by an owned App fixture before Start.
	seedRescueIYUUOrigin             string       // Set only by an owned App fixture before Start.
	cloudflareTestClient             *http.Client // Owned fixture only; never selected by plugin input.
	pluginCustomHostsTestPath        string       // Owned fixture only; never selected by plugin input.
	pluginOriginalSiteInteractions   originalSlashInteractions
	pluginOriginalSubscribes         originalSlashInteractions
	originalChatRSSInvalid           originalChatRSSInvalidEnclosures
	pluginOriginalChatMedia          originalChatMediaInteractions
	originalLocalBackupMu            sync.Mutex
	pluginOriginalMarkets            originalMarketCache
	pluginOriginalInput              originalPluginInputRuntime
	pluginOriginalNotice             originalNoticeRuntime
	pluginImplementationMu           sync.RWMutex
	pluginOriginalInitializingRuns   sync.Map
	pluginOriginalEventsMu           sync.Mutex
	pluginOriginalChainHandlers      map[string][]originalMutableEventHandler
	pluginOriginalReloadQueue        []string
	pluginOriginalEventDispatching   bool
	pluginOriginalRunningMu          sync.Mutex
	pluginOriginalRunningOrder       []string
	pluginOriginalCommandsMu         sync.Mutex
	pluginOriginalCommands           map[string]map[string]any
	pluginOriginalRegisteredCommands map[string]json.RawMessage
	pluginOriginalServicesMu         sync.Mutex
	pluginOriginalBackground         originalBackgroundAdmission
	pluginOriginalUserUID            atomic.Pointer[string]
	pluginOriginalDirectJobs         originalDirectJobs
	pluginRegisteredAPIs             sync.Map // Exact instance routes refreshed by original register_plugin_api.
	pluginOriginalClasses            sync.Map // Original _plugins: imported compiled class, independent of running and installed.
	pluginOriginalClassOrder         []string // guarded by pluginOriginalRunningMu; source _plugins insertion order
	pluginSubmittedObjects           sync.Map // Exact objects held by accepted asynchronous calls, including failed init.
	pluginRunningInstances           sync.Map // Original _running_plugins: successful initialized call objects, not business enable state.
	systemMessages                   systemMessageRuntime
	bigMemoryMode                    bool               // Trusted startup option; not worker or plugin configuration.
	safeMode                         bool               // Recovery startup skips automatic plugin and background execution.
	pluginDeveloperMode              bool               // Immutable DEV startup option, scoped to plugin local monitoring.
	pluginMarketOverride             *[]string          // Immutable process environment override; never persisted by the settings UI.
	brushRoots                       []string           // Trusted mounts for brush file checks; never exposed as media roots.
	pluginLogRotation                *PluginLogRotation // Source ordinary log environment overrides, fixed for this process.
	resourceSearchPages              *big.Int           // Trusted ordinary SEARCH_RESOURCE_PAGES, normalized at main startup.
	originalSearchKeywordMu          sync.Mutex
	originalSearchKeywordNext        time.Time
	originalSearchKeywordOverflow    bool
	mediaSearchConfiguration         *MediaSearchConfiguration
	transferExtensions               nativeTransferExtensionRuntime
	subscriptionStats                subscriptionStatsRuntime
	recognitionMu                    sync.Mutex
	recognitionMatcher               atomic.Pointer[words.Matcher]
	directoryFileMu                  sync.Mutex
	RestartRequested                 chan struct{}
	logs                             *runtimeLogger
	logError                         string
	catMu                            sync.Mutex
	mediaCategories                  mediaCategoryRuntime
	catClient                        *catalog.Client
	catCreds                         TMDBConfig
	catProxy                         networkproxy.Config
	metadataProxyMu                  sync.Mutex
	metadataProxyConfig              networkproxy.Config
	metadataProxyClient              *http.Client
	Store                            *Store
	Dir                              string
	web                              http.Handler
	frontendVersion                  string
	cfgMu                            sync.RWMutex
	settingsMu                       sync.Mutex
	originalHostOCRHost              string // Immutable ordinary OCR_HOST startup snapshot; never exposed to workers.
	originalHostBrowserEmulation     string
	originalHostFlareSolverrURL      string
	originalHostSettingsMu           sync.Mutex
	originalHostSettingsValues       map[string]json.RawMessage
	originalHostMediaRecognition     *HostMediaRecognitionConfiguration
	originalHostMediaCache           originalHostMediaRecognitionCache
	originalHostMediaProviderCaches  sync.Map
	originalHostDouban               originalHostDoubanState
	config                           Settings
	controlMu                        sync.Mutex
	control                          string
	activeWrites                     int
	logMu                            sync.Mutex
	authMu                           sync.Mutex
	pluginOriginalAuth               originalAuthRuntime
	authAttempts                     map[string][]time.Time
	jobWake                          chan struct{}
	wg                               sync.WaitGroup
	importMu                         sync.Mutex
	importSourceLink                 func(int, string, int, string, int) error // Owned syscall-failure seam; nil uses actual Linkat.
	importAutoCommit                 func(*sql.Tx) error                       // Owned lost-ack seam; nil uses actual Commit.
	subMu                            sync.Mutex
	downloadMu                       sync.Mutex
	materialMu                       sync.Mutex
	materials                        map[string][]byte
	siteLocks                        sync.Map
	cancel                           context.CancelFunc
	pluginMu                         sync.Mutex
	// Serialize original endpoint operations separately from live SDK calls.
	// Shutdown may join workers and must not hold a callback snapshot lock.
	pluginOriginalOperationMu sync.Mutex
	pluginOriginalBroadcast   originalBroadcastRuntime
	pluginLifecycleMu         sync.Mutex
	pluginStore               *PluginStore
	pluginWake                chan struct{}
	pluginProcess             chan struct{}
	pluginCommand             func(string) (plugins.Command, error)
}

type Options struct {
	PluginTemporaryFileDays *big.Int // Trusted normalized startup snapshot; nil reads process environment.
	PluginLogRotation       *PluginLogRotation
	ResourceSearchPages     *big.Int
	MediaSearch             *MediaSearchConfiguration
	HostMediaRecognition    *HostMediaRecognitionConfiguration
	PluginDeveloperMode     bool      // Original DEV: plugin monitoring only; no other business debug switches.
	BigMemoryMode           bool      // Original BIG_MEMORY_MODE pool selection, fixed for this App lifetime.
	SafeMode                bool      // Management remains available without automatic background tasks.
	PluginMarketsOverride   *[]string // nil means no process override; an empty list is an explicit empty override.
	DataDir                 string
	Web                     http.Handler
	FrontendVersion         string
}

const appVersion = "0.4.19"

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
	a := &App{pluginDeveloperMode: opts.PluginDeveloperMode, bigMemoryMode: opts.BigMemoryMode, safeMode: opts.SafeMode, RestartRequested: make(chan struct{}, 1), Store: s, Dir: opts.DataDir, web: opts.Web, control: "running", authAttempts: map[string][]time.Time{}, jobWake: make(chan struct{}, 1), materials: map[string][]byte{}}
	a.frontendVersion = opts.FrontendVersion
	a.originalHostDouban.searchStamp = time.Now().Format("20060102")
	a.originalHostOCRHost = originalHostOCRStartupHost(os.LookupEnv)
	a.originalHostBrowserEmulation, a.originalHostFlareSolverrURL = originalHostBrowserStartup(os.LookupEnv)
	a.originalHostBrowserHumanize, a.originalHostBrowserHumanPreset, e = originalHostBrowserHumanStartup(os.LookupEnv)
	if e != nil {
		_ = s.Close()
		return nil, e
	}
	a.originalHostMediaRecognition = cloneHostMediaRecognitionConfiguration(opts.HostMediaRecognition)
	if a.originalHostMediaRecognition == nil {
		a.originalHostMediaRecognition, e = HostMediaRecognitionEnvironment(os.LookupEnv)
		if e != nil {
			_ = s.Close()
			return nil, e
		}
	}
	a.pluginLogRotation = clonePluginLogRotation(opts.PluginLogRotation)
	a.mediaSearchConfiguration = cloneMediaSearchConfiguration(opts.MediaSearch)
	if a.mediaSearchConfiguration == nil {
		a.mediaSearchConfiguration, _ = MediaSearchEnvironment(os.LookupEnv)
	}
	if opts.ResourceSearchPages != nil {
		a.resourceSearchPages = new(big.Int).Set(opts.ResourceSearchPages)
	}
	if opts.PluginMarketsOverride != nil {
		sources := append([]string{}, (*opts.PluginMarketsOverride)...)
		a.pluginMarketOverride = &sources
	}
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
	if e = a.originalHostInitializeMediaExtensions(os.LookupEnv); e != nil {
		_ = s.Close()
		return nil, e
	}
	// Earlier releases imported Rousi as generic NexusPHP. Select its native
	// adapter on load while preserving the saved site ID, origin and credentials.
	// The encrypted configuration is left intact until the user next saves it.
	for i := range a.config.Sites {
		site := &a.config.Sites[i]
		if site.Kind == "nexusphp" {
			if kind, _, _, err := siteImportOrigin(site.BaseURL); err == nil && kind == "rousi" {
				site.Kind = "rousi"
			}
		}
	}
	if e = a.bootstrapNativeSiteIdentities(context.Background()); e != nil {
		s.Close()
		return nil, e
	}
	a.pluginStore, e = NewPluginStore(context.Background(), s)
	if e == nil {
		e = a.loadRecognitionWords(context.Background())
	}
	if e != nil {
		s.Close()
		return nil, e
	}
	a.pluginWake = make(chan struct{}, 1)
	a.pluginProcess = make(chan struct{}, 1)
	// The global default is fixed; explicit directory and manual choices remain independent.
	a.config.AutoImportMode = "hardlink"
	if e = mountedMediaRoots(&a.config, a.Dir, os.Getenv("LIGHT_MEDIA_MEDIA_ROOTS")); e != nil {
		s.Close()
		return nil, e
	}
	if a.brushRoots, e = mountedDirectoryRoots(a.Dir, os.Getenv("LIGHT_MEDIA_BRUSH_ROOTS"), "LIGHT_MEDIA_BRUSH_ROOTS"); e != nil {
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
	a.originalHostMediaCache.mu.Lock()
	e = a.originalHostMediaCacheLoadLocked(context.Background())
	a.originalHostMediaCache.mu.Unlock()
	if e != nil {
		a.log("ERROR", "metadata", "recognize_cache_restore_failed", "恢复媒体识别缓存失败", "", "")
	}
	if e = a.initializeOriginalPluginTemporary(opts.PluginTemporaryFileDays); e != nil {
		a.logs.close()
		_ = s.Close()
		return nil, e
	}
	// Match the original CategoryHelper process lifetime. Syntax failures start
	// empty on a cold process; a later failed/empty reload retains these maps.
	_, _ = a.currentMediaCategoryPolicy()
	return a, nil
}
func (a *App) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	if a.safeMode {
		a.log("INFO", "runtime", "safe_mode", "安全模式已启动：跳过插件加载、定时任务、监控和自动同步，管理接口仍可使用", "", "")
		return
	}
	if err := a.restoreOriginalPluginCode(ctx); err != nil {
		a.log("ERROR", "plugins", "code_restore_failed", "恢复插件代码备份失败，继续加载现有插件", "", "")
	}
	if err := a.initializeOriginalSourceConnections(ctx, nil); err != nil {
		a.log("ERROR", "downloaders", "source_connection_initialize_failed", "下载器模块连接初始化中断", "", "")
	}
	a.initializeOriginalCommonService(time.Now())
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		a.log("ERROR", "plugins", "initialize_failed", "恢复插件初始化失败，请查看插件执行记录", "", "")
	}
	a.completeOriginalChatRestart(ctx)
	if err := a.recoverOriginalScrapeBatches(ctx); err != nil {
		a.log("ERROR", "import", "scrape_batch_recovery_failed", "整理刮削批次恢复失败，请检查任务状态", "", "")
	}
	for i := 0; i < 2; i++ {
		a.wg.Add(1)
		go func() { defer a.wg.Done(); a.worker(ctx) }()
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.scheduler(ctx) }()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.runOriginalPluginScheduler(ctx) }()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.runOriginalLocalRepositoryWatcher(ctx) }()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.runOriginalMarketRefresh(ctx) }()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.runOriginalNoticeQueue(ctx) }()
	if a.admitOriginalBackground() {
		go func() { defer a.wg.Done(); a.synchronizeOriginalPluginsAtStartup(ctx) }()
	}
	a.log("INFO", "runtime", "started", "服务已启动", "", "")
	a.controlMu.Lock()
	initialControl := a.control
	a.controlMu.Unlock()
	a.log("INFO", "runtime", "recovery_checked", fmt.Sprintf("数据库版本1；启动恢复%d个未完成任务；自动化状态%s", a.Store.RecoveredJobs, initialControl), "", "")
}
func (a *App) Close() error {
	// Source Scheduler.stop joins accepted public services while their ordinary
	// manager/notice/continuation authority is still available.
	a.drainOriginalPluginScheduler()
	backupPluginCode := func() {
		if !a.safeMode {
			if err := a.backupOriginalPluginCode(context.Background()); err != nil {
				a.log("ERROR", "plugins", "code_backup_failed", "备份插件代码失败，继续停止插件", "", "")
			}
		}
	}
	privateOwners := a.beginOriginalPrivateSchedulerDrain()
	boundOwners := a.beginOriginalBoundEntriesShutdown()
	if privateOwners || boundOwners {
		a.pluginOriginalOperationMu.Lock()
		backupPluginCode()
		a.pluginOriginalOperationMu.Unlock()
		a.closeOriginalSessions()
	}
	if boundOwners {
		a.drainOriginalBoundEvents()
	}
	a.closeOriginalBackgroundAdmission()
	if a.cancel != nil {
		a.cancel()
	}
	a.pluginOriginalInput.clear()
	a.stopOriginalNoticeQueue()
	// Join any package update before closing its loaded object. Both automatic
	// synchronizers recheck sealed admission under this same operation lock.
	a.pluginOriginalOperationMu.Lock()
	if !privateOwners && !boundOwners {
		backupPluginCode()
		a.closeOriginalSessions()
	}
	a.pluginOriginalOperationMu.Unlock()
	a.stopOriginalUnpublishedSubmissions(context.Background(), "")
	a.systemMessages.close()
	a.stopSubscriptionStatistics()
	a.wg.Wait()
	if err := a.saveOriginalHostMediaCache(context.Background()); err != nil {
		a.log("ERROR", "metadata", "recognize_cache_save_failed", "保存媒体识别缓存失败", "", "")
	}
	a.closeSubscriptionStatisticsHTTP()
	a.log("INFO", "runtime", "stopped", "服务已停止", "", "")
	a.catMu.Lock()
	if a.catClient != nil {
		a.catClient.CloseIdleConnections()
	}
	a.catMu.Unlock()
	a.metadataProxyMu.Lock()
	if a.metadataProxyClient != nil {
		a.metadataProxyClient.CloseIdleConnections()
	}
	a.metadataProxyMu.Unlock()
	// Source stop_modules disposes its DB before clear_temp. This Go logger
	// persists shutdown diagnostics in Store, so keep only logger/store open
	// until this host-owned step completes; all plugin/callback joins precede it.
	if err := a.clearOriginalPluginTemporary(); err != nil {
		a.log("ERROR", "plugins", "temp_cleanup_failed", fmt.Sprintf("清理插件共享临时目录失败，继续关闭服务: %v", err), "", "")
	}
	a.logs.close()
	return a.Store.Close()
}
func (a *App) Settings() Settings {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	b, _ := json.Marshal(a.config)
	var c Settings
	_ = json.Unmarshal(b, &c)
	if c.PluginServerURL == nil {
		value := originalPluginDefaultServerURL(os.LookupEnv)
		c.PluginServerURL = &value
	}
	if a.pluginMarketOverride != nil {
		c.PluginMarkets = append([]string{}, (*a.pluginMarketOverride)...)
	}
	return c
}
func (a *App) saveSettings(ctx context.Context, c Settings) error {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	return a.saveSettingsWithEffect(ctx, c, nil)
}

// Callers hold settingsMu. The existing download/import lock order also binds
// plugin site changes to the same publication boundary as all other settings.
func (a *App) saveSettingsWithEffect(ctx context.Context, c Settings, effect func(context.Context, *sql.Tx, *Settings) error) error {
	if a.pluginMarketOverride != nil {
		a.cfgMu.RLock()
		c.PluginMarkets = append([]string(nil), a.config.PluginMarkets...)
		a.cfgMu.RUnlock()
	}
	if err := normalizeProxySettings(&c); err != nil {
		return err
	}
	c.DebugUntil = ""
	c.AutoImportMode = "hardlink"
	for i := range c.Downloaders {
		c.Downloaders[i].APIKey = strings.TrimSpace(c.Downloaders[i].APIKey)
		if c.Downloaders[i].ClearAPIKey {
			c.Downloaders[i].APIKey = ""
		}
		c.Downloaders[i].ClearAPIKey = false
	}
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	previous := a.Settings()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if effect != nil {
		if err := effect(ctx, tx, &c); err != nil {
			return err
		}
	}
	for i := range c.Sites {
		if c.Sites[i].Enabled {
			if err := normalizeManagedSite(&c.Sites[i]); err != nil {
				return err
			}
		}
	}
	if err := validateSettings(c); err != nil {
		return err
	}
	if err := a.reconcileNativeSiteIdentitiesTx(ctx, tx, previous, c); err != nil {
		return err
	}
	if err := a.pruneDeletedCoreSiteReferencesTx(ctx, tx, previous, &c); err != nil {
		return err
	}
	if err := validateSettings(c); err != nil {
		return err
	}
	if err := bumpPriorityRuleRevisions(ctx, tx, previous.PriorityRules, c.PriorityRules); err != nil {
		return err
	}
	sealed, err := a.Store.Seal(c)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO records VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, sealed, now()); err != nil {
		return err
	}
	for _, site := range c.Sites {
		for _, old := range previous.Sites {
			if old.ID == site.ID && a.managedSiteKey(old) != a.managedSiteKey(site) {
				if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE kind='site_cooldown' AND id=?`, site.ID); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.cfgMu.Lock()
	a.config = c
	a.cfgMu.Unlock()
	a.reloadInitializedOriginalSourceConnections(ctx, previous.Downloaders, c.Downloaders)
	a.emitStandardSiteDeletions(ctx, previous, c)
	a.emitStandardPluginSettingsChanged(ctx, previous, a.Settings())
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
	secrets := []string{c.TMDB.APIKey, c.TMDB.AccessToken, c.FNOS.Password, c.APIToken}
	secrets = append(secrets, proxySecrets(c.ProxyHost)...)
	secrets = append(secrets, c.GitHubToken, c.RepoGitHubToken)
	for _, pair := range strings.Split(c.RepoGitHubToken, ",") {
		if parts := strings.Split(pair, ":"); len(parts) == 2 {
			secrets = append(secrets, strings.TrimSpace(parts[1]))
		}
	}
	for _, x := range c.Sites {
		secrets = append(secrets, x.Cookie, x.APIKey, x.Token, x.RSS, string(x.Note))
	}
	for _, x := range c.Downloaders {
		secrets = append(secrets, x.Password, x.APIKey)
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
	settings := a.Settings()
	c := settings.TMDB
	proxy, err := networkproxy.Resolve(settings.ProxyHost, os.Getenv)
	if err != nil {
		proxy = networkproxy.Config{HTTP: "invalid", HTTPS: "invalid"}
	}
	a.catMu.Lock()
	defer a.catMu.Unlock()
	if a.catClient == nil || a.catCreds != c || a.catProxy != proxy {
		if a.catClient != nil {
			a.catClient.CloseIdleConnections()
		}
		a.catClient = catalog.NewWithProxy(catalog.Credentials{APIKey: c.APIKey, AccessToken: c.AccessToken}, proxy)
		a.catCreds = c
		a.catProxy = proxy
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
