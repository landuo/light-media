package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/torrenttransfer"
)

type pluginRuntime struct {
	core      *Core
	transport *APITransport
	private   *privateSchedule
	cache     *FileTorrentCache
	metadata  runtimeMetadataSlot

	lifetimeMu sync.Mutex
	active     chan struct{}
	closeOnce  sync.Once
}

// Plugin owns one instance's configuration, helper, and volatile recheck state.
// Each method constructs its host adapter from the current SDK callback.
type Plugin struct {
	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	runtime     *pluginRuntime
	generation  uint64
	nextPass    uint64
	passes      map[uint64]*hostPass
	now         func() time.Time
}

func NewPlugin() *Plugin {
	core, _ := newCore(NewConfig(), nil, nil)
	return &Plugin{runtime: newPluginRuntime(core), passes: make(map[uint64]*hostPass), now: time.Now}
}

func (p *Plugin) current() *pluginRuntime {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.runtime
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, p.init(ctx, caller, raw)
	case "get_state", "get_service":
		return p.current().metadataResponse(method)
	case "get_form":
		return formFromHost(ctx, caller)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_page", "get_api", "get_command", "get_dashboard", "get_dashboard_meta", "get_auth_providers", "get_module", "close":
		return json.RawMessage(`null`), nil
	case "auto_seed":
		return nil, p.run(ctx, p.current(), caller, false, time.Time{})
	case "pass_continue":
		var input passPhaseRequest
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return nil, p.continueHostPass(ctx, caller, input)
	case "site_deleted":
		p.lifecycleMu.Lock()
		defer p.lifecycleMu.Unlock()
		runtime := p.current()
		core := runtime.core
		core.runMu.Lock()
		defer core.runMu.Unlock()
		defer runtime.publishMetadata()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, siteDeletedFromHost(ctx, caller, &core.config, raw)
	case "private_scheduler":
		var input schedulerCall
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return nil, p.runPrivate(ctx, caller, input)
	case "stop", "stop_service":
		p.lifecycleMu.Lock()
		defer p.lifecycleMu.Unlock()
		return nil, p.current().stop(ctx)
	default:
		return nil, errors.New("unsupported IYUU plugin method")
	}
}

func publicServices(config Config) (json.RawMessage, error) {
	jobs, err := config.Services()
	if err != nil {
		return nil, err
	}
	declarations := make([]any, 0, len(jobs))
	for _, job := range jobs {
		// Validate the five-field syntax with the existing APScheduler port.
		// The host resolves local_timezone when it registers the declaration.
		if _, err := torrenttransfer.ParseCrontab(job.Cron, "UTC"); err != nil {
			return nil, err
		}
		declarations = append(declarations, map[string]any{
			"id": job.ID, "name": job.Name, "handler": "auto_seed", "kwargs": map[string]any{},
			"trigger": map[string]any{"kind": job.Kind, "cron": job.Cron, "local_timezone": true},
		})
	}
	return json.Marshal(declarations)
}

func (p *Plugin) init(ctx context.Context, caller sdk.HostCaller, raw json.RawMessage) error {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	old := p.current()
	// Keep the first write before stopping the old schedule. Serialize it with
	// any active pass so the previous cache cannot overwrite the new config.
	old.core.runMu.Lock()
	if err := ctx.Err(); err != nil {
		old.core.runMu.Unlock()
		return err
	}
	err := prepareConfigFromHost(ctx, caller, &old.core.config, raw)
	var core *Core
	if err == nil {
		core, err = newCore(old.core.config, nil, nil)
	}
	if err == nil {
		// Source init clears the queue and checked hashes, but retains the
		// running guard for the lifetime of the same loaded plugin object.
		core.recheckRunning = old.core.recheckRunning
		old.core.Stop()
	}
	old.publishMetadata()
	old.core.runMu.Unlock()
	if err != nil {
		return err
	}
	if err := old.stop(ctx); err != nil {
		return err
	}

	next := newPluginRuntime(core)
	if core.config.ShouldSchedule() {
		err = p.prepareSchedule(ctx, caller, next)
		if err != nil {
			_ = next.stop(ctx)
		}
	}
	p.mu.Lock()
	p.runtime = next
	p.mu.Unlock()
	if err != nil || next.private == nil {
		return err
	}
	err = next.private.submit(ctx, caller, 0)
	if err != nil {
		_ = next.stop(ctx)
	}
	return err
}

func (p *Plugin) prepareSchedule(ctx context.Context, caller sdk.HostCaller, next *pluginRuntime) error {
	core := next.core
	token, err := core.config.String("token")
	if err != nil {
		return err
	}
	next.transport, err = newHostAPITransport(ctx, caller, token, core.guard)
	if err != nil {
		return err
	}
	core.api, err = NewHelper(next.transport, p.now)
	if err != nil {
		return err
	}
	location, err := privateSchedulerLocation()
	if err != nil {
		return err
	}

	p.generation++
	next.private = newPrivateSchedule(p.generation)
	if core.config.OnlyOnce() {
		ctx = bindSourceLog(ctx, caller, token)
		sourceLog(ctx, 156, "辅种服务启动，立即运行一次")
		next.private.onceDue = p.now().In(location).Add(3 * time.Second)
		core.config.ConsumeOnlyOnce()
	}
	core.config.ClearCacheFlag()
	if err := (sdkInstanceHost{caller: caller, skipRaw: core.config.skipRaw}).SaveConfig(ctx, core.config.values); err != nil {
		return err
	}
	next.cache, err = openSourceTorrentCache(ctx, caller)
	if err != nil {
		return err
	}
	next.private.checkDue = p.now().In(location).Add(privateCheckInterval)
	return nil
}

func (p *Plugin) run(ctx context.Context, runtime *pluginRuntime, caller sdk.HostCaller, recheck bool, due time.Time) error {
	core := runtime.core
	core.runMu.Lock()
	defer core.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !due.IsZero() && p.now().Sub(due) > privateMisfireGrace {
		return nil
	}
	runtime.lifetimeMu.Lock()
	if core.stopped.Load() || core.api == nil {
		runtime.lifetimeMu.Unlock()
		return nil
	}
	runtime.active = make(chan struct{})
	runtime.lifetimeMu.Unlock()
	defer func() {
		runtime.lifetimeMu.Lock()
		close(runtime.active)
		runtime.active = nil
		runtime.lifetimeMu.Unlock()
	}()
	token, err := core.config.String("token")
	if err != nil {
		return err
	}
	return p.withHostPass(ctx, caller, func(owner context.Context, host sdk.HostCaller) error {
		owner = bindSourceLog(owner, host, token)
		core.host = newRuntimeHost(host, runtime.cache, core.config.skipRaw)
		defer func() {
			core.host = nil
			flushSourceLogRepeats(owner, host)
		}()
		if recheck {
			return core.checkRecheck(owner)
		}
		return core.autoSeed(owner)
	})
}
