package app

import (
	"context"
	"light-media/internal/plugins"
)

// MP carries the original FileItem through transfer; after move the source no
// longer exists. Reuse the frozen item instead of restatting that old path.
func originalTransferRuntimeSourceItem(ctx context.Context, file PlanFile) (*plugins.ModuleFileItem, error) {
	if file.Method == "move" {
		if active, _ := ctx.Value(originalTransferWriteContextKey{}).(*originalTransferWriteContext); active != nil && active.File.Source == file.Source && active.Source.Item != nil {
			return originalTransferSourceItem(active.Source, file)
		}
	}
	return originalLocalFileItem(file.Source)
}
