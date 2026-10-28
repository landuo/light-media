package app

import (
	"context"

	"light-media/internal/catalog"
)

func (a *App) originalTransferRelatedPreview(ctx context.Context, plan ImportPlan, primary PlanFile, meta *importMetadata, selection *nativeDirectorySelection, base string, episodes []catalog.Episode) ([]PlanFile, error) {
	sides, err := a.sourceImportSidecarsChecked(ctx, primary, nil, false)
	if err != nil {
		return nil, err
	}
	files := make([]PlanFile, 0, len(sides))
	for index, side := range sides {
		extra := originalTransferSideFile(primary, side, index)
		extra.ID = id("file_")
		extra.Status, extra.Reason = "ready", ""
		original := meta.OriginalTransfer[primary.ID]
		original.Item = side.Item
		meta.OriginalTransfer[extra.ID] = original
		meta.Methods[extra.ID] = extra.Method
		target, callback, err := a.originalTransferModulePreview(ctx, plan, extra, *meta, selection, base, episodes)
		extra.Target, extra.OriginalPreviewCallback = target, callback
		if err != nil {
			extra.Status, extra.Reason = "blocked", a.clean(err.Error())
		}
		files = append(files, extra)
	}
	return files, nil
}
