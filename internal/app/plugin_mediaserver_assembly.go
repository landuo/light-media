package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// MediaServerFactories contains the six real constructors, not configuration
// proxies or a map with missing entries silently skipped. This is a trusted Go
// assembly input, never an HTTP/worker configuration or a new user setup step.
type MediaServerFactories struct {
	Emby, Jellyfin, Plex, TrimeMedia, Ugreen, ZSpace mediaserverruntime.Factory
}

// The builder receives one real host Current function for all private clients.
// It must resolve concrete dependencies; absent crypto/session/profile adapters
// return errors, never an always-ready vendor. Native default assembly is a
// separate required composition step while those concrete dependencies finish.
type MediaServerFactoryAssembly func(*App, func(context.Context) error) (MediaServerFactories, error)

func (f MediaServerFactories) definitions() ([]mediaserverruntime.Definition, error) {
	out := []mediaserverruntime.Definition{
		{ModuleID: "EmbyModule", ServiceName: "emby", Factory: f.Emby},
		{ModuleID: "JellyfinModule", ServiceName: "jellyfin", Factory: f.Jellyfin},
		{ModuleID: "PlexModule", ServiceName: "plex", Factory: f.Plex},
		{ModuleID: "TrimeMediaModule", ServiceName: "trimemedia", Factory: f.TrimeMedia},
		{ModuleID: "UgreenModule", ServiceName: "ugreen", Factory: f.Ugreen},
		{ModuleID: "ZSpaceModule", ServiceName: "zspace", Factory: f.ZSpace},
	}
	for _, d := range out {
		if d.Factory == nil {
			return nil, errors.New("media server assembly requires all six concrete factories")
		}
	}
	return out, nil
}

type originalMediaServerLifecycleKey struct{}
type originalMediaServerLifecycleCall struct {
	owner    *originalMediaServerAssembly
	shutdown bool
}
type originalMediaServerAssembly struct {
	app         *App
	manager     *mediaserverruntime.Manager
	host        *originalMediaServerHost
	definitions []mediaserverruntime.Definition
	// Global explicit rebuild/close excludes owner callbacks. Separate owner
	// callbacks retain their own original per-owner lock and may run concurrently;
	// Services queries never take this lock or turn prefix publication atomic.
	operations               sync.RWMutex
	mu                       sync.RWMutex
	phase                    string
	root                     context.Context
	cancel                   context.CancelFunc
	waitingForExplicitConfig bool
}

