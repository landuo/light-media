package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// Presence is the durable call-start marker. Result seals the actual returned
// value; ResultSnapshot binds the entry observed with that return, independently
// from the later staged/publication acknowledgement. No result means no replay.
type importStoragePublicationState struct {
	InstanceID     string         `json:"instance_id"`
	Key            string         `json:"key"`
	Method         string         `json:"method"`
	Parent         importSnapshot `json:"parent"`
	FileItem       []byte         `json:"fileitem"`
	SourceSHA256   string         `json:"source_sha256,omitempty"`
	Result         []byte         `json:"result,omitempty"`
	ResultSnapshot importSnapshot `json:"result_snapshot"`
}

func storagePublicationDigest(ctx context.Context, path string, expected importSnapshot, check func() error) (string, error) {
	input, err := importOpen(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !expected.matches(info) {
		return "", bad("REVIEW_SOURCE_CHANGED", "存储发布内容身份已变化")
	}
	digest, err := importCopy(ctx, input, io.Discard, check)
	if err != nil {
		return "", err
	}
	if info, err = input.Stat(); err != nil || !expected.matches(info) {
		return "", bad("REVIEW_SOURCE_CHANGED", "存储发布内容在核验期间变化")
	}
	if actual, err := snapshotPath(path); err != nil || actual != expected {
		return "", bad("REVIEW_SOURCE_CHANGED", "存储发布路径在核验期间变化")
	}
	return digest, nil
}

func storagePublicationShape(receipt importReceipt, method string) error {
	state := receipt.StoragePublication
	if state == nil || state.InstanceID == "" || state.Key == "" || (method != "copy" && method != "hardlink") || state.Method != method || len(state.FileItem) == 0 || state.Parent.Inode == 0 || receipt.SourceSnapshot.Directory != "" || receipt.Softlink != nil || receipt.StorageSoftlink != nil || receipt.Move != nil || receipt.StorageMove != nil || receipt.Stage != ".light-media-"+tokenHash(receipt.ID)[:32]+".part" {
		return bad("REVIEW_STORAGE_PUBLICATION", "存储发布调用与冻结方式或路径不同")
	}
	if len(state.Result) == 0 {
		return bad("REVIEW_STORAGE_PUBLICATION", "源存储已调用但结果未封存，未重复调用或回退")
	}
	return nil
}

func (a *App) verifyStoragePublicationEntry(ctx context.Context, receipt importReceipt, path string, check func() error) (importSnapshot, error) {
	if check == nil {
		check = ctx.Err
	}
	state := receipt.StoragePublication
	if state == nil {
		return importSnapshot{}, bad("REVIEW_STORAGE_PUBLICATION", "存储发布证明缺失")
	}
	if err := storagePublicationShape(receipt, state.Method); err != nil {
		return importSnapshot{}, err
	}
	var result json.RawMessage
	if err := a.Store.Unseal(state.Result, &result); err != nil {
		return importSnapshot{}, err
	}
	if !originalModuleResultTruthy(result) {
		return importSnapshot{}, bad("PLUGIN_STORAGE_FAILED", "插件源存储文件操作返回失败")
	}
	if err := check(); err != nil {
		return importSnapshot{}, err
	}
	parent, err := importDir(filepath.Dir(receipt.Target), false)
	if err != nil {
		return importSnapshot{}, err
	}
	info, err := parent.Stat()
	parent.Close()
	if err != nil {
		return importSnapshot{}, err
	}
	identity, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(identity, state.Parent) {
		return importSnapshot{}, bad("REVIEW_STORAGE_PUBLICATION", "存储发布父目录与调用前不同")
	}
	source, err := snapshotPath(receipt.Source)
	if err != nil || source != receipt.SourceSnapshot {
		return importSnapshot{}, bad("REVIEW_SOURCE_CHANGED", "存储发布源文件与调用前不同")
	}
	entry, err := snapshotPath(path)
	if err != nil || state.ResultSnapshot.Inode == 0 || entry != state.ResultSnapshot {
		return entry, bad("REVIEW_STORAGE_PUBLICATION", "存储返回结果对应的实际条目缺失或已替换")
	}
	if entry.Size != source.Size {
		return entry, bad("REVIEW_COPY_SIZE", "存储发布文件大小与源不同")
	}
	if (state.Method == "hardlink") != sameInode(entry, source) {
		return entry, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "源存储实际条目与所选复制或硬链接方式不同")
	}
	if state.Method == "copy" {
		digest, err := storagePublicationDigest(ctx, path, entry, check)
		if err != nil {
			return entry, err
		}
		sourceDigest, err := storagePublicationDigest(ctx, receipt.Source, source, check)
		if err != nil {
			return entry, err
		}
		if state.SourceSHA256 == "" || digest != state.SourceSHA256 || sourceDigest != state.SourceSHA256 {
			return entry, bad("REVIEW_COPY_CONTENT", "插件复制内容与调用前源内容不同")
		}
	} else if entry != source {
		return entry, bad("REVIEW_SOURCE_CHANGED", "硬链接条目与冻结源身份不同")
	}
	return entry, nil
}

