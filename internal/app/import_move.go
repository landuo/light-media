package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Move publishes the original inode on the same filesystem, falling back to
// copy across filesystems, then durably clears the source. A completed receipt
// never grants deletion of a recreated source. Empty Publication is legacy copy.
type importMoveState struct {
	State       string `json:"state"`
	Quarantine  string `json:"quarantine"`
	Publication string `json:"publication,omitempty"`
}

type importMoveDeferredKey struct{}
type importMovePublishKey struct{}

func importMoveQuarantine(key, source string) string {
	return ".light-media-move-" + tokenHash(key + "\x00" + source)[:32] + ".part"
}

func (a *App) publishImportMove(ctx context.Context, key, source, target string, locked importSnapshot, check func() error) (importReceipt, error) {
	if check == nil {
		check = ctx.Err
	}
	if !importPath(source) || !importPath(target) || source == target || locked.Directory != "" {
		return importReceipt{}, bad("INVALID_TARGET", "移动需要两个不同的普通文件路径")
	}
	receipt := importReceipt{ID: key, Target: target, Source: source, SourceSnapshot: locked, Stage: ".light-media-" + tokenHash(key)[:32] + ".part", State: "planned"}
	var prior importReceipt
	err := a.Store.Get(ctx, "import_receipt", key, &prior)
	if err == nil {
		if prior.ID != key || prior.Source != source || prior.Target != target || prior.SourceSnapshot != locked || prior.Stage != receipt.Stage {
			return prior, bad("REVIEW_RECEIPT", "移动发布收据与计划不一致")
		}
		receipt = prior
	} else if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	}
	if receipt.StorageMove != nil {
		return a.publishStorageMove(ctx, receipt, nil, check)
	}
	if ctx.Value(importDirectoryStorageMoveKey{}) != nil {
		if receipt.Move != nil {
			return receipt, bad("REVIEW_MOVE_STORAGE", "存储目录成员与既有原生移动收据不同")
		}
		storage, e := directoryMoveStorage(ctx)
		if e != nil {
			return receipt, e
		}
		return a.publishStorageMove(ctx, receipt, storage, check)
	}
	if receipt.Move != nil {
		if err = validateImportMoveReceipt(receipt); err != nil {
			return receipt, err
		}
		if receipt.State == "published" {
			// Source storage selection is irrelevant to an already published
			// native operation. Recovery never calls a newly selected plugin.
			if ctx.Value(importMoveDeferredKey{}) == true && receipt.Move.State != "completed" {
				return receipt, verifyImportMoveMaterial(ctx, receipt, target, receipt.StageSnapshot)
			}
			return a.finishImportMove(ctx, key, check)
		}
	} else if receipt.State != "planned" {
		return receipt, bad("REVIEW_RECEIPT", "既有发布缺少移动源处理收据")
	}
	if storage := originalTransferStorageFromContext(ctx); storage != nil && storage.Source != nil {
		if receipt.Move != nil {
			return receipt, bad("REVIEW_MOVE_STORAGE", "既有原生移动不改用新选择的存储对象")
		}
		return a.publishStorageMove(ctx, receipt, storage.Source, check)
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if receipt.Move == nil {
		parent, e := importDir(filepath.Dir(target), true)
		if e != nil {
			return receipt, e
		}
		info, e := parent.Stat()
		parent.Close()
		if e != nil {
			return receipt, e
		}
		stamp, e := originalDirectoryIdentity(info)
		if e != nil {
			return receipt, e
		}
		method := "copy"
		if stamp.Device == locked.Device {
			method = "hardlink"
		}
		receipt.Move = &importMoveState{State: "pending", Quarantine: importMoveQuarantine(key, source), Publication: method}
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
	}
	// Reuse the existing no-clobber publisher. A same-filesystem move creates
	// no second data copy: its publication and source quarantine share one inode.
	// Keeping the old name until publication also supports deferred disc members.
	publicationContext := context.WithValue(ctx, importMovePublishKey{}, true)
	method := receipt.Move.Publication
	if method == "" {
		method = "copy"
	}
	receipt, err = a.publishImport(publicationContext, key, source, target, locked, method, check)
	var crossDevice *importEXDEVFailure
	var linkFailure *APIError
	if method == "hardlink" && (errors.As(err, &crossDevice) || errors.As(err, &linkFailure) && linkFailure.Code == "IMPORT_LINK_FAILED") {
		// Like a move's copy/unlink fallback, an unsupported link or a changed
		// mount may still use copy. The existing copy publisher checks any stage,
		// source change, available space and target conflict before writing.
		receipt.Move.Publication = "copy"
		if e := a.Store.Put(ctx, "import_receipt", key, receipt); e != nil {
			return receipt, e
		}
		receipt, err = a.publishImport(publicationContext, key, source, target, locked, "copy", check)
	}
	if err != nil {
		return receipt, err
	}
	if ctx.Value(importMoveDeferredKey{}) == true {
		return receipt, verifyImportMoveMaterial(ctx, receipt, target, receipt.StageSnapshot)
	}
	return a.finishImportMove(ctx, key, check)
}

