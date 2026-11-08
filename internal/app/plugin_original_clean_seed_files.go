package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"light-media/internal/releasemeta/legacyanime"
)

type originalCleanSeedFileScan struct {
	observed map[string]browserStamp
	trees    map[string][]browserStamp
}

type originalCleanSeedFileFact struct {
	Path  string  `json:"path"`
	Kind  string  `json:"kind"`
	Size  int64   `json:"size"`
	MTime float64 `json:"mtime"`
}

type originalCleanSeedFileList struct {
	SchemaVersion int                         `json:"schema_version"`
	Exists        bool                        `json:"exists"`
	Files         []originalCleanSeedFileFact `json:"files"`
}

func (c *originalCleanSeedCall) fileRoots() map[string][]string {
	roots := map[string][]string{}
	for _, line := range strings.Split(sourceText(c.config["download_dirs"]), "\n") {
		local, remote, ok := strings.Cut(line, ":")
		local = filepath.Clean(legacyanime.Strip(local))
		if !ok || !importPath(local) || local == "/" || browserInternal(local) {
			continue
		}
		if _, exists := roots[local]; !exists {
			roots[local] = nil
		}
		for _, item := range strings.Split(remote, ",") {
			if remote := legacyanime.Strip(item); remote != "" {
				roots[local] = append(roots[local], remote)
			}
		}
	}
	return roots
}

func (a *App) originalCleanSeedFileAllowed(call *originalCleanSeedCall, path string, write bool) bool {
	if !call.filesAllowed(write) || a.originalMetadataAllowed(path) != nil ||
		within(a.Dir, path) || within(path, a.Dir) {
		return false
	}
	roots := call.fileRoots()
	if !write {
		for root := range roots {
			if within(root, path) {
				return true
			}
		}
		return false
	}
	for root := range roots {
		if within(path, root) {
			return false
		}
	}
	for _, root := range a.browserRoots() {
		if within(path, root.Path) {
			return false
		}
	}
	// The source only processes immediate children of an explicitly mapped
	// root. An incomplete mapping cannot authorize a physical write.
	return len(roots[filepath.Dir(path)]) > 0
}

func (a *App) originalCleanSeedFiles(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Reset     bool   `json:"reset"`
	}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	call := scope.cleanSeed
	if request.Reset {
		if request.Path != "" || request.Recursive || !call.filesAllowed(false) {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "当前调用未获无效文件扫描授权")
		}
		call.files = originalCleanSeedFileScan{}
		return json.RawMessage("true"), nil
	}
	if !a.originalCleanSeedFileAllowed(call, request.Path, false) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "无效文件扫描路径不在当前映射目录内")
	}
	reply := originalCleanSeedFileList{SchemaVersion: 1, Files: []originalCleanSeedFileFact{}}
	stamp, err := browserEntryStat(request.Path)
	if errors.Is(err, os.ErrNotExist) {
		return json.Marshal(reply)
	}
	if err != nil {
		return nil, err
	}
	var entries []browserStamp
	if request.Recursive {
		entries, err = browserManifestWithHardlinkGuard(ctx, request.Path, false)
	} else {
		entries, err = originalCleanSeedChildren(ctx, stamp)
	}
	if err != nil {
		return nil, err
	}
	if err := originalCleanSeedValidate(ctx, scope); err != nil {
		return nil, err
	}
	scan := &call.files
	if scan.observed == nil {
		scan.observed = map[string]browserStamp{}
		scan.trees = map[string][]browserStamp{}
	}
	for _, entry := range entries {
		if before, exists := scan.observed[entry.Path]; exists && before != entry {
			return nil, bad("FILE_SNAPSHOT_CHANGED", "无效文件扫描期间路径发生变化")
		}
		kind := "file"
		if os.FileMode(entry.Mode).IsDir() {
			kind = "dir"
		}
		scan.observed[entry.Path] = entry
		reply.Files = append(reply.Files, originalCleanSeedFileFact{Path: entry.Path, Kind: kind, Size: entry.Size, MTime: float64(entry.ModNS) / 1e9})
	}
	if request.Recursive {
		scan.trees[request.Path] = entries
	}
	reply.Exists = true
	return json.Marshal(reply)
}

