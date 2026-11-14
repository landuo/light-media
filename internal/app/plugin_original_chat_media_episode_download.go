package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"

	"light-media/internal/decision"
	"light-media/internal/media"
	"light-media/internal/metainfo"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

type originalChatEpisodeBatch struct {
	mode     string
	needed   []int
	episodes []int
	meta     json.RawMessage
}

// The batch phases may inspect the real single-season material before choosing
// whole Add or file selection. A proven incomplete pack remains available to
// the caller's next phase, but has not reached the downloader.
func (a *App) originalChatDownloadEpisodeBatch(ctx context.Context, request SearchRequest, candidate Candidate, directory originalChatMediaDirectory, needed []int, mode string) (Candidate, error) {
	if request.MediaType != "tv" || (mode != "whole_pack" && mode != "split") {
		return Candidate{}, bad("INVALID_EPISODE_SELECTION", "剧集批量下载阶段无效")
	}
	batch := originalChatEpisodeBatch{mode: mode, needed: slices.Clone(needed)}
	slices.Sort(batch.needed)
	batch.needed = slices.Compact(batch.needed)
	if mode == "split" {
		batch.needed = slices.DeleteFunc(batch.needed, func(ep int) bool { return !originalSelectionAllows(candidate, ep) })
	}
	err := a.originalChatSubmitDownloadMedia(ctx, request, candidate, directory, &batch, false)
	if err != nil && pluginErrorCodeInternal(err) != "CHAT_DOWNLOAD_INCOMPLETE_PACK" {
		return Candidate{}, err
	}
	if err != nil && len(candidate.originalSelectionContext) != 0 {
		var original originalDownloadContext
		if decodeErr := json.Unmarshal(candidate.originalSelectionContext, &original); decodeErr != nil {
			return Candidate{}, decodeErr
		}
		var updateErr error
		original.Meta, updateErr = originalChatEpisodeContextMeta(batch.meta)
		if updateErr != nil {
			return Candidate{}, updateErr
		}
		candidate.originalSelectionContext, updateErr = json.Marshal(original)
		if updateErr != nil {
			return Candidate{}, updateErr
		}
	}
	candidate.Episodes = batch.episodes
	candidate.originalSelectionMeta = batch.meta
	return candidate, err
}

func originalChatEpisodeContextMeta(state json.RawMessage) (json.RawMessage, error) {
	meta, err := sourceNoticeMeta(state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(meta)
}

func (a *App) planOriginalChatEpisodeBatch(ctx context.Context, input *PreviewRequest, target *decision.Target, resource sites.Resource, material metainfo.Material) error {
	batch := input.chatEpisodeBatch
	options := ctx.Value(resourceRecognitionKey{}).(resourceRecognition).options
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return err
	}
	var meta *releasemeta.MetaObject
	if len(input.sourceMeta) > 0 {
		meta, err = releasemeta.DecodeMetaObjectState(input.sourceMeta)
	} else {
		subtitle := resource.SourceSubtitle
		if subtitle == nil && resource.Subtitle != "" {
			subtitle = &resource.Subtitle
		}
		meta, err = parser.ParseTitle(resource.Title, subtitle, nil)
	}
	if err != nil {
		return err
	}
	seasons, listed, err := larkMetaNumbers(meta)
	if err != nil {
		return err
	}
	if len(seasons) != 1 || seasons[0] != target.Season ||
		(batch.mode == "whole_pack" && len(listed) != 0) ||
		(batch.mode == "split" && len(listed) != 0 && !slices.ContainsFunc(listed, func(ep int) bool { return slices.Contains(batch.needed, ep) })) {
		return bad("REVIEW_TORRENT_COVERAGE", "种子标题无法核对当前选集阶段")
	}
	parse, err := sourceIntentEpisodeParser(ctx)
	if err != nil {
		return err
	}
	covered := map[int]bool{}
	for _, file := range material.Files {
		if !isVideo(file.Path) {
			continue
		}
		episodes, err := parse(filepath.Base(file.Path))
		if err != nil {
			return err
		}
		if len(episodes) == 0 {
			return bad("REVIEW_TORRENT_COVERAGE", "种子包含无法确认集数的视频")
		}
		for _, ep := range episodes {
			covered[ep] = true
		}
	}
	if len(covered) == 0 {
		return bad("REVIEW_TORRENT_COVERAGE", "种子没有可核对的剧集视频")
	}
	var full []int
	for ep := range covered {
		full = append(full, ep)
	}
	slices.Sort(full)
	if err := meta.SetEpisodes(full[0], full[len(full)-1]); err != nil {
		return err
	}
	batch.meta, err = meta.EncodeState()
	if err != nil {
		return err
	}
	for _, ep := range batch.needed {
		if covered[ep] {
			batch.episodes = append(batch.episodes, ep)
		}
	}
	if len(batch.episodes) == 0 {
		return bad("REVIEW_TORRENT_COVERAGE", "种子实际视频不包含所需剧集")
	}
	if batch.mode == "whole_pack" {
		if !slices.Equal(full, batch.episodes) {
			return bad("REVIEW_TORRENT_COVERAGE", "整季包包含目标范围之外的视频")
		}
		if !slices.Equal(batch.episodes, batch.needed) {
			return bad("CHAT_DOWNLOAD_INCOMPLETE_PACK", "整季包只包含部分所需剧集")
		}
		input.sourceMeta = batch.meta
	} else {
		if err := a.proveOriginalChatEpisodeFiles(ctx, parse, material, *target, resource.SiteID, batch.episodes); err != nil {
			return err
		}
		input.selectedEpisodes = slices.Clone(batch.episodes)
	}
	input.Episodes = slices.Clone(batch.episodes)
	target.Episodes = slices.Clone(batch.episodes)
	return nil
}

