package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

type originalChatMediaSeason struct {
	number int
	year   int
	all    []int
	needed []int
	whole  bool // The source denotes a wholly absent season with an empty list.
}

var errOriginalChatMediaExists = bad("MEDIA_ALREADY_IN_LIBRARY", "影视已在媒体库")

const (
	originalChatTorrentFailedCode = "CHAT_DOWNLOAD_TORRENT_FAILED"
	originalChatAddRejectedCode   = "REVIEW_CHAT_DOWNLOAD_REJECTED"
	originalChatAddUnknownCode    = "REVIEW_CHAT_DOWNLOAD_UNCONFIRMED"
)

// Only a definite native refusal allows the next chat candidate to acquire the
// same scope. Settle the rejected intent and its reservations with the job.
func (a *App) failOriginalChatDownloadTx(ctx context.Context, tx *sql.Tx, child Job, message string) error {
	var payload struct{ ID string }
	if child.Kind != "download" || json.Unmarshal(child.Payload, &payload) != nil || payload.ID == "" {
		return acquisitionEvidence()
	}
	var in Intent
	if err := qualityRecord(ctx, tx, "intent", payload.ID, &in); err != nil {
		return err
	}
	if in.ID != payload.ID || in.WorkflowID != child.WorkflowID || !in.ChatDownload || in.State != "submitting" || in.SourceModuleStarted || in.SourcePostAddVersion != 1 {
		return acquisitionEvidence()
	}
	var staged downloadContextSnapshot
	if err := a.readQualitySealed(ctx, tx, "download_context", "intent:"+in.ID, &staged); err != nil {
		return err
	}
	if staged.DownloadKind != "intent" || staged.DownloadID != in.ID || !sameHash(staged.Hash, in.Hash) || staged.ChatOrigin == nil || !staged.ChatOrigin.valid() || staged.Original == nil || verifyChatDownloadContext(in, &staged) != nil {
		return acquisitionEvidence()
	}
	in.State, in.Error, in.UpdatedAt = "failed", message, now()
	if err := txPut(ctx, tx, "intent", in.ID, in); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, in.ID)
	return err
}

type originalChatDownloadOrigin struct {
	Channel  string `json:"channel"`
	Source   string `json:"source"`
	UserID   string `json:"userid"`
	Username string `json:"username"`
}

type originalChatDownloadOriginKey struct{}

func (o originalChatDownloadOrigin) valid() bool {
	user := strings.TrimSpace(o.UserID)
	return o.Channel == "飞书" && o.Source == "Lark" && user != "" && !strings.EqualFold(user, "system")
}

func (a *App) withOriginalChatDownloadOrigin(ctx context.Context, reply originalSlashReply) (context.Context, error) {
	_, verified := originalLarkSlashIngress(ctx, reply.info)
	user, err := originalInputID(reply.info.UserID, true)
	if !verified || !a.originalChatSubscriptionIngress(ctx) || err != nil || user == nil || *user != reply.user {
		return ctx, bad("PLUGIN_CAPABILITY_DENIED", "添加下载需要已验证的 Lark 选择")
	}
	origin := originalChatDownloadOrigin{Channel: *reply.info.Channel, Source: *reply.info.Source, UserID: *user, Username: reply.username}
	if !origin.valid() {
		return ctx, bad("PLUGIN_CAPABILITY_DENIED", "添加下载需要已验证的 Lark 选择")
	}
	return context.WithValue(ctx, originalChatDownloadOriginKey{}, origin), nil
}

func (a *App) originalChatDownloadOrigin(ctx context.Context) (originalChatDownloadOrigin, error) {
	origin, ok := ctx.Value(originalChatDownloadOriginKey{}).(originalChatDownloadOrigin)
	if !ok || !origin.valid() || !a.originalChatSubscriptionIngress(ctx) {
		return originalChatDownloadOrigin{}, bad("PLUGIN_CAPABILITY_DENIED", "添加下载需要已验证的 Lark 选择")
	}
	return origin, nil
}

