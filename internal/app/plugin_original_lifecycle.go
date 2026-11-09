package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// originalPluginInstance is the loaded Go calling object corresponding to the
// Python instance in _running_plugins. It owns one persistent standard SDK
// session and reads manager configuration before each direct initializer.
// It is ephemeral and never inferred from installed/desired_enabled flags.
type originalPluginInstance struct {
	submitted             originalSubmittedState
	app                   *App
	installation          PluginInstallation
	manifest              plugins.Manifest
	methodMu              sync.Mutex
	stopping              atomic.Bool
	configMu              sync.RWMutex
	appliedConfig         json.RawMessage
	eventsEnabled         bool
	external              bool
	hostContract          string
	frontendPackageSHA256 atomic.Pointer[string] // verified resource identity may advance without replacing the backend object
	frontendReader        artifacts.FrontendReader
	sessionMu             sync.Mutex
	session               *plugins.Client
	sessionStarted        bool
	sourceLogSecrets      originalSourceLogSecrets
}

func (p *originalPluginInstance) frontendPackageHash() string {
	value := p.frontendPackageSHA256.Load()
	if value == nil {
		return ""
	}
	return *value
}
func (p *originalPluginInstance) setFrontendPackageHash(value string) {
	p.frontendPackageSHA256.Store(&value)
}

type originalInitializingObjectKey struct{}
type originalInitializationCall struct {
	object            *originalPluginInstance
	configurationSave bool
	config            json.RawMessage
	active            atomic.Bool
	retire            atomic.Bool
}

// Keep the actual Config object even when initialization throws after member
// mutation. A checkpoint decoder is not the original running object's getter.

// The manager calls the loaded implementation directly. Initialization is not
// a queued job and has no config-revision, receipt or recovery prerequisite.
func (p *originalPluginInstance) init(ctx context.Context) error {
	unlock := p.lockOriginalMethod()
	defer unlock()
	if !p.external {
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
	}
	config, err := p.app.originalManagerConfiguration(ctx, p.installation.InstanceID)
	if err != nil {
		return err
	}
	initCtx := ctx
	if originalLiveInitialization(ctx, p) == nil {
		var endInit func()
		initCtx, endInit = p.beginOriginalInitialization(ctx, false)
		defer endInit()
	}
	err = p.callOriginalInitializer(initCtx, config)
	if err == nil {
		p.recordOriginalAppliedConfig(config)
	}
	return err
}

func (p *originalPluginInstance) recordOriginalAppliedConfig(config json.RawMessage) {
	p.configMu.Lock()
	defer p.configMu.Unlock()
	if (p.installation.PluginID == "AutoBackup" || p.installation.PluginID == "CloudflareSpeedTest") &&
		len(p.appliedConfig) > 0 && !originalBaseJSONTruthy(config) {
		return // These warm initializers retain their previous fields.
	}
	p.appliedConfig = copyOriginalBaseJSON(config)
}

func (p *originalPluginInstance) callOriginalInitializer(ctx context.Context, config json.RawMessage) error {
	// Only this actual initializer call gets the lease. A reentrant ordinary
	// event named "init" cannot inherit the configuration-save origin.
	initialization := originalLiveInitialization(ctx, p)
	if initialization != nil && (p.installation.PluginID == "ScrapeFileClean" || p.installation.PluginID == "CleanInvalidSeed" ||
		p.installation.PluginID == "CloudflareSpeedTest" || originalCustomHostsInstallation(p.installation)) {
		// Initializer-bound resources and admitted private callbacks use the
		// exact host input before init has published the applied configuration.
		initialization.config = copyOriginalBaseJSON(config)
	}
	_, err := p.callSessionBound(ctx, plugins.Runner{Timeout: 10 * time.Second}, "init", config, nil, initialization)
	return err
}

func (a *App) startOriginalPlugin(ctx context.Context, instance string) *originalPluginInstance {
	in, err := a.originalDirectoryInstallation(ctx, instance)

	if err == nil {
		implementation, resolveErr := a.resolvePluginImplementation(ctx, in)
		if resolveErr != nil {
			return nil
		}
		if !implementation.External {
			a.log("WARN", "plugins", "adapter_unavailable", "插件尚未适配新的标准模块，未加载旧执行器", "", "")
			return nil
		}
		object, class, err := a.prepareOriginalPluginObject(ctx, in, implementation)
		if err == nil {
			return a.startPreparedOriginalPlugin(ctx, object, class)
		}
	}
	// Original start catches per-plugin init errors and leaves no running object.
	a.log("ERROR", "plugins", "load_failed", "插件初始化失败，实例未加载", "", "")
	return nil
}

