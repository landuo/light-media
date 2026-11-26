package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins"
)

// The original directory provider asks the selected storage object for its
// children in provider order. Each file uses the existing recoverable native
// publisher; there is still only one module call, history and result callback.
type originalDirectoryMember struct {
	Target   string `json:"target"`
	FileItem []byte `json:"fileitem"`
}
type originalDirectorySourceItemKey struct{}
type originalDirectoryProviderException struct{ error }

func (e *originalDirectoryProviderException) Unwrap() error { return e.error }

func (r importReceipt) directoryTarget(relative string) string {
	if member, ok := r.DirectoryMembers[relative]; ok {
		return member.Target
	}
	return relative
}

func (a *App) walkOriginalDirectory(ctx context.Context, root string, visit func(string, string, *plugins.ModuleFileItem) error) error {
	seen := map[string]bool{}
	pair := originalTransferStorageFromContext(ctx)
	var walk func(string, string, *plugins.ModuleFileItem, int) error
	walk = func(path, target string, item *plugins.ModuleFileItem, depth int) error {
		if depth > 16 || len(seen) >= 20000 {
			return bad("FILE_SELECTION_LIMIT", "目录整理超过上限")
		}
		var rows []json.RawMessage
		if pair != nil && pair.Source != nil {
			raw, e := pair.Source.call(ctx, "list", item, "", nil, "")
			if e != nil {
				return &originalDirectoryProviderException{e}
			}
			var tuple bool
			rows, tuple = plugins.DecodeModuleResultTuple(raw)
			if !tuple && json.Unmarshal(raw, &rows) != nil {
				return bad("PLUGIN_STORAGE_INVALID", "目录存储对象未返回文件项列表")
			}
		} else {
			var fields map[string]any
			raw, _ := json.Marshal(item)
			_ = json.Unmarshal(raw, &fields)
			list, e := a.originalMetadataNativeList(ctx, fields)
			if e != nil {
				return &originalDirectoryProviderException{e}
			}
			for _, entry := range list {
				raw, e := json.Marshal(entry)
				if e != nil {
					return e
				}
				rows = append(rows, raw)
			}
		}
		for _, raw := range rows {
			entry, e := originalMetadataStorageItem(raw)
			if e != nil {
				return e
			}
			if entry == nil {
				return bad("PLUGIN_STORAGE_INVALID", "目录包含无效文件项")
			}
			child := strings.TrimSuffix(sourceText(entry["path"]), "/")
			if sourceText(entry["storage"]) != "local" || filepath.Dir(child) != path || seen[child] || !within(root, child) {
				return bad("INVALID_SOURCE", "目录存储对象返回越界或重复文件项")
			}
			seen[child] = true
			name := sourceText(entry["name"])
			if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsRune(name, '\x00') {
				return bad("INVALID_TARGET", "目录存储文件名不是安全的单个路径名称")
			}
			nextTarget := filepath.Join(target, name)
			member, e := plugins.NewModuleFileItem(entry)
			if e != nil {
				return e
			}
			if sourceText(entry["type"]) == "dir" {
				if e = walk(child, nextTarget, member, depth+1); e != nil {
					return e
				}
			} else {
				if _, e = snapshotPath(child); e != nil {
					return e
				}
				rel, e := filepath.Rel(root, child)
				if e != nil {
					return e
				}
				if e = visit(rel, nextTarget, member); e != nil {
					return e
				}
			}
		}
		return nil
	}
	item, err := originalLocalFileItem(root)
	if err != nil {
		return err
	}
	return walk(root, "", item, 0)
}

