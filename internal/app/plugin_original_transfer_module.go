package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

// transfer is a concrete ChainBase call at the existing publication boundary.
// Its native provider remains the existing recoverable publisher. Installed Go
// providers must produce the authorized physical file before it can be claimed.
func (a *App) originalTransferModulePublish(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, check func() error, native func() (importReceipt, error), adopt func(importReceipt) error) (importReceipt, error) {
	key := plan.ID + "/" + file.ID
	receipt := importReceipt{ID: key, Source: file.Source, Target: file.Target, SourceSnapshot: planSnapshot(file), Stage: ".light-media-" + tokenHash(key)[:32] + ".part", State: "planned"}
	var prior importReceipt
	err := a.Store.Get(ctx, "import_receipt", key, &prior)
	if err == nil {
		if prior.Source != receipt.Source || prior.Target != receipt.Target || prior.SourceSnapshot != receipt.SourceSnapshot {
			return receipt, bad("REVIEW_RECEIPT", "插件整理收据与冻结文件不符")
		}
		receipt = prior
	} else if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	}
	if parallelImport(ctx) != nil {
		if receipt.OriginalModuleStarted {
			return receipt, bad("REVIEW_PLUGIN_TRANSFER", "整理插件收据需要独占恢复")
		}
		return native()
	}
	var groups []originalPluginModuleGroup
	if !receipt.OriginalModuleStarted {
		// Recover a publication's frozen facts even if its provider was later
		// reconfigured or removed. Registry changes apply to a new operation.
		if receipt.State != "planned" {
			return native()
		}
		groups, err = a.originalPluginModules(ctx, "")
		if err != nil {
			return receipt, err
		}
		if !originalModuleGroupsHaveMethod(groups, "transfer") {
			return native()
		}
	}
	var result json.RawMessage
	nativeRan := false
	var nativeErr error
	var nativeReceipt importReceipt
	if len(receipt.OriginalModuleResult) > 0 && !receipt.OriginalNativeInterrupted {
		if err = a.Store.Unseal(receipt.OriginalModuleResult, &result); err != nil {
			return receipt, err
		}
	} else {
		if receipt.OriginalModuleStarted && !receipt.OriginalNativeInterrupted {
			return receipt, bad("REVIEW_PLUGIN_TRANSFER", "整理插件已调用但结果未持久确认，请核对真实文件后重新操作")
		}
		if err = check(); err != nil {
			return receipt, err
		}
		kwargs, err := a.originalTransferModuleArguments(ctx, plan, file, meta)
		if err != nil {
			return receipt, err
		}
		receipt.OriginalModuleStarted = true
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
		runNative := func() (any, error) {
			nativeRan = true
			nativeReceipt, nativeErr = native()
			nativeFile := file
			if nativeReceipt.Target != "" {
				nativeFile.Target = nativeReceipt.Target
			}
			value, err := a.originalNativeTransferModuleInfo(ctx, nativeFile, plan, nativeErr)
			if err != nil {
				return nil, err
			}
			info := value[plugins.ModuleTransferInfoKey].(map[string]any)
			if file.DirectoryDigest == "" {
				info["fileitem"] = kwargs["fileitem"]
			}
			if err := originalTransferApplyNativeFlags(info, kwargs, nativeErr == nil); err != nil {
				return nil, err
			}
			return value, nil
		}
		if receipt.OriginalNativeInterrupted {
			// A known native cancellation can resume its existing publisher.
			// Keep the completed plugin decision; do not invoke providers twice.
			var priorResult json.RawMessage
			if err = a.Store.Unseal(receipt.OriginalModuleResult, &priorResult); err != nil {
				return receipt, err
			}
			value, runErr := runNative()
			if runErr != nil {
				return receipt, runErr
			}
			var list []json.RawMessage
			if json.Unmarshal(priorResult, &list) == nil && list != nil {
				result = priorResult // Native TransferInfo does not replace a list.
			} else {
				result, err = json.Marshal(value)
			}
		} else {
			result, err = a.originalMetadataStorageGroups(ctx, "transfer", kwargs, runNative, groups)
		}
		if err != nil {
			return receipt, err
		}
		// The native publisher may have advanced the same receipt through staged
		// and published. Never replace that physical ownership with a stale copy.
		if err = a.Store.Get(ctx, "import_receipt", key, &receipt); err != nil {
			return receipt, err
		}
		if nativeRan && nativeErr == nil && nativeReceipt.State == "published" {
			receipt.State, receipt.StageSnapshot, receipt.SHA256 = nativeReceipt.State, nativeReceipt.StageSnapshot, nativeReceipt.SHA256
		}
		receipt.OriginalModuleStarted = true
		receipt.OriginalNativeInterrupted = originalTransferInterrupted(nativeErr)
		receipt.OriginalModuleResult, err = a.Store.Seal(result)
		if err != nil {
			return receipt, err
		}
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
	}
	if originalTransferInterrupted(nativeErr) {
		return receipt, nativeErr
	}
	model, err := originalTransferModuleInfo(result)
	if err != nil {
		return receipt, err
	}
	if model["success"] != true {
		if nativeErr != nil {
			return receipt, nativeErr
		}
		message := sourceText(model["message"])
		if message == "" {
			message = "文件整理模块运行失败"
		}
		return receipt, bad("PLUGIN_TRANSFER_FAILED", a.clean(message))
	}
	if nativeRan {
		if nativeErr != nil {
			return receipt, nativeErr
		}
		return receipt, nil
	}
	if err = check(); err != nil {
		return receipt, err
	}
	// The fixed callback/history consumes target_item from the returned
	// TransferInfo. A valid local provider may choose a different destination
	// within the same authorized root; verify that actual file before adoption.
	if item := sourceMap(model["target_item"]); item != nil {
		if s := sourceText(item["storage"]); s != "" && s != "local" {
			return receipt, bad("PLUGIN_TRANSFER_STORAGE", "当前仅支持已授权本地整理目标")
		}
		if p := sourceText(item["path"]); p != "" {
			if file.DirectoryDigest != "" && sourceText(item["type"]) == "dir" {
				p = strings.TrimSuffix(p, "/")
			}
			if !importPath(p) || !within(meta.RootPath, p) || p == file.Source {
				return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件整理结果不在已授权的本地目标目录")
			}
			file.Target = p
		}
	}
	if receipt.State == "published" {
		if receipt.Target != file.Target {
			return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件整理目标与已确认收据不符")
		}
		actual, err := snapshotTransferPath(file.Target)
		if err != nil || actual != receipt.StageSnapshot {
			return receipt, bad("REVIEW_TARGET_CHANGED", "插件整理目标与已确认收据不符")
		}
		return receipt, nil
	}
	actual, err := snapshotTransferPath(file.Target)
	if err != nil {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "整理插件返回成功，但授权目标文件不存在或不可核对")
	}
	source, err := snapshotTransferPath(file.Source)
	if err != nil || source != planSnapshot(file) {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "整理插件调用期间源文件发生变化")
	}
	if file.DirectoryDigest == "" && actual.Size != source.Size {
		return receipt, bad("REVIEW_COPY_SIZE", "插件整理目标与源文件大小不一致")
	}
	if file.DirectoryDigest == "" && file.Method == "hardlink" && !sameInode(actual, source) {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "插件整理结果不是所选硬链接方式")
	}
	var digest string
	if file.DirectoryDigest != "" {
		digest, err = verifyOriginalDirectoryTransfer(ctx, file, model, check)
		if err != nil {
			return receipt, err
		}
	} else if file.Method == "copy" {
		src, e := importOpen(file.Source)
		if e != nil {
			return receipt, e
		}
		defer src.Close()
		dst, e := importOpen(file.Target)
		if e != nil {
			return receipt, e
		}
		defer dst.Close()
		digest, e = importCopy(ctx, src, io.Discard, check)
		if e != nil {
			return receipt, e
		}
		other, e := importCopy(ctx, dst, io.Discard, check)
		if e != nil {
			return receipt, e
		}
		if digest != other {
			return receipt, bad("REVIEW_COPY_CONTENT", "插件整理文件内容与冻结源文件不同")
		}
	}
	parent, err := importDir(filepath.Dir(file.Target), false)
	if err != nil {
		return receipt, err
	}
	defer parent.Close()
	if err = parent.Sync(); err != nil {
		return receipt, err
	}
	if after, e := snapshotTransferPath(file.Target); e != nil || after != actual {
		return receipt, bad("REVIEW_TARGET_CHANGED", "插件整理目标在确认期间发生变化")
	}
	if after, e := snapshotTransferPath(file.Source); e != nil || after != planSnapshot(file) {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "插件整理源文件在确认期间发生变化")
	}
	if err = check(); err != nil {
		return receipt, err
	}
	receipt.StageSnapshot, receipt.SHA256, receipt.State = actual, digest, "published"
	if receipt.Target != file.Target {
		receipt.Target = file.Target
		return receipt, adopt(receipt)
	}
	err = a.Store.Put(ctx, "import_receipt", key, receipt)
	return receipt, err
}

