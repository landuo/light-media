package app

import (
	"context"
	"math"
	"path/filepath"
	"reflect"
	"slices"

	"light-media/internal/decision"
	"light-media/internal/download"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
)

func rssMagnetPreviewIdentity(in PreviewRequest, proof *originalChatLatestDownloadProof, choice decision.Decision, uri string, quality *subscriptionAcquisitionRSSListing) (string, error) {
	if proof == nil || proof.Mode != "rss" || in.SubscriptionID == "" || in.chatOrigin != nil || in.chatEpisodeBatch != nil ||
		len(in.selectedEpisodes) != 0 || len(in.sourceSeasons) != 0 || in.VerifiedSeasonPack || in.automaticSeasonPack && quality == nil {
		return "", bad("REVIEW_MAGNET_FILES_REQUIRED", "此下载需要种子文件清单，无法使用磁力链接")
	}
	if in.MediaType == "tv" && (in.qualitySelection && quality == nil || len(in.Episodes) == 0 || len(choice.Episodes) != len(in.Episodes) ||
		slices.ContainsFunc(in.Episodes, func(episode int) bool { return !slices.Contains(choice.Episodes, episode) })) {
		return "", bad("REVIEW_MAGNET_FILES_REQUIRED", "磁力链接的标题无法核对全部所选集数")
	}
	if quality != nil && (!choice.Accepted || choice.NeedsConfirmation || choice.Season != quality.Season || !slices.Equal(quality.Episodes, choice.Episodes)) {
		return "", acquisitionEvidence()
	}
	return downloadMagnetHash(uri)
}

func (a *App) rssQualityMagnetPreview(ctx context.Context, in PreviewRequest, proof *originalChatLatestDownloadProof) (*subscriptionAcquisitionRSSListing, error) {
	if !in.qualitySelection || in.MediaType != "tv" {
		return nil, nil
	}
	grant, _ := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant)
	if grant == nil || grant.a != a || grant.root == nil || grant.ordinal < 0 || grant.ordinal >= len(grant.root.Steps) {
		return nil, acquisitionEvidence()
	}
	step := grant.root.Steps[grant.ordinal]
	batch := originalChatLatestFromContext(ctx)
	if step.Phase != "intent" || step.Input.Selected != nil || step.Input.Material != "" || in.sourceMaterial != "" || batch == nil ||
		grant.root.RSSBatchID != batch.id || grant.root.Subscription.ID != in.SubscriptionID || grant.root.Subscription.Revision != in.Revision ||
		!step.Input.RSSListed.matches(batch.id, proof, in.sourceMeta, in.Season, step.Input.Candidate.Resource.PriorityOrder, in.Episodes) {
		return nil, acquisitionEvidence()
	}
	return step.Input.RSSListed, nil
}

func downloadMagnetHash(uri string) (string, error) {
	hash, err := metainfo.MagnetHash(uri)
	if err != nil {
		return "", bad("INVALID_MATERIAL", "磁力链接缺少可核对的种子标识")
	}
	return hash, nil
}

func verifyChatMagnetListing(request SearchRequest, choice decision.Decision, multiSeason bool) error {
	if request.MediaType == "tv" && !multiSeason && (choice.Season != request.Season || len(request.Episodes) == 0 ||
		len(choice.Episodes) != len(request.Episodes) ||
		slices.ContainsFunc(request.Episodes, func(episode int) bool { return !slices.Contains(choice.Episodes, episode) })) {
		return bad("REVIEW_MAGNET_FILES_REQUIRED", "磁力链接的标题无法核对全部所选集数")
	}
	return nil
}

func verifyRSSMagnetContext(in Intent, snapshot *downloadContextSnapshot) error {
	magnet := snapshot != nil && snapshot.RSSMagnet
	if in.RSSMagnet != magnet || magnet && (!in.Auto || !in.LatestResource || in.SubscriptionID == "" || in.MaterialRef == "" ||
		in.ChatDownload || in.SeasonScopeSHA != "" || len(in.SelectedEpisodes) != 0 || snapshot.LatestResource == nil || snapshot.LatestResource.Mode != "rss") {
		return bad("INVALID_DOWNLOAD_CONTEXT", "RSS 磁力下载凭据无法核对")
	}
	var listing *subscriptionAcquisitionRSSListing
	if snapshot != nil {
		listing = snapshot.RSSQualityListing
	}
	if (listing != nil) != (magnet && in.SubscriptionAcquisition != "" && in.Media.MediaType == "tv") || listing != nil &&
		!listing.matches(listing.BatchID, snapshot.LatestResource, snapshot.SourceMeta, in.Season, listing.Priority, in.Episodes) {
		return acquisitionEvidence()
	}
	return nil
}

