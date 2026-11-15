package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// A link's entry identity is saved separately in StageSnapshot/managedFile.
// File proofs bind an ordinary referent; directory proofs bind real directory
// nodes and individual leaf entries. Generic importOpen and snapshotPath retain
// their no-follow contracts.
type importSoftlinkProof struct {
	Directory      *importSoftlinkDirectoryProof `json:"directory,omitempty"`
	Source         string                        `json:"source"`
	SourceSnapshot importSnapshot                `json:"source_snapshot"`
}

func sameSoftlinkProof(a, b *importSoftlinkProof) bool {
	return reflect.DeepEqual(a, b)
}
func sameManagedFile(a, b managedFile) bool {
	if !sameSoftlinkProof(a.Softlink, b.Softlink) || !sameSharedDirectoryProof(a.SharedDirectory, b.SharedDirectory) {
		return false
	}
	a.Softlink, b.Softlink = nil, nil
	a.SharedDirectory, b.SharedDirectory = nil, nil
	return a == b
}

func importPublicationMatchesMethod(receipt importReceipt, method string) bool {
	if method == "softlink" {
		return receipt.Softlink != nil && receipt.Softlink.Source == receipt.Source && receipt.Softlink.SourceSnapshot == receipt.SourceSnapshot && receipt.StageSnapshot.Inode != 0 && receipt.StageSnapshot.Directory == "" && receipt.SHA256 == ""
	}
	return receipt.Softlink == nil && receipt.StageSnapshot.Size == receipt.SourceSnapshot.Size && (method != "hardlink" || receipt.StageSnapshot == receipt.SourceSnapshot)
}