// MoviePilot MediaInteractionChain._search_media_resources checks library
// coverage before searching. An explicit 搜索/下载 searches the full requested
// seasons again only when they are already complete.
func (a *App) originalChatMediaResources(ctx context.Context, meta *releasemeta.MetaObject, media catalog.Media, primary json.RawMessage, reSearch bool, beforeSearch func([]originalChatMediaSeason) error) (SearchRequest, []Candidate, error) {
	provider := originalChatMediaProviderFrom(ctx)
	providerOnly := provider.sourceOnly() && media.ID == 0 && a.originalChatSubscriptionIngress(ctx)
	if meta == nil || !validMedia(media.MediaType, media.ID) && !providerOnly {
		return SearchRequest{}, nil, bad("INVALID_MEDIA", "未取得可核验的影视身份")
	}
	ctx, err := a.ensureResourceRecognition(ctx)
	if err != nil {
		return SearchRequest{}, nil, err
	}
	var information sourceMediaInformation
	if providerOnly {
		information, err = originalChatProviderSearchInformation(primary, provider, media.MediaType)
	} else if provider == nil {
		information, err = a.sourceRecognizeTMDBMedia(ctx, media)
	} else {
		// supplement_tmdb_info calls the native TMDB module directly. A
		// recognized alternate result remains usable if that detail call fails,
		// provided selection already verified an unambiguous native identity.
		information, err = a.sourceRecognizeTMDBCore(ctx, media)
		if err != nil && ctx.Err() == nil {
			information, err = sourceMediaInformation{Media: media}, nil
		}
	}
	if err != nil {
		return SearchRequest{}, nil, err
	}
	if provider != nil && !providerOnly {
		information.MediaInfo, information.Media, err = originalChatSupplementMedia(primary, information.MediaInfo, provider, information.Media)
		if err != nil {
			return SearchRequest{}, nil, err
		}
	}
	media = information.Media
	base := SearchRequest{MediaType: media.MediaType, TMDBID: media.ID, Page: 1, originalSearchInfo: &information, chatProvider: provider}
	var seasons []originalChatMediaSeason
	providerTV := providerOnly && media.MediaType == "tv"
	coverage := information
	if providerTV {
		coverage, err = a.originalChatProviderCoverageMedia(ctx, information, provider)
		if err != nil {
			return SearchRequest{}, nil, err
		}
	}
	if !providerTV || originalChatProviderHasSeasons(coverage) {
		probe := Subscription{Media: coverage.Media}
		resolved, err := a.resolveOriginalSubscriptionExistence(ctx, probe, &standardSubscriptionRecognition{Information: coverage})
		if err != nil {
			return SearchRequest{}, nil, err
		}
		existence := resolved.Existence
		if existence == nil {
			raw, err := a.originalNativeSubscriptionExistence(ctx, probe)
			if err != nil {
				return SearchRequest{}, nil, err
			}
			existence = &originalSubscriptionExistence{MediaInfo: coverage.MediaInfo, Result: raw}
		}
		if providerTV {
			seasons, err = originalChatProviderSeasonScope(meta, coverage, existence)
		} else {
			seasons, err = a.originalChatMediaSeasonScope(ctx, meta, media, existence)
		}
		if err != nil {
			return SearchRequest{}, nil, err
		}
	}
	complete := len(seasons) > 0
	for _, season := range seasons {
		if len(season.needed) != 0 {
			complete = false
			break
		}
	}
	if complete && !reSearch {
		return base, nil, errOriginalChatMediaExists
	}
	if complete {
		if begin := meta.Number("begin_season"); media.MediaType == "tv" && begin != nil && begin.IsInt64() {
			seasons = slices.DeleteFunc(seasons, func(season originalChatMediaSeason) bool { return int64(season.number) != begin.Int64() })
		}
		for i := range seasons {
			seasons[i].needed = seasons[i].all
			seasons[i].whole = true
		}
	}
	base.chatMediaSeasons = seasons
	base.chatUnscoped = providerTV && len(seasons) == 0
	if beforeSearch != nil {
		if err := beforeSearch(seasons); err != nil {
			return base, nil, err
		}
	}
	if providerOnly {
		information, err = originalChatProviderSearchTitle(ctx, information, provider)
		if err != nil {
			return base, nil, err
		}
		media = information.Media
	}
	if base.chatUnscoped {
		fields := originalChatLatestMediaFields(information.MediaInfo)
		number, _ := originalResourceInteger(fields.Get("season"))
		if number < 1 || number > 99 {
			number = 0
		}
		seasons = []originalChatMediaSeason{{number: int(number)}}
	}

	var candidates []Candidate
	var packParser *releasemeta.MetaInfoParser
	if media.MediaType == "tv" {
		packParser, err = originalChatPackParser(ctx)
		if err != nil {
			return base, nil, err
		}
	}
	for _, season := range seasons {
		episodes := season.needed
		if len(episodes) == 0 && !base.chatUnscoped {
			continue
		}
		request := base
		searchInfo := information
		request.originalSearchInfo = &searchInfo
		if media.MediaType == "tv" {
			request.Season, request.Episodes = season.number, episodes
		}
		result, err := a.search(ctx, request, false)
		if err != nil {
			return base, nil, err
		}
		base.WorkflowID = result.WorkflowID
		for _, candidate := range result.Candidates {
			if !candidate.Accepted {
				continue
			}
			if base.chatUnscoped {
				candidates = append(candidates, candidate)
				continue
			}
			if packParser != nil {
				meta, err := originalChatPackMeta(packParser, candidate.Resource)
				if err != nil {
					return base, nil, err
				}
				pack, listed, err := larkMetaNumbers(meta)
				if err != nil {
					return base, nil, err
				}
				if len(pack) > 1 {
					// Per-season native searches may each see the same listing.
					// Retain its entire source scope at the first declared season.
					if pack[0] == season.number && len(listed) == 0 && originalChatPackInSearch(pack, seasons) {
						candidate.Season, candidate.Episodes, candidate.chatSeasonPack = pack[0], nil, pack
						candidates = append(candidates, candidate)
					}
					continue
				}
			}
			if media.MediaType == "tv" {
				if candidate.Season != season.number || len(candidate.Episodes) != 0 &&
					!slices.ContainsFunc(candidate.Episodes, func(ep int) bool { return slices.Contains(episodes, ep) }) {
					continue
				}
				if len(candidate.Episodes) == 0 {
					if !automaticPackCandidate(candidate, season.number) {
						continue
					}
					candidate.Episodes = slices.Clone(episodes)
				}
			}
			candidates = append(candidates, candidate)
		}
	}
	if len(seasons) == 1 && media.MediaType == "tv" {
		base.Season = seasons[0].number
		base.Episodes = slices.Clone(seasons[0].needed)
	}
	if providerTV {
		information, err = originalChatProviderWithCoverage(information, provider, base.chatMediaSeasons)
		if err != nil {
			return base, nil, err
		}
	}
	if len(candidates) < 2 {
		return base, candidates, nil
	}
	candidates, err = a.originalChatSortMediaCandidates(ctx, media, candidates)
	return base, candidates, err
}

