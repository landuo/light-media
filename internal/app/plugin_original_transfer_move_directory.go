package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// Persist the original tree before dispatching a destructive external transfer.
// Adopted distinguishes an external result from a provider's native fallback.
// No per-member native cleanup receipt is invented for an external operation.
type originalMoveDirectoryProof struct {
	Entries []originalMoveDirectoryEntry `json:"entries"`
	Adopted bool                         `json:"adopted,omitempty"`
}

type originalMoveDirectoryEntry struct {
	Path      string         `json:"path"`
	Directory bool           `json:"directory"`
	Snapshot  importSnapshot `json:"snapshot"`
	SHA256    string         `json:"sha256,omitempty"`
}

func freezeOriginalMoveDirectory(ctx context.Context, file PlanFile, check func() error) (*originalMoveDirectoryProof, error) {
	before, entries, err := snapshotOriginalDirectory(file.Source)
	if err != nil || before != planSnapshot(file) {
		return nil, bad("REVIEW_SOURCE_CHANGED", "插件目录移动调用前的源目录已变化")
	}
	proof := &originalMoveDirectoryProof{Entries: make([]originalMoveDirectoryEntry, 0, len(entries))}
	for _, entry := range entries {
		member := originalMoveDirectoryEntry{Path: entry.Path, Directory: entry.Directory, Snapshot: entry.Snapshot}
		if !entry.Directory {
			member.SHA256, err = originalMoveFileDigest(ctx, filepath.Join(file.Source, entry.Path), entry.Snapshot, check, false)
			if err != nil {
				return nil, err
			}
		}
		proof.Entries = append(proof.Entries, member)
	}
	if after, _, err := snapshotOriginalDirectory(file.Source); err != nil || after != before {
		return nil, bad("REVIEW_SOURCE_CHANGED", "插件目录移动源在冻结内容期间发生变化")
	}
	return proof, nil
}

func originalMoveDirectoryFiles(file PlanFile, proof *originalMoveDirectoryProof) (map[string]originalMoveDirectoryEntry, error) {
	invalid := func() (map[string]originalMoveDirectoryEntry, error) {
		return nil, bad("REVIEW_PLUGIN_MOVE_PROOF", "插件目录移动缺少与原目录绑定的完整成员证明")
	}
	if proof == nil || file.DirectoryDigest == "" {
		return invalid()
	}
	entries := make([]originalDirectoryEntry, 0, len(proof.Entries))
	files, seen := map[string]originalMoveDirectoryEntry{}, map[string]bool{}
	var size int64
	for _, entry := range proof.Entries {
		path := filepath.Join(file.Source, entry.Path)
		if entry.Path == "." || filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || !within(file.Source, path) || seen[entry.Path] || entry.Snapshot.Directory != "" {
			return invalid()
		}
		seen[entry.Path] = true
		entries = append(entries, originalDirectoryEntry{Path: entry.Path, Directory: entry.Directory, Snapshot: entry.Snapshot})
		if !entry.Directory {
			digest, err := hex.DecodeString(entry.SHA256)
			if err != nil || len(digest) != sha256.Size || entry.Snapshot.Size < 0 {
				return invalid()
			}
			files[path] = entry
			size += entry.Snapshot.Size
		}
	}
	raw, err := json.Marshal(entries)
	digest := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(digest[:]) != file.DirectoryDigest || size != file.Size {
		return invalid()
	}
	return files, nil
}

// Require explicit member mapping from TransferInfo and verify all actual
// destination files. A root-only pair is not an invented per-file move result.
func verifyOriginalMoveDirectoryTarget(ctx context.Context, file PlanFile, proof *originalMoveDirectoryProof, model map[string]any, check func() error, syncFiles bool) (importSnapshot, string, error) {
	sources, err := originalMoveDirectoryFiles(file, proof)
	if err != nil {
		return importSnapshot{}, "", err
	}
	actual, entries, err := snapshotOriginalDirectory(file.Target)
	if err != nil {
		return importSnapshot{}, "", bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件目录移动的实际目标目录不可核对")
	}
	targets := map[string]importSnapshot{}
	for _, entry := range entries {
		if !entry.Directory {
			targets[filepath.Join(file.Target, entry.Path)] = entry.Snapshot
		}
	}
	var oldPaths, newPaths []string
	for name, out := range map[string]*[]string{"file_list": &oldPaths, "file_list_new": &newPaths} {
		raw, err := json.Marshal(model[name])
		if err != nil || json.Unmarshal(raw, out) != nil {
			return actual, "", bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件目录移动未返回逐成员路径清单")
		}
	}
	if len(oldPaths) != len(sources) || len(newPaths) != len(sources) || len(targets) != len(sources) {
		return actual, "", bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件目录移动清单与实际成员不一致")
	}
	seenSource, seenTarget := map[string]bool{}, map[string]bool{}
	digests := []string{}
	for index, source := range oldPaths {
		target := newPaths[index]
		member, exists := sources[source]
		stamp, found := targets[target]
		if !exists || !found || seenSource[source] || seenTarget[target] || member.Snapshot.Size != stamp.Size {
			return actual, "", bad("REVIEW_COPY_SIZE", "插件目录移动成员的来源、目标或大小不一致")
		}
		seenSource[source], seenTarget[target] = true, true
		digest, err := originalMoveFileDigest(ctx, target, stamp, check, syncFiles)
		if err != nil {
			return actual, "", err
		}
		if digest != member.SHA256 {
			return actual, "", bad("REVIEW_COPY_CONTENT", "插件目录移动成员内容与调用前源证明不同")
		}
		digests = append(digests, target+":"+digest)
	}
	if syncFiles {
		// Children before parents makes created file/directory entries durable.
		dirs := []string{}
		for index := len(entries) - 1; index >= 0; index-- {
			if entries[index].Directory {
				dirs = append(dirs, filepath.Join(file.Target, entries[index].Path))
			}
		}
		dirs = append(dirs, file.Target, filepath.Dir(file.Target))
		for _, path := range dirs {
			dir, err := importDir(path, false)
			if err != nil {
				return actual, "", err
			}
			err = dir.Sync()
			dir.Close()
			if err != nil {
				return actual, "", err
			}
		}
	}
	if after, _, err := snapshotOriginalDirectory(file.Target); err != nil || after != actual {
		return actual, "", bad("REVIEW_TARGET_CHANGED", "插件目录移动目标在核验期间发生变化")
	}
	sort.Strings(digests)
	raw, _ := json.Marshal(digests)
	digest := sha256.Sum256(raw)
	return actual, hex.EncodeToString(digest[:]), nil
}