func snapshotSoftlinkEntry(path string) (importSnapshot, string, error) {
	if !importPath(path) {
		return importSnapshot{}, "", bad("INVALID_PATH", "软链接路径无效")
	}
	parent, err := importDir(filepath.Dir(path), false)
	if err != nil {
		return importSnapshot{}, "", err
	}
	defer parent.Close()
	name := filepath.Base(path)
	var before, after unix.Stat_t
	if err = unix.Fstatat(int(parent.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return importSnapshot{}, "", err
	}
	if before.Mode&unix.S_IFMT != unix.S_IFLNK {
		return importSnapshot{}, "", bad("REVIEW_SOFTLINK", "目标不是已记录的软链接")
	}
	buffer := make([]byte, 4097)
	count, err := unix.Readlinkat(int(parent.Fd()), name, buffer)
	if err != nil {
		return importSnapshot{}, "", err
	}
	if count >= len(buffer) {
		return importSnapshot{}, "", bad("REVIEW_SOFTLINK", "软链接引用路径超过上限")
	}
	if err = unix.Fstatat(int(parent.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return importSnapshot{}, "", err
	}
	// FileInfo supplies portable nanosecond mtime while the descriptor-relative
	// before/after stats bind the leaf and parent used to read its link text.
	info, err := pluginStorageEntryInfo(path)
	if err != nil {
		return importSnapshot{}, "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink == 0 || before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || uint64(stat.Dev) != uint64(after.Dev) || uint64(stat.Ino) != uint64(after.Ino) || info.Size() != after.Size {
		return importSnapshot{}, "", bad("REVIEW_SOFTLINK", "软链接在核对期间发生变化")
	}
	return importSnapshot{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Size: info.Size(), ModNS: info.ModTime().UnixNano()}, string(buffer[:count]), nil
}

func snapshotOwnedTransferPath(path string, expected importSnapshot, proof *importSoftlinkProof, requireReferent bool, shared ...*importSharedDirectoryProof) (importSnapshot, error) {
	if len(shared) > 0 && shared[0] != nil {
		if proof != nil {
			return importSnapshot{}, bad("REVIEW_SHARED_DIRECTORY", "共享目录与整树证明混用")
		}
		return snapshotSharedDirectory(path, expected, shared[0], requireReferent)
	}
	if proof == nil {
		return snapshotTransferPath(path)
	}
	if proof.Directory != nil {
		return snapshotOwnedSoftlinkDirectory(path, expected, proof, requireReferent)
	}
	if !importPath(proof.Source) || proof.Source == path || proof.SourceSnapshot.Directory != "" {
		return importSnapshot{}, bad("REVIEW_SOFTLINK", "软链接引用证明无效")
	}
	actual, text, err := snapshotSoftlinkEntry(path)
	if err != nil {
		return actual, err
	}
	if actual != expected || text != proof.Source {
		return actual, bad("REVIEW_SOFTLINK", "受管软链接的条目或引用路径发生变化")
	}
	if requireReferent {
		reference, err := snapshotPath(proof.Source)
		if err != nil || reference != proof.SourceSnapshot {
			return actual, bad("REVIEW_SOFTLINK_SOURCE", "软链接引用的媒体文件已缺失或发生变化")
		}
	}
	return actual, nil
}

// A storage listing must retain owned auxiliary and broken link entries too;
// it must never mistake them for an empty directory or playable media.
func (a *App) ownedSoftlinkEntry(ctx context.Context, path string, requireReferent bool) (managedFile, bool, error) {
	var owned managedFile
	err := a.Store.Get(ctx, "managed_file", tokenHash(path), &owned)
	if errors.Is(err, sql.ErrNoRows) {
		return a.ownedDirectorySoftlinkMember(ctx, path, requireReferent)
	}
	if err != nil {
		return owned, false, err
	}
	if owned.Softlink == nil {
		return owned, false, nil
	}
	var asset Asset
	root, err := a.importRoot(owned.RootID)
	if err != nil || owned.Path != path || !within(root.Path, path) || owned.Kind != "video" && !auxiliaryMaterialKind(owned.Kind) || a.Store.Get(ctx, "asset", owned.AssetID, &asset) != nil || asset.RootID != owned.RootID || mediaKey(asset.Media) != owned.MediaKey {
		return owned, false, bad("REVIEW_SOFTLINK", "软链接存储条目缺少原资产归属")
	}
	if _, err = snapshotOwnedTransferPath(path, owned.Snapshot, owned.Softlink, requireReferent); err != nil {
		return owned, false, err
	}
	return owned, true, nil
}

// Only a persisted video ownership record admits a link to metadata/asset reads.
func (a *App) ownedMediaSoftlink(ctx context.Context, path string) (managedFile, bool, error) {
	var owned managedFile
	err := a.Store.Get(ctx, "managed_file", tokenHash(path), &owned)
	if errors.Is(err, sql.ErrNoRows) {
		return owned, false, nil
	}
	if err != nil {
		return owned, false, err
	}
	if owned.Softlink == nil {
		return owned, false, nil
	}
	var asset Asset
	if err = a.Store.Get(ctx, "asset", owned.AssetID, &asset); err != nil {
		return owned, false, err
	}
	if owned.Path != path || owned.Kind != "video" || asset.Path != path || asset.RootID != owned.RootID || mediaKey(asset.Media) != owned.MediaKey || asset.State == "removed" || asset.State == "superseded" || asset.State == "missing_confirmed" {
		return owned, false, bad("REVIEW_SOFTLINK", "软链接媒体归属与资产不同")
	}
	if _, err = snapshotOwnedTransferPath(path, owned.Snapshot, owned.Softlink, true); err != nil {
		return owned, false, err
	}
	return owned, true, nil
}

func (a *App) snapshotAssetMedia(ctx context.Context, asset Asset) (importSnapshot, error) {
	var shared managedFile
	if e := a.Store.Get(ctx, "managed_file", tokenHash(asset.Path), &shared); e == nil && shared.SharedDirectory != nil {
		if shared.Path != asset.Path || shared.AssetID != asset.ID || shared.RootID != asset.RootID || shared.Kind != "video" || shared.MediaKey != mediaKey(asset.Media) {
			return importSnapshot{}, bad("REVIEW_ASSET", "共享目录归属已变化")
		}
		return snapshotOwnedTransferPath(asset.Path, shared.Snapshot, shared.Softlink, true, shared.SharedDirectory)
	} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return importSnapshot{}, e
	}
	owned, ok, err := a.ownedMediaSoftlink(ctx, asset.Path)
	if err != nil {
		return importSnapshot{}, err
	}
	if ok {
		if owned.AssetID != asset.ID {
			return importSnapshot{}, bad("REVIEW_ASSET", "软链接归属已变化")
		}
		return owned.Snapshot, nil
	}
	return snapshotTransferPath(asset.Path)
}

func (a *App) originalMetadataMediaStamp(ctx context.Context, path string) (browserStamp, error) {
	// A directory metadata transaction already froze its members. During its
	// authorized writes only bind the root entry here, just like ordinary
	// directory scraping; finish validates every original and written member.
	if directories, ok := ctx.Value(originalMetadataDirectoriesKey{}).([]*originalMetadataDirectory); ok {
		for _, directory := range directories {
			if directory.Owned.Path == path && directory.Owned.Softlink != nil && directory.Owned.Softlink.Directory != nil {
				actual, e := browserStat(path)
				if e != nil || actual.Device != directory.Owned.Snapshot.Device || actual.Inode != directory.Owned.Snapshot.Inode {
					return actual, bad("REVIEW_METADATA_SOURCE_CHANGED", "目录刮削期间根目录归属变化")
				}
				return actual, nil
			}
		}
	}
	owned, ok, err := a.ownedMediaSoftlink(ctx, path)
	if err != nil {
		return browserStamp{}, err
	}
	if !ok {
		return browserStat(path)
	}
	if owned.Softlink.Directory != nil {
		return browserStat(path)
	}
	stamp := owned.Snapshot
	return browserStamp{Path: path, Device: stamp.Device, Inode: stamp.Inode, Size: stamp.Size, ModNS: stamp.ModNS, Mode: uint32(os.ModeSymlink | 0777)}, nil
}

func (a *App) originalPublishedTransferTargetItem(ctx context.Context, plan ImportPlan, file PlanFile) (*plugins.ModuleFileItem, error) {
	if file.Method != "softlink" {
		return originalLocalFileItem(file.Target)
	}
	var receipt importReceipt
	if err := a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt); err != nil {
		return nil, err
	}
	if receipt.SharedDirectory != nil && file.DirectoryDigest != "" {
		if receipt.Target != file.Target || receipt.Source != file.Source {
			return nil, bad("REVIEW_SOFTLINK", "共享目录目标文件项身份不同")
		}
		if err := a.validateSharedSoftlinkPublication(ctx, receipt); err != nil {
			return nil, err
		}
		if _, err := snapshotSharedDirectory(file.Target, receipt.StageSnapshot, receipt.SharedDirectory, true); err != nil {
			return nil, err
		}
		return originalLocalFileItem(file.Target)
	}
	if receipt.State != "published" || receipt.Target != file.Target || receipt.Source != file.Source || receipt.Softlink == nil {
		return nil, bad("REVIEW_SOFTLINK", "目标文件项缺少已发布软链接证明")
	}
	if _, err := snapshotOwnedTransferPath(file.Target, receipt.StageSnapshot, receipt.Softlink, true); err != nil {
		return nil, err
	}
	return softlinkTargetFileItem(file.Target, receipt.Softlink.Source)
}

