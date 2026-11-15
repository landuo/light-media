package app

import (
	"os"
	"path/filepath"
	"sort"
)

func freezeSharedMetadataParents(root, relative string, proof *importSharedDirectoryProof) (map[string]importSnapshot, error) {
	if !softlinkDirectoryRelative(relative) {
		return nil, bad("REVIEW_METADATA_CONTEXT", "共享目录旁车路径无效")
	}
	directory, err := importDir(root, false)
	if err != nil {
		return nil, err
	}
	info, err := directory.Stat()
	directory.Close()
	if err != nil {
		return nil, err
	}
	stamp, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(stamp, proof.Root) {
		return nil, bad("REVIEW_METADATA_CONTEXT", "共享目录根身份已变化")
	}
	parents := map[string]importSnapshot{}
	for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
		dir, err := importDir(filepath.Join(root, parent), false)
		if err != nil {
			return nil, err
		}
		info, err := dir.Stat()
		dir.Close()
		if err != nil {
			return nil, err
		}
		stamp, err := originalDirectoryIdentity(info)
		if err != nil {
			return nil, err
		}
		if old, ok := proof.Directories[parent]; ok && !sameInode(old, stamp) {
			return nil, bad("REVIEW_METADATA_CONTEXT", "共享目录旁车父节点已替换")
		}
		parents[parent] = sharedDirectoryIdentity(stamp)
	}
	return parents, nil
}

// Inspect only prior-owned members and explicit metadata destinations. A new
// unrelated NFO, directory or link is neither scanned nor adopted into ownership.
func scanSharedMetadataDirectory(root string, prior *importSharedDirectoryProof, writes map[string]originalMetadataDirectoryWrite) (importSnapshot, []originalDirectoryEntry, error) {
	candidate := &importSharedDirectoryProof{Root: prior.Root, Directories: map[string]importSnapshot{}, CreatedDirectories: prior.CreatedDirectories, Members: map[string]importSharedDirectoryMember{}}
	for relative, stamp := range prior.Directories {
		candidate.Directories[relative] = stamp
	}
	names := map[string]bool{}
	for relative := range prior.Members {
		names[relative] = true
	}
	for relative, write := range writes {
		if !softlinkDirectoryRelative(relative) {
			return importSnapshot{}, nil, bad("REVIEW_METADATA_CONTEXT", "共享目录旁车写入越界")
		}
		names[relative] = true
		for parent, stamp := range write.Parents {
			if old, ok := candidate.Directories[parent]; ok && old != stamp {
				return importSnapshot{}, nil, bad("REVIEW_METADATA_CONTEXT", "共享目录旁车父节点归属不同")
			}
			candidate.Directories[parent] = stamp
		}
	}
	entries := []originalDirectoryEntry{}
	for relative, expected := range candidate.Directories {
		dir, err := importDir(filepath.Join(root, relative), false)
		if err != nil {
			return importSnapshot{}, nil, err
		}
		info, err := dir.Stat()
		dir.Close()
		if err != nil {
			return importSnapshot{}, nil, err
		}
		stamp, err := originalDirectoryIdentity(info)
		if err != nil || !sameInode(stamp, expected) {
			return importSnapshot{}, nil, bad("REVIEW_METADATA_CONTEXT", "共享目录旁车父节点发生变化")
		}
		entries = append(entries, originalDirectoryEntry{Path: relative, Directory: true, Snapshot: sharedDirectoryIdentity(stamp)})
	}
	for relative := range names {
		member := prior.Members[relative]
		stamp, err := snapshotPath(filepath.Join(root, relative))
		if err != nil && member.Softlink != nil {
			stamp, err = snapshotOwnedTransferPath(filepath.Join(root, relative), member.Snapshot, member.Softlink, false)
		}
		if os.IsNotExist(err) {
			if _, owned := prior.Members[relative]; !owned {
				continue
			}
		}
		if err != nil {
			return importSnapshot{}, nil, err
		}
		entries = append(entries, originalDirectoryEntry{Path: relative, Snapshot: stamp})
		if _, written := writes[relative]; written && stamp != member.Snapshot {
			member.Softlink = nil
		}
		member.Snapshot = stamp
		candidate.Members[relative] = member
	}
	actual, err := snapshotSharedDirectory(root, importSnapshot{}, candidate, true)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return actual, entries, err
}

func refreshMetadataSharedDirectory(prior *importSharedDirectoryProof, entries []originalDirectoryEntry, writes map[string]originalMetadataDirectoryWrite) (*importSharedDirectoryProof, error) {
	next := &importSharedDirectoryProof{Root: prior.Root, Directories: map[string]importSnapshot{}, CreatedDirectories: map[string]bool{}, Members: map[string]importSharedDirectoryMember{}}
	for path, created := range prior.CreatedDirectories {
		next.CreatedDirectories[path] = created
	}
	for _, entry := range entries {
		if entry.Directory {
			next.Directories[entry.Path] = sharedDirectoryIdentity(entry.Snapshot)
			continue
		}
		if _, written := writes[entry.Path]; written {
			next.Members[entry.Path] = importSharedDirectoryMember{Snapshot: entry.Snapshot}
			continue
		}
		member, ok := prior.Members[entry.Path]
		if !ok || member.Snapshot != entry.Snapshot {
			return nil, bad("REVIEW_METADATA_SOURCE_CHANGED", "共享目录包含未确认写入")
		}
		next.Members[entry.Path] = member
	}
	return next, nil
}
