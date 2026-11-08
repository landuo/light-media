package app

import (
	"context"
	"encoding/json"
	"strconv"

	"light-media/internal/plugins"
)

// Fixed SubscribeChain.__apply_episodes_refresh: a mutable chain result only
// overrides when both updated and total_episode are truthy, and can only raise
// the current provider total. The owning subscription path persists the value.
func (a *App) applyStandardSubscriptionEpisodes(ctx context.Context, current, season int, mediaInfo json.RawMessage, subscriptionID any, scene string) (int, error) {
	value, err := decodeSourceJSON(mediaInfo)
	if err != nil || sourceMap(value) == nil {
		return current, bad("PLUGIN_EVENT_INVALID", "订阅集数事件缺少完整媒体信息")
	}
	media := sourceMap(value)
	data := map[string]any{"tmdbid": media["tmdb_id"], "doubanid": media["douban_id"], "bangumiid": media["bangumi_id"], "anilistid": media["anilist_id"], "media_source": media["source"], "media_id": nil, "season": season, "mediainfo": value, "current_total_episode": current, "subscribe_id": subscriptionID, "scene": scene, "updated": false, "total_episode": nil, "source": "未知来源", "reason": ""}
	if data["media_source"] == "themoviedb" {
		data["media_id"] = sourceNumberString(media["tmdb_id"])
	} else {
		provider := originalChatMediaProvider{Source: sourceText(media["source"]), ID: sourceStringValue(media["media_id"])}
		if provider.ID == "" {
			provider.ID = sourceStringValue(media[provider.field()])
		}
		if provider.sourceOnly() {
			data["media_id"] = provider.ID
		}
	}
	event := plugins.MutableEvent{Type: "subscribe.episodes.refresh", Data: mustCleanSeedJSON(data)}
	if !a.sendOriginalChainEvent(ctx, &event) || !originalBaseJSONTruthy(event.Data) {
		return current, nil
	}
	result, err := decodeSourceJSON(event.Data)
	if err != nil || sourceMap(result) == nil {
		return current, bad("PLUGIN_EVENT_INVALID", "订阅集数事件结果不是对象")
	}
	fields := sourceMap(result)
	if !sourceSubscriptionTruth(fields["updated"]) || !sourceSubscriptionTruth(fields["total_episode"]) {
		return current, nil
	}
	var total int
	switch value := fields["total_episode"].(type) {
	case json.Number:
		total, err = strconv.Atoi(value.String())
	case bool:
		if value {
			total = 1
		}
	default:
		err = bad("PLUGIN_EVENT_INVALID", "订阅集数覆盖值须为整数")
	}
	if err != nil {
		return current, bad("PLUGIN_EVENT_INVALID", "订阅集数覆盖值须为整数")
	}
	return max(current, total), nil
}

// Recognition is an independent ordinary module extension. Absence of an
// episodes listener does not skip recognize_media.
func (a *App) refreshStandardSubscriptionEpisodes(ctx context.Context, s Subscription, current int) (int, error) {
	resolved, err := a.resolveStandardSubscriptionRecognition(ctx, s, nil)
	if err != nil {
		return current, err
	}
	return a.refreshStandardSubscriptionEpisodesResolved(ctx, s, current, resolved)
}

func (a *App) refreshStandardSubscriptionEpisodesResolved(ctx context.Context, s Subscription, current int, resolved *standardSubscriptionRecognition) (int, error) {
	value, err := decodeSourceJSON(resolved.Information.MediaInfo)
	if err != nil {
		return current, err
	}
	current = len(sourceRows(sourceMap(sourceMap(value)["seasons"])[strconv.Itoa(s.Season)]))
	return a.applyStandardSubscriptionEpisodes(ctx, current, s.Season, resolved.Information.MediaInfo, s.ID, "precheck")
}
