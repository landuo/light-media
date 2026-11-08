package app

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// A manual scrape can recover the transfer's metadata without reinterpreting
// the original download identity. Only the requested published asset is added
// to the reconstructed event, even if the transfer also published other files.
func (a *App) providerAssetScrapeEvent(ctx context.Context, asset Asset, root Root, stamp importSnapshot) (json.RawMessage, error) {
	invalid := bad("REVIEW_METADATA_CONTEXT", "无法核对来源资产的下载与整理记录")
	planID, fileID, ok := strings.Cut(asset.FNOSRefreshTransfer, "/")
	if !ok || planID == "" || fileID == "" || asset.IntentID == "" {
		return nil, invalid
	}
	var intent Intent
	if err := a.Store.Get(ctx, "intent", asset.IntentID, &intent); err != nil {
		return nil, err
	}
	key := mediaKey(asset.Media)
	if intent.ID != asset.IntentID || intent.ImportPlanID != planID || intent.RootID != root.ID || mediaKey(intent.Media) != key || intent.State == "cancelled" {
		return nil, invalid
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", planID, &plan); err != nil {
		return nil, err
	}
	if plan.ID != planID || plan.RootID != root.ID || plan.IntentID != intent.ID || mediaKey(plan.Media) != key {
		return nil, invalid
	}
	index := slices.IndexFunc(plan.Files, func(file PlanFile) bool { return file.ID == fileID })
	if index < 0 {
		return nil, invalid
	}
	file := plan.Files[index]
	if file.Status != "imported" || file.MaterialKind != "" || file.AssetID != asset.ID || file.Target != asset.Path || file.Source != asset.SourcePath || file.Season != asset.Season || !slices.Equal(file.Episodes, asset.Episodes) {
		return nil, invalid
	}
	var owned managedFile
	if err := a.Store.Get(ctx, "managed_file", tokenHash(asset.Path), &owned); err != nil {
		return nil, err
	}
	if owned.Kind != "video" || owned.Path != asset.Path || owned.RootID != root.ID || owned.AssetID != asset.ID || owned.MediaKey != key ||
		owned.SourceSnapshot != planSnapshot(file) || (owned.Snapshot.Directory == "" && owned.Snapshot != stamp) {
		return nil, invalid
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", planID, &meta); err != nil {
		return nil, err
	}
	source, ok := meta.OriginalTransfer[fileID]
	if !ok || meta.OriginalPreview || meta.RootPath != root.Path || len(source.Meta) == 0 {
		return nil, invalid
	}
	if err := a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, plan, meta); err != nil {
		return nil, err
	}
	sealed, err := a.completedProviderDownloadMedia(ctx, intent)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(mustCleanSeedJSON(source.Media), mustCleanSeedJSON(sealed)) {
		return nil, invalid
	}
	module, _, err := a.originalRecordedTransferModule(ctx, plan, file)
	if err != nil {
		return nil, err
	}
	if module != nil && sourceMap(module["target_diritem"]) != nil {
		return originalTransferMetadataEvent(plan, file, meta, map[string]any{"target_diritem": module["target_diritem"], "file_list_new": []string{file.Target}})
	}
	return originalTransferMetadataEvent(plan, file, meta)
}
