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

// Metadata listing can describe a leaf only through its persisted aggregate
// owner. The derived record is never saved and does not create another asset.
func (a *App) ownedDirectorySoftlinkMember(ctx context.Context, path string, requireReferent bool) (managedFile, bool, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return managedFile{}, false, nil
	}
	for parent := filepath.Dir(path); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		var owner managedFile
		err := a.Store.Get(ctx, "managed_file", tokenHash(parent), &owner)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return managedFile{}, false, err
		}
		if owner.SharedDirectory == nil && (owner.Softlink == nil || owner.Softlink.Directory == nil) {
			continue
		}
		relative, e := filepath.Rel(parent, path)
		if e != nil || !within(parent, path) {
			return managedFile{}, false, bad("REVIEW_SOFTLINK", "目录成员路径越界")
		}
		var member importSoftlinkDirectoryMember
		var ok bool
		var parents map[string]importSnapshot
		var proofRoot importSnapshot
		if owner.SharedDirectory != nil {
			if owner.Softlink != nil {
				return managedFile{}, false, bad("REVIEW_SOFTLINK", "目录归属证明混用")
			}
			shared, present := owner.SharedDirectory.Members[relative]
			member, ok = importSoftlinkDirectoryMember{Snapshot: shared.Snapshot, Softlink: shared.Softlink}, present
			parents, proofRoot = owner.SharedDirectory.Directories, owner.SharedDirectory.Root
		} else {
			member, ok = owner.Softlink.Directory.Members[relative]
			parents, proofRoot = owner.Softlink.Directory.Directories, owner.Softlink.Directory.Root
		}
		if !sameInode(proofRoot, owner.Snapshot) {
			return managedFile{}, false, bad("REVIEW_SOFTLINK", "目录成员根证明与资产不同")
		}
		if !ok || member.Softlink == nil {
			return managedFile{}, false, nil
		}
		var asset Asset
		root, e := a.importRoot(owner.RootID)
		stamp, se := browserStat(parent)
		if e != nil || owner.Path != parent || owner.Kind != "video" || !within(root.Path, path) || se != nil || stamp.Device != owner.Snapshot.Device || stamp.Inode != owner.Snapshot.Inode || a.Store.Get(ctx, "asset", owner.AssetID, &asset) != nil || asset.Path != parent || asset.RootID != owner.RootID || mediaKey(asset.Media) != owner.MediaKey || asset.State == "removed" || asset.State == "superseded" {
			return managedFile{}, false, bad("REVIEW_SOFTLINK", "目录成员缺少原资产归属")
		}
		for relativeParent := filepath.Dir(relative); relativeParent != "."; relativeParent = filepath.Dir(relativeParent) {
			expected, known := parents[relativeParent]
			if !known {
				return managedFile{}, false, bad("REVIEW_SOFTLINK", "目录成员缺少父节点证明")
			}
			dir, e := importDir(filepath.Join(parent, relativeParent), false)
			if e != nil {
				return managedFile{}, false, e
			}
			info, e := dir.Stat()
			dir.Close()
			if e != nil {
				return managedFile{}, false, e
			}
			actual, e := originalDirectoryIdentity(info)
			if e != nil || !sameInode(actual, expected) {
				return managedFile{}, false, bad("REVIEW_SOFTLINK", "目录成员父节点已替换")
			}
		}
		if _, e = snapshotOwnedTransferPath(path, member.Snapshot, member.Softlink, false); e != nil {
			return managedFile{}, false, e
		}
		owner.Path, owner.Kind, owner.Snapshot, owner.SourceSnapshot, owner.Softlink = path, "directory_member", member.Snapshot, member.Softlink.SourceSnapshot, member.Softlink
		owner.SharedDirectory = nil
		if requireReferent {
			if _, e = snapshotOwnedTransferPath(path, member.Snapshot, member.Softlink, true); e != nil {
				return owner, true, e
			}
		}
		return owner, true, nil
	}
	return managedFile{}, false, nil
}

func scanMetadataDirectory(path string, proof *importSoftlinkProof) (importSnapshot, []originalDirectoryEntry, error) {
	if proof == nil || proof.Directory == nil {
		return snapshotOriginalDirectory(path)
	}
	stamp, entries, _, err := scanSoftlinkDirectory(path)
	return stamp, entries, err
}

