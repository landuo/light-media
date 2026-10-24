package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"slices"
	"time"
)

// The loaded object owns one negotiated standard process. Package resolution
// holds the implementation lock only while resolving/starting it, never during
// a potentially long business method. Every invocation has fresh authority.
func (p *originalPluginInstance) callSession(ctx context.Context, runner plugins.Runner, method string, payload json.RawMessage, host plugins.HostHandler) (plugins.RunResult, error) {
	client, err := p.originalSessionClient(ctx, runner)
	if err != nil {
		return plugins.RunResult{}, err
	}
	callback, closePhase := p.originalBaseSessionHost(ctx, method, host)
	defer closePhase()
	options := plugins.CallOptions{Timeout: runner.Timeout, ExcludeHostTime: runner.ExcludeHostTime}
	if client.SupportsConcurrent() {
		options = plugins.CallOptions{CallerDeadlineOnly: true}
	}
	return client.CallWithOptions(ctx, method, payload, callback, options)
}

func (p *originalPluginInstance) originalSessionClient(ctx context.Context, runner plugins.Runner) (*plugins.Client, error) {
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
		if p.sessionStarted {
			return nil, fmt.Errorf("plugin session already stopped")
		}
		implementation, err := p.app.resolvePluginImplementation(ctx, current)
		if err != nil {
			return nil, err
		}
		if !implementation.External {
			return nil, fmt.Errorf("not an external plugin object")
		}
		p.sessionStarted = true
		runner.ConcurrentCalls = true
		// Startup has a finite transport budget; it is not the business-method
		// budget. Idle process lifetime is independent of one HTTP request.
		p.session, err = runner.Start(context.WithoutCancel(ctx), implementation.Command, implementation.Manifest, plugins.SessionBinding{Identity: plugins.Identity{PluginID: current.PluginID, InstanceID: current.InstanceID, Version: current.Version}, Generation: current.Generation})
		if err != nil {
			return nil, err
		}
	}
	return p.session, nil
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
	a.pluginRunningInstances.Range(func(key, value any) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = a.stopOriginalPlugin(ctx, key.(string))
		cancel()
		return true
	})
	a.pluginOriginalClasses.Range(func(key, value any) bool { a.pluginOriginalClasses.Delete(key); return true })
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
	for _, method := range []string{"close", "stop_service"} {
		if !slices.Contains(p.manifest.Methods, method) {
			continue
		}
		phaseCtx := context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
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
