package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Concrete existing file mutations retain the source StorageChain
// signature. The native frozen parent/subtree checks still precede the SDK;
// a truthy module value alone never proves a local operation took effect.
func (a *App) originalBrowserMutation(ctx context.Context, plan *browserOperation, item *browserOperationItem, parent *os.File, before func() error, managedHistory ...bool) (bool, error) {
	native := func() error {
		if plan.Action == "mkdir" {
			err := unix.Mkdirat(int(parent.Fd()), filepath.Base(item.Target), 0750)
			// LocalStorage.create_folder returns an existing directory too. The
			// host already established absence before invoking the plugin.
			if errors.Is(err, unix.EEXIST) && item.SourceModuleStarted {
				actual, e := browserStat(item.Target)
				if e == nil && os.FileMode(actual.Mode).IsDir() {
					return nil
				}
			}
			return err
		}
		if plan.Action == "delete" && item.SourceModuleStarted {
			if _, err := browserStat(item.Source); errors.Is(err, os.ErrNotExist) {
				item.Unrecoverable = true
				return nil
			}
		}
		return importRenameExclusive(int(parent.Fd()), filepath.Base(item.Source), filepath.Base(item.Target))
	}
	method := map[string]string{"mkdir": "create_folder", "rename": "rename_file", "delete": "delete_file"}[plan.Action]
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return false, err
	}
	if !originalModuleGroupsHaveMethod(groups, method) {
		err := native()
		if err != nil {
			return false, bad("FILE_OPERATION_FAILED", "文件操作未全部完成，已完成项目被保留，请刷新核对")
		}
		return true, nil
	}
	path := item.Source
	if plan.Action == "mkdir" {
		path = plan.Parent.Path
	}
	fileItemPath := path
	if item.OriginalFileItem != nil {
		var originalPath, originalType string
		_ = json.Unmarshal(item.OriginalFileItem.Fields["path"], &originalPath)
		_ = json.Unmarshal(item.OriginalFileItem.Fields["type"], &originalType)
		// LocalStorage's directory FileItem constructor appends '/'. Keep that
		// original wire value after independently authorizing the same folder.
		if originalType == "dir" && strings.TrimSuffix(originalPath, "/") == path {
			fileItemPath = originalPath
		}
	}
	fileitem, err := originalTransferSourceItem(originalTransferFileContext{Item: item.OriginalFileItem}, PlanFile{Source: fileItemPath})
	if err != nil {
		return false, err
	}
	encoded, _ := json.Marshal(fileitem)
	decoded, err := decodeSourceJSON(encoded)
	if err != nil {
		return false, err
	}
	if err = before(); err != nil {
		return false, err
	}
	kwargs := map[string]any{"fileitem": sourceMap(decoded)}
	if plan.Action != "delete" {
		kwargs["name"] = filepath.Base(item.Target)
	}
	raw, err := a.originalMetadataStorageGroups(ctx, method, kwargs, func() (any, error) {
		if e := native(); e != nil {
			// LocalStorage.rename/delete catch ordinary filesystem errors and
			// return False; only create_folder propagates to ChainBase.
			if plan.Action != "mkdir" {
				return false, nil
			}
			return nil, e
		}
		if plan.Action == "mkdir" {
			return originalLocalFileItem(item.Target)
		}
		return true, nil
	}, groups)
	// Recheck the same physical parent after an arbitrary installed module ran.
	// Its response cannot redirect the journal to another folder or subtree.
	currentParent, e := browserStat(plan.Parent.Path)
	if e != nil || !sameBrowserIdentity(plan.Parent, currentParent) {
		return false, bad("FILE_PARENT_CHANGED", "插件调用期间父目录已变化，请核对实际文件")
	}
	if root, e := a.browserRoot(plan.Root.ID, plan.Parent.Path); e != nil || root != plan.Root {
		return false, bad("FILE_ROOT_CHANGED", "插件调用期间允许目录设置已变化，请核对实际文件")
	}
	done := false
	if plan.Action == "mkdir" {
		actual, e := browserStat(item.Target)
		done = e == nil && os.FileMode(actual.Mode).IsDir()
	} else if _, e := browserStat(item.Source); os.IsNotExist(e) {
		// History removal already authorizes this single managed file, including
		// a hardlink. Verify its frozen identity after quarantine without running
		// the browser's destructive-operation admission policy a second time.
		var actual []browserStamp
		var e error
		if len(item.Manifest) == 1 && !os.FileMode(item.Manifest[0].Mode).IsDir() {
			var stamp browserStamp
			stamp, e = browserStat(item.Target)
			actual = []browserStamp{stamp}
		} else if len(managedHistory) > 0 && managedHistory[0] {
			actual, e = originalHistoryManifest(item.Target)
		} else {
			actual, e = browserManifest(ctx, item.Target)
		}
		done = e == nil && sameRebasedManifest(item.Manifest, actual, item.Source, item.Target)
		if plan.Action == "delete" && errors.Is(e, os.ErrNotExist) {
			done, item.Unrecoverable = true, true
		}
	}
	if err != nil {
		return done, err
	}
	if !originalModuleResultTruthy(raw) {
		return done, bad("PLUGIN_FILE_OPERATION_REJECTED", "存储插件未确认文件操作成功；已产生的文件变化保留，请刷新核对")
	}
	if !done {
		return false, bad("REVIEW_PLUGIN_FILE_OPERATION", "存储插件返回成功，但实际本地文件变化无法核对")
	}
	return true, nil
}
