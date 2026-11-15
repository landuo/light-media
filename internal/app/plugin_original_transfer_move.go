package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Hash before calling a destructive external provider. The existing source
// snapshot remains the identity proof; this independent content proof survives
// a successful plugin move, when rereading the original source is meaningless.
func originalMoveFileDigest(ctx context.Context, path string, expected importSnapshot, check func() error, sync bool) (string, error) {
	file, err := importOpen(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !expected.matches(info) {
		return "", bad("REVIEW_PLUGIN_MOVE_FILE", "插件移动文件与冻结快照不同")
	}
	digest, err := importCopy(ctx, file, io.Discard, check)
	if err != nil {
		return "", err
	}
	if sync {
		if err = file.Sync(); err != nil {
			return "", err
		}
	}
	info, err = file.Stat()
	actual, pathErr := snapshotPath(path)
	if err != nil || !expected.matches(info) || pathErr != nil || actual != expected {
		return "", bad("REVIEW_PLUGIN_MOVE_FILE", "插件移动文件在内容核验期间发生变化")
	}
	return digest, nil
}

func verifyOriginalPluginMovePublication(ctx context.Context, receipt importReceipt, check func() error) error {
	if receipt.State != "published" || receipt.Move == nil || receipt.Move.State != "completed" || receipt.Move.Publication != "plugin" || receipt.Move.Quarantine != "" || len(receipt.OriginalMoveSHA256) != 64 || receipt.SHA256 != receipt.OriginalMoveSHA256 {
		return bad("REVIEW_PLUGIN_MOVE_PROOF", "插件移动缺少完整的调用前内容证明")
	}
	digest, err := originalMoveFileDigest(ctx, receipt.Target, receipt.StageSnapshot, check, false)
	if err != nil {
		return err
	}
	if digest != receipt.OriginalMoveSHA256 {
		return bad("REVIEW_COPY_CONTENT", "插件移动目标内容与冻结源文件不同")
	}
	return nil
}

func (a *App) adoptOriginalPluginMove(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, receipt importReceipt, model map[string]any, check func() error, adopt func(importReceipt) error) (importReceipt, error) {
	if file.DirectoryDigest != "" || file.Method != "move" || sourceText(model["transfer_type"]) != "move" {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "插件返回的整理方式与普通文件移动不同")
	}
	if receipt.ID != plan.ID+"/"+file.ID || receipt.Source != file.Source || receipt.SourceSnapshot != planSnapshot(file) || len(receipt.OriginalMoveSHA256) != 64 || !receipt.OriginalModuleStarted || len(receipt.OriginalModuleResult) == 0 {
		return receipt, bad("REVIEW_PLUGIN_MOVE_PROOF", "插件移动缺少完整的调用前内容证明")
	}
	root, err := a.importRoot(plan.RootID)
	if err != nil || root.Path != meta.RootPath || !importPath(file.Target) || !within(root.Path, file.Target) || file.Target == file.Source {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件移动目标不在原授权媒体库中")
	}
	actual, err := snapshotPath(file.Target)
	if err != nil {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件返回移动成功，但目标普通文件不存在")
	}
	if actual.Size != receipt.SourceSnapshot.Size {
		return receipt, bad("REVIEW_COPY_SIZE", "插件移动目标与冻结源文件大小不同")
	}
	digest, err := originalMoveFileDigest(ctx, file.Target, actual, check, true)
	if err != nil {
		return receipt, err
	}
	if digest != receipt.OriginalMoveSHA256 {
		return receipt, bad("REVIEW_COPY_CONTENT", "插件移动目标内容与冻结源文件不同")
	}
	if _, err = os.Lstat(file.Source); !errors.Is(err, os.ErrNotExist) {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "插件返回移动成功，但原源路径仍存在或未确认移除")
	}
	parent, err := importDir(filepath.Dir(file.Target), false)
	if err != nil {
		return receipt, err
	}
	err = parent.Sync()
	parent.Close()
	if err != nil {
		return receipt, err
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if current, err := snapshotPath(file.Target); err != nil || current != actual {
		return receipt, bad("REVIEW_TARGET_CHANGED", "插件移动目标在收养前发生变化")
	}
	if _, err = os.Lstat(file.Source); !errors.Is(err, os.ErrNotExist) {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "插件移动源路径在收养前重新出现")
	}
	// This is terminal external ownership, never a request for native cleanup.
	// Later retries verify only the target and leave any recreated source alone.
	receipt.StageSnapshot, receipt.SHA256, receipt.State = actual, digest, "published"
	receipt.Move = &importMoveState{State: "completed", Publication: "plugin"}
	if receipt.Target != file.Target {
		receipt.Target = file.Target
		return receipt, adopt(receipt)
	}
	return receipt, a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
}