func (a *App) publishOriginalDirectory(ctx context.Context, key string, file PlanFile, check func() error, media ...string) (receipt importReceipt, returnErr error) {
	locked := planSnapshot(file)
	receipt = importReceipt{ID: key, Source: file.Source, Target: file.Target, SourceSnapshot: locked, Stage: ".light-media-" + tokenHash(key)[:32] + ".part", State: "planned"}
	if len(media) > 0 {
		receipt.MediaKey = media[0]
	}
	defer func() {
		var exception *originalDirectoryProviderException
		if errors.As(returnErr, &exception) && ctx.Err() == nil && !originalTransferInterrupted(returnErr) {
			receipt.DirectoryException = a.clean(returnErr.Error())
			if err := a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
				returnErr = err
			} else {
				// TransHandler.transfer_media catches storage exceptions and returns
				// an explicit failed TransferInfo; the queue does not retry it.
				returnErr = bad("PLUGIN_TRANSFER_FAILED", receipt.DirectoryException)
			}
		}
	}()
	var prior importReceipt
	err := a.Store.Get(ctx, "import_receipt", key, &prior)
	if err == nil {
		if prior.Source != file.Source || prior.Target != file.Target || prior.SourceSnapshot != locked {
			return receipt, bad("REVIEW_RECEIPT", "目录发布收据与计划不一致")
		}
		receipt = prior
		if receipt.MediaKey == "" && len(media) > 0 {
			receipt.MediaKey = media[0]
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	}
	receipt.DirectoryException = ""
	sharedRoot := receipt.SharedDirectory != nil
	if receipt.State == "planned" {
		if _, e := os.Lstat(file.Target); e == nil {
			sharedRoot = true
		}
	}
	if file.Method == "softlink" && !sharedRoot {
		if receipt.Softlink == nil || receipt.Softlink.Directory == nil {
			if receipt.State != "planned" {
				return receipt, bad("REVIEW_RECEIPT", "目录软链接发布证明缺失")
			}
			receipt.Softlink = &importSoftlinkProof{Source: file.Source, SourceSnapshot: locked, Directory: &importSoftlinkDirectoryProof{Directories: map[string]importSnapshot{}, Members: map[string]importSoftlinkDirectoryMember{}}}
		}
		if receipt.Softlink.Source != file.Source || receipt.Softlink.SourceSnapshot != locked {
			return receipt, bad("REVIEW_RECEIPT", "目录软链接来源证明不符")
		}
	} else if receipt.Softlink != nil {
		return receipt, bad("REVIEW_RECEIPT", "目录发布方式与软链接证明不符")
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if file.Method == "move" && receipt.State == "published" {
		return a.finishDirectoryMove(ctx, receipt, check)
	}
	if file.Method == "softlink" && receipt.State == "published" {
		if actual, e := snapshotOwnedTransferPath(file.Target, receipt.StageSnapshot, receipt.Softlink, true, receipt.SharedDirectory); e != nil || actual != receipt.StageSnapshot {
			return receipt, bad("REVIEW_TARGET_CHANGED", "已发布软链接目录发生变化")
		}
		return receipt, nil
	}
	if receipt.State == "published" && receipt.SharedDirectory != nil {
		if _, e := snapshotSharedDirectory(file.Target, receipt.StageSnapshot, receipt.SharedDirectory, true); e != nil {
			return receipt, e
		}
		return receipt, nil
	}
	if file.Method == "softlink" {
		if err = a.bindDirectoryStorageSoftlink(ctx, &receipt); err != nil {
			return receipt, err
		}
	}
	var entries []originalDirectoryEntry
	if receipt.DirectoryStorageMove != nil {
		if file.Method != "move" {
			return receipt, bad("REVIEW_MOVE_STORAGE", "目录移动收据与本次方式不同")
		}
		entries, err = directoryStorageMoveEntries(receipt)
		if err != nil {
			return receipt, err
		}
		if err = guardDirectoryStorageMoveRoot(receipt); err != nil {
			return receipt, err
		}
	} else {
		var actual importSnapshot
		actual, entries, err = snapshotOriginalDirectory(file.Source)
		if err != nil || actual.Device != locked.Device || actual.Inode != locked.Inode {
			return receipt, bad("REVIEW_SOURCE_CHANGED", "无法核对原盘来源，或原盘目录已被替换")
		}
		if actual != locked {
			return receipt, a.changedDiscSource("蓝光原盘内容在生成整理计划后发生变化", entries)
		}
		if pair := originalTransferStorageFromContext(ctx); file.Method == "move" && pair != nil && pair.Source != nil && receipt.State == "planned" {
			receipt.DirectoryStorageMove = &importDirectoryStorageMoveState{InstanceID: pair.Source.definition.InstanceID, Key: pair.Source.definition.Key, Entries: entries}
			if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
				return receipt, err
			}
		}
	}
	if receipt.State == "published" {
		actual, e := snapshotTransferPath(file.Target)
		if e != nil || actual != receipt.StageSnapshot {
			return receipt, bad("REVIEW_TARGET_CHANGED", "已发布目录发生变化")
		}
		return receipt, nil
	}
	if receipt.State == "planned" {
		if _, e := os.Lstat(file.Target); e == nil {
			if err = a.beginDirectoryMerge(ctx, &receipt, file); err != nil {
				return receipt, err
			}
		} else if !os.IsNotExist(e) {
			return receipt, e
		}
	}
	if receipt.State == "planned" {
		if _, e := os.Lstat(file.Target); !os.IsNotExist(e) {
			return receipt, bad("IMPORT_CONFLICT", "目标目录已存在且不属于本次发布")
		}
		receipt.DirectoryStreamSize, err = a.originalDirectoryStreamSize(ctx, file.Source)
		if err != nil {
			return receipt, &originalDirectoryProviderException{err}
		}
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
		if pair := originalTransferStorageFromContext(ctx); pair != nil && pair.Target != nil {
			raw, e := pair.Target.call(ctx, "get_folder", nil, file.Target, nil, "")
			if e != nil {
				return receipt, &originalDirectoryProviderException{e}
			}
			if !originalModuleResultTruthy(raw) {
				return receipt, bad("PLUGIN_STORAGE_FOLDER", "插件存储未取得目标目录")
			}
		}
		parent, e := importDir(filepath.Dir(file.Target), true)
		if e != nil {
			return receipt, e
		}
		e = unix.Mkdirat(int(parent.Fd()), filepath.Base(file.Target), 0750)
		if e != nil && !errors.Is(e, unix.EEXIST) {
			parent.Close()
			return receipt, e
		}
		if e = parent.Sync(); e != nil {
			parent.Close()
			return receipt, e
		}
		parent.Close()
		stamp, targetEntries, e := snapshotOriginalDirectory(file.Target)
		if e != nil || len(targetEntries) != 0 {
			return receipt, bad("REVIEW_TARGET_CHANGED", "新建目录含未知文件，拒绝认领")
		}
		receipt.StageSnapshot = stamp
		if receipt.Softlink != nil {
			receipt.Softlink.Directory.Root = softlinkDirectoryIdentity(stamp)
		}
		receipt.State = "directory_created"
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
	}
	if receipt.State != "directory_created" {
		return receipt, bad("REVIEW_RECEIPT", "目录发布状态无法核对")
	}
	if err = a.guardOriginalDirectoryTarget(ctx, receipt); err != nil {
		return receipt, err
	}
	if err = a.originalTransferInterceptWrite(ctx, file.Source, file.Target, file.Method, false); err != nil {
		return receipt, err
	}
	if receipt.DirectoryStorageMove != nil && !receipt.DirectoryListed {
		if err = a.freezeDirectoryStorageMoveMembers(ctx, &receipt); err != nil {
			return receipt, err
		}
	}
	stamps := map[string]importSnapshot{}
	for _, entry := range entries {
		if !entry.Directory {
			stamps[entry.Path] = entry.Snapshot
		}
	}
	publishMember := func(relative string) error {
		stamp, ok := stamps[relative]
		if !ok {
			return bad("REVIEW_SOURCE_CHANGED", "目录存储文件不属于冻结源目录")
		}
		if err = check(); err != nil {
			return err
		}
		if err = a.guardOriginalDirectoryTarget(ctx, receipt); err != nil {
			return err
		}
		childCheck := func() error {
			if err := check(); err != nil {
				return err
			}
			if receipt.DirectoryStorageMove != nil {
				return guardDirectoryStorageMoveRoot(receipt)
			}
			current, members, e := snapshotOriginalDirectory(file.Source)
			if e != nil || current.Device != locked.Device || current.Inode != locked.Inode {
				return bad("REVIEW_SOURCE_CHANGED", "整理期间无法核对原盘来源，或原盘目录已被替换")
			}
			if current != locked {
				return a.changedDiscSource("整理期间蓝光原盘内容发生变化", members)
			}
			return nil
		}
		if file.Method == "softlink" && receipt.SharedDirectory == nil {
			if err = a.prepareSoftlinkDirectoryParents(ctx, &receipt, receipt.directoryTarget(relative)); err != nil {
				return err
			}
		}
		childKey := key + "/directory/" + tokenHash(relative)
		memberContext := ctx
		if member, ok := receipt.DirectoryMembers[relative]; ok {
			var item plugins.ModuleFileItem
			if err := a.Store.Unseal(member.FileItem, &item); err != nil {
				return err
			}
			memberContext = context.WithValue(ctx, originalDirectorySourceItemKey{}, &item)
		}
		if file.Method == "move" {
			memberContext = context.WithValue(memberContext, importMoveDeferredKey{}, true)
			if receipt.DirectoryStorageMove != nil {
				memberContext = context.WithValue(memberContext, importDirectoryStorageMoveKey{}, receipt.DirectoryStorageMove)
			}
		}
		if receipt.DirectoryMerge != nil {
			err = a.publishDirectoryMergeMember(memberContext, &receipt, relative, stamp, file.Method, childCheck)
		} else {
			_, err = a.publishImport(memberContext, childKey, filepath.Join(file.Source, relative), filepath.Join(file.Target, receipt.directoryTarget(relative)), stamp, file.Method, childCheck)
		}
		if err != nil {
			return err
		}
		if !slices.Contains(receipt.DirectoryCompleted, relative) {
			receipt.DirectoryCompleted = append(receipt.DirectoryCompleted, relative)
		}
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return err
		}
		return nil
	}
	if receipt.DirectoryListed {
		for _, relative := range receipt.DirectoryFiles {
			if err = publishMember(relative); err != nil {
				return receipt, err
			}
		}
	} else {
		position := 0
		err = a.walkOriginalDirectory(ctx, file.Source, func(relative, target string, item *plugins.ModuleFileItem) error {
			if position < len(receipt.DirectoryFiles) {
				if receipt.DirectoryFiles[position] != relative || receipt.directoryTarget(relative) != target {
					return bad("REVIEW_SOURCE_CHANGED", "恢复时目录存储顺序已变化")
				}
			} else {
				sealed, err := a.Store.Seal(item)
				if err != nil {
					return err
				}
				if receipt.DirectoryMembers == nil {
					receipt.DirectoryMembers = map[string]originalDirectoryMember{}
				}
				receipt.DirectoryMembers[relative] = originalDirectoryMember{Target: target, FileItem: sealed}
				receipt.DirectoryFiles = append(receipt.DirectoryFiles, relative)
				if err := a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
					return err
				}
			}
			position++
			return publishMember(relative)
		})
		if err != nil {
			return receipt, err
		}
		if position != len(receipt.DirectoryFiles) {
			return receipt, bad("REVIEW_SOURCE_CHANGED", "恢复时目录存储遗漏已冻结成员")
		}
		receipt.DirectoryListed = true
		if err := a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if err = a.guardOriginalDirectoryTarget(ctx, receipt); err != nil {
		return receipt, err
	}
	if receipt.DirectoryStorageMove != nil {
		if err = guardDirectoryStorageMoveRoot(receipt); err != nil {
			return receipt, err
		}
	} else if actual, e := snapshotTransferPath(file.Source); e != nil || actual != locked {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "目录发布期间源内容发生变化")
	}
	if receipt.SharedDirectory != nil {
		receipt.StageSnapshot, err = snapshotSharedDirectory(file.Target, receipt.SharedDirectory.Root, receipt.SharedDirectory, true)
	} else if file.Method == "softlink" {
		err = a.finishSoftlinkDirectoryProof(ctx, &receipt)
	} else {
		receipt.StageSnapshot, err = snapshotTransferPath(file.Target)
	}
	if err != nil {
		return receipt, err
	}
	receipt.State = "published"
	if file.Method == "move" {
		receipt.DirectoryMoveState = "cleaning"
	}
	err = a.Store.Put(ctx, "import_receipt", key, receipt)
	if err == nil && file.Method == "move" {
		return a.finishDirectoryMove(ctx, receipt, check)
	}
	return receipt, err
}

