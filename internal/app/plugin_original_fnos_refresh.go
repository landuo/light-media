package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sync"

	"light-media/internal/plugins"
)

type originalFNOSRefreshEvent struct {
	key, target string
	admissions  int
	groups      int
}

type originalFNOSRefreshGroup struct {
	active int
	events map[*originalFNOSRefreshEvent]struct{}
}

type originalFNOSRefreshRuntime struct {
	mu     sync.Mutex
	events map[string]*originalFNOSRefreshEvent
	groups map[*originalPluginInstance]*originalFNOSRefreshGroup
}

// Only a committed native transfer callback can nominate a core follow-up.
// SDK events never receive this host-only ownership identity.
func (a *App) enqueueOriginalTransferRefresh(ctx context.Context, raw json.RawMessage, key, target string) {
	var data struct {
		Transfer struct {
			Target struct {
				Path string `json:"path"`
			} `json:"target_diritem"`
		} `json:"transferinfo"`
	}
	if err := json.Unmarshal(raw, &data); err != nil || data.Transfer.Target.Path == "" || target == "" {
		a.broadcastOriginalPluginEvent(ctx, "transfer.complete", raw)
		return
	}
	dir := filepath.Clean(data.Transfer.Target.Path)
	target = filepath.Clean(target)
	if !filepath.IsAbs(dir) || !filepath.IsAbs(target) || !within(dir, target) {
		a.broadcastOriginalPluginEvent(ctx, "transfer.complete", raw)
		return
	}
	broadcast, err := originalNewEvent("transfer.complete", raw, nil)
	if err != nil {
		a.log("ERROR", "plugins", "event_invalid", "插件事件数据编码失败", "", "")
		return
	}
	event := &originalFNOSRefreshEvent{key: key, target: target, admissions: 1}
	runtime := &a.originalFNOSRefresh
	runtime.mu.Lock()
	if runtime.events == nil {
		runtime.events = make(map[string]*originalFNOSRefreshEvent)
	}
	runtime.events[key] = event
	runtime.mu.Unlock()
	if !a.enqueueOriginalBroadcastWithRefresh(ctx, broadcast, false, event) {
		a.finishOriginalFNOSRefreshAdmission(event)
	}
}

func originalFNOSRefreshHandler(object *originalPluginInstance, method string) bool {
	return object.installation.PluginID == "MediaServerRefresh" && method == "refresh" &&
		object.manifest.ManagedPublicServices &&
		slices.Contains(object.manifest.Capabilities, plugins.MediaServersRefreshCapability)
}

func (a *App) addOriginalFNOSRefreshAdmission(event *originalFNOSRefreshEvent) {
	runtime := &a.originalFNOSRefresh
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	event.admissions++
}

func (a *App) finishOriginalFNOSRefreshAdmission(event *originalFNOSRefreshEvent) {
	if event == nil {
		return
	}
	runtime := &a.originalFNOSRefresh
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	event.admissions--
	runtime.release(event)
}

func (r *originalFNOSRefreshRuntime) release(event *originalFNOSRefreshEvent) {
	if event.admissions == 0 && event.groups == 0 && r.events[event.key] == event {
		delete(r.events, event.key)
	}
}

// A refresh owner is the actual admitted source object, not a desired-enabled
// row. Followers may return while the first callback still owns their batch.
// Keep core verification pending until the active callback group has drained.
type originalFNOSRefreshCallKey struct{}

// Followers may return while the first callback still owns their batch.
// Keep core verification pending until the active callback group has drained.
func (a *App) beginOriginalFNOSRefreshCallback(ctx context.Context, object *originalPluginInstance, event *originalFNOSRefreshEvent) (context.Context, func()) {
	runtime := &a.originalFNOSRefresh
	runtime.mu.Lock()
	if runtime.groups == nil {
		runtime.groups = make(map[*originalPluginInstance]*originalFNOSRefreshGroup)
	}
	group := runtime.groups[object]
	if group == nil {
		group = &originalFNOSRefreshGroup{events: make(map[*originalFNOSRefreshEvent]struct{})}
		runtime.groups[object] = group
	}
	group.active++
	if _, tracked := group.events[event]; event != nil && !tracked {
		group.events[event] = struct{}{}
		event.groups++
	}
	runtime.mu.Unlock()
	ctx = context.WithValue(ctx, originalFNOSRefreshCallKey{}, event)
	return ctx, func() {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		group.active--
		if group.active == 0 {
			delete(runtime.groups, object)
			for member := range group.events {
				member.groups--
				runtime.release(member)
			}
		}
	}
}

// The actual enabled worker probes its selected server before accepting the
// item. Record its ownership of the logical fnOS slot, never scan success.
func (a *App) recordOriginalFNOSRefreshOwner(ctx context.Context, event *originalFNOSRefreshEvent) error {
	var callback originalTransferCallback
	if err := a.Store.Get(ctx, "original_transfer_callback", event.key, &callback); err != nil {
		return err
	}
	if filepath.Clean(callback.File.Target) != event.target {
		return bad("REVIEW_SOURCE_CHANGED", "飞牛刷新对应的整理目标已变化")
	}
	if callback.FNOSRefreshOwner {
		return nil
	}
	callback.FNOSRefreshOwner = true
	return a.Store.Put(ctx, "original_transfer_callback", event.key, callback)
}

func (a *App) originalFNOSRefreshDisposition(ctx context.Context, asset Asset, config FNConfig) (pending, owned bool, err error) {
	if !config.Enabled || asset.FNOSRefreshTransfer == "" {
		return false, false, nil
	}
	target := filepath.Clean(asset.Path)
	runtime := &a.originalFNOSRefresh
	runtime.mu.Lock()
	event := runtime.events[asset.FNOSRefreshTransfer]
	pending = event != nil && event.target == target
	runtime.mu.Unlock()
	if pending {
		return true, false, nil
	}
	var callback originalTransferCallback
	err = a.Store.Get(ctx, "original_transfer_callback", asset.FNOSRefreshTransfer, &callback)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return false, err == nil && callback.FNOSRefreshOwner && filepath.Clean(callback.File.Target) == target, err
}
