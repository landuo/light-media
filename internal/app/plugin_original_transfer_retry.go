package app

import (
	"context"
	"encoding/json"

	"light-media/internal/releasemeta"
)

// TransferChain.redo_transfer_history re-recognizes the current source, obtains
// images and runs a new explicit transfer. A rejected old module receipt must
// not be reused as the result of the user's new retry.
func (a *App) retryOriginalTransferHistory(ctx context.Context, item transferHistoryItem, prior ImportPlan) (Job, error) {
	if _, err := snapshotPath(item.Source); err != nil {
		return Job{}, err
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return Job{}, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return Job{}, err
	}
	meta, err := parser.ParsePath(item.Source, nil)
	if err != nil {
		return Job{}, err
	}
	recognized, err := a.originalCatalogRecognizeByMetaMethod(ctx, "recognize_media", meta, func(meta *releasemeta.MetaObject) (json.RawMessage, error) {
		return a.originalMetadataRecognizeNative(ctx, meta)
	})
	if err != nil {
		return Job{}, err
	}
	if !originalModuleResultTruthy(recognized) {
		return Job{}, originalRecognitionMissing()
	}
	enriched, err := a.originalTransferObtainImages(ctx, recognized)
	if err != nil {
		return Job{}, err
	}
	identity, err := originalCatalogMediaProjection(enriched)
	if err != nil {
		return Job{}, err
	}
	if !validMedia(identity.MediaType, identity.ID) {
		return Job{}, bad("IMPORT_IDENTITY_REVIEW", "当前整理需要可核验的TMDB身份")
	}
	root, err := a.importRoot(prior.RootID)
	if err != nil {
		return Job{}, err
	}
	out, err := a.manualOrganize(ctx, manualOrganizeRequest{originalRetry: true, HistoryIDs: []string{item.ID}, TargetPath: root.Path, Mode: prior.Mode, MediaType: identity.MediaType, TMDBID: identity.ID, Renaming: prior.Renaming, Scraping: prior.Scraping, LibraryTypeFolder: prior.LibraryTypeFolder, originalResolvedMedia: enriched})
	if err != nil {
		return Job{}, err
	}
	if len(out.Items) != 1 || out.Failed != 0 || out.Submitted != 1 {
		return Job{}, bad("TRANSFER_RETRY_FAILED", firstManualOrganizeFailure(out))
	}
	return scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, out.Items[0].JobID))
}
func firstManualOrganizeFailure(out manualOrganizeOutput) string {
	for _, item := range out.Items {
		if item.Error != "" {
			return item.Error
		}
	}
	return "所选整理记录未能提交新的整理任务"
}
