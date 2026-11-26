package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/transferextensions"
)

type directoryWatchEvent struct {
	path   string
	dir    bool
	closed bool
}

// Expand an arriving directory only. A member under BDMV always represents
// the entire disc; a half-written stream must never be submitted on its own.
func (a *App) directoryEventCandidates(ctx context.Context, root string, events []directoryWatchEvent) ([]importScanFile, error) {
	if !a.sourceAllowed(root) {
		return nil, bad("INVALID_SOURCE", "目录监听源路径不在允许范围内")
	}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []importScanFile
	for _, event := range events {
		path := event.path
		if !within(root, path) || path == root {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(relative, ".") || strings.Contains(relative, "/.") {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != event.dir {
			continue
		}
		directory := event.dir
		if i := strings.Index(strings.ToLower(filepath.ToSlash(path))+"/", "/bdmv/"); i >= 0 {
			path, directory = path[:i], true
			if !within(root, path) {
				continue
			}
		}
		if seen[path] {
			continue
		}
		if directory {
			raw, err := a.scanImportFiles(ctx, path, true, nil, nil, true)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			scan := raw.(map[string]any)
			if truncated, _ := scan["truncated"].(bool); truncated {
				return nil, bad("DIRECTORY_SCAN_LIMIT", "新目录超过监听扫描上限")
			}
			for _, file := range scan["files"].([]importScanFile) {
				if !seen[file.Path] {
					files = append(files, file)
					seen[file.Path] = true
				}
			}
		} else if match, _ := classifier.Media(transferextensions.PathFile(path)); match {
			file, err := a.scanImportVideo(path, nil)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			files = append(files, file)
		}
		seen[path] = true
		if len(files) > 2000 {
			return nil, bad("DIRECTORY_SCAN_LIMIT", "本批监听事件超过2000个媒体文件")
		}
	}
	return files, nil
}