func (a *App) guardOriginalDirectoryTarget(ctx context.Context, receipt importReceipt) error {
	if receipt.DirectoryMerge != nil {
		return a.guardDirectoryMerge(ctx, receipt)
	}
	if receipt.Softlink != nil && receipt.Softlink.Directory != nil {
		return a.guardSoftlinkDirectoryTarget(ctx, receipt)
	}
	stamp, entries, err := snapshotOriginalDirectory(receipt.Target)
	if err != nil || !sameInode(stamp, receipt.StageSnapshot) {
		return bad("REVIEW_TARGET_CHANGED", "目录发布目标已被替换")
	}
	allowed := map[string]importSnapshot{}
	dirs := map[string]bool{}
	originals := map[string]importSnapshot{}
	if receipt.DirectoryStorageMove != nil {
		entries, err := directoryStorageMoveEntries(receipt)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.Directory {
				originals[entry.Path] = entry.Snapshot
			}
		}
	}
	for _, relative := range receipt.DirectoryFiles {
		target := receipt.directoryTarget(relative)
		for parent := filepath.Dir(target); parent != "."; parent = filepath.Dir(parent) {
			dirs[parent] = true
		}
		var child importReceipt
		err := a.Store.Get(ctx, "import_receipt", receipt.ID+"/directory/"+tokenHash(relative), &child)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if child.Source != filepath.Join(receipt.Source, relative) || child.Target != filepath.Join(receipt.Target, target) {
			return bad("REVIEW_RECEIPT", "目录成员收据不一致")
		}
		if child.State == "planned" && child.StoragePublication != nil && len(child.StoragePublication.Result) > 0 {
			if child.ID != receipt.ID+"/directory/"+tokenHash(relative) {
				return bad("REVIEW_RECEIPT", "目录存储成员收据身份不同")
			}
			stage, e := a.verifyStoragePublicationStage(ctx, child, ctx.Err)
			if e != nil {
				return e
			}
			allowed[filepath.Join(filepath.Dir(target), child.Stage)] = stage
		}
		if child.State == "planned" && child.StorageMove != nil && len(child.StorageMove.Result) > 0 && receipt.DirectoryStorageMove != nil {
			proof := receipt.DirectoryStorageMove
			if child.ID != receipt.ID+"/directory/"+tokenHash(relative) || child.SourceSnapshot != originals[relative] || child.StorageMove.InstanceID != proof.InstanceID || child.StorageMove.Key != proof.Key {
				return bad("REVIEW_MOVE_STORAGE", "目录暂存移动与冻结成员或存储对象不同")
			}
			stage, err := a.verifyStorageMoveStage(ctx, child, ctx.Err)
			if err != nil {
				return err
			}
			allowed[filepath.Join(filepath.Dir(target), child.Stage)] = stage
		}
		if child.State == "published" || child.State == "staged" {
			allowed[target] = child.StageSnapshot
		}
		if child.StageSnapshot.Inode != 0 {
			allowed[filepath.Join(filepath.Dir(target), child.Stage)] = child.StageSnapshot
		}
	}
	for _, entry := range entries {
		if entry.Directory {
			if !dirs[entry.Path] {
				return bad("REVIEW_TARGET_CHANGED", "目标含未知子目录")
			}
			continue
		}
		owned, ok := allowed[entry.Path]
		if !ok || !sameInode(owned, entry.Snapshot) || owned != entry.Snapshot && !strings.HasPrefix(filepath.Base(entry.Path), ".light-media-") {
			return bad("REVIEW_TARGET_CHANGED", "目标含未知或已变化的文件")
		}
	}
	return nil
}