// The directory parent can recognize this exact returned-but-not-yet-staged
// entry without adopting arbitrary files discovered while walking its target.
func (a *App) verifyStoragePublicationStage(ctx context.Context, receipt importReceipt, check func() error) (importSnapshot, error) {
	if receipt.State != "planned" || receipt.StageSnapshot != (importSnapshot{}) || receipt.SHA256 != "" {
		return importSnapshot{}, bad("REVIEW_STORAGE_PUBLICATION", "存储返回暂存与发布阶段不同")
	}
	return a.verifyStoragePublicationEntry(ctx, receipt, filepath.Join(filepath.Dir(receipt.Target), receipt.Stage), check)
}

func (a *App) publishStorageCopyLink(ctx context.Context, receipt importReceipt, method string, storage *originalTransferStorage, check func() error) (importReceipt, error) {
	if check == nil {
		check = ctx.Err
	}
	if method != "copy" && method != "hardlink" {
		return receipt, bad("REVIEW_STORAGE_PUBLICATION", "存储发布方式不同")
	}
	parent, err := importDir(filepath.Dir(receipt.Target), false)
	if err != nil {
		return receipt, err
	}
	defer parent.Close()
	stage := filepath.Join(filepath.Dir(receipt.Target), receipt.Stage)
	if receipt.StoragePublication == nil {
		operation := originalTransferType(method)
		if storage == nil || storage.object == nil || storage.definition.Methods[operation] == "" || !slices.Contains(storage.object.manifest.Methods, storage.definition.Methods[operation]) || !originalObjectStillLoaded(storage.object) {
			return receipt, bad("PLUGIN_STORAGE_METHOD", "插件源存储缺少已选择的文件操作")
		}
		if receipt.State != "planned" || receipt.StageSnapshot != (importSnapshot{}) || receipt.SHA256 != "" {
			return receipt, bad("REVIEW_STORAGE_PUBLICATION", "已有暂存阶段不切换存储发布来源")
		}
		if err = check(); err != nil {
			return receipt, err
		}
		for _, path := range []string{receipt.Target, stage} {
			if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return receipt, bad("IMPORT_CONFLICT", "存储调用前目标或暂存已存在")
			}
		}
		if actual, err := snapshotPath(receipt.Source); err != nil || actual != receipt.SourceSnapshot {
			return receipt, bad("REVIEW_SOURCE_CHANGED", "存储调用前源文件已变化")
		}
		info, err := parent.Stat()
		if err != nil {
			return receipt, err
		}
		identity, err := originalDirectoryIdentity(info)
		if err != nil {
			return receipt, err
		}
		item, err := storageMoveFileItem(ctx, receipt.Source)
		if write, _ := ctx.Value(originalTransferWriteContextKey{}).(*originalTransferWriteContext); write != nil && write.File.Source == receipt.Source {
			item, err = originalTransferSourceItem(write.Source, write.File)
		}
		if err != nil {
			return receipt, err
		}
		sealed, err := a.Store.Seal(item)
		if err != nil {
			return receipt, err
		}
		state := &importStoragePublicationState{InstanceID: storage.definition.InstanceID, Key: storage.definition.Key, Method: method, Parent: identity, FileItem: sealed}
		if method == "copy" {
			state.SourceSHA256, err = storagePublicationDigest(ctx, receipt.Source, receipt.SourceSnapshot, check)
			if err != nil {
				return receipt, err
			}
		}
		receipt.StoragePublication = state
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
		path := receipt.Target
		var name *string
		if method == "copy" {
			path = filepath.Dir(receipt.Target)
			filename := filepath.Base(receipt.Target)
			name = &filename
		}
		result, err := storage.call(ctx, operation, item, path, name, stage)
		if err != nil {
			return receipt, err
		}
		state.Result, err = a.Store.Seal(result)
		if err != nil {
			return receipt, err
		}
		// Seal the response even if the actor returned False or no valid stage.
		// Capturing a regular entry never authorizes it: method/hash/source checks follow.
		state.ResultSnapshot, _ = snapshotPath(stage)
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
	}
	if err = storagePublicationShape(receipt, method); err != nil {
		return receipt, err
	}
	if _, err = os.Lstat(receipt.Target); err == nil {
		if receipt.State != "staged" && receipt.State != "published" {
			return receipt, bad("IMPORT_CONFLICT", "存储目标缺少正式发布阶段证明")
		}
		entry, e := a.verifyStoragePublicationEntry(ctx, receipt, receipt.Target, check)
		if e != nil {
			return receipt, e
		}
		if entry != receipt.StageSnapshot {
			return receipt, bad("REVIEW_STORAGE_PUBLICATION", "已发布条目与暂存身份不同")
		}
		receipt.State = "published"
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
			return receipt, err
		}
		return receipt, unlinkImportStage(parent, receipt)
	} else if !errors.Is(err, os.ErrNotExist) {
		return receipt, err
	}
	if receipt.State == "published" {
		return receipt, bad("REVIEW_PUBLISHED_MISSING", "已发布存储目标消失，未重复调用")
	}
	entry, err := a.verifyStoragePublicationEntry(ctx, receipt, stage, check)
	if err != nil {
		return receipt, err
	}
	if receipt.State == "planned" {
		if receipt.StageSnapshot != (importSnapshot{}) || receipt.SHA256 != "" {
			return receipt, bad("REVIEW_STORAGE_PUBLICATION", "待确认存储暂存含另一发布身份")
		}
	} else if receipt.State != "staged" || receipt.StageSnapshot != entry {
		return receipt, bad("REVIEW_STORAGE_PUBLICATION", "存储暂存阶段身份不同")
	}
	output, err := importOpen(stage)
	if err != nil {
		return receipt, err
	}
	err = output.Sync()
	output.Close()
	if err != nil {
		return receipt, err
	}
	if err = parent.Sync(); err != nil {
		return receipt, err
	}
	receipt.StageSnapshot, receipt.State = entry, "staged"
	receipt.SHA256 = receipt.StoragePublication.SourceSHA256
	if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
		return receipt, err
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if err = publishImportStage(parent, receipt); err != nil {
		return receipt, bad("IMPORT_CONFLICT", "存储暂存在独占发布时发生冲突")
	}
	if err = parent.Sync(); err != nil {
		return receipt, err
	}
	if actual, e := a.verifyStoragePublicationEntry(ctx, receipt, receipt.Target, check); e != nil || actual != entry {
		if e != nil {
			return receipt, e
		}
		return receipt, bad("REVIEW_TARGET_CHANGED", "存储目标在发布时变化")
	}
	receipt.State = "published"
	if err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
		return receipt, err
	}
	return receipt, unlinkImportStage(parent, receipt)
}
