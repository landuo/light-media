package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// Presence is the durable call-start marker. A missing sealed result never
// permits replaying a potentially destructive storage operation.
type importStorageMoveState struct {
	InstanceID string `json:"instance_id"`
	Key        string `json:"key"`
	Result     []byte `json:"result,omitempty"`
}

// Recover the exact owned stage after the provider returned but before its
// staged snapshot committed. The directory guard uses this same proof rather
// than rejecting a real completed move as an unknown temporary file.
func (a *App) verifyStorageMoveStage(ctx context.Context, receipt importReceipt, check func() error) (importSnapshot, error) {
	proof := receipt.StorageMove
	if receipt.State != "planned" || proof == nil || proof.InstanceID == "" || proof.Key == "" || len(proof.Result) == 0 || len(receipt.SHA256) != 64 || receipt.Stage != ".light-media-"+tokenHash(receipt.ID)[:32]+".part" {
		return importSnapshot{}, bad("REVIEW_MOVE_STORAGE", "存储移动缺少可核对的暂存收据")
	}
	var result json.RawMessage
	if err := a.Store.Unseal(proof.Result, &result); err != nil {
		return importSnapshot{}, err
	}
	if !originalModuleResultTruthy(result) {
		return importSnapshot{}, bad("PLUGIN_STORAGE_FAILED", "插件存储移动返回失败")
	}
	stagePath := filepath.Join(filepath.Dir(receipt.Target), receipt.Stage)
	stage, err := snapshotPath(stagePath)
	if err != nil || stage.Size != receipt.SourceSnapshot.Size {
		return stage, bad("REVIEW_PLUGIN_MOVE_FILE", "存储移动暂存文件与原源大小不同或缺失")
	}
	digest, err := originalMoveFileDigest(ctx, stagePath, stage, check, true)
	if err != nil {
		return stage, err
	}
	if digest != receipt.SHA256 {
		return stage, bad("REVIEW_COPY_CONTENT", "存储移动目标内容与冻结源文件不同")
	}
	if _, err = os.Lstat(receipt.Source); !errors.Is(err, os.ErrNotExist) {
		return stage, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "存储移动返回成功但原源路径仍存在")
	}
	return stage, nil
}

func (a *App) publishStorageMove(ctx context.Context, receipt importReceipt, storage *originalTransferStorage, check func() error) (importReceipt, error) {
	if check == nil {
		check = ctx.Err
	}
	if receipt.StorageMove == nil {
		if ctx.Value(importMoveDeferredKey{}) == true && ctx.Value(importDirectoryStorageMoveKey{}) == nil {
			return receipt, bad("REVIEW_MOVE_STORAGE", "目录成员的移动缺少已冻结的父目录清单")
		}
		if storage == nil || storage.object == nil || storage.definition.Methods["move"] == "" || !slices.Contains(storage.object.manifest.Methods, storage.definition.Methods["move"]) || !originalObjectStillLoaded(storage.object) {
			return receipt, bad("PLUGIN_STORAGE_METHOD", "插件存储对象缺少可调用方法：move")
		}
		if receipt.State != "planned" || receipt.Move != nil {
			return receipt, bad("REVIEW_MOVE_STORAGE", "存储移动与已有发布阶段不同")
		}
		if err := check(); err != nil {
			return receipt, err
		}
		parent, err := importDir(filepath.Dir(receipt.Target), true)
		if err != nil {
			return receipt, err
		}
		parent.Close()
		stagePath := filepath.Join(filepath.Dir(receipt.Target), receipt.Stage)
		for _, path := range []string{receipt.Target, stagePath} {
			if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return receipt, bad("IMPORT_CONFLICT", "存储移动的目标或暂存路径已存在")
			}
		}
		item, err := storageMoveFileItem(ctx, receipt.Source)
		if err != nil {
			return receipt, err
		}
		receipt.SHA256, err = originalMoveFileDigest(ctx, receipt.Source, receipt.SourceSnapshot, check, false)
		if err != nil {
			return receipt, err
		}
		receipt.StorageMove = &importStorageMoveState{InstanceID: storage.definition.InstanceID, Key: storage.definition.Key}
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
		name := filepath.Base(receipt.Target)
		result, err := storage.call(ctx, "move", item, filepath.Dir(receipt.Target), &name, stagePath)
		if err != nil {
			return receipt, err
		}
		receipt.StorageMove.Result, err = a.Store.Seal(result)
		if err != nil {
			return receipt, err
		}
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
	}
	proof := receipt.StorageMove
	if proof.InstanceID == "" || proof.Key == "" || len(receipt.SHA256) != 64 {
		return receipt, bad("REVIEW_MOVE_STORAGE", "存储移动缺少原调用身份或内容证明")
	}
	if len(proof.Result) == 0 {
		return receipt, bad("REVIEW_MOVE_STORAGE", "存储移动已开始但结果未持久确认，请核对真实文件后处理")
	}
	var result json.RawMessage
	if err := a.Store.Unseal(proof.Result, &result); err != nil {
		return receipt, err
	}
	if !originalModuleResultTruthy(result) {
		return receipt, bad("PLUGIN_STORAGE_FAILED", "插件存储移动返回失败")
	}
	if receipt.Move != nil {
		if receipt.Move.Publication != "storage" {
			return receipt, bad("REVIEW_MOVE_STORAGE", "存储移动发布方式不同")
		}
		if err := validateImportMoveReceipt(receipt); err != nil {
			return receipt, err
		}
		return receipt, verifyImportMoveFile(ctx, receipt.Target, receipt.StageSnapshot, receipt.SHA256)
	}
	if receipt.State == "planned" {
		stage, err := a.verifyStorageMoveStage(ctx, receipt, check)
		if err != nil {
			return receipt, err
		}
		// From here onward the original was moved into an owned, verified stage.
		// Recreated source names are unrelated and are never read or cleared again.
		receipt.StageSnapshot, receipt.State = stage, "staged"
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
	}
	if receipt.State != "staged" && receipt.State != "published" {
		return receipt, bad("REVIEW_MOVE_STORAGE", "存储移动发布阶段无效")
	}
	// Reuse the existing staged/target recovery without opening the vanished
	// source or invoking its storage object. Storage stages use exclusive rename.
	publicationContext := context.WithValue(ctx, importMovePublishKey{}, true)
	var err error
	receipt, err = a.publishImport(publicationContext, receipt.ID, receipt.Source, receipt.Target, receipt.SourceSnapshot, "copy", check)
	if err != nil {
		return receipt, err
	}
	if err = verifyImportMoveFile(ctx, receipt.Target, receipt.StageSnapshot, receipt.SHA256); err != nil {
		return receipt, err
	}
	receipt.Move = &importMoveState{State: "completed", Publication: "storage"}
	return receipt, a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
}
