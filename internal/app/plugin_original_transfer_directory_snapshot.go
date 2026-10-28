package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// A directory is one source task in MoviePilot. Its native authority must still
// bind every member, including small non-media files and empty directories.
// Keep snapshotPath's regular-file contract for all other native operations.
type originalDirectoryEntry struct {
	Path      string         `json:"path"`
	Directory bool           `json:"directory"`
	Snapshot  importSnapshot `json:"snapshot"`
}

func originalDirectoryIdentity(info os.FileInfo) (importSnapshot, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return importSnapshot{}, bad("INVALID_FILE", "整理目录不是可核对的本地目录")
	}
	return importSnapshot{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), ModNS: info.ModTime().UnixNano()}, nil
}

func snapshotTransferPath(path string) (importSnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return importSnapshot{}, err
	}
	if !info.IsDir() {
		return snapshotPath(path)
	}
	stamp, _, err := snapshotOriginalDirectory(path)
	return stamp, err
}

func snapshotOriginalDirectory(path string) (importSnapshot, []originalDirectoryEntry, error) {
	entries := []originalDirectoryEntry{}
	var size int64
	var walk func(string, int) (importSnapshot, error)
	walk = func(current string, depth int) (importSnapshot, error) {
		if depth > 16 || len(entries) > 20000 {
			return importSnapshot{}, bad("FILE_SELECTION_LIMIT", "目录快照超过上限，请缩小选择范围")
		}
		dir, err := importDir(current, false)
		if err != nil {
			return importSnapshot{}, err
		}
		defer dir.Close()
		info, err := dir.Stat()
		if err != nil {
			return importSnapshot{}, err
		}
		before, err := originalDirectoryIdentity(info)
		if err != nil {
			return importSnapshot{}, err
		}
		children, err := dir.ReadDir(-1)
		if err != nil {
			return importSnapshot{}, err
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			if len(entries) >= 20000 {
				return importSnapshot{}, bad("FILE_SELECTION_LIMIT", "目录快照超过上限，请缩小选择范围")
			}
			name := filepath.Join(current, child.Name())
			rel, err := filepath.Rel(path, name)
			if err != nil {
				return importSnapshot{}, err
			}
			entry := originalDirectoryEntry{Path: rel, Directory: child.IsDir()}
			if entry.Directory {
				entry.Snapshot, err = walk(name, depth+1)
			} else {
				entry.Snapshot, err = snapshotPath(name)
				size += entry.Snapshot.Size
			}
			if err != nil {
				return importSnapshot{}, err
			}
			entries = append(entries, entry)
		}
		info, err = dir.Stat()
		if err != nil {
			return importSnapshot{}, err
		}
		after, err := originalDirectoryIdentity(info)
		if err != nil || before != after {
			return importSnapshot{}, bad("REVIEW_SOURCE_CHANGED", "目录成员在快照期间发生变化")
		}
		bound, err := importDir(current, false)
		if err != nil {
			return importSnapshot{}, err
		}
		boundInfo, err := bound.Stat()
		bound.Close()
		if err != nil {
			return importSnapshot{}, err
		}
		boundStamp, err := originalDirectoryIdentity(boundInfo)
		if err != nil || boundStamp != before {
			return importSnapshot{}, bad("REVIEW_SOURCE_CHANGED", "目录路径在快照期间发生替换")
		}
		return before, nil
	}
	root, err := walk(path, 0)
	if err != nil {
		return importSnapshot{}, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	raw, err := json.Marshal(entries)
	if err != nil {
		return importSnapshot{}, nil, err
	}
	digest := sha256.Sum256(raw)
	root.Directory = hex.EncodeToString(digest[:])
	root.Size = size
	return root, entries, nil
}