func (a *App) verifyRSSQualityMagnetAdd(ctx context.Context, in Intent, snapshot *downloadContextSnapshot, quality *qualityDownloadSnapshot) error {
	listing := snapshot.RSSQualityListing
	grant, _ := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant)
	if grant == nil || grant.a != a || grant.root == nil || grant.root.ID != in.SubscriptionAcquisition || grant.ordinal < 0 || grant.ordinal >= len(grant.root.Steps) ||
		quality == nil || !quality.BestVersion || quality.Resource.SelectedEpisodes != nil {
		return acquisitionEvidence()
	}
	input := grant.root.Steps[grant.ordinal].Input
	if !reflect.DeepEqual(input.RSSListed, listing) || input.AllowWholeExtra != quality.AllowWholeExtra ||
		!listing.matches(grant.root.RSSBatchID, snapshot.LatestResource, snapshot.SourceMeta, in.Season, quality.Resource.Priority, in.Episodes) {
		return acquisitionEvidence()
	}
	return nil
}

// Magnet Add supplies an identity but no file list. Apply the same automatic
// material checks to the daemon's completed files before touching the library.
func (a *App) verifyCompletedDownloadMagnet(ctx context.Context, in Intent, files []download.File, seasonProof *downloadSeasonProof) error {
	snapshot, err := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
	if err != nil {
		return err
	}
	if err = verifyRSSMagnetContext(in, snapshot); err != nil {
		return err
	}
	if err = verifyChatDownloadContext(in, snapshot); err != nil {
		return err
	}
	total := int64(0)
	for _, file := range files {
		if file.Size < 0 || file.Size > math.MaxInt64-total {
			return bad("REVIEW_DOWNLOAD_FILES", "磁力任务的文件大小无法核对")
		}
		total += file.Size
	}
	if err = actualSizePolicy(total, in.Request.Policy); err != nil || seasonProof != nil {
		return err // Multi-season file identity is verified against its own scope.
	}
	request := in.Request
	request.MediaType, request.TMDBID, request.Season, request.Episodes = in.Media.MediaType, in.Media.ID, in.Season, in.Episodes
	var target decision.Target
	if providerNativeMedia(in.Media) {
		target, err = providerDownloadTarget(request, in.Media, snapshot.Original.Media, true)
	} else {
		target, err = a.targetMedia(ctx, request, true, in.Media)
	}
	if err != nil {
		return err
	}
	if snapshot.RSSQualityListing != nil {
		quality, err := a.qualitySnapshot(ctx, a.Store.DB, "intent", in.ID, in.Hash)
		if err != nil {
			return err
		}
		if quality == nil || !quality.BestVersion || quality.Resource.Priority != snapshot.RSSQualityListing.Priority || !slices.Equal(quality.Episodes, in.Episodes) {
			return acquisitionEvidence()
		}
		target = qualityEvaluationTarget(target)
	}
	videos := 0
	covered := map[int]bool{}
	for _, file := range files {
		if !isVideo(file.Path) {
			continue
		}
		videos++
		choice := a.evaluateResourceContext(ctx, sites.Resource{SiteID: in.SiteID, Title: filepath.Base(file.Path), Size: file.Size}, target, decision.Policy{})
		if !choice.Accepted || choice.NeedsConfirmation {
			return bad("REVIEW_FILE_IDENTITY", "磁力任务的实际视频文件不能确认目标身份")
		}
		for _, episode := range choice.Episodes {
			if !slices.Contains(in.Episodes, episode) || covered[episode] {
				return bad("REVIEW_DOWNLOAD_COVERAGE", "磁力任务的实际文件超出或重复覆盖目标集")
			}
			covered[episode] = true
		}
	}
	if videos == 0 || in.Media.MediaType == "movie" && videos != 1 || in.Media.MediaType == "tv" && len(covered) != len(in.Episodes) {
		return bad("REVIEW_DOWNLOAD_COVERAGE", "磁力任务的实际视频文件无法核对目标范围")
	}
	return nil
}