func originalTransferInterrupted(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == "JOB_CANCELLED"
}

func (a *App) originalTransferModuleArguments(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata) (map[string]any, error) {
	source, ok := meta.OriginalTransfer[file.ID]
	if !ok {
		if write, _ := ctx.Value(originalTransferWriteContextKey{}).(*originalTransferWriteContext); write != nil && write.File.ID == file.ID {
			source, ok = write.Source, true
		}
	}
	if !ok || len(source.Meta) == 0 || len(source.Media) == 0 {
		return nil, bad("PLUGIN_TRANSFER_CONTEXT", "整理插件缺少完整冻结媒体信息，请重新预览")
	}
	item, err := originalTransferSourceItem(source, file)
	if err != nil {
		return nil, err
	}
	var authority nativeDirectoryAuthorization
	if err := a.readQualitySealed(ctx, a.Store.DB, "native_directory_selection", plan.ID, &authority); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return originalTransferModuleKeywords(ctx, plan, file, meta, source, item, authority.Selection, false), nil
}

func originalTransferModuleKeywords(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, source originalTransferFileContext, item *plugins.ModuleFileItem, selection *nativeDirectorySelection, preview bool) map[string]any {
	var directory any
	var targetPath any = meta.RootPath
	if selection != nil {
		if selection.DirectoryID != "" {
			directory = selection.Rule.Values
			targetPath = nil
		}
		if selection.Query.DestPath != nil {
			targetPath = *selection.Query.DestPath
		}
	}
	var sourceOper, targetOper any
	if pair := originalTransferStorageFromContext(ctx); pair != nil {
		if pair.Source != nil {
			sourceOper = pair.Source.definition
		}
		if pair.Target != nil {
			targetOper = pair.Target.definition
		}
	}
	return map[string]any{"fileitem": item, "meta": source.Meta, "mediainfo": source.Media, "target_directory": directory, "target_path": targetPath, "target_storage": "local", "transfer_type": originalTransferType(file.Method), "scrape": plan.Scraping, "library_type_folder": false, "library_category_folder": plan.LibraryTypeFolder, "episodes_info": source.Episodes, "source_oper": sourceOper, "target_oper": targetOper, "preview": preview}
}