func validateImportMoveReceipt(receipt importReceipt) error {
	if receipt.Move == nil || !importPath(receipt.Source) || !importPath(receipt.Target) || receipt.Source == receipt.Target || receipt.SourceSnapshot.Directory != "" || receipt.StageSnapshot.Directory != "" {
		return bad("REVIEW_MOVE_RECEIPT", "移动源处理收据身份无效")
	}
	switch receipt.Move.Publication {
	case "", "copy", "hardlink":
		if receipt.Move.Quarantine != importMoveQuarantine(receipt.ID, receipt.Source) {
			return bad("REVIEW_MOVE_RECEIPT", "移动源暂存身份无效")
		}
	case "plugin", "storage":
		if receipt.Move.State != "completed" || receipt.Move.Quarantine != "" {
			return bad("REVIEW_MOVE_RECEIPT", "插件移动缺少已确认的完成结果")
		}
	default:
		return bad("REVIEW_MOVE_RECEIPT", "移动发布方式无效")
	}
	switch receipt.Move.State {
	case "pending":
	case "quarantined", "completed":
		if receipt.State != "published" {
			return bad("REVIEW_MOVE_RECEIPT", "移动源清理缺少已发布目标")
		}
	default:
		return bad("REVIEW_MOVE_RECEIPT", "移动源处理阶段无效")
	}
	return nil
}

func verifyImportMoveMaterial(ctx context.Context, receipt importReceipt, path string, expected importSnapshot) error {
	if receipt.Move.Publication != "hardlink" {
		return verifyImportMoveFile(ctx, path, expected, receipt.SHA256)
	}
	if receipt.StageSnapshot != receipt.SourceSnapshot || receipt.SHA256 != "" {
		return bad("REVIEW_MOVE_RECEIPT", "同盘移动缺少原文件身份")
	}
	file, err := importOpen(path)
	if err != nil {
		return bad("REVIEW_MOVE_FILE_CHANGED", "移动文件已变化，保留现有文件供核对")
	}
	defer file.Close()
	info, err := file.Stat()
	stamp, pathErr := snapshotPath(path)
	if err != nil || !expected.matches(info) || pathErr != nil || stamp != expected {
		return bad("REVIEW_MOVE_FILE_CHANGED", "同盘移动文件或路径已变化")
	}
	return ctx.Err()
}

