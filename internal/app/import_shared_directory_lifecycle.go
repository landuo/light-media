package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

// A shared root never enters the whole-root quarantine path. Each actual owned
// leaf retains the existing provider journal and its own exclusive quarantine.
func (a *App) removeSharedDirectoryMembers(ctx context.Context, file *assetRemovalFile, persist func() error) error {
	proof := file.SharedDirectory
	if proof == nil || file.Softlink != nil || file.Snapshot.Directory == "" {
		return bad("REVIEW_SHARED_DIRECTORY", "共享目录删除缺少成员归属")
	}
	if file.Deleted {
		return verifySharedDirectoryRemoved(*file)
	}
	current, err := snapshotSharedDirectory(file.Path, file.Snapshot, proof, false)
	if err != nil {
		return err
	}
	if !file.DirectoryRemovalStarted && current != file.Snapshot {
		return bad("FILE_SNAPSHOT_CHANGED", "共享目录删除前成员已变化")
	}
	if file.SharedMembers == nil {
		file.SharedMembers = map[string]*assetRemovalFile{}
	}
	names := make([]string, 0, len(proof.Members))
	for relative, member := range proof.Members {
		names = append(names, relative)
		target := filepath.Join(file.Path, relative)
		child := file.SharedMembers[relative]
		if child == nil {
			if file.DirectoryRemovalStarted {
				return bad("REVIEW_SHARED_DIRECTORY", "共享目录删除进度缺少原成员")
			}
			child = &assetRemovalFile{Path: target, Snapshot: member.Snapshot, Softlink: member.Softlink, Kind: "directory_member", Quarantine: ".light-media-remove-" + tokenHash(file.Quarantine + target)[:32] + ".part"}
			file.SharedMembers[relative] = child
		}
		if child.Path != target || child.Snapshot != member.Snapshot || !sameSoftlinkProof(child.Softlink, member.Softlink) || child.SharedDirectory != nil || child.Quarantine != ".light-media-remove-"+tokenHash(file.Quarantine + target)[:32]+".part" {
			return bad("REVIEW_SHARED_DIRECTORY", "共享目录成员删除进度与归属不同")
		}
	}
	if len(file.SharedMembers) != len(proof.Members) {
		return bad("REVIEW_SHARED_DIRECTORY", "共享目录删除进度含额外成员")
	}
	file.DirectoryRemovalStarted = true
	if err = persist(); err != nil {
		return err
	}
	sort.Strings(names)
	for _, relative := range names {
		if err = ctx.Err(); err != nil {
			return err
		}
		child := file.SharedMembers[relative]
		if child.Deleted {
			continue
		}
		parent, e := importDir(filepath.Dir(child.Path), false)
		if e != nil {
			return e
		}
		e = func() error {
			// Resolve the selected module's existing result before inspecting its
			// physical effect. A started/unreturned delete is never dispatched twice.
			if child.SourceOperation != "" {
				if e := a.originalHistoryStorageDelete(ctx, child, parent, persist, nil); e != nil {
					return e
				}
			}
			if child.Deleted {
				return nil
			}
			location, e := removalLocation(*child)
			if e != nil {
				return e
			}
			if location == "source" {
				if e = a.originalHistoryStorageDelete(ctx, child, parent, persist, nil); e != nil {
					return e
				}
				if child.Deleted {
					return nil
				}
				location, e = removalLocation(*child)
				if e != nil {
					return e
				}
				if location == "source" {
					return bad("PLUGIN_FILE_OPERATION_REJECTED", "存储扩展保留了共享目录的所选成员")
				}
			}
			if location != "gone" {
				child.Quarantined = true
				if e = persist(); e != nil {
					return e
				}
				if e = a.removeHistoryQuarantine(ctx, child, parent, persist); e != nil {
					return e
				}
				if e = parent.Sync(); e != nil {
					return e
				}
			}
			child.Deleted = true
			return persist()
		}()
		parent.Close()
		if e != nil {
			return e
		}
	}
	// Rmdir is the empty check. New unrelated entries retain even a container
	// created by this import; existing containers are never candidates.
	directories := []string{}
	for relative, created := range proof.CreatedDirectories {
		if created {
			directories = append(directories, relative)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, relative := range directories {
		expected, ok := proof.Directories[relative]
		if !ok || !softlinkDirectoryRelative(relative) {
			return bad("REVIEW_SHARED_DIRECTORY", "共享目录新建父节点许可无效")
		}
		path := filepath.Join(file.Path, relative)
		dir, e := importDir(path, false)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		info, e := dir.Stat()
		dir.Close()
		if e != nil {
			return e
		}
		stamp, e := originalDirectoryIdentity(info)
		if e != nil || !sameInode(stamp, expected) {
			return bad("REVIEW_SHARED_DIRECTORY", "共享目录清理父节点已替换")
		}
		parent, e := importDir(filepath.Dir(path), false)
		if e != nil {
			return e
		}
		e = unix.Unlinkat(int(parent.Fd()), filepath.Base(path), unix.AT_REMOVEDIR)
		if errors.Is(e, unix.ENOTEMPTY) || errors.Is(e, unix.EEXIST) {
			e = nil
		} else if e == nil {
			e = parent.Sync()
		}
		parent.Close()
		if e != nil {
			return e
		}
	}
	if err = verifySharedDirectoryRemoved(*file); err != nil {
		return err
	}
	file.Deleted = true
	return persist()
}

// Completion concerns the frozen members, not the existence of their observed
// root. This also prevents parent cleanup from swallowing unrelated old files.
func verifySharedDirectoryRemoved(file assetRemovalFile) error {
	if file.SharedDirectory == nil {
		return bad("REVIEW_SHARED_DIRECTORY", "共享目录清理证明缺失")
	}
	if _, err := snapshotSharedDirectory(file.Path, file.Snapshot, file.SharedDirectory, false); err != nil {
		if errors.Is(err, os.ErrNotExist) && file.Deleted {
			return nil
		}
		return err
	}
	for relative := range file.SharedDirectory.Members {
		child := file.SharedMembers[relative]
		if child == nil || !child.Deleted {
			return bad("REVIEW_SHARED_DIRECTORY", "共享目录成员清理尚未确认")
		}
		if _, err := os.Lstat(filepath.Join(file.Path, relative)); !errors.Is(err, os.ErrNotExist) {
			return bad("REVIEW_SHARED_DIRECTORY", "已清理成员路径再次出现")
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(child.Path), child.Quarantine)); !errors.Is(err, os.ErrNotExist) {
			return bad("REVIEW_SHARED_DIRECTORY", "共享目录成员暂存尚未清理")
		}
	}
	return nil
}
