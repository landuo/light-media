package app

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"light-media/internal/plugins"
)

// EventManager resolves class/method names through running_plugins when the
// callback executes. A reload may replace that class's object after submission.
// The host class identity includes authenticated clone lineage; neither worker
// payload nor a newly unrelated class can redirect the callback's authority.
type originalEventClass struct {
	instance, plugin, source string
}

func originalEventClassOf(object *originalPluginInstance) (originalEventClass, bool) {
	in := object.installation
	identity := originalEventClass{instance: in.InstanceID, plugin: in.PluginID}
	var envelope struct {
		Marker *pluginInstanceMarker `json:"host_instance"`
	}
	if json.Unmarshal(in.Manifest, &envelope) != nil {
		return identity, false
	}
	if envelope.Marker != nil {
		marker := envelope.Marker
		if marker.Purpose != "instance-clone/v1" || marker.InstanceID != in.InstanceID || marker.SourceInstanceID == in.InstanceID || !pluginStorageID.MatchString(marker.SourceInstanceID) {
			return identity, false
		}
		identity.source = marker.SourceInstanceID
	}
	return identity, true
}

func (p *originalPluginInstance) currentOriginalEventObject() *originalPluginInstance {
	bound, valid := originalEventClassOf(p)
	if !valid {
		return nil
	}
	loaded, ok := p.app.pluginRunningInstances.Load(bound.instance)
	if !ok {
		return nil
	}
	current := loaded.(*originalPluginInstance)
	identity, valid := originalEventClassOf(current)
	if !valid || identity != bound {
		return nil
	}
	return current
}

func (p *originalPluginInstance) originalEventEnabled() bool {
	current := p.currentOriginalEventObject()
	if current == nil || current.stopping.Load() {
		return false
	}
	current.configMu.RLock()
	defer current.configMu.RUnlock()
	return current.eventsEnabled
}

// Recheck current enablement after acquiring a legacy worker's serial method
// slot. If reload replaced the object while waiting, resolve it again and use
// the replacement's own session and fresh host call scope. Never reuse a prior
// invocation capability or invoke a stopped object's retained process.
func (p *originalPluginInstance) callOriginalEventMethod(ctx context.Context, method string, input json.RawMessage) (plugins.RunResult, bool, error) {
	result, invoked, err, finish := p.callOriginalEventMethodOwned(ctx, method, input, false, nil)
	defer finish()
	return result, invoked, err
}

// Only the actual host registry dispatcher selects this path. Event payloads,
// command action strings and arbitrary SDK methods cannot create its root.
func (p *originalPluginInstance) callOriginalRegisteredBroadcast(ctx context.Context, method string, input json.RawMessage, refresh *originalFNOSRefreshEvent) (plugins.RunResult, bool, error, func()) {
	if originalMergeSiteSwitchEventMethod(p, method) {
		// Reserve the manager before entering the worker. Acquiring it from a
		// reverse call can fail after a prefix of this event already persisted,
		// or deadlock against an explicit save waiting for the worker.
		operation, release, err := p.app.waitOriginalMergeSiteSwitchEvent(ctx)
		if err != nil {
			return plugins.RunResult{}, false, err, func() {}
		}
		defer release()
		ctx = operation
	}
	return p.callOriginalEventMethodOwned(ctx, method, input, true, refresh)
}
func (p *originalPluginInstance) callOriginalEventMethodOwned(ctx context.Context, method string, input json.RawMessage, registered bool, refresh *originalFNOSRefreshEvent) (plugins.RunResult, bool, error, func()) {
	noop := func() {}
	defer func() { p.app.finishOriginalFNOSRefreshAdmission(refresh) }()
	for {
		if err := ctx.Err(); err != nil {
			return plugins.RunResult{}, false, err, noop
		}
		current := p.currentOriginalEventObject()
		if current == nil || !slices.Contains(current.manifest.Methods, method) {
			return plugins.RunResult{}, false, nil, noop
		}
		unlock := current.lockOriginalMethod()
		if p.currentOriginalEventObject() != current {
			unlock()
			continue
		}
		if !current.originalEventMethodEnabled(method) {
			unlock()
			return plugins.RunResult{}, false, nil, noop
		}
		finish := noop
		if registered && current.manifest.ManagedPublicServices {
			var err error
			ctx, finish, err = current.beginOriginalBoundEntry(ctx, originalSourceEvent)
			if err != nil {
				unlock()
				if pluginErrorCodeInternal(err) == "PLUGIN_CALL_CLOSED" {
					return plugins.RunResult{}, false, nil, noop
				}
				return plugins.RunResult{}, false, err, noop
			}
		}
		if registered && originalFNOSRefreshHandler(current, method) {
			var finishRefresh func()
			ctx, finishRefresh = current.app.beginOriginalFNOSRefreshCallback(ctx, current, refresh)
			current.app.finishOriginalFNOSRefreshAdmission(refresh)
			refresh = nil
			finishEntry := finish
			finish = func() {
				finishRefresh()
				finishEntry()
			}
		}
		if registered && current.installation.PluginID == "CleanInvalidSeed" && method == "handle_commands" {
			ctx = withOriginalCleanSeedCommand(ctx, input)
		}
		if registered && current.installation.PluginID == "CloudflareSpeedTest" && method == "handle_commands" && originalCloudflareCommand(input) {
			ctx = context.WithValue(ctx, originalCloudflareCommandKey{}, current)
		}
		if registered && originalCustomHostsInstallation(current.installation) && method == "reload_event" && originalCustomHostsReload(input, current.installation.InstanceID) {
			ctx = context.WithValue(ctx, originalCustomHostsReloadKey{}, current)
		}
		result, err := current.callSession(ctx, plugins.Runner{Timeout: 30 * time.Second}, method, input, nil)
		unlock()
		return result, true, err, finish
	}
}
