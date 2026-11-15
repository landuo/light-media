package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func (a *App) completedProviderDownloadMedia(ctx context.Context, in Intent) (json.RawMessage, error) {
	if !providerNativeMedia(in.Media) {
		return nil, nil
	}
	snapshot, err := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Original == nil {
		return nil, bad("INVALID_DOWNLOAD_CONTEXT", "已下载媒体缺少可核对的来源信息")
	}
	if err = verifyChatDownloadContext(in, snapshot); err != nil {
		return nil, err
	}
	provider := originalChatMediaProvider{Source: in.Media.ProviderSource, ID: in.Media.ProviderID}
	if _, err = originalChatProviderSearchInformation(snapshot.Original.Media, &provider, in.Media.MediaType); err != nil {
		return nil, err
	}
	return snapshot.Original.Media, nil
}

// The frozen TMDB DTO is not the current identity. Carry only its explicit
// classification, using the same authenticated download-context envelope.
func (a *App) completedDownloadCategory(ctx context.Context, in Intent) (string, error) {
	if providerNativeMedia(in.Media) {
		return "", nil // Keep the existing provider DTO path unchanged.
	}
	snapshot, err := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
	if err != nil {
		return "", err
	}
	value, present := originalDownloadContextCategory(snapshot)
	if !present {
		return "", nil
	}
	category, _ := value.(string)
	return category, nil
}

func (a *App) importSeason(ctx context.Context, identity catalog.Media, providerMedia json.RawMessage, number int, group ...string) (catalog.Season, error) {
	if !providerNativeMedia(identity) {
		if len(group) > 0 && group[0] != "" {
			return a.importGroupSeason(ctx, identity.ID, number, group[0])
		}
		return a.season(ctx, identity.ID, number)
	}
	season := catalog.Season{SeasonNumber: number}
	for _, episode := range subscriptionProviderEpisodes(providerMedia, number) {
		// Only membership is needed for file and slot validation. Keep the
		// original transfer callback's TMDB episode details absent.
		season.Episodes = append(season.Episodes, catalog.Episode{SeasonNumber: number, EpisodeNumber: episode})
	}
	return season, nil
}

// Only the ordinary from-history path can grant this private input. Resolve
// the selected source's exact ID; never reinterpret it as a TMDB number or
// rely on the chat/plugin-only retry flag to make a web submission work.
func (a *App) resolveHistoryProviderImport(ctx context.Context, request *ImportRequest) error {
	if !request.originalHistoryProvider || !request.Manual || !providerNativeMedia(request.providerIdentity) {
		return bad("MEDIA_SOURCE_CHANGED", "历史来源身份缺少当前选择依据")
	}
	provider := originalChatMediaProvider{Source: request.providerIdentity.ProviderSource, ID: request.providerIdentity.ProviderID}
	raw, err := a.originalHostMediaRecognize(ctx, plugins.MediaRecognizeRequest{Source: provider.Source, MediaID: provider.ID, Type: request.MediaType})
	if err != nil {
		return err
	}
	info, err := originalChatProviderSearchInformation(raw, &provider, request.MediaType)
	if err != nil {
		return err
	}
	request.providerIdentity, request.originalResolvedMedia = info.Media, info.MediaInfo
	return nil
}

func (request ImportRequest) providerImport() bool {
	authorized := request.Manual && (request.originalRetry || request.originalHistoryProvider || request.originalManualProvider) || !request.Manual && (request.IntentID != "" || request.originalDirectoryProvider)
	return authorized && request.TMDBID == 0 &&
		request.MediaType == request.providerIdentity.MediaType && providerNativeMedia(request.providerIdentity)
}
