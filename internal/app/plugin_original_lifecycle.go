package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"sync"
	"time"
)

// initializeOriginalPlugin executes the already-ported plugin initializer and
// waits for that invocation, rather than reporting a queued initialization as
// a completed original init_plugin call. It adds no request timeout or retry.
// The existing worker owns its IPC and execution limits.
func (a *App) initializeOriginalPlugin(ctx context.Context, instance string) (PluginRun, error) {
	run, handled, err := a.enqueueOriginalCleanSeedInit(ctx, instance)
	if !handled {
		run, handled, err = a.enqueueOriginalWatcherInit(ctx, instance)
	}
	if !handled {
		run, err = a.pluginStore.EnqueueRun(ctx, instance, "init", nil)
	}
	if err != nil {
		return run, err
	}
	// Execute synchronously through the existing real SDK/host caller. A server
	// worker may have claimed this exact row first; only then observe its result.
	claimed, claimErr := a.pluginStore.claimSelectedRun(ctx, instance, run.ID)
	if claimErr == nil {
		if executeErr := a.executePluginRun(ctx, claimed); executeErr != nil {
			return claimed, executeErr
		}
	} else if !errors.Is(claimErr, sql.ErrNoRows) {
		return run, claimErr
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		current, e := a.pluginStore.Run(ctx, run.ID)
		if e != nil {
			return run, e
		}
		switch current.State {
		case "succeeded":
			return current, nil
		case "failed", "canceled", "cancelled", "reconcile_pending":
			return current, fmt.Errorf("plugin init_plugin failed: %s", current.ErrorCode)
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-tick.C:
		}
	}
}

// originalPluginInstance is the loaded Go calling object corresponding to the
// Python instance in _running_plugins. Configuration is read by each native
// initializer, including writes performed by the preceding initializer.
// It is ephemeral and never inferred from installed/desired_enabled flags.
type originalPluginInstance struct {
	app                  *App
	installation         PluginInstallation
	handler              plugins.Handler
	manifest             plugins.Manifest
	methodMu             sync.Mutex
	configMu             sync.RWMutex
	appliedConfig        json.RawMessage
	eventsEnabled        bool
	nativeState          any
	originalAbsentEngine any
	external             bool
	hostContract         string
	sessionMu            sync.Mutex
	session              *plugins.Client
	sessionStarted       bool
}

type originalInitializingObjectKey struct{}
type originalInitializationCall struct {
	object            *originalPluginInstance
	mu                sync.Mutex
	applied           json.RawMessage
	captured          bool
	nativeState       any
	nativeBefore      any
	configForInit     json.RawMessage
	configForInitRead bool
}

// Keep the actual Config object even when initialization throws after member
// mutation. A checkpoint decoder is not the original running object's getter.
func originalNativeStateBeforeInit(ctx context.Context) any {
	call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if !ok {
		return nil
	}
	return call.nativeBefore
}

func captureOriginalNativeState(ctx context.Context, value any) {
	call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if !ok {
		return
	}
	call.mu.Lock()
	call.nativeState = value
	call.mu.Unlock()
}

