package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"slices"
	"time"
)

// The loaded object, not installed-list membership, owns its process. Run rows
// still supply fresh authorization and active-call admission for each invocation.
func (p *originalPluginInstance) callSession(ctx context.Context, runner plugins.Runner, method string, payload json.RawMessage, host plugins.HostHandler) (plugins.RunResult, error) {
	p.app.pluginImplementationMu.RLock()
	defer p.app.pluginImplementationMu.RUnlock()
	current, err := p.app.pluginStore.Installation(ctx, p.installation.InstanceID)
	if err != nil {
		return plugins.RunResult{}, err
	}
	if current.PluginID != p.installation.PluginID || current.Version != p.installation.Version || current.Generation != p.installation.Generation {
		return plugins.RunResult{}, bad("PLUGIN_SNAPSHOT_STALE", "插件调用对象已经被替换")
	}
	p.sessionMu.Lock()
	if p.session == nil {
		if p.sessionStarted {
			p.sessionMu.Unlock()
			return plugins.RunResult{}, fmt.Errorf("plugin session already stopped")
		}
		implementation, err := p.app.resolvePluginImplementation(ctx, current)
		if err != nil {
			p.sessionMu.Unlock()
			return plugins.RunResult{}, err
		}
		if !implementation.External {
			p.sessionMu.Unlock()
			return plugins.RunResult{}, fmt.Errorf("not an external plugin object")
		}
		p.sessionStarted = true
		// Request cancellation must not become the idle instance lifetime. Call's
		// own context still terminates the transport on a canceled active operation.
		p.session, err = runner.Start(context.WithoutCancel(ctx), implementation.Command, implementation.Manifest, plugins.SessionBinding{Identity: plugins.Identity{PluginID: current.PluginID, InstanceID: current.InstanceID, Version: current.Version}, Generation: current.Generation})
		if err != nil {
			p.sessionMu.Unlock()
			return plugins.RunResult{}, err
		}
	}
	client := p.session
	p.sessionMu.Unlock()
	callback, closePhase := p.originalBaseSessionHost(ctx, method, host)
	defer closePhase()
	return client.CallWithOptions(ctx, method, payload, callback, plugins.CallOptions{Timeout: runner.Timeout, ExcludeHostTime: runner.ExcludeHostTime})
}
func (p *originalPluginInstance) readSession(ctx context.Context, method string, output any) error {
	select {
	case p.app.pluginProcess <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.app.pluginProcess }()
	result, err := p.callSession(ctx, plugins.Runner{Timeout: 10 * time.Second}, method, json.RawMessage(`{}`), nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(result.Result, output)
}
func (p *originalPluginInstance) stopSession(ctx context.Context) error {
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
	for _, method := range []string{"close", "stop_service"} {
		if !slices.Contains(p.manifest.Methods, method) {
			continue
		}
		phaseCtx := context.WithValue(ctx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
		callback, revoke := p.originalBaseSessionHost(phaseCtx, "stop", nil)
		_, err := client.CallWithOptions(phaseCtx, method, json.RawMessage(`{}`), callback, plugins.CallOptions{Timeout: 10 * time.Second})
		revoke()
		if err != nil {
			return err
		}
	}
	return nil
}
