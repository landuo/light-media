package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
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

func (a *App) importSeason(ctx context.Context, identity catalog.Media, providerMedia json.RawMessage, number int) (catalog.Season, error) {
	if !providerNativeMedia(identity) {
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

func (request ImportRequest) providerImport() bool {
	authorized := request.Manual && request.originalRetry || !request.Manual && request.IntentID != ""
	return authorized && request.TMDBID == 0 &&
		request.MediaType == request.providerIdentity.MediaType && providerNativeMedia(request.providerIdentity)
}
