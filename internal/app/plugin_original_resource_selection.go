package app

import (
	"context"
	"encoding/json"
	"slices"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

// batch_download consumes updated_contexts when it is not None, including an
// explicitly empty list. It does not use the input contexts replacement as an
// output. Values stay full source Context snapshots across the SDK boundary.
func (a *App) originalResourceSelection(ctx context.Context, contexts []json.RawMessage, downloader json.RawMessage, origin string) ([]json.RawMessage, error) {
	if contexts == nil {
		contexts = []json.RawMessage{}
	}
	if len(downloader) == 0 {
		downloader = json.RawMessage(`null`)
	}
	raw, err := json.Marshal(map[string]any{"contexts": contexts, "downloader": downloader, "origin": origin, "updated": false, "updated_contexts": nil, "source": "未知拦截源"})
	if err != nil {
		return nil, err
	}
	event := &plugins.MutableEvent{Type: "resource.selection", Data: raw}
	a.sendOriginalChainEvent(ctx, event)
	if !originalBaseJSONTruthy(event.Data) {
		return contexts, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(event.Data, &fields) != nil || fields == nil {
		return nil, bad("PLUGIN_EVENT_INVALID", "资源选择事件字段无效")
	}
	if originalBaseJSONTruthy(fields["updated"]) && len(fields["updated_contexts"]) > 0 && string(fields["updated_contexts"]) != "null" {
		var selected []json.RawMessage
		if json.Unmarshal(fields["updated_contexts"], &selected) != nil {
			return nil, bad("PLUGIN_EVENT_INVALID", "资源选择事件返回的上下文不是列表")
		}
		return selected, nil
	}
	return contexts, nil
}

// Enter the existing subscription candidate batch before native sorting and
// material verification. A returned Context is still checked against the
// selected local site and subscription identity; it is not a material receipt.
func (a *App) originalSubscriptionResourceSelection(ctx context.Context, s Subscription, request SearchRequest, candidates []Candidate) ([]Candidate, error) {
	a.pluginOriginalEventsMu.Lock()
	active := len(a.pluginOriginalChainHandlers["resource.selection"]) > 0
	a.pluginOriginalEventsMu.Unlock()
	if !active {
		return candidates, nil
	}
	subscribe, err := a.subscriptionSourceProjection(ctx, s)
	if err != nil {
		return nil, err
	}
	origin, err := subscriptionAcquisitionSource(subscribe)
	if err != nil {
		return nil, err
	}
	_, downloader, err := a.originalDownloadSelection(s.DownloaderID, SiteConfig{})
	if err != nil {
		return nil, err
	}
	var media json.RawMessage
	if request.originalSearchInfo != nil {
		media = request.originalSearchInfo.MediaInfo
	}
	if len(media) == 0 {
		for _, candidate := range candidates {
			if len(candidate.Resource.ModuleMediaFields) > 0 {
				media = candidate.Resource.ModuleMediaFields
				break
			}
		}
	}
	if len(media) == 0 {
		resolved, e := a.sourceRecognizeSubscriptionTMDBMedia(ctx, s)
		if e != nil {
			return nil, e
		}
		media = resolved.MediaInfo
	}
	contexts := []json.RawMessage{}
	originals := []Candidate{}
	for _, candidate := range candidates {
		if (!candidate.Accepted || candidate.NeedsConfirmation) && !(s.Media.MediaType == "tv" && automaticPackCandidate(candidate, s.Season)) {
			continue
		}
		snapshot, e := a.prepareDownloadContext(ctx, "search_resource", candidate.Resource.SiteID+":"+candidate.Resource.ID, "", candidate.Resource)
		if e != nil {
			return nil, e
		}
		if e = a.buildOriginalDownloadContext(ctx, &snapshot, candidate, s.Media, media); e != nil {
			return nil, e
		}
		snapshot.Original.ResourceSource = "search"
		snapshot.Original.MediaIsTarget = true
		raw, e := json.Marshal(snapshot.Original)
		if e != nil {
			return nil, e
		}
		contexts = append(contexts, raw)
		originals = append(originals, candidate)
	}
	selected, err := a.originalResourceSelection(ctx, contexts, downloader, origin)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(selected))
	for _, raw := range selected {
		var full originalDownloadContext
		if json.Unmarshal(raw, &full) != nil {
			return nil, bad("PLUGIN_EVENT_INVALID", "资源选择上下文无效")
		}
		value, e := decodeSourceJSON(full.Media)
		if e != nil || sourceMap(value) == nil {
			return nil, originalResourceProjectionError()
		}
		fields := sourceMap(value)
		id, e := originalResourceInteger(fields["tmdb_id"])
		if e != nil || id != s.Media.ID || sourceText(fields["type"]) != map[string]string{"movie": "电影", "tv": "电视剧"}[s.Media.MediaType] {
			return nil, bad("REVIEW_MEDIA_CHANGED", "资源选择上下文离开当前订阅身份")
		}
		var candidate Candidate
		found := false
		for i, input := range contexts {
			if originalBaseJSONEqual(input, raw) {
				candidate = originals[i]
				found = true
				break
			}
		}
		if !found {
			resource, e := a.originalResourceFromSourceTorrent(ctx, SiteConfig{}, full.Torrent)
			if e != nil {
				return nil, e
			}
			if len(request.SiteIDs) > 0 && !slices.Contains(request.SiteIDs, resource.SiteID) {
				return nil, bad("REVIEW_SITE_CHANGED", "资源选择上下文离开已选站点")
			}
			candidate = Candidate{Resource: resource, Accepted: true}
		}
		var options releasemeta.Options
		if scope, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition); ok {
			options = scope.options
		} else {
			options, e = a.globalRecognitionOptions(ctx)
			if e != nil {
				return nil, e
			}
		}
		parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
		if e != nil {
			return nil, e
		}
		meta, e := parser.ParseTitle(candidate.Resource.Title, candidate.Resource.SourceSubtitle, nil)
		if e != nil {
			return nil, e
		}
		decoded, e := decodeSourceJSON(full.Meta)
		if e != nil || sourceMap(decoded) == nil {
			return nil, originalResourceProjectionError()
		}
		for key, v := range sourceMap(decoded) {
			if !slices.Contains([]string{"name", "edition", "season_episode", "episode_list"}, key) {
				meta.Set(key, v)
			}
		}
		seasons, episodes, e := larkMetaNumbers(meta)
		if e != nil {
			return nil, e
		}
		if len(seasons) > 0 {
			candidate.Season = seasons[0]
		}
		candidate.Episodes = episodes
		state, e := meta.EncodeState()
		if e != nil {
			return nil, e
		}
		candidate.originalSelectionMeta = state
		candidate.originalSelectionContext = append(json.RawMessage(nil), raw...)
		if full.AllowedEpisodes != nil {
			encoded, e := json.Marshal(full.AllowedEpisodes)
			if e != nil {
				return nil, e
			}
			var allowed []int
			if json.Unmarshal(encoded, &allowed) != nil {
				return nil, bad("PLUGIN_EVENT_INVALID", "资源选择允许集数无效")
			}
			if allowed == nil {
				allowed = []int{}
			}
			candidate.originalSelectionAllowed = &allowed
		}
		out = append(out, candidate)
	}
	return out, nil
}

func originalSelectionAllows(candidate Candidate, episode int) bool {
	return candidate.originalSelectionAllowed == nil || slices.Contains(*candidate.originalSelectionAllowed, episode)
}
