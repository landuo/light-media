package app

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"light-media/internal/decision"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

func originalChatProviderSearchInformation(raw json.RawMessage, provider *originalChatMediaProvider, kind string) (sourceMediaInformation, error) {
	if !provider.sourceOnly() {
		return sourceMediaInformation{}, bad("MEDIA_SOURCE_CHANGED", "所选媒体的来源身份或类型发生变化")
	}
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceSubscriptionTruth(sourceMap(value)["tmdb_id"]) {
		return sourceMediaInformation{}, bad("MEDIA_SOURCE_CHANGED", "所选媒体的来源身份或类型发生变化")
	}
	info := sourceMediaInformation{MediaInfo: raw, ProviderInfo: raw}
	info.Media, err = subscriptionProviderMedia(info, provider.Source, provider.ID, kind)
	if err != nil {
		return sourceMediaInformation{}, bad("MEDIA_SOURCE_CHANGED", "所选媒体的来源身份或类型发生变化")
	}
	return info, nil
}

func originalChatProviderSearchTitle(ctx context.Context, info sourceMediaInformation, provider *originalChatMediaProvider) (sourceMediaInformation, error) {
	options := ctx.Value(resourceRecognitionKey{}).(resourceRecognition).options
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return info, err
	}
	meta, err := parser.ParseTitle(info.Media.Title, nil, nil)
	if err != nil {
		return info, err
	}
	value, err := decodeSourceJSON(info.MediaInfo)
	if err != nil {
		return info, err
	}
	fields := sourceMap(value)
	if name := meta.Name(); name != "" {
		fields["title"] = name
	}
	fields["season"] = nil
	if season := meta.Number("begin_season"); season != nil {
		fields["season"] = json.Number(season.String())
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return info, err
	}
	return originalChatProviderSearchInformation(raw, provider, info.Media.MediaType)
}

func originalChatProviderEpisodes(fields map[string]any, season int) []int {
	var episodes []int
	for _, value := range sourceRows(sourceMap(fields["seasons"])[strconv.Itoa(season)]) {
		if ep, err := originalResourceInteger(value); err == nil && ep > 0 && ep <= 9999 {
			episodes = append(episodes, int(ep))
		}
	}
	slices.Sort(episodes)
	return slices.Compact(episodes)
}

func originalChatProviderHasSeasons(info sourceMediaInformation) bool {
	fields := originalChatLatestMediaFields(info.MediaInfo)
	return len(sourceMap(fields.Get("seasons"))) != 0
}

func (a *App) originalChatProviderCoverageMedia(ctx context.Context, info sourceMediaInformation, provider *originalChatMediaProvider) (sourceMediaInformation, error) {
	if originalChatProviderHasSeasons(info) {
		return info, nil
	}
	fields := originalChatLatestMediaFields(info.MediaInfo)
	// MoviePilot's selected card does not warm the exact-ID cache. Our
	// verification does, so bypass that read for this single coverage retry.
	ctx = context.WithValue(ctx, originalHostMediaFreshContextKey{}, true)
	raw, err := a.originalHostMediaRecognize(ctx, plugins.MediaRecognizeRequest{
		Type: "tv", Source: provider.Source, MediaID: provider.ID,
		EpisodeGroup: sourceText(fields.Get("episode_group")),
	})
	if err != nil || !originalModuleResultTruthy(raw) {
		return info, err
	}
	return originalChatProviderSearchInformation(raw, provider, "tv")
}

// SearchChain receives the selected media model even when the inventory probe
// recovered seasons independently. Only native scope checks need those rows.
func originalChatProviderWithCoverage(info sourceMediaInformation, provider *originalChatMediaProvider, scope []originalChatMediaSeason) (sourceMediaInformation, error) {
	if originalChatProviderHasSeasons(info) || len(scope) == 0 {
		return info, nil
	}
	value, err := decodeSourceJSON(info.MediaInfo)
	if err != nil {
		return info, err
	}
	fields := sourceMap(value)
	seasons, years := map[string][]int{}, map[string]int{}
	for _, season := range scope {
		key := strconv.Itoa(season.number)
		seasons[key] = season.all
		if season.year > 0 {
			years[key] = season.year
		}
	}
	fields["seasons"], fields["season_years"] = seasons, years
	raw, err := json.Marshal(fields)
	if err != nil {
		return info, err
	}
	return originalChatProviderSearchInformation(raw, provider, info.Media.MediaType)
}

func originalChatProviderSeasonScope(meta *releasemeta.MetaObject, info sourceMediaInformation, exists *originalSubscriptionExistence) ([]originalChatMediaSeason, error) {
	value, err := decodeSourceJSON(info.MediaInfo)
	if err != nil {
		return nil, err
	}
	fields := sourceMap(value)
	var wanted []int
	if meta.Number("begin_season") != nil {
		wanted, _, err = larkMetaNumbers(meta)
		if err != nil {
			return nil, err
		}
	} else {
		for key := range sourceMap(fields["seasons"]) {
			if season, err := strconv.Atoi(key); err == nil && season > 0 && season <= 99 {
				wanted = append(wanted, season)
			}
		}
	}
	slices.Sort(wanted)
	var out []originalChatMediaSeason
	for _, season := range slices.Compact(wanted) {
		if season < 1 || season > 99 {
			return nil, bad("INVALID_SEASON", "所选季超出当前资源搜索支持的季号范围")
		}
		all := originalChatProviderEpisodes(fields, season)
		if len(all) == 0 {
			continue
		}
		present, err := exists.episodes(Subscription{Media: info.Media, Season: season})
		if err != nil {
			return nil, err
		}
		missing := slices.DeleteFunc(slices.Clone(all), func(ep int) bool { return slices.Contains(present, ep) })
		year, _ := originalResourceInteger(sourceMap(fields["season_years"])[strconv.Itoa(season)])
		if year < 1 || year > 9999 {
			year = 0
		}
		out = append(out, originalChatMediaSeason{number: season, year: int(year), all: all, needed: missing, whole: len(present) == 0})
	}
	return out, nil
}

func (a *App) originalChatProviderSearchTarget(ctx context.Context, in SearchRequest, auto bool, info sourceMediaInformation) (decision.Target, error) {
	provider := originalChatMediaProviderFrom(ctx)
	if auto || !a.originalChatSubscriptionIngress(ctx) || !provider.sourceOnly() || in.chatProvider == nil || *provider != *in.chatProvider || in.TMDBID != 0 || !providerNativeMedia(info.Media) {
		return decision.Target{}, bad("MEDIA_SOURCE_CHANGED", "无法核对所选媒体的来源身份")
	}
	checked, err := originalChatProviderSearchInformation(info.MediaInfo, provider, in.MediaType)
	if err != nil {
		return decision.Target{}, err
	}
	if mediaKey(checked.Media) != mediaKey(info.Media) {
		return decision.Target{}, bad("MEDIA_SOURCE_CHANGED", "无法核对所选媒体的来源身份")
	}
	checked, err = originalChatProviderWithCoverage(checked, provider, in.chatMediaSeasons)
	if err != nil {
		return decision.Target{}, err
	}
	return providerDownloadTarget(in, checked.Media, checked.MediaInfo, false)
}
