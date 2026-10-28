package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

// Private, encrypted download context. Public Resource/Media responses must not
// carry provider dictionaries, tracker credentials or enclosure URLs.
type originalDownloadContext struct {
	Extra               map[string]json.RawMessage `json:"-"`
	Meta                json.RawMessage            `json:"meta_info"`
	Media               json.RawMessage            `json:"media_info"`
	Torrent             json.RawMessage            `json:"torrent_info"`
	RecognizeFailures   int                        `json:"media_recognize_fail_count"`
	ResourceSource      string                     `json:"resource_source"`
	MatchSource         string                     `json:"match_source"`
	CandidateRecognized bool                       `json:"candidate_recognized"`
	MediaIsTarget       bool                       `json:"media_info_is_target"`
	AllowedEpisodes     any                        `json:"allowed_episodes"`
	FullCoverage        bool                       `json:"confirmed_full_coverage"`
	SelectedEpisodes    *[]int                     `json:"selected_episodes,omitempty"`
}

func (value originalDownloadContext) MarshalJSON() ([]byte, error) {
	type fields originalDownloadContext
	raw, err := json.Marshal(fields(value))
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	for key, v := range value.Extra {
		if _, known := out[key]; !known {
			out[key] = v
		}
	}
	return json.Marshal(out)
}

func (value *originalDownloadContext) UnmarshalJSON(raw []byte) error {
	type fields originalDownloadContext
	var decoded fields
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extra); err != nil {
		return err
	}
	for _, key := range []string{"meta_info", "media_info", "torrent_info", "media_recognize_fail_count", "resource_source", "match_source", "candidate_recognized", "media_info_is_target", "allowed_episodes", "confirmed_full_coverage", "selected_episodes"} {
		delete(extra, key)
	}
	*value = originalDownloadContext(decoded)
	value.Extra = extra
	return nil
}

func (a *App) originalDownloadHasObservers() bool {
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	return len(a.pluginOriginalChainHandlers["resource.download"]) > 0 || len(a.pluginOriginalBroadcast.handlers["download.added"]) > 0
}

func (a *App) completeOriginalDownloadContext(ctx context.Context, snapshot *downloadContextSnapshot, resource Candidate, identity catalog.Media, resolved json.RawMessage) error {
	if !a.originalDownloadHasObservers() {
		groups, err := a.originalPluginModules(ctx, "")
		if err != nil {
			return err
		}
		if !originalModuleGroupsHaveMethod(groups, "download_added") {
			return nil
		}
	}
	return a.buildOriginalDownloadContext(ctx, snapshot, resource, identity, resolved)
}

func (a *App) buildOriginalDownloadContext(ctx context.Context, snapshot *downloadContextSnapshot, resource Candidate, identity catalog.Media, resolved json.RawMessage) error {
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: snapshot.Options})
	if err != nil {
		return err
	}
	var state []byte
	if len(snapshot.SourceMeta) > 0 {
		state = append([]byte(nil), snapshot.SourceMeta...)
	} else {
		meta, err := parser.ParseTitle(snapshot.Title, snapshot.Subtitle, nil)
		if err != nil {
			return err
		}
		state, err = meta.EncodeState()
		if err != nil {
			return err
		}
	}
	fields, err := sourceNoticeMeta(state)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	torrent, err := a.originalTitleFilterResourceModel(ctx, resource)
	if err != nil {
		return err
	}
	if len(resolved) == 0 && validMedia(identity.MediaType, identity.ID) {
		information, err := a.sourceRecognizeTMDBMedia(ctx, identity)
		if err != nil {
			return err
		}
		resolved = information.MediaInfo
	}
	if len(resolved) == 0 {
		resolved = json.RawMessage(`null`)
	} // Native manual Add permits an unidentified title.
	snapshot.Original = &originalDownloadContext{Meta: meta, Media: append(json.RawMessage(nil), resolved...), Torrent: torrent, ResourceSource: "unknown", MatchSource: "unknown"}
	return nil
}

// A full plugin result may cross the existing public search/cache boundary.
// Its private snapshot is identity-bound and encrypted separately; it never
// supplies a material URL or a Client.Add receipt.
func (a *App) originalCachedDownloadInputs(ctx context.Context, resource sites.Resource, identity catalog.Media) (sites.Resource, json.RawMessage, error) {
	cached, err := a.loadDownloadContext(ctx, "search_resource", resource.SiteID+":"+resource.ID, "")
	if err != nil {
		return resource, nil, err
	}
	if cached == nil || cached.Original == nil || cached.Title != resource.Title {
		return resource, nil, nil
	}
	resource.ModuleSourceFields = append(json.RawMessage(nil), cached.Original.Torrent...)
	resolved := append(json.RawMessage(nil), cached.Original.Media...)
	if validMedia(identity.MediaType, identity.ID) {
		var fields struct {
			TMDBID int64  `json:"tmdb_id"`
			Type   string `json:"type"`
		}
		if json.Unmarshal(resolved, &fields) != nil || fields.TMDBID != identity.ID || fields.Type != map[string]string{"movie": "电影", "tv": "电视剧"}[identity.MediaType] {
			resolved = nil
		}
	}
	return resource, resolved, nil
}