func originalCleanSeedChildren(ctx context.Context, root browserStamp) ([]browserStamp, error) {
	out := []browserStamp{root}
	if !os.FileMode(root.Mode).IsDir() {
		return out, nil
	}
	directory, err := importDir(root.Path, false)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil {
		return nil, err
	}
	current, err := browserInfo(root.Path, opened)
	if err != nil || current != root {
		return nil, bad("FILE_SNAPSHOT_CHANGED", "无效文件扫描目录已变化")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		children, readErr := directory.ReadDir(100)
		for _, child := range children {
			if !validBrowserName(child.Name()) || child.Type()&os.ModeSymlink != 0 {
				continue
			}
			entry, err := browserEntryStat(filepath.Join(root.Path, child.Name()))
			if err != nil {
				return nil, err
			}
			out = append(out, entry)
			if len(out) > 10000 {
				return nil, bad("FILE_OPERATION_LIMIT", "无效文件扫描目录超过 10000 项")
			}
		}
		if errors.Is(readErr, io.EOF) {
			return out, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

func (a *App) originalCleanSeedRemove(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	call := scope.cleanSeed
	if !a.originalCleanSeedFileAllowed(call, request.Path, true) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "当前调用未获无效文件删除授权")
	}
	stamp, exists := call.files.observed[request.Path]
	expected := []browserStamp{stamp}
	switch request.Kind {
	case "tree":
		expected = call.files.trees[request.Path]
		exists = exists && os.FileMode(stamp.Mode).IsDir() && len(expected) > 0
	case "file":
		exists = exists && os.FileMode(stamp.Mode).IsRegular()
	default:
		exists = false
	}
	if !exists {
		return nil, bad("FILE_SNAPSHOT_CHANGED", "无效文件删除缺少本轮扫描证据")
	}
	exclusions := sourceText(call.config["exclude_keywords"])
	for _, text := range strings.Split(exclusions, "\n") {
		if exclusions != "" && strings.Contains(filepath.Base(request.Path), text) {
			return nil, bad("FILE_PROTECTED", "无效文件命中排除关键词")
		}
	}
	days, _ := originalResourceInteger(call.config["min_seeding_days"])
	if days > 0 && time.Since(time.Unix(0, stamp.ModNS)).Hours()/24 < float64(days) {
		return nil, bad("FILE_PROTECTED", "无效文件仍未达到最低保留时间")
	}

	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.importMu.Lock()
	defer a.importMu.Unlock()
	parent := filepath.Dir(request.Path)
	parentBefore, scanned := call.files.observed[parent]
	parentNow, err := browserEntryStat(parent)
	if !scanned || err != nil || !sameBrowserIdentity(parentBefore, parentNow) {
		return nil, bad("FILE_PARENT_CHANGED", "无效文件的扫描目录已变化")
	}
	if err := a.browserMutationReferences(ctx, request.Path, false); err != nil {
		return nil, err
	}
	for _, refresh := range []bool{false, true} {
		paths, err := a.originalCleanSeedContents(ctx, scope, refresh)
		if err != nil {
			return nil, err
		}
		if call.fileStillAttached(request.Path, paths) {
			return nil, bad("FILE_DOWNLOADER_SOURCE_PROTECTED", "下载器仍引用此文件，已停止清理")
		}
	}
	actual, err := browserManifestWithHardlinkGuard(ctx, request.Path, false)
	if err != nil || !sameBrowserManifest(actual, expected) {
		return nil, bad("FILE_SNAPSHOT_CHANGED", "无效文件在清理前发生变化")
	}
	// A partially removed tree keeps its directory identity even if its
	// listing or mtime changes. That must not bypass an uncertain receipt.
	key := strings.Join([]string{"file", request.Path, strconv.FormatUint(stamp.Device, 10), strconv.FormatUint(stamp.Inode, 10)}, "\x00")
	removed, err := a.executeOriginalCleanSeedEffect(ctx, scope, key, func() (bool, error) {
		done, err := a.originalGuardedCleanupQuarantine(ctx, request.Path, request.Kind, ".light-media-clean-seed-", "无效文件清理", expected,
			func() error { return originalCleanSeedValidate(ctx, scope) },
			func() bool {
				current, err := browserEntryStat(parent)
				return err == nil && sameBrowserIdentity(parentBefore, current) && a.originalCleanSeedFileAllowed(call, request.Path, true)
			})
		if err != nil || !done {
			return done, err
		}
		reconcile, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := a.originalScrapeCleanMarkMissing(reconcile, expected); err != nil {
			return false, bad("FILE_INDEX_UNCERTAIN", "无效文件已删除，但媒体索引需核对")
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(removed)
}

func (c *originalCleanSeedCall) fileStillAttached(path string, contents []string) bool {
	parent := filepath.Dir(path)
	for _, root := range c.fileRoots()[parent] {
		mapped := strings.ReplaceAll(path, parent, root)
		for _, content := range contents {
			if strings.Contains(content, mapped) || within(content, mapped) || within(content, path) || within(path, content) {
				return true
			}
		}
	}
	return false
}
