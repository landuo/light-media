package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// An observed existing root is not whole-tree ownership. Only actually published
// members belong to this asset; unrelated entries are deliberately not scanned
// into this proof, its size or later deletion permission.
type importSharedDirectoryProof struct {
	Root               importSnapshot                         `json:"root"`
	Directories        map[string]importSnapshot              `json:"directories"`
	CreatedDirectories map[string]bool                        `json:"created_directories,omitempty"`
	Members            map[string]importSharedDirectoryMember `json:"members"`
}
type importSharedDirectoryMember struct {
	Snapshot importSnapshot       `json:"snapshot"`
	Softlink *importSoftlinkProof `json:"softlink,omitempty"`
}

func sameSharedDirectoryProof(a, b *importSharedDirectoryProof) bool { return reflect.DeepEqual(a, b) }

func sharedDirectoryIdentity(stamp importSnapshot) importSnapshot {
	stamp.Size, stamp.ModNS, stamp.Directory = 0, 0, ""
	return stamp
}

func snapshotSharedDirectory(path string, expected importSnapshot, proof *importSharedDirectoryProof, requireReferent bool) (importSnapshot, error) {
	if proof == nil || proof.Root.Inode == 0 || proof.Root != sharedDirectoryIdentity(proof.Root) {
		return importSnapshot{}, bad("REVIEW_SHARED_DIRECTORY", "共享目录根许可缺失")
	}
	root, err := importDir(path, false)
	if err != nil {
		return importSnapshot{}, err
	}
	info, err := root.Stat()
	root.Close()
	if err != nil {
		return importSnapshot{}, err
	}
	actual, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(actual, proof.Root) || expected.Inode != 0 && !sameInode(actual, expected) {
		return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录根已被替换")
	}
	type entry struct {
		Path      string         `json:"path"`
		Directory bool           `json:"directory,omitempty"`
		Snapshot  importSnapshot `json:"snapshot"`
	}
	entries := []entry{}
	for relative, stamp := range proof.Directories {
		if !softlinkDirectoryRelative(relative) || stamp.Inode == 0 || stamp != sharedDirectoryIdentity(stamp) {
			return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录父节点许可无效")
		}
		dir, e := importDir(filepath.Join(path, relative), false)
		if errors.Is(e, os.ErrNotExist) && !requireReferent {
			continue
		}
		if e != nil {
			return actual, e
		}
		info, e := dir.Stat()
		dir.Close()
		if e != nil {
			return actual, e
		}
		current, e := originalDirectoryIdentity(info)
		if e != nil || !sameInode(current, stamp) {
			return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录父节点已变化")
		}
		entries = append(entries, entry{Path: relative, Directory: true, Snapshot: sharedDirectoryIdentity(current)})
	}
	for relative, created := range proof.CreatedDirectories {
		if created {
			if _, ok := proof.Directories[relative]; !ok || !softlinkDirectoryRelative(relative) {
				return actual, bad("REVIEW_SHARED_DIRECTORY", "新增目录清理许可未绑定已知目录")
			}
		}
	}
	var size int64
	for relative, member := range proof.Members {
		if !softlinkDirectoryRelative(relative) || member.Snapshot.Inode == 0 || member.Snapshot.Directory != "" || member.Softlink != nil && member.Softlink.Directory != nil {
			return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录成员许可无效")
		}
		if _, exists := proof.Directories[relative]; exists {
			return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录成员与父节点冲突")
		}
		for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
			if _, ok := proof.Directories[parent]; !ok {
				return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录成员缺少父节点证明")
			}
		}
		current, e := snapshotOwnedTransferPath(filepath.Join(path, relative), member.Snapshot, member.Softlink, requireReferent)
		if errors.Is(e, os.ErrNotExist) && !requireReferent {
			continue
		}
		if e != nil {
			return actual, e
		}
		if current != member.Snapshot {
			return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录已拥有成员被替换")
		}
		if member.Softlink != nil {
			size += member.Softlink.SourceSnapshot.Size
		} else {
			size += current.Size
		}
		entries = append(entries, entry{Path: relative, Snapshot: current})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	raw, err := json.Marshal(entries)
	if err != nil {
		return actual, err
	}
	sum := sha256.Sum256(raw)
	actual = sharedDirectoryIdentity(actual)
	actual.Size = size
	actual.Directory = hex.EncodeToString(sum[:])
	if requireReferent && expected.Directory != "" && actual != expected {
		return actual, bad("REVIEW_SHARED_DIRECTORY", "共享目录已拥有成员集合已变化")
	}
	return actual, nil
}
