package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Keep the pre-call source inventory separate from the destination entry proof.
// Permission uses the ordinary external-file contract without classifying a
// directory as a leaf or inventing per-child native publication receipts.
type originalSoftlinkDirectoryProof struct {
	Permission originalSoftlinkProof            `json:"permission"`
	Entries    []originalDirectoryEntry         `json:"entries"`
	Existing   *importSoftlinkDirectoryExisting `json:"existing,omitempty"`
}

func (a *App) freezeOriginalPluginDirectorySoftlink(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, receipt *importReceipt) error {
	root, err := a.importRoot(plan.RootID)
	if err != nil || root.Path != meta.RootPath || !importPath(file.Source) || !importPath(file.Target) || !within(root.Path, file.Target) || file.Target == root.Path || file.Target == file.Source || within(file.Source, file.Target) || within(file.Target, file.Source) || file.DirectoryDigest == "" || receipt.State != "planned" || receipt.OriginalModuleStarted {
		return bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "目录软链接调用前路径或计划证明不符")
	}
	source, entries, err := snapshotOriginalDirectory(file.Source)
	if err != nil || source != planSnapshot(file) || source != receipt.SourceSnapshot {
		return bad("REVIEW_SOURCE_CHANGED", "目录软链接调用前原树已变化")
	}
	stream, err := a.originalDirectoryStreamSize(ctx, file.Source)
	if err != nil {
		return err
	}
	parent, err := importDir(filepath.Dir(file.Target), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Stat()
	if err != nil {
		return err
	}
	stamp, err := originalDirectoryIdentity(info)
	if err != nil {
		return err
	}
	absent := false
	var existing *importSoftlinkDirectoryExisting
	if _, err = os.Lstat(file.Target); errors.Is(err, os.ErrNotExist) {
		absent = true
	} else if err != nil {
		return err
	} else {
		// MoviePilot writes planned members into an existing destination too.
		// Freeze the pre-call entries and adopt only newly created links; the
		// preserved entries stay unowned by this publication.
		var owner managedFile
		if e := a.Store.Get(ctx, "managed_file", tokenHash(file.Target), &owner); e == nil {
			if e = a.admitOwnedDirectoryAppend(ctx, receipt, file, owner); e != nil {
				return e
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		actual, existingEntries, existingLinks, e := scanSoftlinkDirectory(file.Target)
		if e != nil || actual.Inode == 0 {
			return bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "已有目标不是可增量发布的真实目录")
		}
		existing = &importSoftlinkDirectoryExisting{Root: sharedDirectoryIdentity(actual), Entries: existingEntries, Links: existingLinks}
	}
	if after, _, err := snapshotOriginalDirectory(file.Source); err != nil || after != source {
		return bad("REVIEW_SOURCE_CHANGED", "目录软链接冻结期间原树已变化")
	}
	if absent {
		receipt.Softlink = &importSoftlinkProof{Source: file.Source, SourceSnapshot: source}
	} else {
		receipt.Softlink = nil
	}
	receipt.OriginalSoftlinkDirectory = &originalSoftlinkDirectoryProof{Permission: originalSoftlinkProof{Target: file.Target, TargetAbsent: absent, Parent: stamp}, Entries: entries, Existing: existing}
	receipt.DirectoryStreamSize = stream
	return nil
}

func originalSoftlinkDirectorySources(receipt importReceipt) (map[string]importSnapshot, error) {
	invalid := func() (map[string]importSnapshot, error) {
		return nil, bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "目录软链接调用前成员证明与原树不符")
	}
	proof := receipt.OriginalSoftlinkDirectory
	if proof == nil || receipt.SourceSnapshot.Directory == "" {
		return invalid()
	}
	sources, seen := map[string]importSnapshot{}, map[string]bool{}
	var size int64
	for _, entry := range proof.Entries {
		if !softlinkDirectoryRelative(entry.Path) || seen[entry.Path] || entry.Snapshot.Inode == 0 || entry.Snapshot.Directory != "" {
			return invalid()
		}
		seen[entry.Path] = true
		if !entry.Directory {
			sources[filepath.Join(receipt.Source, entry.Path)] = entry.Snapshot
			size += entry.Snapshot.Size
		}
	}
	raw, err := json.Marshal(proof.Entries)
	digest := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(digest[:]) != receipt.SourceSnapshot.Directory || size != receipt.SourceSnapshot.Size {
		return invalid()
	}
	return sources, nil
}

