package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

// An explicit local hardlink operation can register a link that is already
// present. This resolves unknown overwrite policy only when no video needs
// copying, replacing, or deleting.
func (a *App) reuseExistingImportHardlink(ctx context.Context, job Job, plan *ImportPlan, index int, meta importMetadata, check func() error) (importReceipt, bool, error) {
	file := plan.Files[index]
	if !plan.Manual || plan.Media.MediaType != "movie" || plan.IntentID != "" || meta.Reorganize != nil ||
		meta.Overwrite == nil || meta.Overwrite.Policy.Known || meta.Methods[file.ID] != "hardlink" ||
		file.DirectoryDigest != "" || file.MaterialKind != "" || len(meta.Sidecars[file.ID]) != 0 {
		return importReceipt{}, false, nil
	}
	if storage := originalTransferStorageFromContext(ctx); storage != nil && (storage.Source != nil || storage.Target != nil) {
		return importReceipt{}, false, nil
	}
	receipt := originalTransferPlannedReceipt(*plan, file)
	if err := a.Store.Get(ctx, "import_receipt", receipt.ID, &receipt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return importReceipt{}, false, err
	}
	if !receipt.ReusedHardlink {
		if receipt.State != "planned" || receipt.OriginalModuleStarted {
			return importReceipt{}, false, nil
		}
		var overwriting bool
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind='directory_overwrite' AND id=?)`,
			receipt.ID).Scan(&overwriting); err != nil {
			return importReceipt{}, false, err
		}
		if overwriting {
			return importReceipt{}, false, nil
		}
		if _, err := snapshotPath(filepath.Join(filepath.Dir(file.Target), receipt.Stage)); !errors.Is(err, os.ErrNotExist) {
			return importReceipt{}, false, nil
		}
		target := file.Target
		stamp, err := snapshotPath(target)
		if errors.Is(err, os.ErrNotExist) {
			candidates, err := a.directoryOverwriteLatestCandidates(ctx, target)
			if err != nil {
				return importReceipt{}, false, err
			}
			if len(candidates) != 1 {
				return importReceipt{}, false, nil
			}
			target = candidates[0]
			stamp, err = snapshotPath(target)
			if err != nil {
				return importReceipt{}, false, nil
			}
		} else if err != nil {
			return importReceipt{}, false, nil
		}
		if target == file.Source || filepath.Dir(target) != filepath.Dir(file.Target) ||
			!within(meta.RootPath, target) || stamp != planSnapshot(file) {
			return importReceipt{}, false, nil
		}
		receipt.Target, receipt.OriginalNamingResolved, receipt.ReusedHardlink = target, true, true
		if err := checkExistingImportHardlink(ctx, a.Store.DB, receipt); err != nil {
			return importReceipt{}, false, err
		}
		if err := a.transitionOriginalTransferTarget(ctx, job, plan, index, meta, receipt, false); err != nil {
			return importReceipt{}, false, err
		}
	}
	if err := check(); err != nil {
		return receipt, true, err
	}
	if err := a.guardDirectoryOverwritePolicy(ctx, *meta.Overwrite); err != nil {
		return receipt, true, err
	}
	if err := checkExistingImportHardlink(ctx, a.Store.DB, receipt); err != nil {
		return receipt, true, err
	}
	receipt.State, receipt.StageSnapshot = "published", receipt.SourceSnapshot
	return receipt, true, a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
}

func checkExistingImportHardlink(ctx context.Context, q qualityReader, receipt importReceipt) error {
	source, sourceErr := snapshotPath(receipt.Source)
	target, targetErr := snapshotPath(receipt.Target)
	if sourceErr != nil || targetErr != nil || source != receipt.SourceSnapshot || target != source {
		return bad("REVIEW_TARGET_CHANGED", "已有硬链接与源文件已无法核对，请重新整理")
	}
	var owned bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE
		(kind='managed_file' AND id=?) OR
		(kind='asset' AND json_extract(payload,'$.path')=? AND json_extract(payload,'$.state') NOT IN ('removed','superseded')))`,
		tokenHash(receipt.Target), receipt.Target).Scan(&owned); err != nil {
		return err
	}
	if owned {
		return bad("IMPORT_CONFLICT", "已有硬链接已经登记，请从对应整理记录操作")
	}
	return nil
}
