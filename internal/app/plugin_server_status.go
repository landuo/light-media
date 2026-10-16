package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	serverstatus "light-media/internal/plugins/serverstatus"
)

// The original class keeps samples and positive/negative network caches in
// memory. A short-lived worker cannot own them and plugin KV must not persist
// them across cold restarts. One slot per concrete installation is replaced on
// configuration/reload; a slow old request never publishes into its replacement.
type pluginServerStatusRuntime struct {
	mu      sync.Mutex
	slots   map[string]*pluginServerStatusSlot
	network serverstatus.Network // Test seam; production uses the fixed public probe adapter.
	clock   serverstatus.Clock
}

type pluginServerStatusSlot struct {
	mu                   sync.Mutex
	cancelMu             sync.Mutex
	cancel               context.CancelFunc
	generation, revision int64
	version              string
	machine              *serverstatus.Machine
}

func pluginViewBudget(pluginID string) time.Duration {
	if pluginID == "MPServerStatus" {
		// Original HTTP connect/read timeout is 20s; page also has system DNS
		// and a separate 5s TLS probe. Bound this view without changing all views.
		return time.Minute
	}
	return 10 * time.Second
}

func (s *pluginServerStatusRuntime) slot(installation PluginInstallation, revision int64, config json.RawMessage, reset bool) (*pluginServerStatusSlot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots == nil {
		s.slots = make(map[string]*pluginServerStatusSlot)
	}
	current := s.slots[installation.InstanceID]
	if !reset && current != nil && current.generation == installation.Generation && current.revision == revision && current.version == installation.Version {
		return current, nil
	}
	network := s.network
	if network == nil {
		network = pluginServerStatusNetwork{}
	}
	machine := serverstatus.New(network, s.clock)
	if err := machine.Init(config); err != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "服务监控插件配置格式无效")
	}
	if current != nil {
		current.cancelProbe()
	}
	current = &pluginServerStatusSlot{generation: installation.Generation, revision: revision, version: installation.Version, machine: machine}
	s.slots[installation.InstanceID] = current
	return current, nil
}

func (s *pluginServerStatusSlot) cancelProbe() {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *pluginServerStatusRuntime) invalidate(instance string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slot := s.slots[instance]; slot != nil {
		slot.cancelProbe()
		delete(s.slots, instance)
	}
}

func (s *pluginServerStatusRuntime) current(instance string, slot *pluginServerStatusSlot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slots[instance] == slot
}

func (a *App) pluginServerStatusConfigure(ctx context.Context, installation PluginInstallation, run *PluginRun, raw json.RawMessage) (json.RawMessage, error) {
	if installation.PluginID != "MPServerStatus" || installation.Version != "1.4" || run == nil || (run.Action != "init" && run.Action != "reload") || !pluginStrictJSON(raw, &struct{}{}) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "服务监控初始化只接受当前插件的真实初始化执行")
	}
	// No network and no waiting for an old slot's network mutex under pluginMu.
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := a.validatePluginHostRun(ctx, installation, run, "server.status.configure"); err != nil {
		return nil, err
	}
	config, err := a.pluginStore.Config(ctx, installation.InstanceID)
	if err != nil {
		return nil, err
	}
	if _, err := a.pluginServerStatus.slot(installation, config.Revision, config.Value, true); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"initialized":true}`), nil
}

func (a *App) pluginServerStatusSnapshot(ctx context.Context, installation PluginInstallation, revision int64, config json.RawMessage, method string, raw json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Details *bool `json:"include_network_details"`
	}
	if installation.PluginID != "MPServerStatus" || installation.Version != "1.4" || (method != "page" && method != "dashboard") || !pluginStrictJSON(raw, &in) || in.Details == nil || *in.Details != (method == "page") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "服务监控只能查询当前页面对应的固定探针")
	}
	// Serialize the brief identity check/allocation with configuration writes,
	// so a delayed old view cannot replace a newly initialized cache slot.
	a.pluginMu.Lock()
	if err := a.pluginViewCurrent(ctx, installation, revision); err != nil {
		a.pluginMu.Unlock()
		return nil, err
	}
	slot, err := a.pluginServerStatus.slot(installation, revision, config, false)
	a.pluginMu.Unlock()
	if err != nil {
		return nil, err
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if err := a.pluginViewCurrent(ctx, installation, revision); err != nil {
		return nil, err
	}
	if !a.pluginServerStatus.current(installation.InstanceID, slot) {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "服务监控已重新加载，请重试")
	}
	// Disabled page must not probe; the source's direct dashboard call does.
	if method == "page" && !slot.machine.State() {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "未启用的服务监控详情不访问网络")
	}
	// This mutex belongs only to this instance. No SQL transaction, pluginMu,
	// configuration lock or registry lock spans HTTP/DNS/TLS waits.
	probeCtx, cancel := context.WithCancel(ctx)
	slot.cancelMu.Lock()
	slot.cancel = cancel
	slot.cancelMu.Unlock()
	defer func() {
		cancel()
		slot.cancelMu.Lock()
		slot.cancel = nil
		slot.cancelMu.Unlock()
	}()
	// Invalidation may have happened just before the cancel function was
	// installed. Recheck after publishing it, before the first network call.
	if !a.pluginServerStatus.current(installation.InstanceID, slot) {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "服务监控已重新加载，请重试")
	}
	snapshot := slot.machine.Load(probeCtx, *in.Details)
	if err := probeCtx.Err(); err != nil {
		return nil, err
	}
	if err := a.pluginViewCurrent(ctx, installation, revision); err != nil {
		return nil, err
	}
	if !a.pluginServerStatus.current(installation.InstanceID, slot) {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "服务监控已重新加载，请重试")
	}
	return json.Marshal(snapshot)
}