func (a *App) originalChatMediaSeasonScope(ctx context.Context, meta *releasemeta.MetaObject, media catalog.Media, existence *originalSubscriptionExistence) ([]originalChatMediaSeason, error) {
	if media.MediaType == "movie" {
		present, err := existence.episodes(Subscription{Media: media})
		result := originalChatMediaSeason{all: []int{0}}
		if len(present) == 0 {
			result.needed = result.all
			result.whole = true
		}
		return []originalChatMediaSeason{result}, err
	}
	var wanted []int
	if meta.Number("begin_season") != nil {
		var err error
		wanted, _, err = larkMetaNumbers(meta)
		if err != nil {
			return nil, err
		}
	} else {
		for _, season := range media.Seasons {
			if season.SeasonNumber > 0 && season.EpisodeCount > 0 {
				wanted = append(wanted, season.SeasonNumber)
			}
		}
	}
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)
	var out []originalChatMediaSeason
	for _, number := range wanted {
		if number < 1 || number > 99 {
			return nil, bad("INVALID_SEASON", "所选季超出当前下载支持的季号范围")
		}
		season, err := a.season(ctx, media.ID, number)
		if err != nil {
			return nil, err
		}
		present, err := existence.episodes(Subscription{Media: media, Season: number})
		if err != nil {
			return nil, err
		}
		row := originalChatMediaSeason{number: number, whole: len(present) == 0}
		for _, episode := range season.Episodes {
			if n := episode.EpisodeNumber; n > 0 {
				row.all = append(row.all, n)
				if !slices.Contains(present, n) {
					row.needed = append(row.needed, n)
				}
			}
		}
		if len(row.all) != 0 {
			slices.Sort(row.all)
			slices.Sort(row.needed)
			out = append(out, row)
		}
	}
	if len(out) == 0 {
		return nil, bad("INVALID_SEASON", "当前影视信息未提供可核验的季集")
	}
	return out, nil
}