// Only a whole video whose raw filename episodes are all selected can stay
// wanted in qB/Transmission. An E01-E02 file cannot satisfy E02 alone. Native
// mapping must also identify that same file as the selected standard episodes.
func (a *App) proveOriginalChatEpisodeFiles(ctx context.Context, parse func(string) ([]int, error), material metainfo.Material, target decision.Target, siteID string, selected []int) error {
	known := make([]media.Episode, 0, len(target.KnownEpisodes))
	for _, ep := range target.KnownEpisodes {
		known = append(known, media.Episode{Season: target.Season, Episode: ep})
	}
	coverage := map[int]bool{}
	for _, file := range material.Files {
		if !isVideo(file.Path) {
			continue
		}
		episodes, err := parse(file.Path)
		if err != nil {
			return err
		}
		if len(episodes) == 0 || slices.ContainsFunc(episodes, func(ep int) bool { return !slices.Contains(selected, ep) }) {
			continue
		}
		parsed := a.parseMediaNameContext(ctx, filepath.Base(file.Path))
		if parsed.Status != media.Parsed || parsed.Original == nil {
			return bad("REVIEW_TORRENT_COVERAGE", "选中的视频文件无法核对标准集号")
		}
		resolved := media.Resolve(mediaKey(target.Media), decision.AliasSeasonNumbering(target.Media, target.Season, *parsed.Original), media.Source{Site: siteID}, target.Mappings, known, nil)
		if resolved.Status != media.Parsed || len(resolved.Standard) != len(episodes) || slices.ContainsFunc(resolved.Standard, func(ep media.Episode) bool {
			return ep.Season != target.Season || !slices.Contains(episodes, ep.Episode)
		}) {
			return bad("REVIEW_TORRENT_COVERAGE", "选中的视频文件与下载器集号不一致")
		}
		for _, ep := range episodes {
			coverage[ep] = true
		}
	}
	for _, ep := range selected {
		if !coverage[ep] || (!slices.Contains(target.AiredEpisodes, ep) && !slices.Contains(target.AllowEarly, ep)) {
			return bad("REVIEW_TORRENT_COVERAGE", "种子文件无法独立选择当前目标集")
		}
	}
	return nil
}

func (a *App) verifyOriginalChatSplitDownload(ctx context.Context, in Intent, selected []int) error {
	receipt, err := a.sourcePostAddReceipt(ctx, a.Store.DB, in)
	if err != nil || !slices.Equal(in.SelectedEpisodes, selected) || receipt.Mode != "new_hash" || receipt.Selection.MetadataMissing || !receipt.Selection.Started ||
		slices.ContainsFunc(receipt.Selection.Effects, func(effect sourceEpisodeSelectionEffect) bool {
			return effect.Operation != "files" && (!effect.Acknowledged || effect.Failure != "")
		}) {
		return bad("CHAT_DOWNLOAD_UNCONFIRMED", "下载器已接收种子，按集优先级或启动结果待确认")
	}
	return nil
}
