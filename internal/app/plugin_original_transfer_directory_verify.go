package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// A directory result can select its transferred members, but it cannot claim
// unrelated source bytes, escape the returned root or conceal target members.
func verifyOriginalDirectoryTransfer(ctx context.Context, file PlanFile, info map[string]any, check func() error) (string, error) {
	_, sources, err := snapshotOriginalDirectory(file.Source)
	if err != nil {
		return "", err
	}
	_, targets, err := snapshotOriginalDirectory(file.Target)
	if err != nil {
		return "", err
	}
	sourceFiles, targetFiles := map[string]importSnapshot{}, map[string]importSnapshot{}
	for _, entry := range sources {
		if !entry.Directory {
			sourceFiles[filepath.Join(file.Source, entry.Path)] = entry.Snapshot
		}
	}
	for _, entry := range targets {
		if !entry.Directory {
			targetFiles[filepath.Join(file.Target, entry.Path)] = entry.Snapshot
		}
	}
	var oldPaths, newPaths []string
	for key, out := range map[string]*[]string{"file_list": &oldPaths, "file_list_new": &newPaths} {
		raw, e := json.Marshal(info[key])
		if e != nil || json.Unmarshal(raw, out) != nil {
			return "", bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录结果的文件清单无法核对")
		}
	}
	if len(oldPaths) == 0 && len(newPaths) == 0 || len(oldPaths) == 1 && len(newPaths) == 1 && strings.TrimSuffix(oldPaths[0], "/") == file.Source && strings.TrimSuffix(newPaths[0], "/") == file.Target {
		oldPaths, newPaths = nil, nil
		for path := range sourceFiles {
			oldPaths = append(oldPaths, path)
		}
		sort.Strings(oldPaths)
		for _, path := range oldPaths {
			relative, _ := filepath.Rel(file.Source, path)
			newPaths = append(newPaths, filepath.Join(file.Target, relative))
		}
	}
	if len(oldPaths) != len(newPaths) || len(newPaths) != len(targetFiles) {
		return "", bad("REVIEW_PLUGIN_TRANSFER_TARGET", "目录结果未完整描述实际目标文件")
	}
	seen := map[string]bool{}
	digests := []string{}
	for i, path := range oldPaths {
		target := newPaths[i]
		source, ok := sourceFiles[path]
		actual, exists := targetFiles[target]
		if !ok || !exists || seen[target] || source.Size != actual.Size {
			return "", bad("REVIEW_COPY_SIZE", "目录结果的来源、目标或大小不一致")
		}
		seen[target] = true
		if file.Method == "hardlink" {
			if !sameInode(source, actual) {
				return "", bad("REVIEW_PLUGIN_TRANSFER_METHOD", "目录成员不符合所选硬链接方式")
			}
			continue
		}
		if err := check(); err != nil {
			return "", err
		}
		input, e := importOpen(path)
		if e != nil {
			return "", e
		}
		sum, e := importCopy(ctx, input, io.Discard, check)
		input.Close()
		if e != nil {
			return "", e
		}
		output, e := importOpen(target)
		if e != nil {
			return "", e
		}
		other, e := importCopy(ctx, output, io.Discard, check)
		output.Close()
		if e != nil {
			return "", e
		}
		if sum != other {
			return "", bad("REVIEW_COPY_CONTENT", "目录成员内容与冻结源文件不同")
		}
		digests = append(digests, target+":"+sum)
	}
	if file.Method == "hardlink" {
		return "", nil
	}
	sort.Strings(digests)
	raw, _ := json.Marshal(digests)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