func verifyImportMoveFile(ctx context.Context, path string, expected importSnapshot, digest string) error {
	if len(digest) != 64 {
		return bad("REVIEW_MOVE_RECEIPT", "移动发布缺少完整内容校验")
	}
	file, err := importOpen(path)
	if err != nil {
		return bad("REVIEW_MOVE_FILE_CHANGED", "移动文件已变化，保留现有文件供核对")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !expected.matches(info) {
		return bad("REVIEW_MOVE_FILE_CHANGED", "移动文件指纹已变化，保留现有文件供核对")
	}
	actual, err := importCopy(ctx, file, io.Discard, nil)
	if err != nil {
		return err
	}
	info, err = file.Stat()
	current, pathErr := snapshotPath(path)
	if err != nil || !expected.matches(info) || pathErr != nil || current != expected || actual != digest {
		return bad("REVIEW_MOVE_FILE_CHANGED", "移动文件内容或路径已变化，保留现有文件供核对")
	}
	return nil
}

// finishImportMove is also used after all members of a directory have been
// published and the complete original directory snapshot has been checked.
func (a *App) finishImportMove(ctx context.Context, key string, check func() error) (importReceipt, error) {
	var receipt importReceipt
	if err := a.Store.Get(ctx, "import_receipt", key, &receipt); err != nil {
		return receipt, err
	}
	if receipt.ID != key || receipt.State != "published" {
		return receipt, bad("REVIEW_MOVE_RECEIPT", "移动源清理缺少对应的已发布收据")
	}
	if err := validateImportMoveReceipt(receipt); err != nil {
		return receipt, err
	}
	if err := verifyImportMoveMaterial(ctx, receipt, receipt.Target, receipt.StageSnapshot); err != nil {
		return receipt, err
	}
	if receipt.Move.State == "completed" {
		return receipt, nil // Do not even inspect a possibly recreated source.
	}
	if check == nil {
		check = ctx.Err
	}
	if err := check(); err != nil {
		return receipt, err
	}
	parent, err := importDir(filepath.Dir(receipt.Source), false)
	if err != nil {
		return receipt, err
	}
	defer parent.Close()
	quarantine := filepath.Join(parent.Name(), receipt.Move.Quarantine)
	stamp, quarantineErr := snapshotPath(quarantine)
	if quarantineErr != nil && !errors.Is(quarantineErr, os.ErrNotExist) {
		return receipt, bad("REVIEW_MOVE_SOURCE", "移动源暂存路径发生变化")
	}
	if receipt.Move.State == "pending" {
		if errors.Is(quarantineErr, os.ErrNotExist) {
			if err = verifyImportMoveMaterial(ctx, receipt, receipt.Source, receipt.SourceSnapshot); err != nil {
				return receipt, err
			}
			if err = check(); err != nil {
				return receipt, err
			}
			if err = importRenameExclusive(int(parent.Fd()), filepath.Base(receipt.Source), receipt.Move.Quarantine); err != nil {
				return receipt, err
			}
			stamp, quarantineErr = snapshotPath(quarantine)
			if quarantineErr != nil || stamp != receipt.SourceSnapshot {
				_ = importRenameExclusive(int(parent.Fd()), receipt.Move.Quarantine, filepath.Base(receipt.Source))
				_ = parent.Sync()
				return receipt, bad("REVIEW_MOVE_SOURCE", "移动源在隔离时发生变化，已保留文件")
			}
		} else {
			// Recovery from rename-before-SQL: the exact quarantined inode is
			// evidence; a different, recreated source is deliberately untouched.
			source, sourceErr := snapshotPath(receipt.Source)
			if sourceErr == nil && source == receipt.SourceSnapshot || sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
				return receipt, bad("REVIEW_MOVE_SOURCE", "源与移动暂存文件归属待核对")
			}
		}
		if stamp != receipt.SourceSnapshot {
			return receipt, bad("REVIEW_MOVE_SOURCE", "移动源暂存文件指纹已变化")
		}
		if err = parent.Sync(); err != nil {
			return receipt, err
		}
		receipt.Move.State = "quarantined"
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
	}
	if quarantineErr == nil {
		if err = verifyImportMoveMaterial(ctx, receipt, quarantine, receipt.SourceSnapshot); err != nil {
			return receipt, err
		}
		if err = check(); err != nil {
			return receipt, err
		}
		// Recheck both names immediately before unlink. The source name itself
		// is never unlinked, even if it was recreated after quarantine.
		target, targetErr := snapshotPath(receipt.Target)
		current, currentErr := snapshotPath(quarantine)
		if targetErr != nil || target != receipt.StageSnapshot || currentErr != nil || current != receipt.SourceSnapshot {
			return receipt, bad("REVIEW_MOVE_FILE_CHANGED", "移动清源前目标或暂存文件发生变化")
		}
		if err = unix.Unlinkat(int(parent.Fd()), receipt.Move.Quarantine, 0); err != nil {
			return receipt, err
		}
	}
	if err = parent.Sync(); err != nil {
		return receipt, err
	}
	receipt.Move.State = "completed"
	return receipt, a.Store.Put(ctx, "import_receipt", key, receipt)
}
