package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// Presence marks an actual source-actor call start. Its sealed result is the
// existing recovery boundary: a missing result never permits a second call or
// a silent native fallback.
type importStorageSoftlinkState struct {
	InstanceID string `json:"instance_id"`
	Key        string `json:"key"`
	Result     []byte `json:"result,omitempty"`
}

func (a *App) stageStorageSoftlink(ctx context.Context, receipt importReceipt, storage *originalTransferStorage, check func() error) (importReceipt, error) {
	if check == nil {
		check = ctx.Err
	}
	if receipt.Softlink == nil || receipt.Softlink.Directory != nil || receipt.SourceSnapshot.Directory != "" || receipt.State != "planned" || receipt.Move != nil || receipt.StorageMove != nil || receipt.StageSnapshot != (importSnapshot{}) || receipt.SHA256 != "" {
		return receipt, bad("REVIEW_STORAGE_SOFTLINK", "存储软链接暂存与发布收据不同")
	}
	stage := filepath.Join(filepath.Dir(receipt.Target), receipt.Stage)
	if receipt.Stage != ".light-media-"+tokenHash(receipt.ID)[:32]+".part" {
		return receipt, bad("REVIEW_STORAGE_SOFTLINK", "存储软链接暂存位置不同")
	}
	if receipt.StorageSoftlink == nil {
		if storage == nil || storage.object == nil || storage.definition.Methods["softlink"] == "" || !slices.Contains(storage.object.manifest.Methods, storage.definition.Methods["softlink"]) || !originalObjectStillLoaded(storage.object) {
			return receipt, bad("PLUGIN_STORAGE_METHOD", "插件源存储对象缺少可调用方法：softlink")
		}
		if err := check(); err != nil {
			return receipt, err
		}
		for _, path := range []string{receipt.Target, stage} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return receipt, bad("IMPORT_CONFLICT", "存储软链接目标或暂存已存在")
			}
		}
		source, err := snapshotPath(receipt.Source)
		if err != nil || source != receipt.SourceSnapshot {
			return receipt, bad("REVIEW_SOURCE_CHANGED", "源存储调用前普通引用文件已变化")
		}
		item, err := storageMoveFileItem(ctx, receipt.Source)
		if write, _ := ctx.Value(originalTransferWriteContextKey{}).(*originalTransferWriteContext); write != nil && write.File.Source == receipt.Source {
			item, err = originalTransferSourceItem(write.Source, write.File)
		}
		if err != nil {
			return receipt, err
		}
		receipt.StorageSoftlink = &importStorageSoftlinkState{InstanceID: storage.definition.InstanceID, Key: storage.definition.Key}
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
		// MP dispatches to the source object's softlink(fileitem, target_file).
		// The logical destination remains exact; PublicationPath is its owned stage.
		result, err := storage.call(ctx, "softlink", item, receipt.Target, nil, stage)
		if err != nil {
			return receipt, err
		}
		receipt.StorageSoftlink.Result, err = a.Store.Seal(result)
		if err != nil {
			return receipt, err
		}
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
	}
	entry, err := a.verifyStorageSoftlinkStage(ctx, receipt)
	if err != nil {
		return receipt, err
	}
	if err = check(); err != nil {
		return receipt, err
	}
	receipt.StageSnapshot, receipt.State = entry, "staged"
	return receipt, a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
}

// Directory recovery uses the same returned-result proof before accepting an
// actor-created stage whose staged-snapshot SQL confirmation did not complete.
func (a *App) verifyStorageSoftlinkStage(ctx context.Context, receipt importReceipt) (importSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return importSnapshot{}, err
	}
	if receipt.State != "planned" || receipt.StageSnapshot != (importSnapshot{}) || receipt.Softlink == nil || receipt.Softlink.Directory != nil || receipt.Softlink.Source != receipt.Source || receipt.Softlink.SourceSnapshot != receipt.SourceSnapshot || receipt.SHA256 != "" || receipt.Stage != ".light-media-"+tokenHash(receipt.ID)[:32]+".part" || receipt.StorageSoftlink == nil {
		return importSnapshot{}, bad("REVIEW_STORAGE_SOFTLINK", "存储软链接暂存缺少已确认结果证明")
	}
	stage := filepath.Join(filepath.Dir(receipt.Target), receipt.Stage)
	proof := receipt.StorageSoftlink
	if proof.InstanceID == "" || proof.Key == "" || len(proof.Result) == 0 {
		return importSnapshot{}, bad("REVIEW_STORAGE_SOFTLINK", "源存储软链接已调用但结果尚未持久确认，未重复调用")
	}
	var result json.RawMessage
	if err := a.Store.Unseal(proof.Result, &result); err != nil {
		return importSnapshot{}, err
	}
	if !originalModuleResultTruthy(result) {
		return importSnapshot{}, bad("PLUGIN_STORAGE_FAILED", "插件源存储软链接返回失败")
	}
	entry, text, err := snapshotSoftlinkEntry(stage)
	if err != nil || text != receipt.Source {
		return entry, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "源存储返回成功但暂存不是所选真实软链接")
	}
	if _, err = snapshotOwnedTransferPath(stage, entry, receipt.Softlink, true); err != nil {
		return entry, err
	}
	return entry, nil
}
