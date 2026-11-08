package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

// TransferChain.redo_transfer_history re-recognizes the current source, obtains
// images and runs a new explicit transfer. A rejected old module receipt must
// not be reused as the result of the user's new retry.
func (a *App) retryOriginalTransferHistory(ctx context.Context, item transferHistoryItem, prior ImportPlan) (Job, error) {
	if prior.ID != item.PlanID {
		return Job{}, originalTransferRetryStale()
	}
	var generation int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT generation FROM jobs WHERE id=? AND kind='import' AND json_extract(payload,'$.id')=?`, item.JobID, item.PlanID).Scan(&generation); err != nil {
		return Job{}, err
	}
	return a.submitOriginalTransferRetry(ctx, originalTransferAttempt(item, generation), nil)
}

func (a *App) submitOriginalTransferRetry(ctx context.Context, attempt originalTransferRetryAttempt, recognize func(context.Context) (json.RawMessage, error)) (Job, error) {
	key := struct {
		Store   *Store
		History string
	}{a.Store, attempt.HistoryID}
	if _, active := originalTransferRetryAdmissions.LoadOrStore(key, struct{}{}); active {
		return Job{}, bad("TRANSFER_RETRY_BUSY", "这条整理记录正在重试")
	}
	defer originalTransferRetryAdmissions.Delete(key)
	item, err := a.originalCurrentTransferRetry(ctx, attempt, false)
	if err != nil {
		return Job{}, err
	}
	var prior ImportPlan
	if err := a.Store.Get(ctx, "import_plan", item.PlanID, &prior); err != nil {
		return Job{}, err
	}
	file, _, _, err := a.historyPlannedFile(ctx, prior, item)
	if err != nil {
		return Job{}, err
	}
	if current, err := snapshotTransferPath(item.Source); err != nil || current != planSnapshot(file) {
		return Job{}, bad("REVIEW_REORGANIZE_SOURCE", "原整理源文件已发生变化")
	}
	if recognize == nil {
		recognize = func(ctx context.Context) (json.RawMessage, error) {
			return a.recognizeOriginalTransferRetry(ctx, item.Source, prior, file)
		}
	}
	recognized, err := recognize(ctx)
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
	view, err := originalCatalogMediaProjection(enriched)
	if err != nil {
		return Job{}, err
	}
	identity := view.Media
	var providerIdentity catalog.Media
	if !validMedia(identity.MediaType, identity.ID) {
		provider := originalChatMediaProvider{Source: view.Source, ID: view.SourceID}
		info, err := originalChatProviderSearchInformation(enriched, &provider, view.MediaType)
		if err != nil {
			return Job{}, err
		}
		identity, providerIdentity = info.Media, info.Media
	}
	targetPath := ""
	if prior.RootID != "" {
		root, err := a.importRoot(prior.RootID)
		if err != nil {
			return Job{}, err
		}
		targetPath = root.Path
	} else if item.FileStatus != "blocked" || item.Target != "" {
		return Job{}, originalTransferRetryStale()
	}
	out, err := a.manualOrganize(ctx, manualOrganizeRequest{
		originalRetry: true, originalRetryAttempt: &attempt, originalResolvedMedia: enriched,
		providerIdentity: providerIdentity, HistoryIDs: []string{item.ID}, TargetPath: targetPath,
		Mode: prior.Mode, MediaType: identity.MediaType, TMDBID: identity.ID, Renaming: prior.Renaming,
		Scraping: prior.Scraping, LibraryTypeFolder: prior.LibraryTypeFolder,
	})
	if err != nil {
		return Job{}, err
	}
	if len(out.Items) != 1 || out.Failed != 0 || out.Submitted != 1 {
		return Job{}, bad("TRANSFER_RETRY_FAILED", firstManualOrganizeFailure(out))
	}
	return scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, out.Items[0].JobID))
}

func (a *App) recognizeOriginalTransferRetry(ctx context.Context, path string, prior ImportPlan, file PlanFile) (json.RawMessage, error) {
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	meta, err := parser.ParsePath(path, nil)
	if err != nil {
		return nil, err
	}
	if prior.RootID != "" {
		var priorMeta importMetadata
		if err := a.Store.Get(ctx, "import_meta", prior.ID, &priorMeta); err != nil {
			return nil, err
		}
		source := priorMeta.OriginalTransfer[file.ID]
		if file.ParentFileID != "" {
			for _, sidecar := range priorMeta.Sidecars[file.ParentFileID] {
				if sidecar.Source == file.Source && sidecar.Original != nil {
					source = *sidecar.Original
					break
				}
			}
		}
		if len(source.Media) != 0 {
			value, err := decodeSourceJSON(source.Media)
			if err != nil {
				return nil, err
			}
			meta.Set("episode_group", sourceMap(value)["episode_group"])
		}
	}
	return a.originalCatalogRecognizeByMetaMethod(ctx, "recognize_media", meta, func(meta *releasemeta.MetaObject) (json.RawMessage, error) {
		return a.originalMetadataRecognizeNative(ctx, meta)
	})
}
func firstManualOrganizeFailure(out manualOrganizeOutput) string {
	for _, item := range out.Items {
		if item.Error != "" {
			return item.Error
		}
	}
	return "所选整理记录未能提交新的整理任务"
}
