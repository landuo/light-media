package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// This call permission is distinct from a completed physical entry proof. It
// records the exact absent target observed before invoking an external provider.
type originalSoftlinkProof struct {
	Target       string         `json:"target"`
	TargetAbsent bool           `json:"target_absent"`
	Parent       importSnapshot `json:"parent"`
	Adopted      bool           `json:"adopted,omitempty"`
}

func (a *App) freezeOriginalPluginSoftlink(plan ImportPlan, file PlanFile, meta importMetadata, receipt *importReceipt) error {
	root, err := a.importRoot(plan.RootID)
	if err != nil || root.Path != meta.RootPath || !importPath(file.Source) || !importPath(file.Target) || !within(root.Path, file.Target) || file.Source == file.Target || file.DirectoryDigest != "" || receipt.State != "planned" || receipt.OriginalModuleStarted {
		return bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "外部软链接调用前路径与计划证明不符")
	}
	source, err := snapshotPath(file.Source)
	if err != nil || source != planSnapshot(file) || source != receipt.SourceSnapshot {
		return bad("REVIEW_SOURCE_CHANGED", "外部软链接源文件与冻结快照不同")
	}
	parent, err := importDir(filepath.Dir(file.Target), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Stat()
	if err != nil {
		return err
	}
	stamp, err := originalDirectoryIdentity(info)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(file.Target); !errors.Is(err, os.ErrNotExist) {
		return bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "外部软链接调用前目标已存在或尚未确认空缺")
	}
	receipt.Softlink = &importSoftlinkProof{Source: file.Source, SourceSnapshot: source}
	receipt.OriginalSoftlink = &originalSoftlinkProof{Target: file.Target, TargetAbsent: true, Parent: stamp}
	return nil
}

func verifyOriginalPluginSoftlink(receipt importReceipt, model map[string]any) (importSnapshot, error) {
	permission := receipt.OriginalSoftlink
	item := sourceMap(model["target_item"])
	if sourceText(model["transfer_type"]) != "softlink" || item == nil || sourceText(item["storage"]) != "local" || sourceText(item["type"]) != "file" {
		return importSnapshot{}, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "外部软链接结果需声明本地普通文件与明确整理方式")
	}
	if permission == nil || !permission.TargetAbsent || permission.Target != receipt.Target || sourceText(item["path"]) != permission.Target || !receipt.OriginalModuleStarted || len(receipt.OriginalModuleResult) == 0 || receipt.Softlink == nil || receipt.Softlink.Directory != nil || receipt.Softlink.Source != receipt.Source || receipt.Softlink.SourceSnapshot != receipt.SourceSnapshot {
		return importSnapshot{}, bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "外部软链接缺少精确调用前目标与来源证明")
	}
	parent, err := importDir(filepath.Dir(receipt.Target), false)
	if err != nil {
		return importSnapshot{}, err
	}
	defer parent.Close()
	info, err := parent.Stat()
	if err != nil {
		return importSnapshot{}, err
	}
	stamp, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(stamp, permission.Parent) {
		return importSnapshot{}, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "外部软链接目标父目录已变化")
	}
	actual, text, err := snapshotSoftlinkEntry(receipt.Target)
	if err != nil || text != receipt.Source {
		return importSnapshot{}, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "外部整理结果不是引用冻结源文件的真实软链接")
	}
	if _, err = snapshotOwnedTransferPath(receipt.Target, actual, receipt.Softlink, true); err != nil {
		return importSnapshot{}, err
	}
	return actual, nil
}

func (a *App) adoptOriginalPluginSoftlink(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, receipt importReceipt, model map[string]any, check func() error) (importReceipt, error) {
	if file.DirectoryDigest != "" {
		return receipt, bad("PLUGIN_SOFTLINK_TRANSFER_UNSUPPORTED", "外部目录软链接结果尚未接入真实条目认领")
	}
	root, err := a.importRoot(plan.RootID)
	if err != nil || root.Path != meta.RootPath || receipt.ID != plan.ID+"/"+file.ID || receipt.Source != file.Source || receipt.SourceSnapshot != planSnapshot(file) || receipt.Target != file.Target || !within(meta.RootPath, file.Target) {
		return receipt, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "外部软链接结果偏离调用前冻结路径")
	}
	actual, err := verifyOriginalPluginSoftlink(receipt, model)
	if err != nil {
		return receipt, err
	}
	parent, err := importDir(filepath.Dir(receipt.Target), false)
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
	if after, e := verifyOriginalPluginSoftlink(receipt, model); e != nil || after != actual {
		return receipt, bad("REVIEW_TARGET_CHANGED", "外部软链接确认期间目标或引用已变化")
	}
	receipt.StageSnapshot, receipt.State = actual, "published"
	receipt.OriginalSoftlink.Adopted = true
	return receipt, a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
}
