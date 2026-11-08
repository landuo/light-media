package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"slices"
	"sync/atomic"
)

// Only the manager binds this exact object/client stop admission. Source
// close/stop_service differs from the final transport stop. A worker method
// name or an old ordinary callback cannot create either authority.
type originalBaseStopKey struct{}
type originalBaseStopCall struct {
	object       *originalPluginInstance
	client       *plugins.Client
	businessHook bool // close/stop_service only; never the physical transport stop
}

// Each SDK exchange gets a new authority closure. Neither a plugin's stored
// callback nor a replacement object can borrow this phase after it closes.
func (p *originalPluginInstance) originalBaseSessionHost(callCtx context.Context, phase string, fallback plugins.HostHandler) (plugins.HostHandler, func()) {
	return p.originalBaseSessionHostBound(callCtx, phase, fallback, nil)
}

func (p *originalPluginInstance) originalBaseSessionHostBound(callCtx context.Context, phase string, fallback plugins.HostHandler, initialization *originalInitializationCall) (plugins.HostHandler, func()) {
	if p.hostContract != pluginBaseSDKContract && p.hostContract != pluginResourcesSDKContract {
		return fallback, func() {}
	}
	var active atomic.Bool
	active.Store(true)
	logCall := originalPluginLogCall{id: id("pc_"), phase: phase}
	var iyuuMaterial originalIYUUMTeamMaterialGrant
	var localBackup *atomic.Bool
	var brush *originalBrushSourceCall
	if p.installation.PluginID == "AutoBackup" {
		localBackup = new(atomic.Bool)
	}
	if p.installation.PluginID == "BrushFlowLanduo" && phase == "brush" {
		brush = &originalBrushSourceCall{}
	}
	cleanSeed := p.originalCleanSeedCallFor(callCtx, phase)
	seedRescue := p.originalSeedRescueCallFor(callCtx, phase)
	cloudflare := p.originalCloudflareCallFor(callCtx, phase)
	customHosts := p.originalCustomHostsCallFor(callCtx, phase, initialization)
	validate := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := callCtx.Err(); err != nil {
			return err
		}
		if !active.Load() {
			return bad("PLUGIN_CALL_CLOSED", "插件公共方法调用已结束")
		}
		if p.stopping.Load() && phase != "stop" && !p.originalPrivateSchedulerScopeAllowed(callCtx) && !p.originalPublicServiceScopeAllowed(callCtx) {
			return bad("PLUGIN_CALL_CLOSED", "插件对象正在停止")
		}
		admitted := originalObjectStillLoaded(p) || p.originalSubmittedScopeAllowed(callCtx)
		if call, ok := callCtx.Value(originalInitializingObjectKey{}).(*originalInitializationCall); ok && call != nil && call.object == p && call.active.Load() {
			admitted = true
		}
		if phase == "stop" {
			cleanup, ok := callCtx.Value(originalBaseStopKey{}).(*originalBaseStopCall)
			if !ok || cleanup == nil || cleanup.object != p || cleanup.client == nil {
				return bad("PLUGIN_CALL_CLOSED", "插件清理调用未绑定实际对象")
			}
			// The original endpoint may remove installation/code or replace the
			// stored version before stopping this object. This exact detached
			// client retains only its fresh cleanup phase, not stale run grants.
			return nil
		}
		if !admitted {
			return bad("PLUGIN_NOT_LOADED", "插件调用对象已移除")
		}
		if p.originalPublicServiceScopeAllowed(callCtx) {
			return nil
		}
		current, err := p.app.pluginStore.Installation(ctx, p.installation.InstanceID)
		if err != nil {
			return err
		}
		if current.PluginID != p.installation.PluginID || current.Version != p.installation.Version || current.Generation != p.installation.Generation {
			if !p.originalPrivateSchedulerSnapshotAllowed(callCtx, current) {
				return bad("PLUGIN_SNAPSHOT_STALE", "插件实例已被替换")
			}
		}
		return nil
	}
	return func(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
			if !originalStandardCapabilityAllowed(p.hostContract, method) || !slices.Contains(p.manifest.Capabilities, method) {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "公共宿主契约未声明该方法")
			}
			scope := originalBaseCallScope{object: p, phase: phase, installation: p.installation, validate: validate, logCall: logCall, iyuuMaterial: &iyuuMaterial, localBackup: localBackup, brush: brush, cleanSeed: cleanSeed, seedRescue: seedRescue, cloudflare: cloudflare, customHosts: customHosts}
			if originalFNOSRefreshHandler(p, phase) {
				scope.fnosRefresh, _ = callCtx.Value(originalFNOSRefreshCallKey{}).(*originalFNOSRefreshEvent)
			}
			if (method == plugins.BaseConfigSaveOrigin || method == originalMergeSiteSwitchCapability || method == originalLarkEndpointCapability) && phase == "init" && originalLiveInitialization(callCtx, p) == initialization {
				scope.initialization = initialization
			}
			if plugins.IsBaseMethod(method) {
				return p.app.pluginOriginalBaseHost(ctx, scope, identity, method, raw)
			}
			return p.app.pluginOriginalResourceHost(ctx, scope, identity, method, raw)
		}, func() {
			active.Store(false)
			if cloudflare != nil {
				cloudflare.close()
			}
		}
}
