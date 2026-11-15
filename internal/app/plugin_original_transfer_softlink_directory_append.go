package app

import (
	"context"
	"path/filepath"
	"strings"
)

// Incremental external directory softlink: MoviePilot writes the planned members
// into an existing destination. Only links this call actually created become
// owned members; entries that already existed are validated as unchanged and
// stay with their previous owner (if any). Directories created by this call are
// recorded so history deletion removes only what this publication owns.
func (a *App) adoptExistingExternalSoftlinkDirectory(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, receipt importReceipt, model map[string]any, check func() error) (importReceipt, error) {
	root, err := a.importRoot(plan.RootID)
	if err != nil || root.Path != meta.RootPath || receipt.ID != plan.ID+"/"+file.ID || receipt.Source != file.Source || receipt.SourceSnapshot != planSnapshot(file) || receipt.Target != file.Target || !within(meta.RootPath, file.Target) || file.DirectoryDigest == "" || file.Method != "softlink" {
		return receipt, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "外部目录软链接偏离调用前冻结路径")
	}
	mapping, sources, err := originalSoftlinkDirectoryMappings(receipt, model)
	if err != nil {
		return receipt, err
	}
	if actual, _, err := snapshotOriginalDirectory(receipt.Source); err != nil || actual != receipt.SourceSnapshot {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "目录软链接调用期间原树已变化")
	}
	actual, entries, links, err := scanSoftlinkDirectory(receipt.Target)
	if err != nil {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "外部目录软链接目标须为真实目录节点")
	}
	existing := receipt.OriginalSoftlinkDirectory.Existing
	if existing == nil || !sameInode(actual, existing.Root) {
		return receipt, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "目录软链接目标根已变化")
	}
	before := map[string]originalDirectoryEntry{}
	for _, entry := range existing.Entries {
		before[entry.Path] = entry
	}
	now := map[string]originalDirectoryEntry{}
	for _, entry := range entries {
		now[entry.Path] = entry
	}
	for relative, prior := range before {
		current, ok := now[relative]
		if !ok || prior.Directory != current.Directory {
			return receipt, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "既有目录条目消失或被替换")
		}
		if current.Directory {
			if !sameInode(prior.Snapshot, current.Snapshot) {
				return receipt, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "既有子目录身份已变化")
			}
			continue
		}
		if existing.Links[relative] != links[relative] || existing.Links[relative] == "" && prior.Snapshot != current.Snapshot {
			return receipt, bad("REVIEW_PLUGIN_SOFTLINK_TARGET", "既有目录成员被改动")
		}
	}
	proof := &importSoftlinkDirectoryProof{Root: softlinkDirectoryIdentity(actual), Directories: map[string]importSnapshot{}, Members: map[string]importSoftlinkDirectoryMember{}, Existing: existing}
	created := []string{}
	for relative, source := range mapping {
		current, ok := now[relative]
		if prior, existed := before[relative]; existed {
			if prior.Directory || existing.Links[relative] != source || links[relative] != source {
				return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "既有成员不是指向对应原文件的软链接")
			}
			continue
		}
		if !ok || links[relative] != source || current.Directory {
			return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "目录成员不是指向对应原文件的真实软链接")
		}
		leaf := &importSoftlinkProof{Source: source, SourceSnapshot: sources[source]}
		if _, err = snapshotOwnedTransferPath(filepath.Join(receipt.Target, relative), current.Snapshot, leaf, true); err != nil {
			return receipt, err
		}
		proof.Members[relative] = importSoftlinkDirectoryMember{Snapshot: current.Snapshot, Softlink: leaf}
		for dir := filepath.Dir(relative); dir != "."; dir = filepath.Dir(dir) {
			if _, existed := before[dir]; existed {
				continue
			}
			if _, ok := proof.Directories[dir]; ok {
				continue
			}
			stamp, ok := now[dir]
			if !ok || !stamp.Directory {
				return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录软链接父目录缺失")
			}
			proof.Directories[dir] = stamp.Snapshot
			created = append(created, filepath.Join(receipt.Target, dir))
		}
	}
	for relative, current := range now {
		if _, prior := before[relative]; prior {
			continue
		}
		if _, mapped := mapping[relative]; mapped {
			continue
		}
		if _, recorded := proof.Directories[relative]; recorded {
			continue
		}
		if current.Directory {
			empty := true
			for other := range now {
				if strings.HasPrefix(other, relative+string(filepath.Separator)) {
					empty = false
					break
				}
			}
			if empty {
				continue // an empty provider directory is a side effect, not ownership
			}
		}
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录软链接出现映射外新增条目")
	}
	if err = check(); err != nil {
		return receipt, err
	}
	for i := len(created) - 1; i >= 0; i-- {
		if err = syncOriginalSoftlinkDirectory(created[i]); err != nil {
			return receipt, err
		}
	}
	for _, dir := range []string{receipt.Target, filepath.Dir(receipt.Target)} {
		if err = syncOriginalSoftlinkDirectory(dir); err != nil {
			return receipt, err
		}
	}
	if source, _, err := snapshotOriginalDirectory(receipt.Source); err != nil || source != receipt.SourceSnapshot {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "目录软链接核验期间原树已变化")
	}
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