// Explicit previews take the same storage-selection and module-dispatch path
// as execution, without publication receipts or the normal transfer callback.
func (a *App) originalTransferModulePreview(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, selection *nativeDirectorySelection, base string, episodes []catalog.Episode) (string, bool, error) {
	source, ok := meta.OriginalTransfer[file.ID]
	if !ok {
		return "", false, bad("PLUGIN_TRANSFER_CONTEXT", "整理预览缺少媒体信息")
	}
	sourceStorage, err := a.originalSelectTransferStorage(ctx)
	if err != nil {
		return "", false, err
	}
	targetStorage, err := a.originalSelectTransferStorage(ctx)
	if err != nil {
		return "", false, err
	}
	ctx = context.WithValue(ctx, originalTransferStorageKey{}, &originalTransferStoragePair{Source: sourceStorage, Target: targetStorage})
	item, err := originalTransferSourceItem(source, file)
	if err != nil {
		return "", false, err
	}
	kwargs := originalTransferModuleKeywords(ctx, plan, file, meta, source, item, selection, true)
	raw, err := a.originalMetadataStorage(ctx, "transfer", kwargs, func() (any, error) {
		if importBool(plan.Renaming, true) {
			file.Target, err = a.originalTransferNativeRename(ctx, plan.Media, file, base, source, episodes)
			if err != nil {
				return nil, err
			}
		}
		root, e := originalTransferMediaRoot(plan, file)
		if e != nil {
			return nil, e
		}
		// Source __build_preview_item uses synthetic FileItem values; it
		// deliberately never calls get_folder/get_item or tests target existence.
		target := originalTransferPreviewItem(file.Target, "file", file.Size)
		dir := originalTransferPreviewItem(root, "dir", 0)
		if file.DirectoryDigest != "" {
			target = originalTransferPreviewItem(file.Target, "dir", 0)
			var sourcePath string
			_ = json.Unmarshal(item.Fields["path"], &sourcePath)
			return map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "fileitem": item, "target_item": target, "target_diritem": target, "transfer_type": originalTransferType(file.Method), "file_count": 0, "file_list": []string{sourcePath}, "file_list_new": []string{file.Target}, "total_size": 0, "need_notify": false, "need_scrape": importBool(plan.Scraping, false)}}, nil
		}
		return map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "fileitem": item, "target_item": target, "target_diritem": dir, "transfer_type": originalTransferType(file.Method), "file_count": 1, "file_list": []string{file.Source}, "file_list_new": []string{file.Target}, "total_size": file.Size, "need_notify": false, "need_scrape": importBool(plan.Scraping, false)}}, nil
	})
	if err != nil {
		return "", false, err
	}
	model, err := originalTransferModuleInfo(raw)
	if err != nil {
		return "", false, err
	}
	path := ""
	if target := sourceMap(model["target_item"]); target != nil {
		path = sourceText(target["path"])
		if file.DirectoryDigest != "" && sourceText(target["type"]) == "dir" {
			path = strings.TrimSuffix(path, "/")
		}
		if path != "" && (!importPath(path) || !within(meta.RootPath, path) || path == file.Source) {
			return "", true, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件预览路径不在已授权的本地目标目录")
		}
	}
	if model["success"] != true {
		message := sourceText(model["message"])
		if message == "" {
			message = "文件整理模块运行失败"
		}
		return path, true, bad("PLUGIN_TRANSFER_FAILED", a.clean(message))
	}
	return path, true, nil
}

