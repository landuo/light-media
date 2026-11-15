package app

import (
	"context"
	"strconv"
	"strings"

	"light-media/internal/plugins"
)

func normalizeManualProviderInput(in *manualOrganizeRequest) error {
	in.MediaSource = originalCatalogSource(in.MediaSource)
	in.MediaID = strings.TrimSpace(in.MediaID)
	if in.MediaSource == "" && in.MediaID == "" {
		return nil
	}
	if err := validateRecognitionSource(&in.MediaSource); err != nil {
		return err
	}
	if in.MediaID == "" {
		return nil
	}
	if in.FromHistory && len(in.HistoryIDs) > 0 {
		return nil
	} // selected history wins
	if in.MediaType != "movie" && in.MediaType != "tv" {
		return bad("INVALID_MEDIA", "请先选择媒体类型，再填写来源编号")
	}
	if in.MediaSource == "themoviedb" {
		id, err := strconv.ParseInt(in.MediaID, 10, 64)
		if err != nil || !validMedia(in.MediaType, id) || strconv.FormatInt(id, 10) != in.MediaID {
			return bad("INVALID_MEDIA", "TheMovieDb编号无效")
		}
		if in.TMDBID != 0 && in.TMDBID != id {
			return bad("IMPORT_IDENTITY_MISMATCH", "来源编号与TMDB编号不一致")
		}
		in.TMDBID = id
		return nil
	}
	if in.TMDBID != 0 || in.EpisodeGroup != "" {
		return bad("IMPORT_IDENTITY_MISMATCH", "非TMDB来源不使用TMDB编号或TMDB剧集组")
	}
	if !(originalChatMediaProvider{Source: in.MediaSource, ID: in.MediaID}).sourceValid() {
		return bad("INVALID_MEDIA", "来源编号无效")
	}
	return nil
}

func (a *App) applyManualProviderInput(ctx context.Context, in manualOrganizeRequest, request *ImportRequest) error {
	request.originalRecognitionSource = in.MediaSource
	if in.MediaID == "" || in.MediaSource == "themoviedb" || in.MediaSource == "" {
		return nil
	}
	provider := originalChatMediaProvider{Source: in.MediaSource, ID: in.MediaID}
	raw, err := a.originalHostMediaRecognize(ctx, plugins.MediaRecognizeRequest{Source: provider.Source, MediaID: provider.ID, Type: in.MediaType})
	if err != nil {
		return err
	}
	raw, err = a.originalTransferObtainImages(ctx, raw)
	if err != nil {
		return err
	}
	info, err := originalChatProviderSearchInformation(raw, &provider, in.MediaType)
	if err != nil {
		return err
	}
	request.MediaType, request.TMDBID = info.Media.MediaType, 0
	request.providerIdentity, request.originalResolvedMedia = info.Media, info.MediaInfo
	request.originalManualProvider = true
	request.EpisodeGroup = ""
	return nil
}