// A sealed provider result must describe a bijection over every original leaf.
// Empty source directories are intentionally not destination members.
func originalSoftlinkDirectoryMappings(receipt importReceipt, model map[string]any) (map[string]string, map[string]importSnapshot, error) {
	sources, err := originalSoftlinkDirectorySources(receipt)
	if err != nil {
		return nil, nil, err
	}
	item := sourceMap(model["target_item"])
	if sourceText(model["transfer_type"]) != "softlink" || item == nil || sourceText(item["storage"]) != "local" || sourceText(item["type"]) != "dir" {
		return nil, nil, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "目录软链接结果需明确本地目录及整理方式")
	}
	permission := receipt.OriginalSoftlinkDirectory.Permission
	existing := receipt.OriginalSoftlinkDirectory.Existing
	if permission.Target != receipt.Target || strings.TrimSuffix(sourceText(item["path"]), "/") != receipt.Target || !receipt.OriginalModuleStarted || len(receipt.OriginalModuleResult) == 0 {
		return nil, nil, bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "目录软链接结果缺少精确调用前根目录与来源证明")
	}
	if existing == nil {
		if !permission.TargetAbsent || receipt.Softlink == nil || receipt.Softlink.Source != receipt.Source || receipt.Softlink.SourceSnapshot != receipt.SourceSnapshot {
			return nil, nil, bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "目录软链接结果缺少精确调用前根目录与来源证明")
		}
	} else if permission.TargetAbsent || existing.Root.Inode == 0 {
		return nil, nil, bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "已有目录增量发布的调用前证明无效")
	}
	parent, err := importDir(filepath.Dir(receipt.Target), false)
	if err != nil {
		return nil, nil, err
	}
	info, err := parent.Stat()
	parent.Close()
	if err != nil {
		return nil, nil, err
	}
	actual, err := originalDirectoryIdentity(info)
	if err != nil || !sameInode(actual, permission.Parent) {
		return nil, nil, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "目录软链接父目录已变化")
	}
	if existing != nil {
		root, _, _, rootErr := scanSoftlinkDirectory(receipt.Target)
		if rootErr != nil || !sameInode(root, existing.Root) {
			return nil, nil, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "目录软链接目标根已变化")
		}
	}
	var oldPaths, newPaths []string
	for name, out := range map[string]*[]string{"file_list": &oldPaths, "file_list_new": &newPaths} {
		raw, err := json.Marshal(model[name])
		if err != nil || json.Unmarshal(raw, out) != nil || *out == nil {
			return nil, nil, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录软链接需返回完整逐成员双向路径清单")
		}
	}
	if len(oldPaths) != len(sources) || len(newPaths) != len(sources) {
		return nil, nil, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录软链接路径清单缺失原文件")
	}
	mapping, seen := map[string]string{}, map[string]bool{}
	for index, source := range oldPaths {
		target := newPaths[index]
		_, exists := sources[source]
		rel, err := filepath.Rel(receipt.Target, target)
		if !exists || seen[source] || !importPath(target) || err != nil || !softlinkDirectoryRelative(rel) || mapping[rel] != "" {
			return nil, nil, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录软链接映射重复、越界或包含未知成员")
		}
		mapping[rel], seen[source] = source, true
	}
	return mapping, sources, nil
}

func verifyOriginalPluginDirectorySoftlink(receipt importReceipt, model map[string]any) error {
	mapping, sources, err := originalSoftlinkDirectoryMappings(receipt, model)
	if err != nil {
		return err
	}
	if receipt.State != "published" || !receipt.OriginalSoftlinkDirectory.Permission.Adopted || receipt.Softlink.Directory == nil {
		return bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "外部目录软链接尚未持久认领")
	}
	if _, err = snapshotOwnedSoftlinkDirectory(receipt.Target, receipt.StageSnapshot, receipt.Softlink, true); err != nil {
		return err
	}
	existing := receipt.Softlink.Directory.Existing
	for rel, source := range mapping {
		member, ok := receipt.Softlink.Directory.Members[rel]
		if !ok {
			if existing != nil && existing.Links[rel] == source {
				continue // preserved pre-call link, owned elsewhere or unmanaged
			}
			return bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "已认领目录缺少原始映射成员")
		}
		if member.Softlink != nil && (member.Softlink.Source != source || member.Softlink.SourceSnapshot != sources[source]) {
			return bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "已认领目录成员偏离封存原始映射")
		}
		// A nil proof can only come from the later explicit metadata-write rebase.
	}
	for rel, member := range receipt.Softlink.Directory.Members {
		if member.Softlink != nil && mapping[rel] == "" {
			return bad("REVIEW_PLUGIN_SOFTLINK_PROOF", "已认领目录出现原映射之外的链接")
		}
	}
	return nil
}