// Called only after the existing metadata journal has verified every prior
// member and every explicitly authorized write. This is not discovery/adoption
// of arbitrary new files. Media link proofs remain unchanged; new sidecars are
// ordinary entries written beside those links.
func refreshMetadataSoftlinkDirectory(prior *importSoftlinkProof, actual importSnapshot, entries []originalDirectoryEntry, writes map[string]originalMetadataDirectoryWrite) (*importSoftlinkProof, error) {
	proof := *prior
	directory := &importSoftlinkDirectoryProof{Root: importSnapshot{Device: actual.Device, Inode: actual.Inode, ModNS: actual.ModNS}, Directories: map[string]importSnapshot{}, Members: map[string]importSoftlinkDirectoryMember{}}
	for _, entry := range entries {
		if entry.Directory {
			directory.Directories[entry.Path] = entry.Snapshot
			continue
		}
		if _, written := writes[entry.Path]; written {
			directory.Members[entry.Path] = importSoftlinkDirectoryMember{Snapshot: entry.Snapshot}
			continue
		}
		member, ok := prior.Directory.Members[entry.Path]
		if !ok || member.Snapshot != entry.Snapshot {
			return nil, bad("REVIEW_METADATA_SOURCE_CHANGED", "软链接目录包含未确认的成员")
		}
		directory.Members[entry.Path] = member
	}
	proof.Directory = directory
	return &proof, nil
}

func metadataDirectoryLinkMember(ctx context.Context, target string) (importSoftlinkDirectoryMember, bool) {
	directories, _ := ctx.Value(originalMetadataDirectoriesKey{}).([]*originalMetadataDirectory)
	for _, directory := range directories {
		proof := directory.Owned.Softlink
		if !within(directory.Owned.Path, target) {
			continue
		}
		relative, err := filepath.Rel(directory.Owned.Path, target)
		if err != nil || relative == "." {
			continue
		}
		if shared := directory.Owned.SharedDirectory; shared != nil {
			member, ok := shared.Members[relative]
			return importSoftlinkDirectoryMember{Snapshot: member.Snapshot, Softlink: member.Softlink}, ok && member.Softlink != nil
		}
		if proof != nil && proof.Directory != nil {
			member, ok := proof.Directory.Members[relative]
			return member, ok && member.Softlink != nil
		}
	}
	return importSoftlinkDirectoryMember{}, false
}

func (a *App) removeSoftlinkDirectoryQuarantine(ctx context.Context, file *assetRemovalFile, parent *os.File, persist func() error) error {
	target := filepath.Join(filepath.Dir(file.Path), file.Quarantine)
	actual, err := snapshotOwnedTransferPath(target, file.Snapshot, file.Softlink, false)
	if err != nil {
		return err
	}
	if !file.DirectoryRemovalStarted && actual != file.Snapshot {
		return bad("FILE_SNAPSHOT_CHANGED", "软链接目录删除前成员已变化")
	}
	file.DirectoryRemovalStarted = true
	if err = persist(); err != nil {
		return err
	}
	_, entries, _, err := scanSoftlinkDirectory(target)
	if err != nil {
		return err
	}
	// Deeper children before their parents, independent of map/filesystem order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path > entries[j].Path })
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(target, entry.Path)
		dir, err := importDir(filepath.Dir(path), false)
		if err != nil {
			return err
		}
		flags := 0
		if entry.Directory {
			flags = unix.AT_REMOVEDIR
		} else if member, ok := file.Softlink.Directory.Members[entry.Path]; !ok {
			dir.Close()
			return bad("FILE_SNAPSHOT_CHANGED", "软链接目录含未知成员")
		} else if _, err = snapshotOwnedTransferPath(path, member.Snapshot, member.Softlink, false); err != nil {
			dir.Close()
			return err
		}
		if entry.Directory {
			info, e := os.Lstat(path)
			if e != nil || !info.IsDir() {
				dir.Close()
				return bad("FILE_SNAPSHOT_CHANGED", "删除期间子目录发生替换")
			}
			stamp, e := originalDirectoryIdentity(info)
			if e != nil || !sameInode(stamp, entry.Snapshot) {
				dir.Close()
				return bad("FILE_SNAPSHOT_CHANGED", "删除期间子目录发生替换")
			}
		}
		err = unix.Unlinkat(int(dir.Fd()), filepath.Base(path), flags)
		if err == nil {
			err = dir.Sync()
		}
		dir.Close()
		if err != nil {
			return err
		}
	}
	root, err := browserStat(target)
	if err != nil || root.Device != file.Snapshot.Device || root.Inode != file.Snapshot.Inode {
		return bad("FILE_SNAPSHOT_CHANGED", "删除期间目录根发生变化")
	}
	return unix.Unlinkat(int(parent.Fd()), file.Quarantine, unix.AT_REMOVEDIR)
}