func verifyOriginalPluginDirectoryMove(ctx context.Context, file PlanFile, receipt importReceipt, model map[string]any, check func() error) error {
	if receipt.State != "published" || receipt.DirectoryMoveState != "completed" || receipt.OriginalMoveDirectory == nil || !receipt.OriginalMoveDirectory.Adopted || sourceText(model["transfer_type"]) != "move" {
		return bad("REVIEW_PLUGIN_MOVE_PROOF", "插件目录移动缺少已确认的结果证明")
	}
	actual, digest, err := verifyOriginalMoveDirectoryTarget(ctx, file, receipt.OriginalMoveDirectory, model, check, false)
	if err != nil {
		return err
	}
	if actual != receipt.StageSnapshot || digest != receipt.SHA256 {
		return bad("REVIEW_TARGET_CHANGED", "插件目录移动目标与已发布结果不同")
	}
	return nil
}

func (a *App) adoptOriginalPluginDirectoryMove(ctx context.Context, plan ImportPlan, file PlanFile, meta importMetadata, receipt importReceipt, model map[string]any, check func() error, adopt func(importReceipt) error) (importReceipt, error) {
	if file.Method != "move" || file.DirectoryDigest == "" || sourceText(model["transfer_type"]) != "move" {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "插件返回的整理方式与目录移动不同")
	}
	if receipt.ID != plan.ID+"/"+file.ID || receipt.Source != file.Source || receipt.SourceSnapshot != planSnapshot(file) || receipt.OriginalMoveDirectory == nil || !receipt.OriginalModuleStarted || len(receipt.OriginalModuleResult) == 0 {
		return receipt, bad("REVIEW_PLUGIN_MOVE_PROOF", "插件目录移动缺少调用前成员证明")
	}
	root, err := a.importRoot(plan.RootID)
	if err != nil || root.Path != meta.RootPath || !importPath(file.Target) || !within(root.Path, file.Target) || file.Target == root.Path || file.Target == file.Source || within(file.Source, file.Target) || within(file.Target, file.Source) {
		return receipt, bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件目录移动目标超出原授权范围或与源目录重叠")
	}
	actual, digest, err := verifyOriginalMoveDirectoryTarget(ctx, file, receipt.OriginalMoveDirectory, model, check, true)
	if err != nil {
		return receipt, err
	}
	if err = check(); err != nil {
		return receipt, err
	}
	var residual *importSnapshot
	if _, err = os.Lstat(file.Source); err == nil {
		stamp, entries, err := snapshotOriginalDirectory(file.Source)
		if err != nil || !sameInode(stamp, receipt.SourceSnapshot) {
			return receipt, bad("REVIEW_SOURCE_CHANGED", "插件移动后的源目录已被替换或不可核对")
		}
		for _, entry := range entries {
			if !entry.Directory {
				return receipt, bad("REVIEW_PLUGIN_TRANSFER_METHOD", "插件目录移动仍保留源文件或出现了新文件")
			}
		}
		residual = &stamp
	} else if !errors.Is(err, os.ErrNotExist) {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "插件目录移动源路径的移除状态不可核对")
	}
	proof := *receipt.OriginalMoveDirectory
	proof.Adopted = true
	receipt.OriginalMoveDirectory = &proof
	receipt.StageSnapshot, receipt.SHA256, receipt.State = actual, digest, "published"
	receipt.DirectoryMoveState, receipt.MoveSourceSnapshot = "completed", residual
	if receipt.Target != file.Target {
		receipt.Target = file.Target
		return receipt, adopt(receipt)
	}
	return receipt, a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
}
