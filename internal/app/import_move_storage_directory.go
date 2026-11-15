package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
)

// Source's tree and storage identity precede all destructive member moves.
// Existing DirectoryFiles/Members/Listed preserve provider order and FileItems;
// each existing child receipt still owns that member's publication/recovery.
type importDirectoryStorageMoveState struct {
	InstanceID string                   `json:"instance_id"`
	Key        string                   `json:"key"`
	Entries    []originalDirectoryEntry `json:"entries"`
}
type importDirectoryStorageMoveKey struct{}

func directoryStorageMoveEntries(receipt importReceipt) ([]originalDirectoryEntry, error) {
	proof := receipt.DirectoryStorageMove
	if proof == nil || proof.InstanceID == "" || proof.Key == "" || receipt.SourceSnapshot.Directory == "" {
		return nil, bad("REVIEW_MOVE_STORAGE", "目录存储移动缺少原树和存储身份")
	}
	raw, err := json.Marshal(proof.Entries)
	digest := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(digest[:]) != receipt.SourceSnapshot.Directory {
		return nil, bad("REVIEW_MOVE_STORAGE", "目录存储移动成员与冻结源树不符")
	}
	if receipt.DirectoryListed {
		if err = validateDirectoryStorageMoveList(proof.Entries, receipt.DirectoryFiles, receipt.DirectoryMembers); err != nil {
			return nil, err
		}
	}
	return proof.Entries, nil
}

func guardDirectoryStorageMoveRoot(receipt importReceipt) error {
	dir, err := importDir(receipt.Source, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	stamp, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(stamp, receipt.SourceSnapshot) {
		return bad("REVIEW_SOURCE_CHANGED", "目录存储移动的原根已被替换")
	}
	return nil
}

func (a *App) freezeDirectoryStorageMoveMembers(ctx context.Context, receipt *importReceipt) error {
	pair := originalTransferStorageFromContext(ctx)
	proof := receipt.DirectoryStorageMove
	if pair == nil || pair.Source == nil || pair.Source.definition.InstanceID != proof.InstanceID || pair.Source.definition.Key != proof.Key {
		return bad("REVIEW_MOVE_STORAGE", "目录移动的存储对象与原选择不同")
	}
	files := []string{}
	members := map[string]originalDirectoryMember{}
	err := a.walkOriginalDirectory(ctx, receipt.Source, func(relative, target string, item *plugins.ModuleFileItem) error {
		sealed, err := a.Store.Seal(item)
		if err != nil {
			return err
		}
		files = append(files, relative)
		members[relative] = originalDirectoryMember{Target: target, FileItem: sealed}
		return nil
	})
	if err != nil {
		return err
	}
	if err = validateDirectoryStorageMoveList(proof.Entries, files, members); err != nil {
		return err
	}
	actual, err := snapshotTransferPath(receipt.Source)
	if err != nil || actual != receipt.SourceSnapshot {
		return bad("REVIEW_SOURCE_CHANGED", "存储目录列表冻结期间源树发生变化")
	}
	receipt.DirectoryFiles, receipt.DirectoryMembers, receipt.DirectoryListed = files, members, true
	return a.Store.Put(ctx, "import_receipt", receipt.ID, *receipt)
}

// Freeze a complete physical tree, not a provider's accidentally partial list.
// Otherwise moving only the listed files could be misreported as a whole-disc move.
func validateDirectoryStorageMoveList(entries []originalDirectoryEntry, files []string, members map[string]originalDirectoryMember) error {
	remaining := map[string]bool{}
	for _, entry := range entries {
		if !entry.Directory {
			remaining[entry.Path] = true
		}
	}
	targets := map[string]bool{}
	for _, relative := range files {
		member, ok := members[relative]
		if !remaining[relative] || !ok || member.Target == "." || filepath.IsAbs(member.Target) || filepath.Clean(member.Target) != member.Target || member.Target == ".." || strings.HasPrefix(member.Target, "../") || targets[member.Target] {
			return bad("REVIEW_MOVE_STORAGE", "存储目录列表包含遗漏、重复或越界成员")
		}
		delete(remaining, relative)
		targets[member.Target] = true
	}
	if len(remaining) != 0 || len(files) != len(members) {
		return bad("REVIEW_MOVE_STORAGE", "存储目录列表未覆盖冻结源树的所有文件")
	}
	for target := range targets {
		for parent := filepath.Dir(target); parent != "."; parent = filepath.Dir(parent) {
			if targets[parent] {
				return bad("REVIEW_MOVE_STORAGE", "存储目录文件名与成员目录路径冲突")
			}
		}
	}
	return nil
}

// Scope a new member call to the same storage object selected for its parent.
// Completed child receipts bypass selection and never reopen a new source.
func directoryMoveStorage(ctx context.Context) (*originalTransferStorage, error) {
	proof, _ := ctx.Value(importDirectoryStorageMoveKey{}).(*importDirectoryStorageMoveState)
	pair := originalTransferStorageFromContext(ctx)
	if proof == nil || pair == nil || pair.Source == nil || pair.Source.definition.InstanceID != proof.InstanceID || pair.Source.definition.Key != proof.Key {
		return nil, bad("REVIEW_MOVE_STORAGE", "尚未移动成员的存储对象与原选择不同")
	}
	return pair.Source, nil
}

func storageMoveFileItem(ctx context.Context, source string) (*plugins.ModuleFileItem, error) {
	if member, ok := ctx.Value(originalDirectorySourceItemKey{}).(*plugins.ModuleFileItem); ok {
		var path string
		if member == nil || json.Unmarshal(member.Fields["path"], &path) != nil || filepath.Clean(path) != source {
			return nil, bad("REVIEW_RECEIPT", "目录移动文件项与冻结源路径不同")
		}
		return member, nil
	}
	return originalLocalFileItem(source)
}
