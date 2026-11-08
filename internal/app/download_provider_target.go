package app

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/decision"
)

func (a *App) providerSubscriptionDownloadModel(ctx context.Context, in PreviewRequest, auto bool, selected *originalDownloadContext) (json.RawMessage, error) {
	owner := in.originalSubscription
	guard, _ := ctx.Value(subscriptionGuardKey{}).(subscriptionIdentity)
	if !auto || in.chatOrigin != nil || in.chatProvider != nil || owner == nil || in.originalSearchInfo == nil ||
		in.SubscriptionID == "" || in.Revision < 1 || guard.ID != in.SubscriptionID || guard.Revision != in.Revision ||
		owner.ID != in.SubscriptionID || owner.Revision != in.Revision || owner.Season != in.Season ||
		mediaKey(owner.Media) != mediaKey(in.originalSearchInfo.Media) {
		return nil, bad("INVALID_DOWNLOAD_CONTEXT", "下载预览无法核对来源订阅")
	}
	if err := a.guardSubscriptionContext(ctx); err != nil {
		return nil, err
	}
	var current Subscription
	if err := a.Store.Get(ctx, "subscription", owner.ID, &current); err != nil {
		return nil, err
	}
	if current.Revision != owner.Revision || current.Season != owner.Season || mediaKey(current.Media) != mediaKey(owner.Media) {
		return nil, bad("SUBSCRIPTION_CHANGED", "订阅身份或季号已变化，请重新检查")
	}
	if selected != nil {
		return selected.Media, nil
	}
	return in.originalSearchInfo.MediaInfo, nil
}

func providerDownloadTarget(in SearchRequest, identity catalog.Media, raw json.RawMessage, auto bool) (decision.Target, error) {
	if !providerNativeMedia(identity) || in.TMDBID != 0 || in.MediaType != identity.MediaType {
		return decision.Target{}, invalidSubscriptionProvider()
	}
	media, err := subscriptionProviderMedia(sourceMediaInformation{MediaInfo: raw}, identity.ProviderSource, identity.ProviderID, identity.MediaType)
	if err != nil {
		return decision.Target{}, err
	}
	target := decision.Target{Media: media, ProviderSearchKey: mediaKey(media), Season: in.Season, Episodes: in.Episodes, Mappings: in.Mappings, Auto: auto}
	if media.MediaType == "movie" {
		return target, nil
	}
	fields := originalChatLatestMediaFields(raw)
	if in.chatUnscoped {
		if auto || len(in.Episodes) != 0 {
			return target, bad("INVALID_DOWNLOAD_CONTEXT", "无法核对无季集的来源影视")
		}
		if target.Season == 0 {
			if season, err := originalResourceInteger(fields.Get("season")); err == nil && season > 0 && season <= 99 {
				target.Season = int(season)
			}
		}
		target.ProviderUnscoped = true
		return target, nil
	}
	if in.Season < 1 {
		return target, bad("INVALID_SEASON", "请先选择标准季号")
	}
	target.KnownEpisodes = subscriptionProviderEpisodes(raw, in.Season)
	year, _ := originalResourceInteger(sourceMap(fields.Get("season_years"))[strconv.Itoa(in.Season)])
	if year > 0 && year <= 9999 {
		target.SeasonYear = int(year)
	}
	today := time.Now().In(shanghai()).Format("2006-01-02")
	for _, episode := range target.KnownEpisodes {
		if providerEpisodeAired(in.EpisodeDates[episode], media.ReleaseDate, today) {
			target.AiredEpisodes = append(target.AiredEpisodes, episode)
		}
		if slices.Contains(in.AllowEarly, episode) {
			target.AllowEarly = append(target.AllowEarly, episode)
		}
	}
	for _, episode := range in.Episodes {
		if !slices.Contains(target.KnownEpisodes, episode) {
			return target, bad("UNKNOWN_EPISODE", "选择的剧集尚未被来源元数据确认")
		}
	}
	return target, nil
}

func providerEpisodeAired(date, releaseDate, today string) bool {
	return date <= today && (date != "" || releaseDate <= today)
}
