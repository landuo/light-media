package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"sync"

	"light-media/internal/plugins"
	cf "light-media/internal/plugins/cloudflarespeedtest"
)

type originalCloudflareCommandKey struct{}
type originalCloudflareAPIKey struct{}

type originalCloudflareCall struct {
	mu         sync.Mutex
	applied    json.RawMessage
	config     cf.Object
	init       *originalInitializationCall
	native     *originalCloudflareNative
	peer       PluginInstallation
	peerStamp  originalBaseConfigFingerprint
	peerValue  json.RawMessage
	ran        bool
	completed  bool
	bestIP     string
	hostsSaved bool
	reloaded   bool
}

func originalCloudflareCommand(raw json.RawMessage) bool {
	var event plugins.MutableEvent
	var data struct{ Action string }
	return json.Unmarshal(raw, &event) == nil && event.Type == "plugin.action" &&
		json.Unmarshal(event.Data, &data) == nil && data.Action == cf.Action
}

func (p *originalPluginInstance) originalCloudflareCallFor(ctx context.Context, phase string) *originalCloudflareCall {
	if p.installation.PluginID != cf.PluginID {
		return nil
	}
	var allowed bool
	switch phase {
	case "handle_commands":
		owner, _ := ctx.Value(originalCloudflareCommandKey{}).(*originalPluginInstance)
		allowed = owner == p && p.originalPublicServiceScopeAllowed(ctx)
	case "http":
		owner, _ := ctx.Value(originalCloudflareAPIKey{}).(*originalPluginInstance)
		allowed = owner == p && p.originalPublicServiceScopeAllowed(ctx)
	case cf.Action:
		service, _ := ctx.Value(originalServiceCallKey{}).(*originalServiceCall)
		allowed = service != nil && service.job != nil && service.job.object == p &&
			service.job.definition.Handler == phase && p.originalPublicServiceScopeAllowed(ctx)
	case "private_once":
		allowed = p.originalPrivateSchedulerScopeAllowed(ctx)
	}
	if !allowed {
		return nil
	}
	p.configMu.RLock()
	raw := copyOriginalBaseJSON(p.appliedConfig)
	p.configMu.RUnlock()
	init := originalLiveInitialization(ctx, p)
	if phase == "private_once" && init != nil && len(init.config) > 0 {
		raw = copyOriginalBaseJSON(init.config)
	}
	value, err := decodeSourceJSON(raw)
	if err != nil && len(raw) > 0 {
		return nil
	}
	config := cf.Object(sourceMap(value))
	if phase == "private_once" && (!sourceSubscriptionTruth(config["onlyonce"]) ||
		!sourceSubscriptionTruth(config["ipv4"]) && !sourceSubscriptionTruth(config["ipv6"])) {
		return nil
	}
	return &originalCloudflareCall{applied: raw, config: config, init: init}
}

func (c *originalCloudflareCall) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.native != nil {
		c.native.Close()
	}
}

func originalCloudflareValidate(ctx context.Context, scope originalBaseCallScope) error {
	if err := scope.validate(ctx); err != nil {
		return err
	}
	call := scope.cloudflare
	if call.init != nil && call.init.active.Load() && bytes.Equal(call.applied, call.init.config) {
		return nil
	}
	scope.object.configMu.RLock()
	defer scope.object.configMu.RUnlock()
	if !bytes.Equal(call.applied, scope.object.appliedConfig) {
		return bad("PLUGIN_SNAPSHOT_STALE", "Cloudflare IP优选配置已变化")
	}
	return nil
}

func originalCloudflareCapability(method string) bool {
	switch method {
	case "cloudflare_speed.custom_hosts", "cloudflare_speed.save_hosts", "cloudflare_speed.reload_hosts",
		"cloudflare_speed.platform", "cloudflare_speed.exists", "cloudflare_speed.ensure", "cloudflare_speed.reset",
		"cloudflare_speed.latest", "cloudflare_speed.download", "cloudflare_speed.extract", "cloudflare_speed.remove",
		"cloudflare_speed.run", "cloudflare_speed.read_result":
		return true
	}
	return false
}

func (a *App) originalCloudflareResource(ctx context.Context, scope originalBaseCallScope, operation string, raw json.RawMessage) (json.RawMessage, error) {
	call := scope.cloudflare
	if call == nil || scope.object == nil || scope.object.app != a || scope.installation.PluginID != cf.PluginID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "Cloudflare IP优选必须绑定已接受的插件入口")
	}
	call.mu.Lock()
	defer call.mu.Unlock()
	if err := originalCloudflareValidate(ctx, scope); err != nil {
		return nil, err
	}
	switch operation {
	case "custom_hosts", "save_hosts", "reload_hosts":
		return a.originalCloudflareHostsResource(ctx, scope, operation, raw)
	case "run":
		var request struct {
			Binary string   `json:"binary"`
			Args   []string `json:"args"`
			Result string   `json:"result"`
			Wait   int64    `json:"windows_wait"`
		}
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		config := call.config
		if !sourceSubscriptionTruth(config["ipv4"]) && !sourceSubscriptionTruth(config["ipv6"]) {
			config = cf.Object{"ipv4": true, "additional_args": config["additional_args"]}
		}
		expected, err := cf.SourceArgs(config)
		if err != nil || call.ran || call.peer.InstanceID == "" || request.Result != "result_hosts.txt" ||
			request.Wait != 0 || !slices.Equal(request.Args, expected) {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "测速只能使用当前已保存的参数及私有结果文件")
		}
		call.ran = true
		result, err := a.originalCloudflareNativeResource(ctx, scope, operation, raw)
		if err != nil {
			return nil, err
		}
		var run struct{ Completed bool }
		if json.Unmarshal(result, &run) != nil {
			return nil, bad("PLUGIN_VALUE_INVALID", "测速工具状态无效")
		}
		call.completed = run.Completed
		return result, nil
	case "read_result":
		if !call.completed {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "测速结果尚未确认")
		}
		result, err := a.originalCloudflareNativeResource(ctx, scope, operation, raw)
		if err != nil {
			return nil, err
		}
		var response struct{ Content []byte }
		if json.Unmarshal(result, &response) != nil {
			return nil, bad("PLUGIN_VALUE_INVALID", "测速结果无效")
		}
		call.bestIP = cf.BestIP(response.Content, false)
		return result, nil
	}
	return a.originalCloudflareNativeResource(ctx, scope, operation, raw)
}

