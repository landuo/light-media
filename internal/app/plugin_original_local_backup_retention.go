package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autobackup"
)

type originalLocalBackupEntry struct {
	name  string
	info  os.FileInfo
	ctime float64
}

func (a *App) originalLocalBackupRotate(ctx context.Context, root *os.Root, destination, defaultPath string, retain json.RawMessage, result *plugins.LocalBackupResultV1, guard func() error) error {
	count, countErr := autobackup.LocalRetentionCount(retain)
	if count == nil && countErr == nil {
		return nil
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	files, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	var entries []originalLocalBackupEntry
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := file.Name()
		if !strings.HasPrefix(name, "bk") {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return bad("FILE_SYMLINK_REJECTED", "备份清理不读取符号链接")
		}
		ctime, err := autobackup.LocalBackupCreationTime(info)
		if err != nil {
			return err
		}
		entries = append(entries, originalLocalBackupEntry{name: name, info: info, ctime: ctime})
	}
	if countErr != nil {
		return bad("INVALID_BACKUP_RETENTION", "备份保留数量无法转换为整数")
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ctime < entries[j].ctime })
	result.BackupCount = len(entries)
	deleteCount := new(big.Int).Sub(big.NewInt(int64(len(entries))), count)
	result.DeleteCount, result.RemainingCount = json.Number(deleteCount.String()), json.Number(count.String())
	if deleteCount.Sign() <= 0 {
		return nil
	}
	for i, entry := range entries {
		if deleteCount.IsInt64() && int64(i) >= deleteCount.Int64() {
			return nil
		}
		if !entry.info.IsDir() && !entry.info.Mode().IsRegular() {
			continue
		}
		if err := a.originalLocalBackupRemove(ctx, root, directory, filepath.Join(destination, entry.name), defaultPath, entry, guard); err != nil {
			return err
		}
	}
	if deleteCount.Cmp(big.NewInt(int64(len(entries)))) > 0 {
		return bad("INVALID_BACKUP_RETENTION", "备份保留数量小于零，已清理可处理的历史备份")
	}
	return nil
}

func (a *App) originalLocalBackupRemove(ctx context.Context, root *os.Root, parent *os.File, path, defaultPath string, entry originalLocalBackupEntry, guard func() error) error {
	if !within(defaultPath, path) {
		a.importMu.Lock()
		defer a.importMu.Unlock()
		if err := a.browserMutationAllowed(ctx, path); err != nil {
			return err
		}
	}
	var children []originalLocalBackupEntry
	if entry.info.IsDir() {
		if err := fs.WalkDir(root.FS(), entry.name, func(path string, _ fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path == entry.name {
				return nil
			}
			info, err := root.Lstat(path)
			if err == nil {
				children = append(children, originalLocalBackupEntry{name: path, info: info})
			}
			return err
		}); err != nil {
			return err
		}
	}
	if err := guard(); err != nil {
		return err
	}
	stage, err := originalLocalBackupCapture(root, parent, entry.name, entry.info)
	if err != nil {
		return err
	}
	removed := false
	defer func() {
		if !removed {
			_ = importRenameExclusive(int(parent.Fd()), stage, entry.name)
		}
	}()
	// Delete only the frozen entries, children before their directories. A
	// newly added child prevents its parent removal instead of being swept up.
	for i := len(children) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return err
		}
		child := children[i]
		actual := stage + strings.TrimPrefix(child.name, entry.name)
		dir, err := root.Open(filepath.Dir(actual))
		if err != nil {
			return err
		}
		captured, err := originalLocalBackupCapture(root, dir, actual, child.info)
		if err == nil {
			err = root.Remove(filepath.Join(filepath.Dir(actual), captured))
			if err != nil {
				_ = importRenameExclusive(int(dir.Fd()), captured, filepath.Base(actual))
			}
		}
		dir.Close()
		if err != nil {
			return err
		}
	}
	if err = root.Remove(stage); err != nil {
		return err
	}
	removed = true
	return nil
}

func originalLocalBackupCapture(root *os.Root, directory *os.File, path string, expected os.FileInfo) (string, error) {
	base := filepath.Base(path)
	capture := ".autobackup-retention-" + id("")
	if err := importRenameExclusive(int(directory.Fd()), base, capture); err != nil {
		return "", err
	}
	info, err := root.Lstat(filepath.Join(filepath.Dir(path), capture))
	if err != nil || !os.SameFile(expected, info) || expected.Mode() != info.Mode() ||
		!info.IsDir() && (expected.Size() != info.Size() || !expected.ModTime().Equal(info.ModTime())) {
		_ = importRenameExclusive(int(directory.Fd()), capture, base)
		return "", bad("FILE_SNAPSHOT_CHANGED", "备份清理前条目发生变化")
	}
	return capture, nil
}
