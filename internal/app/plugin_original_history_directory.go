package app

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

// History has already authorized its frozen source or published target. Unlike
// the file browser, unlinking that selected hardlink is explicitly allowed.
func originalHistoryManifest(path string) ([]browserStamp, error) {
	before, err := snapshotTransferPath(path)
	if err != nil {
		return nil, err
	}
	root, err := browserStat(path)
	if err != nil {
		return nil, err
	}
	result := []browserStamp{root}
	if before.Directory != "" {
		_, entries, err := snapshotOriginalDirectory(path)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			stamp, err := browserStat(filepath.Join(path, entry.Path))
			if err != nil {
				return nil, err
			}
			result = append(result, stamp)
		}
	}
	if after, err := snapshotTransferPath(path); err != nil || before != after {
		return nil, bad("FILE_SNAPSHOT_CHANGED", "历史文件在目录核对期间变化")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func (a *App) removeHistoryQuarantine(ctx context.Context, file *assetRemovalFile, parent *os.File, persist func() error) error {
	if file.Snapshot.Directory == "" {
		return unix.Unlinkat(int(parent.Fd()), file.Quarantine, 0)
	}
	var operation browserOperation
	sealed, err := base64.StdEncoding.DecodeString(file.SourceOperation)
	if err != nil || a.Store.Unseal(sealed, &operation) != nil || len(operation.Items) != 1 {
		return bad("REVIEW_HISTORY_DELETE", "目录删除缺少冻结成员收据")
	}
	item := operation.Items[0]
	target := filepath.Join(filepath.Dir(file.Path), file.Quarantine)
	if item.Source != file.Path || item.Target != target || len(item.Manifest) == 0 || !item.Done {
		return bad("REVIEW_HISTORY_DELETE", "目录删除成员收据不一致")
	}
	current, err := originalHistoryManifest(target)
	if err != nil {
		return err
	}
	expected := map[string]browserStamp{}
	for _, stamp := range item.Manifest {
		relative, err := filepath.Rel(item.Source, stamp.Path)
		if err != nil || !within(item.Source, stamp.Path) && stamp.Path != item.Source {
			return bad("REVIEW_HISTORY_DELETE", "目录删除成员越界")
		}
		expected[filepath.Join(target, relative)] = stamp
	}
	if !file.DirectoryRemovalStarted && !sameRebasedManifest(item.Manifest, current, item.Source, target) {
		return bad("FILE_SNAPSHOT_CHANGED", "待删除目录在暂存后发生变化")
	}
	for _, actual := range current {
		stamp, ok := expected[actual.Path]
		if !ok || !sameBrowserIdentity(stamp, actual) || !os.FileMode(stamp.Mode).IsDir() && (actual.Size != stamp.Size || actual.ModNS != stamp.ModNS) {
			return bad("FILE_SNAPSHOT_CHANGED", "暂存目录含未知或已变化的成员，未继续删除")
		}
	}
	file.DirectoryRemovalStarted = true
	if err := persist(); err != nil {
		return err
	}
	for index := len(current) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		stamp := current[index]
		actual, err := browserStat(stamp.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		isDir := os.FileMode(stamp.Mode).IsDir()
		if err != nil || !sameBrowserIdentity(stamp, actual) || !isDir && (stamp.Size != actual.Size || stamp.ModNS != actual.ModNS) {
			return bad("FILE_SNAPSHOT_CHANGED", "删除期间目录成员变化，已停止")
		}
		dir, err := importDir(filepath.Dir(stamp.Path), false)
		if err != nil {
			return err
		}
		flags := 0
		if isDir {
			flags = unix.AT_REMOVEDIR
		}
		err = unix.Unlinkat(int(dir.Fd()), filepath.Base(stamp.Path), flags)
		if err == nil {
			err = dir.Sync()
		}
		dir.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
