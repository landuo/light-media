package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/fileextensions"
	"light-media/internal/plugins"

	"golang.org/x/sys/unix"
)

// The ordinary StorageChain caller checks parents after deleting the selected
// file. The private, sealed record also remembers completed directory module
// invocations; a cold restart never blindly repeats a storage write.
type historyParentCleanup struct {
	Original, Root    string
	Current           json.RawMessage
	Extensions        []string
	Operation         *browserOperation
	NativeStarted     bool
	SelfDeleteStarted bool
	SelfDeleteDone    bool
	Completed         bool
}

func (a *App) completeHistoricalParentCleanup(ctx context.Context, item transferHistoryItem, source bool) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var file assetRemovalFile
	key := item.executionFileID() + "/target"
	if source {
		var receipt historySourceRemoval
		err := a.Store.Get(ctx, "history_source_removal", item.executionFileID(), &receipt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		file, key = receipt.File, item.executionFileID()+"/source"
	} else {
		var receipt historyTargetRemoval
		err := a.Store.Get(ctx, "history_target_removal", item.executionFileID(), &receipt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		file = receipt.File
	}
	if !file.Deleted {
		return nil
	}
	var initial *plugins.ModuleFileItem
	if file.SourceOperation != "" {
		var operation browserOperation
		sealed, err := base64.StdEncoding.DecodeString(file.SourceOperation)
		if err != nil || a.Store.Unseal(sealed, &operation) != nil || len(operation.Items) != 1 || operation.Items[0].Source != file.Path {
			return bad("REVIEW_HISTORY_DELETE", "父目录清理缺少可核验的原文件项")
		}
		if operation.ModuleError != nil {
			return operation.ModuleError
		}
		initial = operation.Items[0].OriginalFileItem
	}
	if initial == nil {
		var err error
		initial, err = plugins.NewModuleFileItem(map[string]any{"path": file.Path, "storage": "local", "type": "file"})
		if err != nil {
			return err
		}
	}
	raw, err := json.Marshal(initial)
	if err != nil {
		return err
	}
	release, err := a.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	return a.originalHistoryCleanupParents(ctx, key, file.Path, raw, file.DirectoryParentOnly)
}

func (a *App) historyDeleteExtensions(ctx context.Context) ([]string, error) {
	snapshot, err := a.sourceFileExtensions(ctx)
	if err != nil {
		return nil, err
	}
	// Reuse Settings' exact list conversion, with the pinned DOWNLOAD_TMPEXT
	// default. It is independent from TransferChain's three cached lists.
	raw := json.RawMessage(`[".!qb",".part"]`)
	if len(a.Settings().DownloadTmpExt) > 0 {
		raw = a.Settings().DownloadTmpExt
	}
	if value, present := os.LookupEnv("DOWNLOAD_TMPEXT"); present {
		raw = json.RawMessage(value)
	}
	temporary, err := fileextensions.ResolveSettingsList(raw, json.RawMessage(`[".!qb",".part"]`))
	if err != nil {
		return nil, err
	}
	out := snapshot.Values(fileextensions.MediaKey)
	out = append(out, temporary...)
	out = append(out, snapshot.Values(fileextensions.SubtitleKey)...)
	return append(out, snapshot.Values(fileextensions.AudioKey)...), nil
}

// FileManager.any_files calls its concrete storage list recursively. It does
// not dispatch the high-level list_files module while walking descendants.
func (a *App) originalHistoryNativeAnyFiles(ctx context.Context, item map[string]any, extensions []string) (bool, error) {
	entries, err := a.originalMetadataNativeList(ctx, item)
	if err != nil {
		return false, err
	}
	if len(extensions) == 0 {
		return len(entries) != 0, nil
	}
	for _, entry := range entries {
		if sourceText(entry["type"]) == "dir" {
			found, err := a.originalHistoryNativeAnyFiles(ctx, entry, extensions)
			if err != nil || found {
				return found, err
			}
		} else if extension := sourceText(entry["extension"]); extension != "" {
			for _, allowed := range extensions {
				if "."+strings.ToLower(extension) == allowed {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (a *App) originalHistoryCleanupParents(ctx context.Context, key, original string, initial json.RawMessage, parentOnly ...bool) error {
	var state historyParentCleanup
	var sealed []byte
	err := a.Store.Get(ctx, "history_parent_cleanup", key, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		state.Original = original
		for _, root := range a.browserRoots() {
			if within(root.Path, original) && len(root.Path) > len(state.Root) {
				state.Root = root.Path
			}
		}
		if state.Root == "" {
			return bad("FILE_ROOT_REJECTED", "历史删除路径不在当前允许目录内")
		}
		state.Extensions, err = a.historyDeleteExtensions(ctx)
		if err != nil {
			return err
		}
		fields, e := originalMetadataStorageItem(initial)
		if e != nil {
			return e
		}
		if sourceText(fields["type"]) == "dir" && !(len(parentOnly) > 0 && parentOnly[0]) {
			state.Current = initial
		} else {
			state.Current, err = a.originalMetadataStorage(ctx, "get_parent_item", map[string]any{"fileitem": initial}, func() (any, error) {
				return a.originalMetadataLocalItem("local", filepath.Dir(original))
			})
		}
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if err := a.Store.Unseal(sealed, &state); err != nil || state.Original != original {
		return bad("REVIEW_HISTORY_DELETE", "父目录清理收据不可读取或归属不一致")
	}
	if state.Completed {
		return nil
	}
	if state.Operation != nil && state.Operation.ModuleError != nil && state.Operation.ModuleError.Code == "PLUGIN_FILE_OPERATION_REJECTED" && !state.Operation.Items[0].Done {
		// A later explicit request may retry a known false/no-effect call.
		state.Operation, state.NativeStarted = nil, false
	}
	save := func() error {
		sealed, err := a.Store.Seal(state)
		if err != nil {
			return err
		}
		return a.Store.Put(ctx, "history_parent_cleanup", key, sealed)
	}
	if err := save(); err != nil {
		return err
	}
	for !state.Completed {
		rootPresent := false
		for _, root := range a.browserRoots() {
			rootPresent = rootPresent || root.Path == state.Root
		}
		if !rootPresent {
			return bad("FILE_ROOT_CHANGED", "父目录清理期间允许目录设置已变化")
		}
		item, err := originalMetadataStorageItem(state.Current)
		if err != nil {
			return err
		}
		path := strings.TrimSuffix(sourceText(item["path"]), "/")
		if item == nil || path == "" || path == state.Root || within(path, state.Root) || len(strings.Split(strings.Trim(path, "/"), "/")) <= 1 {
			state.Completed = true
			return save()
		}
		if !importPath(path) || sourceText(item["storage"]) != "local" || sourceText(item["type"]) != "dir" || !within(state.Root, path) || !(within(path, original) || path == original) {
			return bad("REVIEW_HISTORY_DELETE", "存储插件返回的父目录与已确认文件不一致")
		}
		if state.Operation == nil {
			raw, err := a.originalMetadataStorage(ctx, "any_files", map[string]any{"fileitem": state.Current, "extensions": state.Extensions}, func() (any, error) {
				return a.originalHistoryNativeAnyFiles(ctx, item, state.Extensions)
			})
			if err != nil {
				return err
			}
			// Python's `is not False` stops even for 0, [], None or a tuple.
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
				state.Completed = true
				return save()
			}
			// StorageChain starts a directory cleanup at the directory itself,
			// even after deleting its Blu-ray tree. Preserve the ordinary
			// any_files -> delete_file -> get_parent_item calls for that now
			// missing path without authorizing a newly substituted directory.
			if path == original {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					return bad("FILE_SNAPSHOT_CHANGED", "已删除原盘目录被重新创建，未继续清理")
				}
				if !state.SelfDeleteDone {
					if state.SelfDeleteStarted {
						return bad("REVIEW_HISTORY_DELETE", "原盘目录清理扩展结果尚未确认，未重复调用")
					}
					state.SelfDeleteStarted = true
					if err := save(); err != nil {
						return err
					}
					result, err := a.originalMetadataStorage(ctx, "delete_file", map[string]any{"fileitem": state.Current}, func() (any, error) { return true, nil })
					if err != nil {
						return err
					}
					if !originalModuleResultTruthy(result) {
						return bad("PLUGIN_FILE_OPERATION_REJECTED", "存储插件未确认原盘目录清理")
					}
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						return bad("FILE_SNAPSHOT_CHANGED", "原盘目录清理扩展创建了新路径，未继续清理")
					}
					state.SelfDeleteDone = true
					if err := save(); err != nil {
						return err
					}
				}
				state.Current, err = a.originalMetadataStorage(ctx, "get_parent_item", map[string]any{"fileitem": state.Current}, func() (any, error) { return a.originalMetadataLocalItem("local", filepath.Dir(path)) })
				if err != nil {
					return err
				}
				if err := save(); err != nil {
					return err
				}
				continue
			}
			if err := a.guardBrowserDownloaderSources(ctx, []string{path}); err != nil {
				return err
			}
			manifest, err := browserManifest(ctx, path)
			if err != nil {
				return err
			}
			parent, err := browserStat(filepath.Dir(path))
			if err != nil {
				return err
			}
			var root browserRoot
			for _, candidate := range a.browserRoots() {
				if candidate.Path == state.Root {
					root = candidate
				}
			}
			if root.ID == "" {
				return bad("FILE_ROOT_CHANGED", "父目录清理期间允许目录设置已变化")
			}
			frozen, err := originalTransferFrozenItem(item)
			if err != nil {
				return err
			}
			state.Operation = &browserOperation{ID: id("historyparent_"), Root: root, Action: "delete", Parent: parent, State: "executing", Items: []browserOperationItem{{Source: path, Target: filepath.Join(parent.Path, ".light-media-remove-"+tokenHash(key + path)[:32]+".part"), Manifest: manifest, OriginalFileItem: frozen}}}
			if err := save(); err != nil {
				return err
			}
		}
		op := state.Operation
		selected := &op.Items[0]
		parent, err := importDir(op.Parent.Path, false)
		if err != nil {
			return err
		}
		if !selected.Done {
			if !selected.SourceModuleStarted {
				if _, err := browserStat(selected.Source); errors.Is(err, os.ErrNotExist) {
					moved, err := browserManifest(ctx, selected.Target)
					if err == nil && sameRebasedManifest(selected.Manifest, moved, selected.Source, selected.Target) {
						selected.Done = true
						if err := save(); err != nil {
							parent.Close()
							return err
						}
					}
				}
			}
		}
		if !selected.Done {
			if selected.SourceModuleStarted {
				parent.Close()
				return bad("REVIEW_HISTORY_DELETE", "父目录删除扩展已调用，结果尚未确认，未重复调用")
			}
			actual, inspectErr := browserManifest(ctx, path)
			if inspectErr != nil || !sameBrowserManifest(selected.Manifest, actual) {
				parent.Close()
				return bad("FILE_SNAPSHOT_CHANGED", "清理前父目录内容已变化")
			}
			op.ModuleError = nil
			done, callErr := a.originalBrowserMutation(ctx, op, selected, parent, func() error { selected.SourceModuleStarted = true; return save() })
			selected.Done = done
			if callErr != nil {
				var api *APIError
				if errors.As(callErr, &api) {
					op.ModuleError = api
				}
			}
			if err := save(); err != nil {
				parent.Close()
				return err
			}
			if callErr != nil {
				parent.Close()
				return callErr
			}
		}
		if op.ModuleError != nil {
			parent.Close()
			return op.ModuleError
		}
		if !selected.Unrecoverable {
			state.NativeStarted = true
			if err := save(); err != nil {
				parent.Close()
				return err
			}
			// The same-parent atomic rename froze this exact, symlink-free
			// subtree. Remove children first with descriptor-relative operations;
			// unexpected new files leave a nonempty directory and stop cleanup.
			for index := len(selected.Manifest) - 1; index >= 0; index-- {
				expected := selected.Manifest[index]
				relative, _ := filepath.Rel(selected.Source, expected.Path)
				moved := filepath.Join(selected.Target, relative)
				actual, err := browserStat(moved)
				if errors.Is(err, os.ErrNotExist) && state.NativeStarted {
					continue
				}
				isDir := os.FileMode(expected.Mode).IsDir()
				if err != nil || !sameBrowserIdentity(expected, actual) || !isDir && (actual.Size != expected.Size || actual.ModNS != expected.ModNS) {
					parent.Close()
					return bad("FILE_SNAPSHOT_CHANGED", "暂存目录内容已变化，未继续清理")
				}
				dir, err := importDir(filepath.Dir(moved), false)
				if err != nil {
					parent.Close()
					return err
				}
				flags := 0
				if isDir {
					flags = unix.AT_REMOVEDIR
				}
				err = unix.Unlinkat(int(dir.Fd()), filepath.Base(moved), flags)
				if err == nil {
					err = dir.Sync()
				}
				dir.Close()
				if err != nil {
					parent.Close()
					return err
				}
			}
		}
		if err := parent.Sync(); err != nil {
			parent.Close()
			return err
		}
		parent.Close()
		state.Current, err = a.originalMetadataStorage(ctx, "get_parent_item", map[string]any{"fileitem": state.Current}, func() (any, error) {
			return a.originalMetadataLocalItem("local", filepath.Dir(path))
		})
		if err != nil {
			return err
		}
		state.Operation, state.NativeStarted = nil, false
		if err := save(); err != nil {
			return err
		}
	}
	return nil
}
