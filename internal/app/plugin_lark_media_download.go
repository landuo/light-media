package app

import (
	"context"
	"encoding/json"
	"math/big"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

func larkPageSelection(count, page int, index *big.Int) (int, bool) {
	start, end, _, _ := larkMediaPage(count, page)
	if index == nil || !index.IsInt64() || index.Sign() <= 0 || index.Int64() > int64(end-start) {
		return 0, false
	}
	return start + int(index.Int64()) - 1, true
}

func (a *App) larkSelectTorrent(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, index *big.Int, user, message, chat string) error {
	if s.Phase != "torrent" || s.Selected == nil {
		return a.larkCoreReply(ctx, core, user, chat, "输入有误！", "")
	}
	mode := "single"
	var candidate *Candidate
	if index != nil && index.Sign() == 0 {
		mode = "auto"
	} else {
		i, ok := larkPageSelection(len(s.Candidates), s.Page, index)
		if !ok {
			return a.larkCoreReply(ctx, core, user, chat, "输入有误！", "")
		}
		v := s.Candidates[i]
		candidate = &v
	}
	facts, e := a.larkDownloadDirectoryFacts(ctx, core, *s.Selected)
	if e != nil {
		return e
	}
	if len(facts.Choices) > 1 {
		s.PendingPage = s.Page
		s.PendingMode, s.PendingCandidate = mode, candidate
		s.PendingDirectoryFacts = &facts
		s.Phase, s.Page, s.Directories = "download-dir", 0, facts.Choices
		s, e = a.larkSaveMediaSession(ctx, core, *s)
		if e != nil {
			return e
		}
		return a.larkRenderMedia(ctx, core, s, user, message, chat)
	}
	return a.larkExecuteMediaDownload(ctx, core, s, mode, candidate, facts, nil)
}

func (a *App) larkSelectDownloadDirectory(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, index *big.Int, user, chat string) error {
	i, ok := larkPageSelection(len(s.Directories), s.Page, index)
	if s.Phase != "download-dir" || !ok {
		return a.larkCoreReply(ctx, core, user, chat, "输入有误！", "")
	}
	d := s.Directories[i]
	var path *string
	if !larkIsAutoDirectory(d) {
		p := larkDirectoryPath(d)
		if p == "" {
			return a.larkCoreReply(ctx, core, user, chat, "下载目录配置无效！", "")
		}
		path = &p
	}
	mode, candidate, facts := s.PendingMode, s.PendingCandidate, s.PendingDirectoryFacts
	// The source restores the torrent phase before executing, including invalid
	// or failed download actions. Persist that transition before native effects.
	s.Phase, s.Page = "torrent", s.PendingPage
	s.PendingPage, s.PendingMode = 0, ""
	s.PendingCandidate, s.PendingDirectoryFacts, s.Directories = nil, nil, nil
	s, e := a.larkSaveMediaSession(ctx, core, *s)
	if e != nil {
		return e
	}
	if facts == nil || mode != "auto" && (mode != "single" || candidate == nil) {
		return a.larkCoreReply(ctx, core, user, chat, "下载操作已失效，请重新选择资源", "")
	}
	return a.larkExecuteMediaDownload(ctx, core, s, mode, candidate, *facts, path)
}

func larkCandidateRequest(s *pluginLarkMediaSession, candidate Candidate, media catalog.Media) SearchRequest {
	request := SearchRequest{MediaType: media.MediaType, TMDBID: media.ID, Season: candidate.Season}
	for _, target := range s.Targets {
		if target.Season == candidate.Season || media.MediaType == "movie" {
			request = target
			break
		}
	}
	if media.MediaType == "tv" && len(candidate.Episodes) > 0 {
		request.Episodes = append([]int{}, candidate.Episodes...)
	}
	return request
}
func (a *App) larkExecuteMediaDownload(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, mode string, candidate *Candidate, facts larkDownloadDirectorySnapshot, explicit *string) error {
	if mode == "auto" {
		return a.larkAutoDownloadMedia(ctx, core, s, facts, explicit)
	}
	if mode != "single" || candidate == nil || s.Selected == nil {
		return larkEvidenceError()
	}
	_, e := a.larkDownloadCandidate(ctx, core, s, *candidate, facts, explicit)
	return e
}
func (a *App) larkDownloadCandidate(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, candidate Candidate, facts larkDownloadDirectorySnapshot, explicit *string) (Intent, error) {
	if facts.Media.MediaType == "tv" {
		for i, c := range s.Candidates {
			if c.Resource.SiteID != candidate.Resource.SiteID || c.Resource.ID != candidate.Resource.ID {
				continue
			}
			if i >= len(s.CandidateMetas) {
				return Intent{}, larkEvidenceError()
			}
			meta, e := releasemeta.DecodeMetaObjectState(s.CandidateMetas[i])
			if e != nil {
				return Intent{}, e
			}
			seasons, sourceEpisodes, e := larkMetaNumbers(meta)
			if e != nil {
				return Intent{}, e
			}
			if len(seasons) > 1 {
				basis, e := a.selectedLarkSeasonBasis(ctx, core, candidate, facts.Media, s.CandidateMetas[i])
				if e != nil {
					return Intent{}, e
				}
				candidate.Season, candidate.Episodes = basis.Targets.Requests[0].Season, qualityCopyNote(basis.Targets.Requests[0].Episodes)
				if len(sourceEpisodes) > 0 {
					candidate.Episodes = qualityCopyNote(sourceEpisodes)
				}
				return a.larkDownloadCandidateSeasons(ctx, core, s, candidate, facts, explicit, nil, "", s.CandidateMetas[i], basis.Targets.Requests, mustCleanSeedJSON(basis))
			}
			break
		}
	}
	return a.larkDownloadCandidateSelection(ctx, core, s, candidate, facts, explicit, nil, "", nil)
}

func (a *App) larkDownloadCandidateSelection(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, candidate Candidate, facts larkDownloadDirectorySnapshot, explicit *string, episodes []int, materialRef string, sourceMeta json.RawMessage) (Intent, error) {
	return a.larkDownloadCandidateSeasons(ctx, core, s, candidate, facts, explicit, episodes, materialRef, sourceMeta, nil)
}
func (a *App) larkDownloadCandidateSeasons(ctx context.Context, core *pluginLarkCoreExecution, s *pluginLarkMediaSession, candidate Candidate, facts larkDownloadDirectorySnapshot, explicit *string, episodes []int, materialRef string, sourceMeta json.RawMessage, seasons []SearchRequest, basis ...json.RawMessage) (Intent, error) {
	// The menu is an immutable original interaction snapshot, while the source
	// downloader revalidates the selected path against the current directory
	// configuration. Freeze that new read as a separate execution step.
	path, e := larkCoreRead(ctx, a, core, "download.path", struct {
		Media    catalog.Media
		Explicit *string
	}{facts.Media, explicit}, func(ctx context.Context) (string, error) {
		m, category, e := a.importMediaCategory(ctx, facts.Media)
		if e != nil {
			return "", e
		}
		return larkResolveDownloadPath(a.Settings().Directories, m, category, explicit)
	})
	if e != nil {
		return Intent{}, e
	}
	request := PreviewRequest{SearchRequest: larkCandidateRequest(s, candidate, facts.Media), DownloadPath: path, SiteID: candidate.Resource.SiteID, ResourceID: candidate.Resource.ID}
	request.sourceSeasons = seasons
	if len(basis) > 1 {
		return Intent{}, seasonScopeError()
	}
	if len(basis) == 1 {
		request.sourceSeasonBasis = append(json.RawMessage(nil), basis[0]...)
	}
	request.selectedEpisodes, request.sourceMaterial, request.sourceMeta = append([]int(nil), episodes...), materialRef, sourceMeta
	preview, e := larkCoreRead(ctx, a, core, "download.preview", struct {
		Request     PreviewRequest
		Selected    []int
		Material    string
		SourceMeta  json.RawMessage
		Seasons     []SearchRequest
		SeasonBasis json.RawMessage `json:",omitempty"`
	}{request, episodes, materialRef, sourceMeta, seasons, request.sourceSeasonBasis}, func(ctx context.Context) (DownloadPreview, error) {
		return a.previewDownload(ctx, request, false)
	})
	if e != nil {
		return Intent{}, e
	}
	confirmed, e := larkCoreWrite(ctx, a, core, "download.confirm", preview.ID, func(ctx context.Context) (Intent, error) {
		// The source user's explicit resource selection authorizes its material,
		// including additional files. The P0 transaction still authenticates the
		// actual torrent, downloader, directory and exact acquisition scope.
		return a.confirmDownload(ctx, preview.ID, true)
	})
	if e != nil {
		return Intent{}, e
	}
	return a.larkSubmitDownload(ctx, core, confirmed)
}