func originalDownloadEpisodes(meta json.RawMessage, selected []int) []int {
	if len(selected) > 0 {
		out := slices.Clone(selected)
		slices.Sort(out)
		return slices.Compact(out)
	}
	var fields struct {
		Episodes []int `json:"episode_list"`
	}
	_ = json.Unmarshal(meta, &fields)
	if fields.Episodes == nil {
		return []int{}
	}
	return fields.Episodes
}

func originalDownloadContextCategory(snapshot *downloadContextSnapshot) (any, bool) {
	if snapshot == nil || snapshot.Original == nil {
		return nil, false
	}
	value, err := decodeSourceJSON(snapshot.Original.Media)
	if err != nil || sourceMap(value) == nil {
		return nil, false
	}
	category, present := sourceMap(value)["category"]
	return category, present
}

// download_single consumes only cancel and options.save_path. Replacing the
// event's downloader/episodes/context does not grant a new downloader or media
// identity. The Go ABI carries value snapshots, not Python heap aliases.
func (a *App) originalResourceDownload(ctx context.Context, snapshot *downloadContextSnapshot, path string, selected []int, origin string, downloader json.RawMessage, client *download.Client) (string, error) {
	if snapshot == nil || snapshot.Original == nil {
		return path, nil
	}
	var category any
	var media map[string]json.RawMessage
	if json.Unmarshal(snapshot.Original.Media, &media) == nil {
		category = media["category"]
	}
	if len(downloader) == 0 {
		downloader = json.RawMessage(`null`)
	}
	episodes := originalDownloadEpisodes(snapshot.Original.Meta, selected)
	raw, err := json.Marshal(map[string]any{"context": snapshot.Original, "episodes": episodes, "channel": nil, "origin": origin, "downloader": downloader, "options": map[string]any{"save_path": path, "userid": nil, "username": nil, "media_category": category}, "cancel": false, "source": "未知拦截源", "reason": ""})
	if err != nil {
		return "", err
	}
	event := &plugins.MutableEvent{Type: "resource.download", Data: raw}
	a.sendOriginalChainEvent(ctx, event)
	if originalBaseJSONTruthy(event.Data) {
		value, err := decodeSourceJSON(event.Data)
		if err != nil || sourceMap(value) == nil {
			return "", bad("PLUGIN_EVENT_INVALID", "资源下载事件字段无效")
		}
		fields := sourceMap(value)
		if sourceSubscriptionTruth(fields["cancel"]) {
			return "", bad("REVIEW_DOWNLOAD_CANCELLED", "下载被事件取消")
		}
		options := fields["options"]
		if sourceSubscriptionTruth(options) {
			options, ok := options.(map[string]any)
			if !ok {
				return "", bad("PLUGIN_EVENT_INVALID", "资源下载事件 options 无效")
			}
			if changed, present := options["save_path"]; present {
				if changed == nil {
					path, err = client.DefaultSavePath(ctx)
					if err != nil {
						return "", err
					}
				} else {
					var ok bool
					path, ok = changed.(string)
					if !ok {
						return "", bad("INVALID_DOWNLOAD_PATH", "资源下载事件返回的路径无效")
					}
				}
			}
		}
	}
	if err := safeRoot(path); err != nil {
		return "", err
	}
	if !sourceAllowed(a.Settings(), path) {
		return "", bad("DIRECTORY_SOURCE_NOT_ALLOWED", "插件下载路径不在已授权的文件目录内")
	}
	for _, root := range a.Settings().Roots {
		if _, overlap := sharedDirectoryPath(root.Path, path); overlap {
			return "", bad("DIRECTORY_OVERLAP", "插件下载路径与媒体库目录重叠")
		}
	}
	selection := episodes
	if selected != nil {
		selection = append([]int{}, selected...)
		slices.Sort(selection)
		selection = slices.Compact(selection)
	}
	snapshot.Original.SelectedEpisodes = &selection
	snapshot.OriginalEpisodes = episodes
	snapshot.OriginalOrigin = origin
	return filepath.Clean(path), nil
}

func (a *App) emitOriginalDownloadAdded(ctx context.Context, hash string, snapshot *downloadContextSnapshot, downloader json.RawMessage) {
	if snapshot == nil || snapshot.Original == nil {
		return
	}
	if len(downloader) == 0 {
		downloader = json.RawMessage(`null`)
	}
	a.submitOriginalDownloadAdded(ctx, snapshot)
	a.emitStandardBusinessEvent(ctx, "download.added", map[string]any{"hash": hash, "context": snapshot.Original, "username": nil, "downloader": downloader, "episodes": snapshot.OriginalEpisodes, "source": snapshot.OriginalOrigin})
}