// Startup imports the full fixed class list before any initializer. Preparing
// these finite objects does not publish classes or run business initializers.
func (a *App) prepareOriginalPluginObject(ctx context.Context, in PluginInstallation, implementation pluginImplementation) (*originalPluginInstance, originalPluginClass, error) {
	meta, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, in.InstanceID)
	if err != nil {
		return nil, originalPluginClass{}, err
	}
	class := originalPluginClass{installation: in, isClone: meta.SourceInstanceID != "", external: implementation.External}
	object := &originalPluginInstance{app: a, installation: in, manifest: implementation.Manifest, external: implementation.External, hostContract: implementation.HostContract, frontendReader: implementation.frontendReader}
	object.setFrontendPackageHash(implementation.PackageSHA256)
	return object, class, nil
}
func (a *App) startPreparedOriginalPlugin(ctx context.Context, object *originalPluginInstance, class originalPluginClass) *originalPluginInstance {
	instance := object.installation.InstanceID
	a.storeOriginalPluginClass(instance, class)
	a.registerOriginalObjectChainHandlers(object)
	err := object.init(ctx)
	if err != nil {
		object.finishOriginalInitialization(false)
		a.log("ERROR", "plugins", "load_failed", "插件初始化失败，实例未加载", "", "")
		return nil
	}
	// Source start unconditionally inserts the pre-imported object after init,
	// even if init removed its own class, code or installed membership.
	a.pluginOriginalRunningMu.Lock()
	if _, exists := a.pluginRunningInstances.Load(instance); !exists {
		a.pluginOriginalRunningOrder = append(a.pluginOriginalRunningOrder, instance)
	}
	a.pluginRunningInstances.Store(instance, object)
	a.pluginOriginalRunningMu.Unlock()
	object.finishOriginalInitialization(true)
	if err = a.updateOriginalPluginEventState(ctx, object); err != nil {
		a.log("ERROR", "plugins", "load_failed", "插件事件状态设置失败", "", "")
		return nil
	}
	return object
}

// Clone uses the same reload lifecycle, then repeats the source initializer
// when its copied configuration is nonempty.
func (a *App) initializeOriginalCloneCalls(ctx context.Context, instance string) error {
	object, reloadErr := a.reloadOriginalPlugin(ctx, instance)
	if reloadErr != nil {
		return reloadErr
	}
	if object != nil {
		config, err := a.pluginStore.Config(ctx, instance)
		if object.hostContract == pluginBaseSDKContract || object.hostContract == pluginResourcesSDKContract {
			var present bool
			config.Value, present, err = a.readOriginalBaseConfigForInit(ctx, instance)
			if err == nil && !present {
				err = sql.ErrNoRows
			}
		}
		if err != nil && !missingPluginValue(err) {
			return err
		}
		if err == nil {
			var value map[string]json.RawMessage
			if err = json.Unmarshal(config.Value, &value); err != nil {
				return err
			}
			if len(value) != 0 {
				// A direct init exception does not remove the already loaded object.
				if err = object.init(ctx); err != nil {
					return err
				}
			}
		}
	}
	// Endpoint reload replaces the prior running object, even after failed start.
	if _, err := a.reloadOriginalPlugin(ctx, instance); err != nil {
		return err
	}
	a.registerOriginalPlugin(ctx, instance)
	return nil
}

// Only the manager decides stop order. Client.StopWithHost owns the actual
// process shutdown/join; no legacy queue transition can veto this method.
func (a *App) stopOriginalPlugin(ctx context.Context, instance string) error {
	a.log("INFO", "plugins", "plugin_stopping", a.clean("正在停止插件 "+instance+"..."), "", "")
	return a.stopOriginalPluginObject(ctx, instance)
}