func (a *App) originalChatSortMediaCandidates(ctx context.Context, media catalog.Media, candidates []Candidate) ([]Candidate, error) {
	options := ctx.Value(resourceRecognitionKey{}).(resourceRecognition).options
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	metas := make([]json.RawMessage, len(candidates))
	for i, candidate := range candidates {
		if len(candidate.originalSelectionMeta) != 0 {
			metas[i] = candidate.originalSelectionMeta
			continue
		}
		resource := candidate.Resource
		subtitle := resource.SourceSubtitle
		if subtitle == nil && resource.Subtitle != "" {
			subtitle = &resource.Subtitle
		}
		meta, err := parser.ParseTitle(resource.Title, subtitle, nil)
		if err != nil {
			return nil, err
		}
		metas[i], err = meta.EncodeState()
		if err != nil {
			return nil, err
		}
	}
	out, _, err := a.larkSortCandidateMetas(ctx, candidates, metas, media)
	return out, err
}

func (a *App) originalChatDownloadMedia(ctx context.Context, request SearchRequest, candidate Candidate, directory originalChatMediaDirectory) error {
	return a.originalChatSubmitDownloadMedia(ctx, request, candidate, directory, nil, false)
}

func (a *App) originalChatSubmitDownloadMedia(ctx context.Context, request SearchRequest, candidate Candidate, directory originalChatMediaDirectory, batch *originalChatEpisodeBatch, explicitChoice bool) error {
	origin, err := a.originalChatDownloadOrigin(ctx)
	if err != nil {
		return err
	}
	if !candidate.Accepted || candidate.Resource.SiteID == "" || candidate.Resource.ID == "" {
		return bad("RESOURCE_CHANGED", "下载资源已失效，请重新选择")
	}
	if provider := request.chatProvider; provider.sourceOnly() {
		selected := originalChatMediaProviderFrom(ctx)
		if selected == nil || *selected != *provider || request.TMDBID != 0 || request.originalSearchInfo == nil ||
			request.originalSearchInfo.Media.MediaType != request.MediaType {
			return bad("MEDIA_SOURCE_CHANGED", "下载任务无法核对已选择的媒体来源")
		}
		if _, err := originalChatProviderTargetMedia(request.originalSearchInfo.MediaInfo, provider, request.originalSearchInfo.Media); err != nil {
			return err
		}
		if candidate.chatSearchQuery != "" {
			request.Query = candidate.chatSearchQuery
		}
	} else if !validMedia(request.MediaType, request.TMDBID) {
		return bad("RESOURCE_CHANGED", "下载资源已失效，请重新选择")
	}
	var sourceSeasons []SearchRequest
	var sourceSeasonBasis json.RawMessage
	unscoped := request.chatUnscoped && explicitChoice && request.chatProvider.sourceOnly() && request.MediaType == "tv" && batch == nil &&
		len(candidate.Episodes) == 0 && len(candidate.chatSeasonPack) == 0
	if request.chatUnscoped && !unscoped {
		return bad("REVIEW_EPISODES", "所选资源无法核对当前季集，请重新选择")
	}
	if unscoped {
		request.Season, request.Episodes = 0, nil
	} else if len(candidate.chatSeasonPack) > 1 {
		if batch != nil || request.MediaType != "tv" {
			return seasonScopeError()
		}
		ctx, err = a.ensureResourceRecognition(ctx)
		if err != nil {
			return err
		}
		parser, err := originalChatPackParser(ctx)
		if err != nil {
			return err
		}
		basis, err := originalChatMultiSeasonBasis(parser, request, candidate)
		if err != nil {
			return err
		}
		sourceSeasons, sourceSeasonBasis = basis.Targets.Requests, mustCleanSeedJSON(basis)
		request.Season, request.Episodes = sourceSeasons[0].Season, sourceSeasons[0].Episodes
		candidate.originalSelectionMeta = basis.SourceMeta
	} else if request.MediaType == "tv" {
		episodes := candidate.Episodes
		if batch != nil {
			episodes = batch.needed
		}
		if candidate.Season < 1 || len(episodes) == 0 || (request.Season != 0 && request.Season != candidate.Season) ||
			slices.ContainsFunc(episodes, func(ep int) bool {
				return !originalSelectionAllows(candidate, ep) || (len(request.Episodes) != 0 && !slices.Contains(request.Episodes, ep))
			}) {
			return bad("REVIEW_EPISODES", "所选资源无法核对当前季集，请重新选择")
		}
		request.Season = candidate.Season
		request.Episodes = slices.Clone(episodes)
	}
	preview, err := a.previewDownload(ctx, PreviewRequest{
		SearchRequest: request, SiteID: candidate.Resource.SiteID, ResourceID: candidate.Resource.ID,
		DirectoryID: directory.ID, DownloadPath: directory.Path, DownloaderID: directory.Downloader, RootID: directory.RootID,
		sourceContext: candidate.originalSelectionContext, sourceMeta: candidate.originalSelectionMeta,
		sourceSeasons: sourceSeasons, sourceSeasonBasis: sourceSeasonBasis,
		chatOrigin: &origin, chatEpisodeBatch: batch, chatWholeUnscoped: unscoped,
	}, false)
	if err != nil {
		if pluginErrorCodeInternal(err) == originalChatTorrentFailedCode {
			a.postOriginalChatDownloadFailure(ctx, origin, request, candidate, err)
		}
		return err
	}
	if batch != nil && batch.mode == "whole_pack" && preview.ExtraFiles {
		return bad("REVIEW_TORRENT_COVERAGE", "整季包包含目标范围之外的视频")
	}
	if preview.NeedsConfirmation && slices.ContainsFunc(preview.Warnings, func(warning string) bool {
		return warning != "season_pack_requires_confirmation" && warning != "此资源还包含额外视频，确认后仅整理所选目标"
	}) {
		return bad("REVIEW_CHAT_DOWNLOAD", "所选资源的影视身份或季集需要核对，请在光影中确认")
	}
	intent, err := a.confirmDownload(ctx, preview.ID, preview.NeedsConfirmation)
	if err != nil {
		return err
	}
	queued, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='download' AND json_extract(payload,'$.id')=?`, intent.ID))
	if err != nil || queued.WorkflowID != intent.WorkflowID {
		return bad("CHAT_DOWNLOAD_UNCONFIRMED", "下载任务已提交，但无法核对下载器结果")
	}
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	job, err := a.awaitOriginalChatSearchChild(ctx, queued)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return bad("CHAT_DOWNLOAD_UNCONFIRMED", "下载任务已提交，但未能确认下载器结果")
	}
	var actual Intent
	if err := a.Store.Get(ctx, "intent", intent.ID, &actual); err != nil {
		return bad("CHAT_DOWNLOAD_UNCONFIRMED", "无法核对下载器接收记录")
	}
	if actual.ID != intent.ID || actual.DownloaderID != intent.DownloaderID || !sameHash(actual.Hash, intent.Hash) || mediaKey(actual.Media) != mediaKey(intent.Media) {
		return bad("CHAT_DOWNLOAD_UNCONFIRMED", "无法核对下载器接收记录")
	}
	if job.State == "cancelled" || actual.State == "cancelled" {
		return bad("CHAT_DOWNLOAD_CANCELLED", "所选资源的下载任务已取消")
	}
	if job.State != "succeeded" {
		message := strings.TrimSpace(a.clean(job.ErrorMessage))
		if job.State == "failed" && job.ErrorCode == originalChatAddRejectedCode && actual.State == "failed" && !actual.SourceModuleStarted {
			if message == "" {
				message = "添加种子任务失败"
			}
			failure := bad("CHAT_DOWNLOAD_FAILED", message)
			a.postOriginalChatDownloadFailure(ctx, origin, request, candidate, failure)
			return failure
		}
		if job.State == "waiting_paused" || job.State == "needs_review" || actual.SourceModuleStarted || actual.State != "planned" {
			return bad("CHAT_DOWNLOAD_UNCONFIRMED", "所选资源的下载结果待确认")
		}
		if message == "" {
			message = "所选资源未提交到下载器"
		}
		return bad("CHAT_DOWNLOAD_NOT_SUBMITTED", message)
	}
	switch actual.State {
	case "accepted", "downloading", "downloaded", "imported":
		if batch != nil && batch.mode == "split" {
			return a.verifyOriginalChatSplitDownload(ctx, actual, batch.episodes)
		}
		return nil
	}
	return bad("CHAT_DOWNLOAD_UNCONFIRMED", "无法核对下载器接收记录")
}

func (a *App) originalChatAutoDownloadMedia(ctx context.Context, request SearchRequest, candidates []Candidate, directory originalChatMediaDirectory) ([]Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := a.originalChatDownloadOrigin(ctx); err != nil {
		return nil, err
	}
	if a.originalResourceSelectionActive() {
		var err error
		candidates, err = a.originalChatMediaResourceSelection(ctx, request, candidates)
		if err != nil {
			return nil, err
		}
	}
	if request.MediaType == "tv" {
		return a.originalChatAutoDownloadTV(ctx, request, candidates, directory)
	}
	attempted := map[[2]string]bool{}
	for _, candidate := range candidates {
		key := originalChatBatchResourceKey(candidate)
		if !candidate.Accepted || attempted[key] {
			continue
		}
		attempted[key] = true
		err := a.originalChatDownloadMedia(ctx, request, candidate, directory)
		if err == nil {
			return []Candidate{candidate}, nil
		}
		if stop := originalChatBatchStop(ctx, err); stop != nil {
			return nil, stop
		}
	}
	return nil, originalChatBatchIncomplete()
}

func (a *App) originalChatMediaResourceSelection(ctx context.Context, request SearchRequest, candidates []Candidate) ([]Candidate, error) {
	ctx, err := a.ensureResourceRecognition(ctx)
	if err != nil {
		return nil, err
	}
	information := request.originalSearchInfo
	if information == nil {
		return nil, bad("REVIEW_MEDIA_CHANGED", "资源选择缺少可核对的影视身份")
	}
	// Source MediaInteractionChain._auto_download sends no explicit downloader
	// to batch_download. Native directory constraints still govern Add below.
	selected, err := a.originalSelectResourceCandidates(ctx, request, information.Media, information.MediaInfo, candidates, nil, "Lark", func(candidate Candidate) bool {
		return candidate.Accepted
	})
	if err != nil {
		return nil, err
	}
	for i := range selected {
		candidate := &selected[i]
		if request.MediaType != "tv" || len(candidate.chatSeasonPack) > 1 || len(candidate.Episodes) != 0 || !automaticPackCandidate(*candidate, candidate.Season) {
			continue
		}
		for _, season := range request.chatMediaSeasons {
			if season.number == candidate.Season {
				candidate.Episodes = slices.Clone(season.needed)
				break
			}
		}
	}
	return a.originalChatSortMediaCandidates(ctx, information.Media, selected)
}
