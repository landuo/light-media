package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
)

func canonicalSlotsContain(coverage, required []canonicalEpisodeSlot) bool {
	for _, slot := range required {
		if !slices.Contains(coverage, slot) {
			return false
		}
	}
	return true
}

// A grouped physical file is indivisible. Replacing it must preserve all of its
// canonical seasons, not just the presentation season used in its filename.
func (a *App) guardDirectoryOverwriteInventory(ctx context.Context, op directoryOverwriteOperation) error {
	var file PlanFile
	var key string
	if active, _ := ctx.Value(originalTransferWriteContextKey{}).(*originalTransferWriteContext); active != nil {
		file, key = active.File, mediaKey(active.Media)
	} else if planID, suffix, ok := strings.Cut(op.ID, "/"); ok {
		var plan ImportPlan
		if err := a.Store.Get(ctx, "import_plan", planID, &plan); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		fileID, _, _ := strings.Cut(suffix, "/")
		for _, candidate := range plan.Files {
			if candidate.ID == fileID {
				file, key = candidate, mediaKey(plan.Media)
				break
			}
		}
	}
	for _, target := range op.Targets {
		if target.Owner == nil {
			continue
		}
		var asset Asset
		if err := a.Store.Get(ctx, "asset", target.Owner.AssetID, &asset); err != nil {
			if errors.Is(err, sql.ErrNoRows) && target.Owner.Kind != "video" {
				continue // An independently published extra need not own a video.
			}
			return err
		}
		if len(asset.EpisodeScopes) == 0 && len(file.EpisodeScopes) == 0 {
			continue
		}
		if key != mediaKey(asset.Media) || !canonicalSlotsContain(file.inventorySlots(), asset.inventorySlots()) {
			return bad("QUALITY_PARTIAL_PATH_CONFLICT", "原文件还承载未替换季集，保留原文件")
		}
	}
	return nil
}
