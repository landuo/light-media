package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins"
	"sync"
	"sync/atomic"
	"time"
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
	frontendPackageSHA256 string // real installed content identity captured for this loaded object
	sessionMu             sync.Mutex
	session               *plugins.Client
	sessionStarted        bool
}

type originalInitializingObjectKey struct{}
type originalInitializationCall struct{ object *originalPluginInstance }

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
	call := &originalInitializationCall{object: p}
	_, err = p.callSession(context.WithValue(ctx, originalInitializingObjectKey{}, call), plugins.Runner{Timeout: 10 * time.Second}, "init", config, nil)
	if err == nil {
		p.configMu.Lock()
		p.appliedConfig = append(json.RawMessage(nil), config...)
		p.configMu.Unlock()
	}
	return err
}

func (a *App) startOriginalPlugin(ctx context.Context, instance string) *originalPluginInstance {
	in, err := a.originalDirectoryInstallation(ctx, instance)

	if err == nil {
		implementation, resolveErr := a.resolvePluginImplementation(ctx, in)
		manifest := implementation.Manifest
		if resolveErr != nil {
			return nil
		}
		if !implementation.External {
			a.log("WARN", "plugins", "adapter_unavailable", "插件尚未适配新的标准模块，未加载旧执行器", "", "")
			return nil
		}
		meta, metadataErr := a.pluginStore.instanceMetadata(ctx, a.Store.DB, instance)
		if metadataErr != nil {
			return nil
		}
		a.pluginOriginalClasses.Store(instance, originalPluginClass{installation: in, isClone: meta.SourceInstanceID != ""})
		object := &originalPluginInstance{app: a, installation: in, manifest: manifest, external: implementation.External, hostContract: implementation.HostContract, frontendPackageSHA256: implementation.PackageSHA256}
		err = object.init(ctx)
		if err != nil && object.external {
			object.finishOriginalInitialization(false)
		}
		if err == nil {
			a.pluginOriginalRunningMu.Lock()
			if _, exists := a.pluginRunningInstances.Load(instance); !exists {
				a.pluginOriginalRunningOrder = append(a.pluginOriginalRunningOrder, instance)
			}
			a.pluginRunningInstances.Store(instance, object)
			a.pluginOriginalRunningMu.Unlock()
			a.registerOriginalObjectResetHandlers(object)
			object.finishOriginalInitialization(true)
			// Original start stores the object before get_state/event registration.
			// A later exception is caught without rolling back that stored object.
			if err = a.updateOriginalPluginEventState(ctx, object); err != nil {
				a.log("ERROR", "plugins", "load_failed", "插件事件状态设置失败", "", "")
				return nil
			}
			return object
		}
	}
	// Original start catches per-plugin init errors and leaves no running object.
	a.log("ERROR", "plugins", "load_failed", "插件初始化失败，实例未加载", "", "")
	return nil
}

// This initializer slice is connected to the actual Clone HTTP endpoint.
// Full stop_service/reload-event/register translation remains the next slice;
// these calls alone are not represented as complete original reload behavior.
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
	a.unregisterOriginalObjectResetHandlers(instance)
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		a.stopOriginalUnpublishedSubmissions(ctx, instance)
		a.pluginOriginalClasses.Delete(instance)
		return nil
	}
	object := loaded.(*originalPluginInstance)
	object.stopping.Store(true)
	object.closeOriginalSubmissions()
	object.configMu.Lock()
	object.eventsEnabled = false
	object.configMu.Unlock()
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := object.stopOriginalMethods(stopCtx); err != nil {
		a.log("ERROR", "plugins", "stop_method_failed", "插件停止方法失败", "", "")
	}
	if err := object.stopSession(stopCtx); err != nil {
		a.log("ERROR", "plugins", "stop_failed", "插件停止方法失败", "", "")
	}
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	a.pluginOriginalClasses.Delete(instance)
	a.pluginRunningInstances.Delete(instance)
	for i, id := range a.pluginOriginalRunningOrder {
		if id == instance {
			a.pluginOriginalRunningOrder = append(a.pluginOriginalRunningOrder[:i], a.pluginOriginalRunningOrder[i+1:]...)
			break
		}
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
		for i := range apis {
			if apis[i].Auth == "" {
				apis[i].Auth = "apikey"
			}
		}
		// Route declarations are data, not a grant of native host capabilities.
		// The existing instance URL dispatcher supplies the instance prefix.
		declaration := object.manifest
		declaration.APIs = apis
		if err := declaration.Validate(); err != nil {
			a.log("ERROR", "plugins", "register_apis_failed", "插件API声明无效", "", "")
			return
		}
		a.pluginRegisteredAPIs.Store(instance, apis)
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
