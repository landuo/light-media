package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"slices"
	"sync/atomic"
)

// Only stopSession creates this admission after detaching the exact real client.
// A method named "stop" or an old ordinary callback is not cleanup authority.
type originalBaseStopKey struct{}
type originalBaseStopCall struct {
	object *originalPluginInstance
	client *plugins.Client
}

// Each SDK exchange gets a new authority closure. Neither a plugin's stored
// callback nor a replacement object can borrow this phase after it closes.
func (p *originalPluginInstance) originalBaseSessionHost(callCtx context.Context, phase string, fallback plugins.HostHandler) (plugins.HostHandler, func()) {
	if p.hostContract != pluginBaseSDKContract && p.hostContract != pluginResourcesSDKContract {
		return fallback, func() {}
	}
	var active atomic.Bool
	active.Store(true)
	torrentTransfers := newOriginalTorrentTransfers()
	downloaderUploads := newOriginalDownloaderCommandUploads()
	logCall := originalPluginLogCall{id: id("pc_"), phase: phase}
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
		if p.stopping.Load() && phase != "stop" {
			return bad("PLUGIN_CALL_CLOSED", "插件对象正在停止")
		}
		admitted := originalObjectStillLoaded(p) || p.originalSubmittedScopeAllowed(callCtx) || p.originalRetainedScopeAllowed(callCtx)
		if call, ok := callCtx.Value(originalInitializingObjectKey{}).(*originalInitializationCall); ok && call != nil && call.object == p {
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
		current, err := p.app.pluginStore.Installation(ctx, p.installation.InstanceID)
		if err != nil {
			return err
		}
		if current.PluginID != p.installation.PluginID || current.Version != p.installation.Version || current.Generation != p.installation.Generation {
			return bad("PLUGIN_SNAPSHOT_STALE", "插件实例已被替换")
		}
		return nil
	}
	return func(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
			if !originalStandardCapabilityAllowed(p.hostContract, method) || !slices.Contains(p.manifest.Capabilities, method) {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "公共宿主契约未声明该方法")
			}
			scope := originalBaseCallScope{object: p, phase: phase, installation: p.installation, validate: validate, logCall: logCall, torrentTransfers: torrentTransfers, downloaderUploads: downloaderUploads}
			if plugins.IsBaseMethod(method) {
				return p.app.pluginOriginalBaseHost(ctx, scope, identity, method, raw)
			}
			return p.app.pluginOriginalResourceHost(ctx, scope, identity, method, raw)
		}, func() {
			active.Store(false)
			torrentTransfers.close()
			downloaderUploads.close()
		}
}
