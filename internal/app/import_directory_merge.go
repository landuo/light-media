package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

// Observed preserves existing unrelated entries during publication; none of
// these entries becomes ownership just because it was visible in the root.
type importDirectoryMergeState struct {
	Observed []originalDirectoryEntry              `json:"observed"`
	Children map[string]*importDirectoryMergeChild `json:"children"`
}
type importDirectoryMergeChild struct {
	Replaced  *managedFile    `json:"replaced,omitempty"`
	Source    string          `json:"source"`
	Before    *importSnapshot `json:"before,omitempty"`
	Parent    importSnapshot  `json:"parent"`
	Backup    string          `json:"backup"`
	Published bool            `json:"published,omitempty"`
	Cleaned   bool            `json:"cleaned,omitempty"`
}

func (a *App) beginDirectoryMerge(ctx context.Context, receipt *importReceipt, file PlanFile) error {
	// Existing unmanaged ordinary roots admit the dedicated leaf commands,
	// never whole-root ownership or a transfer from another managed asset.
	if file.Method != "copy" && file.Method != "hardlink" && file.Method != "move" && file.Method != "softlink" {
		return bad("DIRECTORY_MERGE_METHOD", "目录合并方式无效")
	}
	if within(file.Source, file.Target) || within(file.Target, file.Source) {
		return bad("INVALID_TARGET", "目录合并源与目标不可互相包含")
	}
	var owner managedFile
	if err := a.Store.Get(ctx, "managed_file", tokenHash(file.Target), &owner); err == nil {
		if err = a.admitOwnedDirectoryAppend(ctx, receipt, file, owner); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	actual, entries, err := snapshotOriginalDirectory(file.Target)
	if err != nil {
		return err
	}
	if receipt.DirectoryMerge == nil {
		receipt.SharedDirectory = &importSharedDirectoryProof{Root: sharedDirectoryIdentity(actual), Directories: map[string]importSnapshot{}, CreatedDirectories: map[string]bool{}, Members: map[string]importSharedDirectoryMember{}}
		receipt.DirectoryMerge = &importDirectoryMergeState{Observed: entries, Children: map[string]*importDirectoryMergeChild{}}
		receipt.DirectoryStreamSize, err = a.originalDirectoryStreamSize(ctx, file.Source)
		if err != nil {
			return err
		}
	} else if err = a.guardDirectoryMerge(ctx, *receipt); err != nil {
		return err
	}
	// Record the pre-call observation before allowing even target get_folder.
	if err = a.Store.Put(ctx, "import_receipt", receipt.ID, *receipt); err != nil {
		return err
	}
	if pair := originalTransferStorageFromContext(ctx); pair != nil && pair.Target != nil {
		raw, e := pair.Target.call(ctx, "get_folder", nil, file.Target, nil, "")
		if e != nil {
			return &originalDirectoryProviderException{e}
		}
		if !originalModuleResultTruthy(raw) {
			return bad("PLUGIN_STORAGE_FOLDER", "插件存储未取得已有目标目录")
		}
	}
	receipt.State = "directory_created"
	receipt.StageSnapshot = sharedDirectoryIdentity(actual)
	if err = a.Store.Put(ctx, "import_receipt", receipt.ID, *receipt); err != nil {
		return err
	}
	return a.guardDirectoryMerge(ctx, *receipt)
}

func mergeObserved(receipt importReceipt) map[string]originalDirectoryEntry {
	out := map[string]originalDirectoryEntry{}
	if receipt.DirectoryMerge != nil {
		for _, entry := range receipt.DirectoryMerge.Observed {
			out[entry.Path] = entry
		}
	}
	return out
}

func (a *App) prepareDirectoryMergeParents(ctx context.Context, receipt *importReceipt, relative string) error {
	if receipt.SharedDirectory == nil || !softlinkDirectoryRelative(relative) {
		return bad("REVIEW_DIRECTORY_MERGE", "目录合并成员路径无效")
	}
	observed := mergeObserved(*receipt)
	var parents []string
	for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
		parents = append(parents, parent)
	}
	for i := len(parents) - 1; i >= 0; i-- {
		relative := parents[i]
		path := filepath.Join(receipt.Target, relative)
		prior, known := receipt.SharedDirectory.Directories[relative]
		old, existed := observed[relative]
		if existed && !old.Directory {
			return bad("IMPORT_CONFLICT", "合并父路径已由普通文件占用")
		}
		if !known && !existed {
			parent, err := importDir(filepath.Dir(path), false)
			if err != nil {
				return err
			}
			err = unix.Mkdirat(int(parent.Fd()), filepath.Base(path), 0750)
			if err == nil {
				err = parent.Sync()
			}
			parent.Close()
			if err != nil {
				return err
			}
		}
		dir, err := importDir(path, false)
		if err != nil {
			return err
		}
		info, err := dir.Stat()
		dir.Close()
		if err != nil {
			return err
		}
		stamp, err := originalDirectoryIdentity(info)
		if err != nil {
			return err
		}
		if known && !sameInode(stamp, prior) || existed && !sameInode(stamp, old.Snapshot) {
			return bad("REVIEW_DIRECTORY_MERGE", "合并父目录已变化")
		}
		if !known {
			receipt.SharedDirectory.Directories[relative] = sharedDirectoryIdentity(stamp)
			if !existed {
				receipt.SharedDirectory.CreatedDirectories[relative] = true
			}
			if err = a.Store.Put(ctx, "import_receipt", receipt.ID, *receipt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) publishDirectoryMergeMember(ctx context.Context, receipt *importReceipt, relative string, stamp importSnapshot, method string, check func() error) error {
	targetRelative := receipt.directoryTarget(relative)
	if err := a.prepareDirectoryMergeParents(ctx, receipt, targetRelative); err != nil {
		return err
	}
	childKey := receipt.ID + "/directory/" + tokenHash(relative)
	target := filepath.Join(receipt.Target, targetRelative)
	child := receipt.DirectoryMerge.Children[targetRelative]
	if child == nil {
		var own managedFile
		var replaced *managedFile
		if err := a.Store.Get(ctx, "managed_file", tokenHash(target), &own); err == nil {
			// MoviePilot rewrites an existing destination file during a directory
			// transfer, including when a different earlier transfer produced it.
			// Require the exact registered snapshot before taking the leaf over.
			before, exists := mergeObserved(*receipt)[targetRelative]
			if !exists || before.Directory || before.Snapshot != own.Snapshot {
				return bad("REVIEW_DIRECTORY_MERGE", "待替换受管成员与登记快照不一致")
			}
			if own.AssetID != receipt.AppendAssetID {
				value := own
				replaced = &value
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		parent, err := importDir(filepath.Dir(target), false)
		if err != nil {
			return err
		}
		info, err := parent.Stat()
		parent.Close()
		if err != nil {
			return err
		}
		identity, err := originalDirectoryIdentity(info)
		if err != nil {
			return err
		}
		child = &importDirectoryMergeChild{Replaced: replaced, Source: relative, Parent: sharedDirectoryIdentity(identity), Backup: ".light-media-merge-" + tokenHash(childKey)[:32] + ".old"}
		if before, exists := mergeObserved(*receipt)[targetRelative]; exists {
			if before.Directory {
				return bad("IMPORT_CONFLICT", "合并成员目的已由目录占用")
			}
			frozen := before.Snapshot
			child.Before = &frozen
		} else if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			return bad("IMPORT_CONFLICT", "合并成员出现未授权目标")
		}
		receipt.DirectoryMerge.Children[targetRelative] = child
		if err = a.Store.Put(ctx, "import_receipt", receipt.ID, *receipt); err != nil {
			return err
		}
	}
	if child.Source != relative {
		return bad("IMPORT_CONFLICT", "目录存储映射到重复目标")
	}
	parent, err := importDir(filepath.Dir(target), false)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Stat()
	if err != nil {
		return err
	}
	identity, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(identity, child.Parent) {
		return bad("REVIEW_DIRECTORY_MERGE", "合并成员父目录已被替换")
	}
	if method == "softlink" && child.Before != nil {
		return bad("IMPORT_CONFLICT", "目录软链接目标已占用，原叶项保留")
	}
	if child.Before != nil && !child.Published {
		backup := filepath.Join(filepath.Dir(target), child.Backup)
		prior, e := snapshotPath(backup)
		if errors.Is(e, os.ErrNotExist) {
			if e = check(); e != nil {
				return e
			}
			current, e := snapshotPath(target)
			if e != nil || current != *child.Before {
				return bad("REVIEW_DIRECTORY_MERGE", "待替换叶项已变化")
			}
			if e = importRenameExclusive(int(parent.Fd()), filepath.Base(target), child.Backup); e != nil {
				return e
			}
			if e = parent.Sync(); e != nil {
				return e
			}
			prior, e = snapshotPath(backup)
		}
		if e != nil || prior != *child.Before {
			return bad("REVIEW_DIRECTORY_MERGE", "合并备份条目已变化")
		}
	}
	published, err := a.publishImport(ctx, childKey, filepath.Join(receipt.Source, relative), target, stamp, method, check)
	if err != nil {
		return err
	}
	child.Published = true
	receipt.SharedDirectory.Members[targetRelative] = importSharedDirectoryMember{Snapshot: published.StageSnapshot, Softlink: published.Softlink}
	return a.Store.Put(ctx, "import_receipt", receipt.ID, *receipt)
}

func (a *App) guardDirectoryMerge(ctx context.Context, receipt importReceipt) error {
	if receipt.DirectoryMerge == nil || receipt.SharedDirectory == nil {
		return bad("REVIEW_DIRECTORY_MERGE", "目录合并观察与成员许可缺失")
	}
	actual, entries, links, err := scanSoftlinkDirectory(receipt.Target)
	if err != nil || !sameInode(actual, receipt.SharedDirectory.Root) {
		return bad("REVIEW_DIRECTORY_MERGE", "合并目标根已变化")
	}
	allowed := map[string]importSnapshot{}
	allowedLinks := map[string]*importSoftlinkProof{}
	dirs := map[string]importSnapshot{}
	for _, entry := range receipt.DirectoryMerge.Observed {
		if entry.Directory {
			dirs[entry.Path] = entry.Snapshot
		} else {
			allowed[entry.Path] = entry.Snapshot
		}
	}
	for relative, stamp := range receipt.SharedDirectory.Directories {
		dirs[relative] = stamp
	}
	for target, permit := range receipt.DirectoryMerge.Children {
		if !softlinkDirectoryRelative(target) || !softlinkDirectoryRelative(permit.Source) {
			return bad("REVIEW_DIRECTORY_MERGE", "合并成员许可路径无效")
		}
		key := receipt.ID + "/directory/" + tokenHash(permit.Source)
		if permit.Before != nil && !permit.Cleaned {
			allowed[filepath.Join(filepath.Dir(target), permit.Backup)] = *permit.Before
		}
		var child importReceipt
		err = a.Store.Get(ctx, "import_receipt", key, &child)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if child.ID != key || child.Source != filepath.Join(receipt.Source, permit.Source) || child.Target != filepath.Join(receipt.Target, target) {
			return bad("REVIEW_DIRECTORY_MERGE", "合并发布子收据路径不符")
		}
		if child.State == "planned" && child.StoragePublication != nil && len(child.StoragePublication.Result) > 0 {
			stage, e := a.verifyStoragePublicationStage(ctx, child, ctx.Err)
			if e != nil {
				return e
			}
			allowed[filepath.Join(filepath.Dir(target), child.Stage)] = stage
		}
		if child.State == "planned" && child.StorageSoftlink != nil && len(child.StorageSoftlink.Result) > 0 {
			stage, e := a.verifyStorageSoftlinkStage(ctx, child)
			if e != nil {
				return e
			}
			child.StageSnapshot = stage
		}
		if child.State == "planned" && child.StorageMove != nil && len(child.StorageMove.Result) > 0 {
			proof := receipt.DirectoryStorageMove
			if proof == nil || child.StorageMove.InstanceID != proof.InstanceID || child.StorageMove.Key != proof.Key {
				return bad("REVIEW_MOVE_STORAGE", "共享目录存储移动身份不同")
			}
			sourceEntries, e := directoryStorageMoveEntries(receipt)
			if e != nil {
				return e
			}
			matched := false
			for _, source := range sourceEntries {
				if source.Path == permit.Source && !source.Directory && source.Snapshot == child.SourceSnapshot {
					matched = true
				}
			}
			if !matched {
				return bad("REVIEW_MOVE_STORAGE", "共享目录移动子项与冻结原树不同")
			}
			stage, e := a.verifyStorageMoveStage(ctx, child, ctx.Err)
			if e != nil {
				return e
			}
			allowed[filepath.Join(filepath.Dir(target), child.Stage)] = stage
		}
		if child.Softlink != nil && child.StageSnapshot.Inode != 0 {
			if !importPublicationMatchesMethod(child, "softlink") || child.Softlink.Directory != nil {
				return bad("REVIEW_SOFTLINK_DIRECTORY", "共享目录软链接子项身份无效")
			}
			allowedLinks[filepath.Join(filepath.Dir(target), child.Stage)] = child.Softlink
			if child.State == "published" || child.State == "staged" {
				allowedLinks[target] = child.Softlink
			}
		}
		if child.StageSnapshot.Inode != 0 {
			allowed[filepath.Join(filepath.Dir(target), child.Stage)] = child.StageSnapshot
		}
		if child.State == "published" || child.State == "staged" {
			allowed[target] = child.StageSnapshot
		}
	}
	for _, entry := range entries {
		if entry.Directory {
			if before, ok := dirs[entry.Path]; !ok || !sameInode(before, entry.Snapshot) {
				return bad("REVIEW_DIRECTORY_MERGE", "合并目标含新增或替换子目录")
			}
			continue
		}
		before, ok := allowed[entry.Path]
		proof, knownLink := allowedLinks[entry.Path]
		text, actualLink := links[entry.Path]
		if actualLink != knownLink || actualLink && text != proof.Source {
			return bad("REVIEW_SOFTLINK_DIRECTORY", "共享目录包含未知或已替换链接")
		}
		if actualLink {
			if _, e := snapshotOwnedTransferPath(filepath.Join(receipt.Target, entry.Path), before, proof, true); e != nil {
				return e
			}
		}
		if !ok || before != entry.Snapshot {
			return bad("REVIEW_DIRECTORY_MERGE", "合并目标含新增或替换文件")
		}
	}
	// Unrelated observations are retained, not swallowed into this asset.
	actualPaths := map[string]bool{}
	for _, entry := range entries {
		actualPaths[entry.Path] = true
	}
	for _, entry := range receipt.DirectoryMerge.Observed {
		if _, replaced := receipt.DirectoryMerge.Children[entry.Path]; !replaced && !actualPaths[entry.Path] {
			return bad("REVIEW_DIRECTORY_MERGE", "合并期间既有无关条目消失")
		}
	}
	return nil
}

func (a *App) finishDirectoryMerge(ctx context.Context, key string) error {
	var receipt importReceipt
	if err := a.Store.Get(ctx, "import_receipt", key, &receipt); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if receipt.DirectoryMerge == nil {
		return nil
	}
	if receipt.State != "published" || receipt.SharedDirectory == nil {
		return bad("REVIEW_DIRECTORY_MERGE", "共享目录尚未发布完成")
	}
	if _, err := snapshotSharedDirectory(receipt.Target, receipt.StageSnapshot, receipt.SharedDirectory, true); err != nil {
		return err
	}
	targets := make([]string, 0, len(receipt.DirectoryMerge.Children))
	for target := range receipt.DirectoryMerge.Children {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		child := receipt.DirectoryMerge.Children[target]
		if child.Before == nil || child.Cleaned {
			continue
		}
		path := filepath.Join(receipt.Target, filepath.Dir(target), child.Backup)
		actual, err := snapshotPath(path)
		if !errors.Is(err, os.ErrNotExist) {
			if err != nil || actual != *child.Before {
				return bad("REVIEW_DIRECTORY_MERGE", "共享目录旧叶备份已变化")
			}
			parent, e := importDir(filepath.Dir(path), false)
			if e != nil {
				return e
			}
			e = unix.Unlinkat(int(parent.Fd()), filepath.Base(path), 0)
			if e == nil {
				e = parent.Sync()
			}
			parent.Close()
			if e != nil {
				return e
			}
		}
		child.Cleaned = true
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return err
		}
	}
	return nil
}
