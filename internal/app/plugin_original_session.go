package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"slices"
	"strings"
	"time"
)

// The loaded object owns one negotiated standard process. Package resolution
// holds the implementation lock only while resolving/starting it, never during
// a potentially long business method. Every invocation has fresh authority.
func (p *originalPluginInstance) callSession(ctx context.Context, runner plugins.Runner, method string, payload json.RawMessage, host plugins.HostHandler) (plugins.RunResult, error) {
	return p.callSessionBound(ctx, runner, method, payload, host, nil)
}

func (p *originalPluginInstance) callSessionBound(ctx context.Context, runner plugins.Runner, method string, payload json.RawMessage, host plugins.HostHandler, initialization *originalInitializationCall) (plugins.RunResult, error) {
	p.inflight.Add(1)
	defer p.inflight.Add(-1)
	// The periodic market status probe is bookkeeping, not use: it keeps the
	// inflight guard (a retire never races the read) but must not reset the
	// idle clock, or the 30-minute TTL could never elapse.
	if ctx.Value(originalMarketProbeKey{}) == nil {
		p.lastUsed.Store(time.Now().UnixNano())
	}
	var client plugins.Session
	if p.originalPublicServiceScopeAllowed(ctx) {
		lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
		client = lease.client
	} else {
		if err := p.ensureOriginalSessionReady(ctx, runner, method); err != nil {
			return plugins.RunResult{}, err
		}
		var err error
		client, err = p.originalSessionClient(ctx, runner)
		if err != nil {
			return plugins.RunResult{}, err
		}
	}
	// A fixed plugin method belongs to its loaded object. Disconnecting one
	// HTTP caller does not stop that object. Legacy serial RPC has no per-call
	// cancellation frame: aborting mid-exchange would kill the persistent worker.
	// Once admitted, drain that bounded exchange; its session/application stop
	// and runner budget still apply. Concurrent SDKs can cancel only this call.
	if !client.SupportsConcurrent() {
		if err := ctx.Err(); err != nil {
			return plugins.RunResult{}, err
		}
		ctx = context.WithoutCancel(ctx)
	}
	callback, closePhase := p.originalBaseSessionHostBound(ctx, method, host, initialization)
	defer closePhase()
	options := plugins.CallOptions{Timeout: runner.Timeout, ExcludeHostTime: runner.ExcludeHostTime}
	if client.SupportsConcurrent() {
		options = plugins.CallOptions{CallerDeadlineOnly: true}
	}
	return client.CallWithOptions(ctx, method, payload, callback, options)
}

func (p *originalPluginInstance) originalSessionClient(ctx context.Context, runner plugins.Runner) (plugins.Session, error) {
	if p.stopping.Load() {
		return nil, bad("PLUGIN_NOT_LOADED", "插件对象正在停止")
	}
	p.app.pluginImplementationMu.RLock()
	defer p.app.pluginImplementationMu.RUnlock()
	current, err := p.app.pluginStore.Installation(ctx, p.installation.InstanceID)
	if err != nil {
		return nil, err
	}
	if current.PluginID != p.installation.PluginID || current.Version != p.installation.Version || current.Generation != p.installation.Generation {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "插件调用对象已经被替换")
	}
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	if p.stopping.Load() {
		return nil, bad("PLUGIN_NOT_LOADED", "插件对象正在停止")
	}
	if p.session == nil {
		if p.sessionStarted && !p.sessionRetired.Load() {
			return nil, fmt.Errorf("plugin session already stopped")
		}
		implementation, err := p.app.resolvePluginImplementation(ctx, current)
		if err != nil {
			return nil, err
		}
		if !implementation.External {
			return nil, fmt.Errorf("not an external plugin object")
		}
		p.sessionRetired.Store(false)
		p.sessionStarted = true
		if implementation.Builtin {
			p.builtin.Store(true)
			// Compiled-in plugin: the session is a direct library call. There
			// is no process to launch and no transport to budget.
			session, startErr := plugins.NewInProcess(plugins.SessionBinding{Identity: plugins.Identity{PluginID: current.PluginID, InstanceID: current.InstanceID, Version: current.Version}, Generation: current.Generation}, plugins.InProcessOptions{Manifest: implementation.Manifest, Concurrent: implementation.builtinConcurrent}, implementation.newInProcess())
			if startErr != nil {
				p.session = nil
				p.sessionRetired.Store(true)
				return nil, startErr
			}
			p.session = session
			p.lastUsed.Store(time.Now().UnixNano())
			return p.session, nil
		}
		runner.ConcurrentCalls = true
		runner.SequentialHostStream = p.hostContract == pluginResourcesSDKContract && plugins.SupportsSequentialHostStream(implementation.Manifest)
		// Startup has a finite transport budget; it is not the business-method
		// budget. Idle process lifetime is independent of one HTTP request.
		p.session, err = runner.Start(context.WithoutCancel(ctx), implementation.Command, implementation.Manifest, plugins.SessionBinding{Identity: plugins.Identity{PluginID: current.PluginID, InstanceID: current.InstanceID, Version: current.Version}, Generation: current.Generation})
		if err != nil {
			p.session = nil
			// Keep the retired mark so a later call can attempt the rebuild
			// instead of hitting the stopped-session terminal state.
			p.sessionRetired.Store(true)
			return nil, err
		}
		p.lastUsed.Store(time.Now().UnixNano())
	}
	return p.session, nil
}

