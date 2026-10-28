package app

import (
	"context"
	"database/sql"
	"errors"
)

func originalTransferPlannedReceipt(plan ImportPlan, file PlanFile) importReceipt {
	key := plan.ID + "/" + file.ID
	return importReceipt{ID: key, Source: file.Source, Target: file.Target, SourceSnapshot: planSnapshot(file), Stage: ".light-media-" + tokenHash(key)[:32] + ".part", State: "planned"}
}

// Called only from the actual native transfer provider, after storage selection
// and module dispatch. Terminal plugin results never reach this function.
func (a *App) resolveOriginalNativeTransferName(ctx context.Context, job Job, plan *ImportPlan, index int, meta importMetadata) error {
	file := plan.Files[index]
	base, exists := meta.OriginalNamingBases[file.ID]
	if !exists {
		return nil
	}
	receipt := originalTransferPlannedReceipt(*plan, file)
	err := a.Store.Get(ctx, "import_receipt", receipt.ID, &receipt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if receipt.State != "planned" || receipt.OriginalNamingResolved {
		return nil
	}
	source, exists := meta.OriginalTransfer[file.ID]
	if !exists {
		return bad("PLUGIN_TRANSFER_CONTEXT", "原生整理命名缺少冻结媒体信息")
	}
	target, err := a.originalTransferNativeRename(ctx, plan.Media, file, base, source, nil)
	if err != nil {
		return err
	}
	receipt.Target = target
	receipt.OriginalNamingResolved = true
	return a.transitionOriginalTransferTarget(ctx, job, plan, index, meta, receipt, false)
}
