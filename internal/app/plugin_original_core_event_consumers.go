package app

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

// These concrete consumers already exist in Command and Scheduler. A plugin's
// ordinary Event.Send must reach them just as a native producer does. They are
// submitted independently of plugin observers, matching EventManager's thread
// pool; native producers that already consumed the event mark only that queue
// entry so the same side effect cannot execute twice.
func originalCoreBroadcastHandler(kind string) string {
	switch kind {
	case "config.updated":
		return "go.core.MediaServer.handle_config_changed"
	case "command.excute":
		return "go.core.Command.command_event"
	case "module.reload":
		return "go.core.Command.module_reload_event"
	case "plugin.reload":
		return "go.core.Scheduler.on_plugin_reload"
	case "site.deleted":
		return "go.core.SiteReferences.remove_site"
	case "site.updated":
		return "go.core.Site.cache_site_userdata"
	case "downloadfile.deleted":
		return "go.core.Download.download_file_deleted"
	case "metadata.scrape":
		return "go.core.Media.scrape_metadata_event"
	}
	return ""
}

func (a *App) submitOriginalCoreBroadcast(ctx context.Context, kind string, raw json.RawMessage) {
	if kind == "config.updated" {
		a.submitOriginalMediaServerConfigChanged(ctx, raw)
		return
	}
	if kind == "site.updated" {
		for _, kind := range []string{"userdata", "clear", "icon"} {
			if !a.admitOriginalBackground() {
				return
			}
			go func(kind string) {
				defer a.wg.Done()
				handler := "go.core.Site.cache_site_userdata"
				consume := a.consumeOriginalCoreSiteUpdated
				if kind == "clear" {
					handler, consume = "go.core.Site.clear_site_data", a.consumeOriginalCoreSiteDataClear
				} else if kind == "icon" {
					handler, consume = "go.core.Site.cache_site_icon", a.consumeOriginalCoreSiteIcon
				}
				if err := consume(ctx, raw); err != nil {
					a.reportOriginalPluginExecutionError(ctx, "system", "站点更新事件处理失败", map[string]any{"type": "event", "event_type": "site.updated", "event_handle": handler}, err)
				}
			}(kind)
		}
		return
	}
	if kind == "site.deleted" {
		for _, subscription := range []bool{false, true} {
			if !a.admitOriginalBackground() {
				return
			}
			go func(subscription bool) {
				defer a.wg.Done()
				if err := a.consumeOriginalCoreSiteDeleted(ctx, raw, subscription); err != nil {
					handler := "go.core.Search.remove_site"
					if subscription {
						handler = "go.core.Subscribe.remove_site"
					}
					a.reportOriginalPluginExecutionError(ctx, "system", "站点引用清理事件处理失败", map[string]any{"type": "event", "event_type": kind, "event_handle": handler}, err)
				}
			}(subscription)
		}
		return
	}
	if originalCoreBroadcastHandler(kind) == "" || !a.admitOriginalBackground() {
		return
	}
	go func() {
		defer a.wg.Done()
		switch kind {
		case "metadata.scrape":
			if err := a.consumeOriginalCoreMetadataScrape(ctx, raw); err != nil {
				a.reportOriginalPluginExecutionError(ctx, "system", "媒体刮削事件处理失败", map[string]any{"type": "event", "event_type": kind, "event_handle": originalCoreBroadcastHandler(kind)}, err)
			}
		case "downloadfile.deleted":
			if err := a.consumeOriginalCoreDownloadFileDeleted(ctx, raw); err != nil {
				a.reportOriginalPluginExecutionError(ctx, "system", "下载文件删除事件处理失败", map[string]any{"type": "event", "event_type": kind, "event_handle": originalCoreBroadcastHandler(kind)}, err)
			}
		case "command.excute":
			var input map[string]json.RawMessage
			invalid := func() {
				a.reportOriginalPluginExecutionError(ctx, "system", "命令事件处理失败", map[string]any{"type": "event", "event_type": kind, "event_handle": originalCoreBroadcastHandler(kind)}, errors.New("invalid command event data"))
			}
			if json.Unmarshal(raw, &input) != nil || input == nil {
				invalid()
				return
			}
			if !originalBaseJSONTruthy(input["cmd"]) {
				return
			}
			var command string
			if json.Unmarshal(input["cmd"], &command) != nil {
				invalid()
				return
			}
			for _, field := range []string{"user", "source", "channel"} {
				if len(input[field]) == 0 {
					input[field] = json.RawMessage(`null`)
				}
			}
			if _, err := a.dispatchOriginalCommand(ctx, originalCommandEvent{Text: command, Channel: input["channel"], Source: input["source"], User: input["user"]}); err != nil {
				a.reportOriginalPluginExecutionError(ctx, "system", "执行插件命令出错", nil, err)
			}
		case "module.reload":
			a.initOriginalPluginCommands()
		case "plugin.reload":
			var input struct {
				PluginID string `json:"plugin_id"`
			}
			if json.Unmarshal(raw, &input) == nil && input.PluginID != "" {
				a.updateOriginalPluginJobs(ctx, input.PluginID)
			}
		}
	}()
}

func originalCoreEventHandlerInventory() []plugins.EventHandlerInfo {
	items := make([]plugins.EventHandlerInfo, 0, 4)
	for _, kind := range []string{"command.excute", "module.reload", "plugin.reload", "site.updated", "downloadfile.deleted", "metadata.scrape"} {
		items = append(items, plugins.EventHandlerInfo{EventType: kind, HandlerIdentifier: originalCoreBroadcastHandler(kind), Status: "enabled"})
	}
	for _, handler := range []string{"go.core.Search.remove_site", "go.core.Subscribe.remove_site"} {
		items = append(items, plugins.EventHandlerInfo{EventType: "site.deleted", HandlerIdentifier: handler, Status: "enabled"})
	}
	items = append(items, plugins.EventHandlerInfo{EventType: "site.updated", HandlerIdentifier: "go.core.Site.clear_site_data", Status: "enabled"})
	items = append(items, plugins.EventHandlerInfo{EventType: "site.updated", HandlerIdentifier: "go.core.Site.cache_site_icon", Status: "enabled"})
	for _, moduleID := range []string{"EmbyModule", "JellyfinModule", "PlexModule", "TrimeMediaModule", "UgreenModule", "ZSpaceModule"} {
		items = append(items, plugins.EventHandlerInfo{EventType: "config.updated", HandlerIdentifier: "go.core." + moduleID + ".handle_config_changed", Status: "enabled"})
	}
	return items
}