// Global stop has one original manager INFO header; object cleanup is shared.
func (a *App) stopOriginalPluginObject(ctx context.Context, instance string) error {
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		a.stopOriginalUnpublishedSubmissions(ctx, instance)
		a.forgetOriginalPluginClass(instance)
		return nil
	}
	object := loaded.(*originalPluginInstance)
	retainedPublic := object.beginOriginalPublicServiceStop()
	drainedPrivate := object.drainOriginalPrivateScheduler(context.WithoutCancel(ctx))
	if !drainedPrivate {
		object.stopping.Store(true)
	}
	if retainedPublic {
		object.closeOriginalUnretainedSubmissions()
	} else {
		object.closeOriginalSubmissions()
	}
	object.configMu.Lock()
	object.eventsEnabled = false
	object.configMu.Unlock()
	stopParent := ctx
	if drainedPrivate {
		stopParent = context.WithoutCancel(ctx)
	}
	stopCtx, cancel := context.WithTimeout(stopParent, 10*time.Second)
	defer cancel()
	if retainedPublic {
		if !drainedPrivate {
			if err := object.stopOriginalPublicServiceMethods(stopCtx); err != nil {
				a.log("ERROR", "plugins", "stop_method_failed", "插件停止方法失败", "", "")
			}
		}
	} else {
		if err := object.stopOriginalMethods(stopCtx); err != nil {
			a.log("ERROR", "plugins", "stop_method_failed", "插件停止方法失败", "", "")
		}
		if err := object.stopSession(stopCtx); err != nil {
			a.log("ERROR", "plugins", "stop_failed", "插件停止方法失败", "", "")
		}
	}
	a.pluginOriginalRunningMu.Lock()
	a.forgetOriginalPluginClassLocked(instance)
	a.pluginRunningInstances.Delete(instance)
	for i, id := range a.pluginOriginalRunningOrder {
		if id == instance {
			a.pluginOriginalRunningOrder = append(a.pluginOriginalRunningOrder[:i], a.pluginOriginalRunningOrder[i+1:]...)
			break
		}
	}
	a.pluginOriginalRunningMu.Unlock()
	if retainedPublic {
		object.finishOriginalPublicServiceStop()
	}
	return nil
}

func (a *App) registerOriginalPluginAPIs(instance string) {
	// Original registration removes old routes even when start failed and no
	// replacement running instance exists. Business disabled is not a filter.
	a.pluginRegisteredAPIs.Delete(instance)
	if loaded, ok := a.pluginRunningInstances.Load(instance); ok {
		object := loaded.(*originalPluginInstance)
		apis, err := object.getAPIs(context.Background())
		if err != nil {
			a.log("ERROR", "plugins", "register_apis_failed", "读取插件API失败", "", "")
			return
		}
		// Route declarations are data, not a grant of native host capabilities.
		// Like add_api_route's per-item try/except, a bad declaration must not
		// discard valid routes before or after it. Keep the SDK's collision and
		// size checks while publishing only individually accepted declarations.
		declaration := object.manifest
		declaration.APIs = nil
		for _, api := range apis {
			if api.Auth == "" {
				api.Auth = "apikey"
			}
			candidate := declaration
			candidate.APIs = append(append([]plugins.HTTPAPI(nil), declaration.APIs...), api)
			if err := candidate.Validate(); err != nil {
				a.log("ERROR", "plugins", "register_apis_failed", "插件API声明无效，已跳过该接口", "", "")
				continue
			}
			declaration.APIs = candidate.APIs
		}
		a.pluginRegisteredAPIs.Store(instance, declaration.APIs)
	}
}

// reload_plugin: stop, start, enqueue PluginReload. The broadcast handlers do
// not complete synchronously before this method returns in the original.
func (a *App) reloadOriginalPlugin(ctx context.Context, instance string) (*originalPluginInstance, error) {
	if err := a.stopOriginalPlugin(ctx, instance); err != nil {
		return nil, err
	}
	a.startOriginalPlugin(ctx, instance)
	a.emitOriginalPluginReload(instance)
	// Original reload returns no object; its caller reads the running map after
	// start catches errors, including failures after the object was stored.
	if loaded, ok := a.pluginRunningInstances.Load(instance); ok {
		return loaded.(*originalPluginInstance), nil
	}
	return nil, nil
}
