package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func originalTransferSideFile(primary PlanFile, side importSidecar, index int) PlanFile {
	primary.ID = fmt.Sprintf("%s/side-%d", primary.ID, index)
	primary.Source, primary.Target = side.Source, side.Target
	primary.Size, primary.ModNS = side.Snapshot.Size, side.Snapshot.ModNS
	primary.Device, primary.Inode = side.Snapshot.Device, side.Snapshot.Inode
	primary.HistorySource, primary.AssetID = "", ""
	return primary
}

// Related files are ordinary transfer calls in the pinned manual task loop.
// Keep the existing host's grouped ownership, but dispatch each physical file
// separately and resolve its native name only after its module fallback.
func (a *App) publishOriginalTransferSide(ctx context.Context, job Job, plan *ImportPlan, index, sideIndex int, meta *importMetadata, quality *qualityImportProof, check func() error) (importReceipt, error) {
	primary := plan.Files[index]
	side := meta.Sidecars[primary.ID][sideIndex]
	extra := originalTransferSideFile(primary, side, sideIndex)
	if side.Original != nil {
		ctx = context.WithValue(ctx, originalTransferWriteContextKey{}, &originalTransferWriteContext{File: extra, Source: *side.Original, Media: plan.Media})
		source, err := a.originalSelectTransferStorage(ctx)
		if err != nil {
			return importReceipt{}, err
		}
		target, err := a.originalSelectTransferStorage(ctx)
		if err != nil {
			return importReceipt{}, err
		}
		ctx = context.WithValue(ctx, originalTransferStorageKey{}, &originalTransferStoragePair{Source: source, Target: target})
	}

	native := func() (importReceipt, error) {
		if side.Original != nil && side.NamingBase != "" {
			receipt := originalTransferPlannedReceipt(*plan, extra)
			err := a.Store.Get(ctx, "import_receipt", receipt.ID, &receipt)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return receipt, err
			}
			if receipt.State == "planned" && !receipt.OriginalNamingResolved {
				path, err := a.originalTransferNativeRename(ctx, plan.Media, extra, side.NamingBase, *side.Original, nil)
				if err != nil {
					return receipt, err
				}
				receipt.Target = path
				receipt.OriginalNamingResolved = true
				if err := a.transitionOriginalTransferTargetAt(ctx, job, plan, index, meta, receipt, false, sideIndex); err != nil {
					return receipt, err
				}
				side = meta.Sidecars[primary.ID][sideIndex]
				extra = originalTransferSideFile(primary, side, sideIndex)
			}
		}
		if err := a.originalPrepareTransferTarget(ctx, side.Target); err != nil {
			return importReceipt{}, err
		}
		if meta.Overwrite != nil {
			return a.publishDirectoryOverwrite(ctx, plan.ID+"/"+extra.ID, side.Source, side.Target, plan.RootID, primary.Method, side.Snapshot, *meta.Overwrite, check)
		}
		if quality == nil {
			return a.publishImport(ctx, plan.ID+"/"+extra.ID, side.Source, side.Target, side.Snapshot, primary.Method, check)
		}
		return a.publishQualitySidecar(ctx, *plan, primary, quality, sideIndex, side, check)
	}
	return a.originalTransferModulePublish(ctx, *plan, extra, *meta, check, native, func(receipt importReceipt) error {
		return a.transitionOriginalTransferTargetAt(ctx, job, plan, index, meta, receipt, true, sideIndex)
	})
}
