package app

import (
	"context"
	"path/filepath"
)

// A shared root has no whole-tree Softlink proof. The original method remains
// bound to its real per-member publication receipts, even when a later explicit
// metadata write replaces an owned NFO link with an ordinary file.
func (a *App) validateSharedSoftlinkPublication(ctx context.Context, receipt importReceipt) error {
	if receipt.SharedDirectory == nil || receipt.Softlink != nil || receipt.SourceSnapshot.Directory == "" || receipt.State != "published" {
		return bad("REVIEW_SOFTLINK_DIRECTORY", "共享目录软链接发布证明无效")
	}
	for _, relative := range receipt.DirectoryFiles {
		var child importReceipt
		key := receipt.ID + "/directory/" + tokenHash(relative)
		if err := a.Store.Get(ctx, "import_receipt", key, &child); err != nil {
			return err
		}
		if child.ID != key || child.Source != filepath.Join(receipt.Source, relative) || child.Target != filepath.Join(receipt.Target, receipt.directoryTarget(relative)) || child.State != "published" || !importPublicationMatchesMethod(child, "softlink") || child.Softlink.Directory != nil {
			return bad("REVIEW_SOFTLINK_DIRECTORY", "共享目录缺少原软链接子项发布")
		}
	}
	return nil
}