func (a *App) originalCloudflareHostsResource(ctx context.Context, scope originalBaseCallScope, operation string, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Config json.RawMessage `json:"config,omitempty"`
	}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if (operation == "save_hosts") != (len(request.Config) > 0) {
		return nil, bad("INVALID_PLUGIN_VALUE", "CustomHosts 操作参数无效")
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.pluginOriginalBase.mu.Lock()
	defer a.pluginOriginalBase.mu.Unlock()
	if err := originalCloudflareValidate(ctx, scope); err != nil {
		return nil, err
	}
	const peerID = "CustomHosts"
	call := scope.cloudflare
	peer, err := a.pluginStore.Installation(ctx, peerID)
	if errors.Is(err, sql.ErrNoRows) && operation == "custom_hosts" {
		return json.RawMessage("null"), nil
	}
	if err != nil {
		return nil, err
	}
	livePeer := slices.ContainsFunc(a.originalBroadcastSnapshot("plugin.reload"), func(handler originalBroadcastHandler) bool {
		return handler.Instance == peerID && originalObjectStillLoaded(handler.object) &&
			handler.object.installation.Version == peer.Version && handler.object.installation.Generation == peer.Generation
	})
	if peer.PluginID != peerID || !peer.Installed || !livePeer {
		if operation == "custom_hosts" {
			return json.RawMessage("null"), nil
		}
		return nil, bad("CLOUDFLARE_CUSTOM_HOSTS_CONFLICT", "CustomHosts 已卸载、替换或停止")
	}
	stamp, sealed, err := a.pluginStore.readOriginalBaseConfigRow(ctx, a.Store.DB, peerID)
	if err != nil {
		return nil, err
	}
	value, err := a.originalBaseObserveConfigLocked(peerID, stamp, sealed)
	if err != nil {
		return nil, err
	}
	if operation == "custom_hosts" {
		if call.peer.InstanceID != "" {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "本次测速已读取 CustomHosts")
		}
		// The worker holds its serial run lock at this first callback. Its
		// previous completed run may already have updated the saved IP.
		self, err := a.originalBaseConfigLocked(ctx, scope.installation.InstanceID)
		if err != nil {
			return nil, err
		}
		current, err := decodeSourceJSON(self)
		if err != nil {
			return nil, err
		}
		if fields := sourceMap(current); len(fields) > 0 {
			call.config = cf.Object(fields)
		}
		call.peer, call.peerStamp, call.peerValue = peer, stamp, copyOriginalBaseJSON(value)
		if !originalBaseJSONTruthy(value) {
			return value, nil
		}
		fields, err := decodeSourceJSON(value)
		if err != nil {
			return nil, err
		}
		custom := sourceMap(fields)
		return json.Marshal(cf.Object{"hosts": custom["hosts"], "err_hosts": custom["err_hosts"], "enabled": custom["enabled"]})
	}
	if call.peer.InstanceID != peerID || call.peer.Version != peer.Version || call.peer.Generation != peer.Generation ||
		stamp != call.peerStamp || !originalBaseJSONEqual(value, call.peerValue) {
		return nil, bad("CLOUDFLARE_CUSTOM_HOSTS_CONFLICT", "CustomHosts 已编辑、重置或卸载")
	}
	best, err := netip.ParseAddr(call.bestIP)
	if err != nil || best.Zone() != "" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "本次测速未取得有效优选 IP")
	}
	if operation == "save_hosts" {
		custom, err := decodeSourceJSON(value)
		if err != nil {
			return nil, err
		}
		expected, err := cf.ExpectedHostReplacement(call.config, cf.Object(sourceMap(custom)), call.bestIP)
		if err != nil {
			return nil, err
		}
		target, err := json.Marshal(expected)
		if err != nil || call.hostsSaved || expected == nil || !originalBaseJSONEqual(target, request.Config) {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "CustomHosts 更新与本次优选结果不一致")
		}
		if _, err = a.originalBaseUpdateConfigLocked(ctx, peerID, target); err != nil {
			return nil, err
		}
		cached := a.pluginOriginalBase.config[peerID]
		call.peerStamp, call.peerValue, call.hostsSaved = cached.observed, copyOriginalBaseJSON(cached.value), true
		return json.RawMessage("true"), nil
	}
	self, err := a.originalBaseConfigLocked(ctx, scope.installation.InstanceID)
	if err != nil {
		return nil, err
	}
	var saved struct {
		IP string `json:"cf_ip"`
	}
	if !call.hostsSaved || call.reloaded || json.Unmarshal(self, &saved) != nil || saved.IP != call.bestIP {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "测速结果尚未保存")
	}
	event, err := originalNewEvent("plugin.reload", json.RawMessage(`{"plugin_id":"CustomHosts"}`), nil)
	if err != nil {
		return nil, err
	}
	if !a.enqueueOriginalBroadcast(ctx, event) {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	call.reloaded = true
	return json.RawMessage("true"), nil
}