func originalTransferPreviewItem(path, kind string, size int64) map[string]any {
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	var extension, length any
	if kind == "file" {
		extension, length = strings.TrimPrefix(ext, "."), size
	}
	return map[string]any{"storage": "local", "type": kind, "path": path, "name": name, "basename": strings.TrimSuffix(name, ext), "extension": extension, "size": length}
}

func (a *App) originalNativeTransferModuleInfo(ctx context.Context, file PlanFile, plan ImportPlan, failure error) (map[string]any, error) {
	item, err := originalLocalFileItem(file.Source)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"success": failure == nil, "fileitem": item, "target_diritem": nil, "target_item": nil, "transfer_type": originalTransferType(file.Method), "file_count": 0, "file_list": []any{}, "file_list_new": []any{}, "total_size": 0, "fail_list": []any{}, "message": nil, "need_scrape": false, "need_notify": true}
	if file.DirectoryDigest != "" {
		return a.originalNativeDirectoryModuleInfo(ctx, file, plan, out, failure)
	}
	if failure != nil {
		out["message"] = a.clean(failure.Error())
		out["fail_list"] = []string{file.Source}
		return map[string]any{plugins.ModuleTransferInfoKey: out}, nil
	}
	root, err := originalTransferMediaRoot(plan, file)
	if err != nil {
		return nil, err
	}
	target, err := originalLocalFileItem(file.Target)
	if err != nil {
		return nil, err
	}
	dir, err := originalLocalFileItem(root)
	if err != nil {
		return nil, err
	}
	out["target_diritem"], out["target_item"], out["file_count"], out["total_size"] = dir, target, 1, file.Size
	out["file_list"], out["file_list_new"], out["need_scrape"] = []string{file.Source}, []string{file.Target}, importBool(plan.Scraping, true)
	return map[string]any{plugins.ModuleTransferInfoKey: out}, nil
}

func originalTransferModuleInfo(raw json.RawMessage) (map[string]any, error) {
	if !originalModuleResultTruthy(raw) {
		return nil, bad("PLUGIN_TRANSFER_FAILED", "文件整理模块运行失败")
	}
	if _, tuple := plugins.DecodeModuleResultTuple(raw); tuple {
		return nil, bad("PLUGIN_TRANSFER_RESULT_INVALID", "整理模块没有返回有效的整理结果")
	}
	model, err := plugins.DecodeModuleTransferInfo(raw)
	if err != nil {
		return nil, bad("PLUGIN_TRANSFER_RESULT_INVALID", "整理模块返回的 TransferInfo 字段无效")
	}
	return model, nil
}

func (a *App) originalRecordedTransferModule(ctx context.Context, plan ImportPlan, file PlanFile) (map[string]any, bool, error) {
	var receipt importReceipt
	err := a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !receipt.OriginalModuleStarted {
		return nil, false, nil
	}
	if len(receipt.OriginalModuleResult) == 0 {
		return nil, true, nil
	}
	var raw json.RawMessage
	if err := a.Store.Unseal(receipt.OriginalModuleResult, &raw); err != nil {
		return nil, true, err
	}
	model, err := originalTransferModuleInfo(raw)
	if err != nil {
		return nil, true, nil
	} // Source never calls its TransferInfo callback.
	return model, true, nil
}