func (a *App) adoptOriginalPluginDirectorySoftlink(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, receipt importReceipt, model map[string]any, check func() error) (importReceipt, error) {
	root, err := a.importRoot(plan.RootID)
	if err != nil || root.Path != meta.RootPath || receipt.ID != plan.ID+"/"+file.ID || receipt.Source != file.Source || receipt.SourceSnapshot != planSnapshot(file) || receipt.Target != file.Target || !within(meta.RootPath, file.Target) || file.DirectoryDigest == "" || file.Method != "softlink" {
		return receipt, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "外部目录软链接偏离调用前冻结路径")
	}
	mapping, sources, err := originalSoftlinkDirectoryMappings(receipt, model)
	if err != nil {
		return receipt, err
	}
	if receipt.OriginalSoftlinkDirectory != nil && receipt.OriginalSoftlinkDirectory.Existing != nil {
		return a.adoptExistingExternalSoftlinkDirectory(ctx, plan, file, meta, receipt, model, check)
	}
	if actual, _, err := snapshotOriginalDirectory(receipt.Source); err != nil || actual != receipt.SourceSnapshot {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "目录软链接调用期间原树已变化")
	}
	actual, entries, links, err := scanSoftlinkDirectory(receipt.Target)
	if err != nil {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "外部目录软链接目标须为真实目录节点")
	}
	proof := &importSoftlinkDirectoryProof{Root: softlinkDirectoryIdentity(actual), Directories: map[string]importSnapshot{}, Members: map[string]importSoftlinkDirectoryMember{}}
	needed := map[string]bool{}
	for rel := range mapping {
		for dir := filepath.Dir(rel); dir != "."; dir = filepath.Dir(dir) {
			needed[dir] = true
		}
	}
	dirs := []string{}
	for _, entry := range entries {
		if entry.Directory {
			if !needed[entry.Path] {
				return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录软链接包含映射外或空目录")
			}
			proof.Directories[entry.Path] = entry.Snapshot
			dirs = append(dirs, filepath.Join(receipt.Target, entry.Path))
			continue
		}
		source, ok := mapping[entry.Path]
		if !ok || links[entry.Path] != source {
			return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "目录成员不是指向对应原文件的真实软链接")
		}
		leaf := &importSoftlinkProof{Source: source, SourceSnapshot: sources[source]}
		if _, err = snapshotOwnedTransferPath(filepath.Join(receipt.Target, entry.Path), entry.Snapshot, leaf, true); err != nil {
			return receipt, err
		}
		proof.Members[entry.Path] = importSoftlinkDirectoryMember{Snapshot: entry.Snapshot, Softlink: leaf}
	}
	if len(proof.Members) != len(mapping) || len(proof.Directories) != len(needed) {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "实际目录软链接成员与完整映射不符")
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err = syncOriginalSoftlinkDirectory(dirs[i]); err != nil {
			return receipt, err
		}
	}
	for _, dir := range []string{receipt.Target, filepath.Dir(receipt.Target)} {
		if err = syncOriginalSoftlinkDirectory(dir); err != nil {
			return receipt, err
		}
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if source, _, err := snapshotOriginalDirectory(receipt.Source); err != nil || source != receipt.SourceSnapshot {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "目录软链接核验期间原树已变化")
	}
	// Use copies so a failed store does not mutate the caller's pre-call permit.
	permission := *receipt.OriginalSoftlinkDirectory
	permission.Permission.Adopted = true
	receipt.OriginalSoftlinkDirectory = &permission
	receipt.Softlink = &importSoftlinkProof{Source: receipt.Source, SourceSnapshot: receipt.SourceSnapshot, Directory: proof}
	receipt.StageSnapshot, receipt.State = actual, "published"
	if err = verifyOriginalPluginDirectorySoftlink(receipt, model); err != nil {
		return receipt, err
	}
	return receipt, a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
}

func syncOriginalSoftlinkDirectory(path string) error {
	dir, err := importDir(path, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