func softlinkTargetFileItem(path, source string) (*plugins.ModuleFileItem, error) {
	stamp, err := snapshotPath(source)
	if err != nil {
		return nil, err
	}
	return softlinkTargetFileItemSnapshot(path, stamp)
}

func softlinkTargetFileItemSnapshot(path string, stamp importSnapshot) (*plugins.ModuleFileItem, error) {
	fields := map[string]any{}
	for _, key := range []string{"fileid", "parent_fileid", "thumbnail", "pickcode", "drive_id", "url"} {
		fields[key] = nil
	}
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	fields["storage"], fields["type"], fields["path"], fields["children"] = "local", "file", path, []any{}
	fields["name"], fields["basename"], fields["extension"] = name, strings.TrimSuffix(name, ext), strings.TrimPrefix(ext, ".")
	fields["size"], fields["modify_time"] = stamp.Size, float64(stamp.ModNS)/1e9
	return plugins.NewModuleFileItem(fields)
}

func (a *App) reuseOwnedInPlaceSoftlink(ctx context.Context, key string, file PlanFile, owned managedFile) (importReceipt, error) {
	if owned.Softlink == nil || owned.Path != file.Target || owned.Softlink.Source != file.Source || owned.Softlink.SourceSnapshot != planSnapshot(file) {
		return importReceipt{}, bad("REVIEW_SOFTLINK", "原位重整的软链接引用与原登记不同")
	}
	if _, err := snapshotOwnedTransferPath(owned.Path, owned.Snapshot, owned.Softlink, true); err != nil {
		return importReceipt{}, err
	}
	var receipt importReceipt
	err := a.Store.Get(ctx, "import_receipt", key, &receipt)
	if errors.Is(err, sql.ErrNoRows) {
		receipt = importReceipt{ID: key, Source: file.Source, Target: file.Target, SourceSnapshot: planSnapshot(file), Stage: ".light-media-" + tokenHash(key)[:32] + ".part", State: "planned"}
	} else if err != nil {
		return receipt, err
	}
	if receipt.ID != key || receipt.Source != file.Source || receipt.Target != file.Target || receipt.SourceSnapshot != planSnapshot(file) || receipt.Move != nil || receipt.StorageMove != nil {
		return receipt, bad("REVIEW_RECEIPT", "原位软链接收据与计划不同")
	}
	if owned.Softlink.Directory != nil {
		rows, e := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='import_receipt' AND json_extract(payload,'$.target')=? AND json_extract(payload,'$.state')='published'`, owned.Path)
		if e != nil {
			return receipt, e
		}
		found := false
		for rows.Next() {
			var raw []byte
			var prior importReceipt
			if e = rows.Scan(&raw); e != nil {
				break
			}
			if e = json.Unmarshal(raw, &prior); e != nil {
				break
			}
			if prior.Source == file.Source && prior.SourceSnapshot == planSnapshot(file) && prior.StageSnapshot == owned.Snapshot && sameSoftlinkProof(prior.Softlink, owned.Softlink) {
				receipt.DirectoryMembers, receipt.DirectoryFiles, receipt.DirectoryCompleted = prior.DirectoryMembers, prior.DirectoryFiles, prior.DirectoryCompleted
				receipt.DirectoryListed, receipt.DirectoryStreamSize = prior.DirectoryListed, prior.DirectoryStreamSize
				found = true
				break
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return receipt, e
		}
		if !found {
			return receipt, bad("REVIEW_RECEIPT", "原位目录软链接缺少对应成员收据")
		}
	}
	receipt.State, receipt.StageSnapshot, receipt.Softlink = "published", owned.Snapshot, owned.Softlink
	return receipt, a.Store.Put(ctx, "import_receipt", key, receipt)
}

func (a *App) publishImportSoftlink(ctx context.Context, key, source, target string, locked importSnapshot, check func() error) (importReceipt, error) {
	if check == nil {
		check = ctx.Err
	}
	receipt := importReceipt{ID: key, Source: source, Target: target, SourceSnapshot: locked, Stage: ".light-media-" + tokenHash(key)[:32] + ".part", State: "planned", Softlink: &importSoftlinkProof{Source: source, SourceSnapshot: locked}}
	if !importPath(source) || !importPath(target) || source == target || locked.Directory != "" {
		return receipt, bad("INVALID_TARGET", "软链接需要不同的普通源文件与目的路径")
	}
	var storage *originalTransferStorage
	if pair := originalTransferStorageFromContext(ctx); pair != nil {
		storage = pair.Source
	}
	var prior importReceipt
	if err := a.Store.Get(ctx, "import_receipt", key, &prior); err == nil {
		if prior.ID != key || prior.Source != source || prior.Target != target || prior.SourceSnapshot != locked || prior.Stage != receipt.Stage || prior.Move != nil || prior.StorageMove != nil || prior.Softlink != nil && !sameSoftlinkProof(prior.Softlink, receipt.Softlink) {
			return prior, bad("REVIEW_RECEIPT", "软链接收据与冻结计划不同")
		}
		if prior.Softlink == nil {
			if prior.State != "planned" || prior.StageSnapshot != (importSnapshot{}) || prior.SHA256 != "" {
				return prior, bad("REVIEW_RECEIPT", "既有发布不是未执行的软链接任务")
			}
			prior.Softlink = receipt.Softlink
			if err = a.Store.Put(ctx, "import_receipt", key, prior); err != nil {
				return prior, err
			}
		}
		receipt = prior
	} else if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	} else if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
		return receipt, err
	}
	parent, err := importDir(filepath.Dir(target), true)
	if err != nil {
		return receipt, err
	}
	defer parent.Close()
	stage := filepath.Join(filepath.Dir(target), receipt.Stage)
	if actual, _, e := snapshotSoftlinkEntry(target); e == nil {
		if (receipt.State != "staged" && receipt.State != "published") || actual != receipt.StageSnapshot {
			return receipt, bad("IMPORT_CONFLICT", "目标软链接不属于本次发布")
		}
		if _, err = snapshotOwnedTransferPath(target, receipt.StageSnapshot, receipt.Softlink, true); err != nil {
			return receipt, err
		}
		receipt.State = "published"
		return receipt, a.Store.Put(ctx, "import_receipt", key, receipt)
	} else if !errors.Is(e, os.ErrNotExist) {
		return receipt, bad("IMPORT_CONFLICT", "目标路径已存在或不可核对")
	}
	if receipt.State == "published" {
		return receipt, bad("REVIEW_PUBLISHED_MISSING", "已发布软链接被移除，请核对后处理")
	}
	if err = check(); err != nil {
		return receipt, err
	}
	reference, err := snapshotPath(source)
	if err != nil || reference != locked {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "软链接源文件与冻结快照不同")
	}
	if receipt.State == "planned" && (storage != nil || receipt.StorageSoftlink != nil) {
		receipt, err = a.stageStorageSoftlink(ctx, receipt, storage, check)
		if err != nil {
			return receipt, err
		}
		if err = parent.Sync(); err != nil {
			return receipt, err
		}
	}
	if receipt.State == "planned" {
		if _, _, e := snapshotSoftlinkEntry(stage); !errors.Is(e, os.ErrNotExist) {
			return receipt, bad("REVIEW_STAGE", "软链接暂存条目缺少已确认的所有权收据")
		}
		if err = unix.Symlinkat(source, int(parent.Fd()), receipt.Stage); err != nil {
			return receipt, err
		}
		stamp, text, e := snapshotSoftlinkEntry(stage)
		if e != nil || text != source {
			return receipt, bad("REVIEW_SOFTLINK", "新建软链接暂存条目与源不同")
		}
		receipt.StageSnapshot, receipt.State = stamp, "staged"
		if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
			return receipt, err
		}
		if err = parent.Sync(); err != nil {
			return receipt, err
		}
	}
	if receipt.State != "staged" {
		return receipt, bad("REVIEW_RECEIPT", "软链接发布阶段无效")
	}
	if _, err = snapshotOwnedTransferPath(stage, receipt.StageSnapshot, receipt.Softlink, true); err != nil {
		return receipt, err
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if err = importRenameExclusive(int(parent.Fd()), receipt.Stage, filepath.Base(target)); err != nil {
		return receipt, bad("IMPORT_CONFLICT", "软链接目的路径发生冲突")
	}
	if err = parent.Sync(); err != nil {
		return receipt, err
	}
	if _, err = snapshotOwnedTransferPath(target, receipt.StageSnapshot, receipt.Softlink, true); err != nil {
		return receipt, err
	}
	receipt.State = "published"
	return receipt, a.Store.Put(ctx, "import_receipt", key, receipt)
}