// ensureOriginalSessionReady replays the initializer when this call woke a
// session retired by the idle reaper. The manager's own init calls skip it:
// they ARE the initializer. The stop lane keeps its exclusive semantics.
func (p *originalPluginInstance) ensureOriginalSessionReady(ctx context.Context, runner plugins.Runner, method string) error {
	switch {
	case method == "init" || method == "stop" || strings.HasPrefix(method, "transport."):
		return nil
	case !p.sessionNeedsInit.Load():
		return nil
	}
	p.wakeMu.Lock()
	defer p.wakeMu.Unlock()
	if !p.sessionNeedsInit.Load() || p.stopping.Load() {
		// A concurrent caller already replayed the initializer, or the object
		// is going away; the ordinary client acquisition handles both.
		if p.stopping.Load() {
			return bad("PLUGIN_NOT_LOADED", "插件对象正在停止")
		}
		return nil
	}
	if _, err := p.originalSessionClient(ctx, runner); err != nil {
		return err
	}
	if err := p.initSerialized(ctx); err != nil {
		// The fresh worker stays up; the next call retries the replay instead
		// of thrashing processes. Its session already carries the failure.
		return err
	}
	p.sessionNeedsInit.Store(false)
	p.marketMu.Lock()
	p.marketKnown.Store(false)
	p.marketMu.Unlock()
	p.app.log("INFO", "plugins", "session_woken", "空闲插件会话已重建并重放初始化："+p.installation.PluginID, "", "")
	return nil
}

// Older standard workers explicitly retain their serial semantics. An actual
// negotiated concurrent worker is never serialized behind an App method lock.
func (p *originalPluginInstance) lockOriginalMethod() func() {
	p.sessionMu.Lock()
	concurrent := p.session != nil && p.session.SupportsConcurrent()
	p.sessionMu.Unlock()
	if concurrent {
		return func() {}
	}
	p.methodMu.Lock()
	return p.methodMu.Unlock
}

func (p *originalPluginInstance) readSession(ctx context.Context, method string, output any) error {
	release, err := p.acquireOriginalProcessSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	result, err := p.callSession(ctx, plugins.Runner{Timeout: 10 * time.Second}, method, json.RawMessage(`{}`), nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(result.Result, output)
}
func (p *originalPluginInstance) stopSession(ctx context.Context) error {
	p.closeOriginalSubmissions()
	p.sessionMu.Lock()
	client := p.session
	p.session = nil
	p.sessionMu.Unlock()
	if client == nil {
		return nil
	}
	// Pure contract has no cleanup host capability. Native contracts keep their
	// existing actual owner stop method; no old invocation authorization is reused.
	ctx = context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
	callback, closePhase := p.originalBaseSessionHost(ctx, "stop", nil)
	defer closePhase()
	return client.StopWithHost(ctx, callback)
}
func (a *App) closeOriginalSessions() {
	a.log("INFO", "plugins", "plugins_stopping", "正在停止所有插件...", "", "")
	for _, object := range a.originalLoadedPluginSnapshot() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = a.stopOriginalPluginObject(ctx, object.installation.InstanceID)
		cancel()
	}
	a.pluginOriginalRunningMu.Lock()
	a.pluginOriginalClasses.Range(func(key, value any) bool { a.pluginOriginalClasses.Delete(key); return true })
	a.pluginOriginalClassOrder = nil
	a.pluginOriginalRunningMu.Unlock()
}

// Original close and stop_service are business methods; transport stop is
// unconditional process cleanup. The detached/current exact client is the
// authority, even when update has already replaced its installation metadata.
func (p *originalPluginInstance) stopOriginalMethods(ctx context.Context) error {
	p.sessionMu.Lock()
	client := p.session
	p.sessionMu.Unlock()
	if client == nil {
		return nil
	}
	if client.SupportsConcurrent() {
		if err := client.BeginShutdown(ctx); err != nil {
			return err
		}
	}
	p.submitted.mu.Lock()
	privateRan, privateErr := p.submitted.privateMethodsRan, p.submitted.privateStopErr
	p.submitted.mu.Unlock()
	if privateRan {
		return privateErr
	}
	for _, method := range []string{"close", "stop_service"} {
		if !slices.Contains(p.manifest.Methods, method) {
			continue
		}
		phaseCtx := context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client, businessHook: true})
		callback, revoke := p.originalBaseSessionHost(phaseCtx, "stop", nil)
		var err error
		if client.SupportsConcurrent() {
			_, err = client.CallCleanup(phaseCtx, method, json.RawMessage(`{}`), callback)
		} else {
			_, err = client.CallWithOptions(phaseCtx, method, json.RawMessage(`{}`), callback, plugins.CallOptions{Timeout: 10 * time.Second})
		}
		revoke()
		if err != nil {
			return err
		}
	}
	return nil
}

// The old semaphore protected serial per-call worker use. A negotiated
// persistent concurrent process has its own bounded SDK invocation table;
// taking this global one-call slot would silently serialize it again.
func (p *originalPluginInstance) acquireOriginalProcessSlot(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.sessionMu.Lock()
	concurrent := p.session != nil && p.session.SupportsConcurrent()
	p.sessionMu.Unlock()
	if concurrent {
		return func() {}, nil
	}
	select {
	case p.app.pluginProcess <- struct{}{}:
		return func() { <-p.app.pluginProcess }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