func captureOriginalAppliedConfiguration(ctx context.Context, value any) {
	call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if !ok {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	call.mu.Lock()
	call.applied = raw
	call.captured = true
	call.mu.Unlock()
}

func (p *originalPluginInstance) init(ctx context.Context) error {
	p.methodMu.Lock()
	defer p.methodMu.Unlock()
	p.configMu.RLock()
	previous := p.nativeState
	p.configMu.RUnlock()
	call := &originalInitializationCall{object: p, nativeBefore: previous}
	if p.hostContract == pluginBaseSDKContract {
		raw, _, err := p.app.readOriginalBaseConfigForInit(ctx, p.installation.InstanceID)
		if err != nil {
			return err
		}
		call.configForInit, call.configForInitRead = raw, true
	}
	run, err := p.app.initializeOriginalPlugin(context.WithValue(ctx, originalInitializingObjectKey{}, call), p.installation.InstanceID)
	if run.ID != "" {
		p.app.pluginOriginalInitializingRuns.Delete(run.ID)
		p.app.pluginStore.originalInitializingRuns.Delete(run.ID)
	}
	call.mu.Lock()
	captured, raw := call.captured, append(json.RawMessage(nil), call.applied...)
	nativeState := call.nativeState
	call.mu.Unlock()
	if !captured && err == nil {
		var invocation plugins.Invocation
		if decodeErr := json.Unmarshal(run.Input, &invocation); decodeErr != nil {
			return decodeErr
		}
		raw = append(json.RawMessage(nil), invocation.Config...)
	}
	// Keep actual partial native member mutation if direct init throws, as the
	// original object survives that exception. Start failures discard the object.
	if captured || err == nil {
		p.configMu.Lock()
		p.appliedConfig = raw
		p.nativeState = nativeState
		p.configMu.Unlock()
	}
	return err
}

func (a *App) startOriginalPlugin(ctx context.Context, instance string) *originalPluginInstance {
	in, err := a.originalDirectoryInstallation(ctx, instance)
	if err == nil && !in.DesiredEnabled {
		// The retired staging flag is an internal Go worker eligibility bit,
		// not the original plugin object's get_state. Installed disabled plugins
		// are initialized by original start too. Their configuration is untouched.
		_, err = a.Store.DB.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id=?`, instance)
		in.DesiredEnabled = err == nil
	}
	if err == nil {
		implementation, resolveErr := a.resolvePluginImplementation(ctx, in)
		manifest := implementation.Manifest
		_, handler, _ := builtin.Lookup(in.PluginID)
		if resolveErr != nil {
			return nil
		}
		meta, metadataErr := a.pluginStore.instanceMetadata(ctx, a.Store.DB, instance)
		if metadataErr != nil {
			return nil
		}
		a.pluginOriginalClasses.Store(instance, originalPluginClass{installation: in, isClone: meta.SourceInstanceID != ""})
		object := &originalPluginInstance{app: a, installation: in, handler: handler, manifest: manifest, external: implementation.External, hostContract: implementation.HostContract}
		err = object.init(ctx)
		if err != nil && object.external {
			_ = object.stopSession(context.WithoutCancel(ctx))
		}
		if err == nil {
			a.pluginOriginalRunningMu.Lock()
			if _, exists := a.pluginRunningInstances.Load(instance); !exists {
				a.pluginOriginalRunningOrder = append(a.pluginOriginalRunningOrder, instance)
			}
			a.pluginRunningInstances.Store(instance, object)
			a.pluginOriginalRunningMu.Unlock()
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
		if object.hostContract == pluginBaseSDKContract {
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

// stop follows the original loaded-object check. The Go worker is short-lived;
// its existing handler is the ported stop_service method, while cancellation
// and join close the instance's actual supervised work. Config/KV are untouched.
func (a *App) stopOriginalPlugin(ctx context.Context, instance string) error {
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		a.pluginOriginalClasses.Delete(instance)
		return nil
	}
	object := loaded.(*originalPluginInstance)

	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, instance); err != nil {
		a.log("WARN", "plugins", "stop_failed", "插件事件处理器停止失败", "", "")
		return err
	}
	object.configMu.Lock()
	object.eventsEnabled = false
	object.configMu.Unlock()
	if err := a.stopOriginalInstanceResources(ctx, instance, object.installation.PluginID); err != nil {
		a.log("WARN", "plugins", "stop_failed", "插件资源停止失败", "", "")
	}
	transition, err := a.pluginStore.runSupervisor.beginTransition([]string{instance})
	if err == nil {
		for _, attempt := range transition.join.attempts {
			if _, err = a.pluginStore.finishCanceledUnenteredAttempt(ctx, attempt); err != nil {
				break
			}
		}
		if err == nil {
			err = transition.Wait(ctx)
		}
		if err == nil {
			err = transition.releaseAfterResolution()
		}
		if err != nil {
			_ = transition.quarantine()
		}
	}
	if err != nil {
		a.log("WARN", "plugins", "stop_failed", "插件执行停止未完成", "", "")
	}
	func() {
		a.pluginOriginalRunningMu.Lock()
		a.pluginOriginalClasses.Delete(instance)
		a.pluginRunningInstances.Delete(instance)
		for n, id := range a.pluginOriginalRunningOrder {
			if id == instance {
				a.pluginOriginalRunningOrder = append(a.pluginOriginalRunningOrder[:n], a.pluginOriginalRunningOrder[n+1:]...)
				break
			}
		}
		a.pluginOriginalRunningMu.Unlock()
	}()
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