func newOriginalMediaServerAssembly(a *App, build MediaServerFactoryAssembly) (*originalMediaServerAssembly, error) {
	if a == nil || a.originalMediaServerConfig == nil || build == nil {
		return nil, errors.New("media server assembly lacks sealed store or concrete factory builder")
	}
	h := &originalMediaServerAssembly{app: a, phase: "prepared"}
	factories, err := build(a, h.current)
	if err != nil {
		return nil, err
	}
	h.definitions, err = factories.definitions()
	if err != nil {
		return nil, err
	}
	read := func(ctx context.Context) (json.RawMessage, error) {
		if err := h.current(ctx); err != nil {
			return nil, err
		}
		// get owns and releases its short cache lock before returning this copy.
		value, err := a.originalMediaServerConfig.get(ctx)
		if current := h.current(ctx); current != nil {
			return nil, current
		}
		return value, err
	}
	reportConfig := func(d plugins.MediaServerConfigDiagnostic) {
		// The type contains only index/class/field/kind, never credential values.
		raw, err := json.Marshal(d)
		if err != nil {
			return
		}
		level := "WARN"
		if d.Severity == "error" {
			level = "ERROR"
		}
		a.log(level, "mediaservers", "config_model", string(raw), "", "")
	}
	rows, err := mediaserverruntime.NewRawConfigRows(read, reportConfig)
	if err != nil {
		return nil, err
	}
	report := func(d mediaserverruntime.Diagnostic) {
		raw, err := json.Marshal(d)
		if err != nil {
			return
		}
		level := "DEBUG"
		if d.Class != "" {
			level = "ERROR"
		}
		a.log(level, "mediaservers", "module_lifecycle", string(raw), "", "")
	}
	reloaded := func(ctx context.Context) error {
		if err := h.current(ctx); err != nil {
			return err
		}
		// This is the real existing non-durable event queue. No config/credential
		// value is copied into the ModuleReload event or ordinary listeners.
		event, err := originalNewEvent("module.reload", json.RawMessage(`{}`), nil)
		if err != nil {
			return err
		}
		if !a.enqueueOriginalBroadcast(ctx, event) {
			return bad("APP_STOPPING", "服务正在停止")
		}
		return nil
	}
	h.manager, err = mediaserverruntime.NewManager(h.definitions, rows, report, reloaded)
	if err != nil {
		return nil, err
	}
	services, err := mediaserverruntime.NewServices(h.manager, read, reportConfig)
	if err != nil {
		return nil, err
	}
	h.host, err = newOriginalMediaServerHost(services)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// Lifecycle tokens are private identity objects. They cannot be supplied by
// worker JSON, old retained callbacks or an arbitrary context.Background().
// Ordinary vendor operations use the bridge's fresh plugin invocation guard.
func (h *originalMediaServerAssembly) current(ctx context.Context) error {
	if ctx == nil {
		return bad("MEDIA_SERVER_CALL_CLOSED", "媒体服务器调用缺少上下文")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.RLock()
	phase, root := h.phase, h.root
	h.mu.RUnlock()
	token, internal := ctx.Value(originalMediaServerLifecycleKey{}).(*originalMediaServerLifecycleCall)
	if internal && token != nil && token.owner == h {
		if token.shutdown {
			if phase != "closing" {
				return bad("MEDIA_SERVER_CALL_CLOSED", "媒体服务器关闭调用已结束")
			}
			return ctx.Err()
		}
		if phase != "running" || root == nil {
			return bad("MEDIA_SERVER_CALL_CLOSED", "媒体服务器生命周期未运行")
		}
		if err := root.Err(); err != nil {
			return err
		}
		return ctx.Err()
	}
	if phase != "running" || root == nil {
		return bad("MEDIA_SERVER_CALL_CLOSED", "媒体服务器生命周期未运行")
	}
	if err := root.Err(); err != nil {
		return err
	}
	return originalMediaServerOperationCurrent(ctx)
}
func (h *originalMediaServerAssembly) lifecycleContext(ctx context.Context, shutdown bool) context.Context {
	return context.WithValue(ctx, originalMediaServerLifecycleKey{}, &originalMediaServerLifecycleCall{owner: h, shutdown: shutdown})
}

// Link caller cancellation and the real App lifetime without changing process
// globals or reusing a completed constructor invocation as later IO authority.
func (h *originalMediaServerAssembly) operationContext(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("media lifecycle operation requires context")
	}
	h.mu.RLock()
	root := h.root
	h.mu.RUnlock()
	if root == nil {
		return nil, nil, bad("MEDIA_SERVER_CALL_CLOSED", "媒体服务器生命周期未运行")
	}
	linked, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(root, cancel)
	release := func() { stop(); cancel() }
	linked = h.lifecycleContext(linked, false)
	if err := h.current(linked); err != nil {
		release()
		return nil, nil, err
	}
	return linked, release, nil
}
func (h *originalMediaServerAssembly) start(parent context.Context) error {
	if parent == nil {
		return errors.New("media server startup requires parent context")
	}
	if err := parent.Err(); err != nil {
		return err
	}
	h.operations.Lock()
	defer h.operations.Unlock()
	h.mu.Lock()
	if h.phase != "prepared" {
		h.mu.Unlock()
		return errors.New("media server assembly already started or closed")
	}
	h.root, h.cancel = context.WithCancel(parent)
	h.phase = "running"
	root := h.root
	h.mu.Unlock()
	// Publish the SAME Services/Manager, not snapshots or a replacement registry.
	h.app.pluginMu.Lock()
	h.app.originalMediaServerInstances = h.host
	h.app.pluginMu.Unlock()
	ctx := h.lifecycleContext(root, false)
	if _, err := h.app.originalMediaServerConfig.get(ctx); err != nil {
		if boundary, ok := err.(*APIError); ok && boundary.Code == "MEDIA_SERVER_CONFIG_NOT_ESTABLISHED" {
			// Unknown migration origin is not invented source None. The already-owned
			// admin producer can establish it; the first committed event performs Load.
			h.mu.Lock()
			h.waitingForExplicitConfig = true
			h.mu.Unlock()
		}
		return err
	}
	return h.manager.Load(ctx)
}
func (h *originalMediaServerAssembly) reload(ctx context.Context) error {
	h.operations.Lock()
	defer h.operations.Unlock()
	ctx, release, err := h.operationContext(ctx)
	if err != nil {
		return err
	}
	defer release()
	h.mu.RLock()
	waiting := h.waitingForExplicitConfig
	h.mu.RUnlock()
	if waiting {
		if _, err := h.app.originalMediaServerConfig.get(ctx); err != nil {
			return err
		}
		h.mu.Lock()
		h.waitingForExplicitConfig = false
		h.mu.Unlock()
		return h.manager.Load(ctx)
	}
	return h.manager.Reload(ctx)
}

func (h *originalMediaServerAssembly) configChanged(ctx context.Context, moduleID string, keys []string) error {
	h.operations.RLock()
	defer h.operations.RUnlock()
	ctx, release, err := h.operationContext(ctx)
	if err != nil {
		return err
	}
	defer release()
	// Resolve the current running owner when the callback actually executes,
	// matching EventManager module lookup rather than capturing a stale owner.
	owner := h.manager.RunningModule(moduleID)
	if owner == nil {
		return nil
	}
	return owner.HandleConfigChanged(ctx, true, keys)
}
func (h *originalMediaServerAssembly) close() error {
	h.mu.Lock()
	if h.phase == "closed" || h.phase == "closing" {
		h.mu.Unlock()
		return nil
	}
	previous := h.phase
	h.phase = "closing"
	cancel := h.cancel
	h.mu.Unlock()
	// Stop admitting normal lifecycle/SDK work and cancel its actual parent-bound
	// IO before joining it. Shutdown has its own explicit cleanup admission, as
	// App.Close's other owned cleanup does; no operation cancellation is retried.
	if cancel != nil {
		cancel()
	}
	h.operations.Lock()
	defer h.operations.Unlock()
	var err error
	if previous == "running" {
		err = h.manager.Stop(h.lifecycleContext(context.Background(), true))
	}
	h.mu.Lock()
	h.phase = "closed"
	h.mu.Unlock()
	return err
}

func (a *App) startOriginalMediaServerAssembly(ctx context.Context) {
	if a.originalMediaServerAssembly == nil {
		return
	} // Explicitly unassembled; SDK still returns its existing boundary.
	if err := a.originalMediaServerAssembly.start(ctx); err != nil {
		a.log("ERROR", "mediaservers", "startup_failed", "媒体服务器实例初始化未完成", "", "")
	}
}
func (a *App) closeOriginalMediaServerAssembly() {
	if a.originalMediaServerAssembly == nil {
		return
	}
	if err := a.originalMediaServerAssembly.close(); err != nil {
		a.log("ERROR", "mediaservers", "stop_failed", "媒体服务器实例停止未完成", "", "")
	}
}

// reload is host-only. It does not create an arbitrary worker/system API.
func (a *App) reloadOriginalMediaServerAssembly(ctx context.Context) error {
	if a.originalMediaServerAssembly == nil {
		return bad("MEDIA_SERVER_INSTANCES_NOT_ESTABLISHED", "媒体服务器实例尚未装配")
	}
	return a.originalMediaServerAssembly.reload(ctx)
}

// Called by the actual core broadcast dispatcher, not directly by the setter.
// Safe public event values do not contain credentials; every owner reads the
// committed sealed fact through its raw reader after selecting MediaServers.
func (a *App) submitOriginalMediaServerConfigChanged(ctx context.Context, raw json.RawMessage) {
	h := a.originalMediaServerAssembly
	if h == nil {
		return
	}
	var event struct {
		Keys []string `json:"key"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	matched := false
	for _, key := range event.Keys {
		if key == originalMediaServerConfigKey {
			matched = true
			break
		}
	}
	if !matched {
		return
	}
	h.mu.RLock()
	waiting := h.waitingForExplicitConfig
	h.mu.RUnlock()
	if waiting {
		// A previously unknown origin has no fabricated running owners. Exactly one
		// ordinary queued callback activates the first real Load under the write lock.
		if !a.admitOriginalBackground() {
			return
		}
		keys := append([]string(nil), event.Keys...)
		go func() {
			defer a.wg.Done()
			activated, err := h.activateExplicitConfig(ctx)
			if err != nil {
				a.log("ERROR", "mediaservers", "config_load_failed", "媒体服务器配置初始化未完成", "", "")
				return
			}
			if !activated {
				a.submitOriginalMediaServerOwnerChanges(ctx, h, keys)
			}
		}()
		return
	}
	a.submitOriginalMediaServerOwnerChanges(ctx, h, event.Keys)
}
func (a *App) submitOriginalMediaServerOwnerChanges(ctx context.Context, h *originalMediaServerAssembly, changedKeys []string) {
	for _, definition := range h.definitions {
		if !a.admitOriginalBackground() {
			return
		}
		id := definition.ModuleID
		keys := append([]string(nil), changedKeys...)
		go func() {
			defer a.wg.Done()
			if err := h.configChanged(ctx, id, keys); err != nil {
				a.log("ERROR", "mediaservers", "config_reload_failed", "媒体服务器配置重载未完成", "", "")
			}
		}()
	}
}
func (h *originalMediaServerAssembly) activateExplicitConfig(ctx context.Context) (bool, error) {
	h.operations.Lock()
	defer h.operations.Unlock()
	ctx, release, err := h.operationContext(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	h.mu.RLock()
	waiting := h.waitingForExplicitConfig
	h.mu.RUnlock()
	// A second event queued before the first Load must still dispatch its own
	// owner callbacks after this lock releases; it is not a deduplication key.
	if !waiting {
		return false, nil
	}
	if _, err := h.app.originalMediaServerConfig.get(ctx); err != nil {
		return false, err
	}
	h.mu.Lock()
	h.waitingForExplicitConfig = false
	h.mu.Unlock()
	return true, h.manager.Load(ctx)
}
